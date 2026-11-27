package crowdsec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
)

const maxLAPIResponseBytes = 1 << 20

type AlertQuery struct {
	Page       int
	Limit      int
	SinceHours int
	Scenario   string
	Source     string
	Origin     string
}

type AlertPage struct {
	Items      []docker.Alert `json:"items"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	HasMore    bool           `json:"has_more"`
	ObservedAt string         `json:"observed_at"`
}

type AllowlistEntry = docker.AllowlistEntry

type Allowlist = docker.Allowlist

type lapiAlert struct {
	ID          int64  `json:"id"`
	Scenario    string `json:"scenario"`
	Kind        string `json:"kind"`
	Origin      string `json:"origin"`
	CreatedAt   string `json:"created_at"`
	EventsCount int32  `json:"events_count"`
	Source      *struct {
		Scope    string `json:"scope"`
		Value    string `json:"value"`
		Cn       string `json:"cn"`
		AS       string `json:"as_name"`
		ASNumber string `json:"as_number"`
	} `json:"source"`
	Decisions []docker.Decision `json:"decisions"`
}

func (m *crowdsecManager) doLAPI(ctx context.Context, machine bool, method, path string, query url.Values, body any, authHeader, authValue string, out any, expected ...int) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return ErrCrowdSecUnreachable
		}
	}
	lapiURL, err := m.effectiveLAPIURL(ctx)
	if err != nil {
		return err
	}
	base, err := registeredURL(lapiURL)
	if err != nil {
		return err
	}
	base.Path = path
	base.RawPath = ""
	base.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(payload))
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Traefik-Console/2.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authHeader != "" {
		req.Header.Set(authHeader, authValue)
	}
	client, err := m.clientForRole(ctx, machine)
	if err != nil {
		return err
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copyClient.Do(req)
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrCrowdSecInvalidKey
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrCrowdSecNotFound
	}
	valid := false
	for _, status := range expected {
		valid = valid || resp.StatusCode == status
	}
	if !valid {
		return ErrCrowdSecUnreachable
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLAPIResponseBytes+1))
	if err != nil {
		return ErrCrowdSecUnreachable
	}
	if len(data) > maxLAPIResponseBytes {
		return errLAPIResponseTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(out); err != nil {
		return ErrCrowdSecUnreachable
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrCrowdSecUnreachable
	}
	return nil
}

func (m *crowdsecManager) machineToken(ctx context.Context, machineID, password string) (string, error) {
	if machineID == "" || password == "" || len(machineID) > 96 || len(password) > 4096 || strings.ContainsAny(machineID+password, "\x00\r\n") {
		return "", ErrCrowdSecInvalidKey
	}
	var response struct {
		Token string `json:"token"`
	}
	body := struct {
		ID       string `json:"machine_id"`
		Password string `json:"password"`
	}{machineID, password}
	if err := m.doLAPI(ctx, true, http.MethodPost, "/v1/watchers/login", nil, body, "", "", &response, http.StatusOK); err != nil {
		return "", err
	}
	if response.Token == "" || len(response.Token) > 16384 || strings.ContainsAny(response.Token, "\x00\r\n") {
		return "", ErrCrowdSecUnreachable
	}
	return response.Token, nil
}

func (m *crowdsecManager) machineCredentials(ctx context.Context) (string, string, bool) {
	machineID := m.settings.MachineID
	password := m.settings.MachinePassword
	if cfg, err := m.GetConfig(ctx); err == nil {
		if machineID == "" {
			machineID = cfg.MachineID
		}
		if password == "" && cfg.MachineID != "" {
			if saved, err := m.secrets.DecryptSecret(ctx, crowdsecMachineSecret); err == nil {
				password = saved
			}
		}
	}
	return machineID, password, machineID != "" && password != ""
}

func (m *crowdsecManager) bouncerCredential(ctx context.Context) string {
	if strings.TrimSpace(m.settings.APIKey) != "" {
		return strings.TrimSpace(m.settings.APIKey)
	}
	if key, err := m.secrets.DecryptSecret(ctx, crowdsecSecretName); err == nil && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	return ""
}

func (m *crowdsecManager) machineAuthorization(ctx context.Context) (string, string, error) {
	certificate, err := m.hasClientCertificate(ctx, true)
	if err != nil {
		return "", "", err
	}
	if certificate {
		return "", "", nil
	}
	machineID, password, ok := m.machineCredentials(ctx)
	if !ok {
		return "", "", ErrCrowdSecUnavailableCapability
	}
	token, err := m.machineToken(ctx, machineID, password)
	if err != nil {
		return "", "", err
	}
	return "Authorization", "Bearer " + token, nil
}

func (m *crowdsecManager) verifyMachineCredentials(ctx context.Context, machineID, password string) error {
	certificate, err := m.hasClientCertificate(ctx, true)
	if err != nil {
		return err
	}
	var alerts []lapiAlert
	if certificate {
		return m.doLAPI(ctx, true, http.MethodGet, "/v1/alerts", url.Values{"limit": {"1"}}, nil, "", "", &alerts, http.StatusOK)
	}
	token, err := m.machineToken(ctx, machineID, password)
	if err != nil {
		return err
	}
	return m.doLAPI(ctx, true, http.MethodGet, "/v1/alerts", url.Values{"limit": {"1"}}, nil, "Authorization", "Bearer "+token, &alerts, http.StatusOK)
}

func validObservationText(value string, max int) bool {
	return len(value) <= max && !strings.ContainsAny(value, "\x00\r\n")
}

func (m *crowdsecManager) listAlertsLAPI(ctx context.Context, query AlertQuery) (*AlertPage, error) {
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return nil, err
	}
	end := (query.Page + 1) * query.Limit
	// The LAPI includes community-blocklist (CAPI) alerts unless told otherwise, and each of those embeds
	// every decision it pulled (about 15,000): a page of them is megabytes. cscli leaves them out by
	// default too (include_capi=false unless -a); they are only asked for when filtering on their origin.
	blocklist := isBlocklistOrigin(query.Origin)
	filters := url.Values{
		"limit":        {strconv.Itoa(end + 1)},
		"since":        {strconv.Itoa(query.SinceHours) + "h"},
		"include_capi": {strconv.FormatBool(blocklist)},
	}
	if query.Scenario != "" {
		filters.Set("scenario", query.Scenario)
	}
	if query.Origin != "" {
		filters.Set("origin", query.Origin)
	}
	if query.Source != "" {
		if strings.Contains(query.Source, "/") {
			filters.Set("range", query.Source)
		} else {
			filters.Set("ip", query.Source)
		}
	}
	var native []lapiAlert
	if err := m.doLAPI(ctx, true, http.MethodGet, "/v1/alerts", filters, nil, authHeader, authValue, &native, http.StatusOK); err != nil {
		if blocklist && errors.Is(err, errLAPIResponseTooLarge) {
			return nil, ErrCrowdSecBlocklistTooLarge
		}
		return nil, err
	}
	page := &AlertPage{Items: []docker.Alert{}, Page: query.Page, Limit: query.Limit, ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	page.HasMore = len(native) > end
	if len(native) > end {
		native = native[:end]
	}
	start := query.Page * query.Limit
	for i, alert := range native {
		if i < start || alert.ID < 0 || alert.EventsCount < 0 || !validObservationText(alert.Scenario, 512) || !validObservationText(alert.Kind, 64) {
			continue
		}
		created, valid := parseAlertTime(alert.CreatedAt)
		if !valid {
			return nil, ErrCrowdSecUnreachable
		}
		item := docker.Alert{ID: alert.ID, Reason: alert.Scenario, Kind: alert.Kind, Origin: alert.Origin, EventsCount: alert.EventsCount, CreatedAt: created.Format(time.RFC3339), Decisions: alert.Decisions}
		if alert.Source != nil {
			item.Scope, item.Value, item.Country, item.AS, item.ASNumber = alert.Source.Scope, alert.Source.Value, alert.Source.Cn, alert.Source.AS, alert.Source.ASNumber
		}
		for _, secret := range []string{strings.TrimPrefix(authValue, "Bearer ")} {
			if secret != "" {
				item.Reason = strings.ReplaceAll(item.Reason, secret, "[redacted]")
				item.Value = strings.ReplaceAll(item.Value, secret, "[redacted]")
			}
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// isBlocklistOrigin reports an origin whose alerts come from a blocklist pull (the community blocklist,
// or subscribed lists) rather than from detections on this host.
func isBlocklistOrigin(origin string) bool {
	return strings.EqualFold(origin, "CAPI") || strings.EqualFold(origin, "lists")
}

func (m *crowdsecManager) getAlertLAPI(ctx context.Context, id int64) (*docker.Alert, error) {
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return nil, err
	}
	var alert lapiAlert
	if err := m.doLAPI(ctx, true, http.MethodGet, "/v1/alerts/"+strconv.FormatInt(id, 10), nil, nil, authHeader, authValue, &alert, http.StatusOK); err != nil {
		return nil, err
	}
	created, valid := parseAlertTime(alert.CreatedAt)
	if !valid || alert.ID != id || alert.EventsCount < 0 {
		return nil, ErrCrowdSecUnreachable
	}
	result := &docker.Alert{ID: alert.ID, Reason: alert.Scenario, Kind: alert.Kind, Origin: alert.Origin, EventsCount: alert.EventsCount, CreatedAt: created.Format(time.RFC3339), Decisions: alert.Decisions}
	if alert.Source != nil {
		result.Scope, result.Value, result.Country, result.AS, result.ASNumber = alert.Source.Scope, alert.Source.Value, alert.Source.Cn, alert.Source.AS, alert.Source.ASNumber
	}
	return result, nil
}

func (m *crowdsecManager) listAllowlistsLAPI(ctx context.Context) ([]Allowlist, error) {
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return nil, err
	}
	lists := []Allowlist{}
	if err := m.doLAPI(ctx, true, http.MethodGet, "/v1/allowlists", nil, nil, authHeader, authValue, &lists, http.StatusOK); err != nil {
		return nil, err
	}
	if len(lists) > 1000 {
		return nil, ErrCrowdSecUnreachable
	}
	for i := range lists {
		if !validObservationText(lists[i].Name, 128) || !validObservationText(lists[i].Description, 512) || len(lists[i].Items) > 1000 {
			return nil, ErrCrowdSecUnreachable
		}
		if lists[i].Items == nil {
			lists[i].Items = []AllowlistEntry{}
		}
		for _, entry := range lists[i].Items {
			if !isValidIPOrCIDR(entry.Value) || len(entry.Description) > 512 {
				return nil, ErrCrowdSecUnreachable
			}
		}
	}
	return lists, nil
}

func (m *crowdsecManager) checkAllowlistLAPI(ctx context.Context, value string) (bool, string, error) {
	if !isValidIPOrCIDR(value) {
		return false, "", ErrCrowdSecInvalidIP
	}
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return false, "", err
	}
	var response struct {
		Results []struct {
			Target     string   `json:"target"`
			Allowlists []string `json:"allowlists"`
		} `json:"results"`
	}
	body := map[string][]string{"targets": {value}}
	if err := m.doLAPI(ctx, true, http.MethodPost, "/v1/allowlists/check", nil, body, authHeader, authValue, &response, http.StatusOK); err != nil {
		return false, "", err
	}
	if len(response.Results) != 1 || response.Results[0].Target != value {
		return false, "", ErrCrowdSecUnreachable
	}
	reason := strings.Join(response.Results[0].Allowlists, ", ")
	return len(response.Results[0].Allowlists) > 0, reason, nil
}
