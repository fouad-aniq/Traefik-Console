package crowdsec

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
)

func validateAlertQuery(query *AlertQuery, defaultLimit int) error {
	if query.Page < 0 || query.Page > 9 {
		return ErrCrowdSecInvalidIP
	}
	if query.Limit == 0 {
		query.Limit = 25
	}
	if query.SinceHours == 0 {
		query.SinceHours = 24
	}
	if query.Limit < 1 || query.Limit > 100 || query.SinceHours < 1 || query.SinceHours > 720 {
		return ErrCrowdSecInvalidIP
	}
	if query.Scenario != "" && !validObservationText(query.Scenario, 160) {
		return ErrCrowdSecInvalidIP
	}
	if query.Origin != "" && !validObservationText(query.Origin, 160) {
		return ErrCrowdSecInvalidIP
	}
	if query.Source != "" && query.Source != strings.TrimSpace(query.Source) {
		return ErrCrowdSecInvalidIP
	}
	if query.Source != "" && !isValidIPOrCIDR(query.Source) {
		return ErrCrowdSecInvalidIP
	}
	if defaultLimit > 0 && (query.Page+1)*query.Limit+1 > defaultLimit {
		return ErrCrowdSecInvalidIP
	}
	return nil
}

// ListAlerts reads native LAPI alerts when machine credentials are available.
// The registered container remains a compatibility fallback for existing setups.
func (m *crowdsecManager) ListAlerts(ctx context.Context, query AlertQuery) (*AlertPage, error) {
	alertLimit, err := m.effectiveAlertLimit(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateAlertQuery(&query, alertLimit); err != nil {
		return nil, err
	}
	page, err := m.listAlertsLAPI(ctx, query)
	if err == nil || !isCapabilityUnavailable(err) {
		return page, err
	}
	inspector, ok := m.decisions.(docker.CrowdSecInspector)
	if !ok {
		return nil, ErrCrowdSecUnavailableCapability
	}
	end := (query.Page + 1) * query.Limit
	fetchLimit := end + 1
	if query.Scenario != "" || query.Source != "" || query.Origin != "" {
		fetchLimit = alertLimit
		if fetchLimit <= 0 {
			fetchLimit = 500
		}
	}
	alerts, err := inspector.ListAlerts(ctx, query.SinceHours, fetchLimit)
	if err != nil {
		return nil, err
	}
	filtered := make([]docker.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if query.Scenario != "" && alert.Reason != query.Scenario {
			continue
		}
		if query.Source != "" && alert.Value != query.Source {
			continue
		}
		if query.Origin != "" && alert.Origin != query.Origin {
			continue
		}
		filtered = append(filtered, alert)
	}
	result := &AlertPage{Items: []docker.Alert{}, Page: query.Page, Limit: query.Limit, HasMore: len(filtered) > end, ObservedAt: time.Now().UTC().Format(time.RFC3339)}
	start := query.Page * query.Limit
	for i, alert := range filtered {
		if i < start {
			continue
		}
		if i >= end {
			break
		}
		result.Items = append(result.Items, alert)
	}
	return result, nil
}

func (m *crowdsecManager) GetAlert(ctx context.Context, id int64) (*docker.Alert, error) {
	if id <= 0 {
		return nil, ErrCrowdSecNotFound
	}
	alert, err := m.getAlertLAPI(ctx, id)
	if err == nil || !isCapabilityUnavailable(err) {
		return alert, err
	}
	inspector, ok := m.decisions.(docker.CrowdSecInspector)
	if !ok {
		return nil, ErrCrowdSecUnavailableCapability
	}
	return inspector.InspectAlert(ctx, id)
}

func (m *crowdsecManager) listDecisionsMachine(ctx context.Context) ([]CrowdSecDecision, error) {
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return nil, err
	}
	alertLimit, err := m.effectiveAlertLimit(ctx)
	if err != nil {
		return nil, err
	}
	var alerts []struct {
		Decisions []CrowdSecDecision `json:"decisions"`
	}
	// Only this host's decisions: blocklist alerts embed thousands of decisions each and would exceed what
	// Console reads. Blocklist bans are listed completely by the bouncer's /v1/decisions, which is used
	// whenever a bouncer identity exists; this machine-only fallback leaves them out.
	filters := url.Values{"limit": {strconv.Itoa(alertLimit)}, "has_active_decision": {"true"}, "simulated": {"false"}, "include_capi": {"false"}}
	if err := m.doLAPI(ctx, true, http.MethodGet, "/v1/alerts", filters, nil, authHeader, authValue, &alerts, http.StatusOK); err != nil {
		return nil, err
	}
	result := []CrowdSecDecision{}
	seen := map[int64]bool{}
	for _, alert := range alerts {
		for _, decision := range alert.Decisions {
			if seen[decision.ID] || decision.ID <= 0 || !validObservationText(decision.Value, 160) || !validObservationText(decision.Duration, 64) {
				continue
			}
			if duration, err := time.ParseDuration(decision.Duration); err == nil && duration <= 0 {
				continue
			}
			seen[decision.ID] = true
			result = append(result, decision)
			if len(result) > alertLimit {
				return nil, ErrCrowdSecUnreachable
			}
		}
	}
	return result, nil
}

func (m *crowdsecManager) listDecisionsBouncer(ctx context.Context) ([]CrowdSecDecision, error) {
	authHeader, authValue := "", ""
	certificate, err := m.hasClientCertificate(ctx, false)
	if err != nil {
		return nil, err
	}
	if !certificate {
		key := m.bouncerCredential(ctx)
		if key == "" {
			return nil, ErrCrowdSecUnavailableCapability
		}
		authHeader, authValue = "X-Api-Key", key
	}
	// Every active decision, blocklists included (the bouncer view has no paging): the alert limit does
	// not apply, only the decision ceiling.
	result := []CrowdSecDecision{}
	if err := m.doLAPIWithin(ctx, maxLAPIDecisionBytes, false, http.MethodGet, "/v1/decisions", nil, nil, authHeader, authValue, &result, http.StatusOK); err != nil {
		return nil, err
	}
	if len(result) > maxLAPIDecisions {
		return nil, ErrCrowdSecUnreachable
	}
	return result, nil
}

func (m *crowdsecManager) hasBouncerIdentity(ctx context.Context) (bool, error) {
	if m.bouncerCredential(ctx) != "" {
		return true, nil
	}
	return m.hasClientCertificate(ctx, false)
}

func (m *crowdsecManager) hasMachineIdentity(ctx context.Context) (bool, error) {
	certificate, err := m.hasClientCertificate(ctx, true)
	if err != nil || certificate {
		return certificate, err
	}
	_, _, present := m.machineCredentials(ctx)
	return present, nil
}

func (m *crowdsecManager) listDecisionsNative(ctx context.Context) ([]CrowdSecDecision, error) {
	bouncer, err := m.hasBouncerIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if bouncer {
		return m.listDecisionsBouncer(ctx)
	}
	return m.listDecisionsMachine(ctx)
}

func (m *crowdsecManager) deleteDecisionByIDNative(ctx context.Context, id int64) error {
	authHeader, authValue, err := m.machineAuthorization(ctx)
	if err != nil {
		return err
	}
	var result struct {
		Deleted string `json:"nbDeleted"`
	}
	if err := m.doLAPI(ctx, true, http.MethodDelete, "/v1/decisions/"+strconv.FormatInt(id, 10), nil, nil, authHeader, authValue, &result, http.StatusOK); err != nil {
		return err
	}
	if result.Deleted == "0" {
		return ErrCrowdSecNotFound
	}
	return nil
}

// DeleteDecisionByID removes exactly one decision instead of every decision
// sharing an address or CIDR.
func (m *crowdsecManager) DeleteDecisionByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrCrowdSecNotFound
	}
	err := m.deleteDecisionByIDNative(ctx, id)
	if err == nil || !isCapabilityUnavailable(err) {
		return err
	}
	return m.decisions.DeleteDecisionByID(ctx, id)
}

func (m *crowdsecManager) listAllowlists(ctx context.Context) ([]Allowlist, error) {
	lists, err := m.listAllowlistsLAPI(ctx)
	if err == nil || !isCapabilityUnavailable(err) {
		return lists, err
	}
	return m.listAllowlistsContainer(ctx)
}

func (m *crowdsecManager) checkAllowlist(ctx context.Context, value string) (bool, string, error) {
	allowed, reason, err := m.checkAllowlistLAPI(ctx, value)
	if err == nil || !isCapabilityUnavailable(err) {
		return allowed, reason, err
	}
	return m.checkAllowlistContainer(ctx, value)
}

func (m *crowdsecManager) ListHubItems(ctx context.Context) ([]docker.HubItem, error) {
	capability, ok := m.decisions.(docker.HubManager)
	if !ok {
		return nil, ErrCrowdSecUnavailableCapability
	}
	return capability.ListHubItems(ctx)
}

func isCapabilityUnavailable(err error) bool {
	return err == ErrCrowdSecUnavailableCapability
}
