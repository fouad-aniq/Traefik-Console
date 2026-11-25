package docker

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strconv"
	"strings"
)

// CrowdSecEngine reports the registered CrowdSec container without running anything inside it.
func (m *Client) CrowdSecEngine(ctx context.Context) (*EngineState, error) {
	if m.crowdsecRef == "" {
		return &EngineState{}, nil
	}
	status, err := m.inspectContainer(ctx, m.crowdsecRef)
	if err != nil {
		return nil, err
	}
	return &EngineState{Configured: true, Running: status.Running, Name: status.Name, Image: status.Image}, nil
}

// ListAlerts lists alerts of the last sinceHours hours (newest first, at most limit). The raw CSV output is
// used because it is small and carries the country; the JSON form embeds every event and exceeds the
// bounded exec output quickly.
func (m *Client) ListAlerts(ctx context.Context, sinceHours int, limit int) ([]Alert, error) {
	if sinceHours < 1 || sinceHours > 24*90 || limit < 1 || limit > 1000 {
		return nil, ErrInvalidRequest
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	raw, e := m.execCrowdSec(ctx, "alerts", "list", "-o", "raw", "--since", strconv.Itoa(sinceHours)+"h", "--limit", strconv.Itoa(limit))
	if e != nil {
		return nil, e
	}
	return parseAlerts(raw)
}

// parseAlerts reads cscli's raw alert table. Columns are located by header name so a reordering in a
// later cscli release does not misattribute values.
func parseAlerts(raw []byte) ([]Alert, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return []Alert{}, nil
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, ErrExecFailed
	}
	index := map[string]int{}
	for i, name := range records[0] {
		index[strings.ToLower(strings.TrimSpace(name))] = i
	}
	if _, ok := index["id"]; !ok {
		return nil, ErrExecFailed
	}
	field := func(record []string, name string) string {
		if i, ok := index[name]; ok && i < len(record) {
			return strings.TrimSpace(record[i])
		}
		return ""
	}
	alerts := make([]Alert, 0, len(records)-1)
	for _, record := range records[1:] {
		id, _ := strconv.ParseInt(field(record, "id"), 10, 64)
		alerts = append(alerts, Alert{ID: id, Scope: field(record, "scope"), Value: field(record, "value"), Reason: field(record, "reason"), Country: field(record, "country"), AS: field(record, "as"), ASNumber: field(record, "as_number"), Origin: field(record, "origin"), CreatedAt: field(record, "created_at")})
	}
	return alerts, nil
}

func (m *Client) InspectAlert(ctx context.Context, id int64) (*Alert, error) {
	if id <= 0 {
		return nil, ErrInvalidRequest
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	raw, err := m.execCrowdSec(ctx, "alerts", "inspect", strconv.FormatInt(id, 10), "-o", "json")
	if err != nil {
		return nil, err
	}
	var native struct {
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
		Decisions []Decision `json:"decisions"`
	}
	if json.Unmarshal(raw, &native) != nil || native.ID != id || native.EventsCount < 0 || len(native.Scenario) > 512 || len(native.Kind) > 64 {
		return nil, ErrExecFailed
	}
	alert := &Alert{ID: native.ID, Reason: native.Scenario, Kind: native.Kind, Origin: native.Origin, EventsCount: native.EventsCount, CreatedAt: native.CreatedAt, Decisions: native.Decisions}
	if native.Source != nil {
		alert.Scope, alert.Value, alert.Country, alert.AS, alert.ASNumber = native.Source.Scope, native.Source.Value, native.Source.Cn, native.Source.AS, native.Source.ASNumber
	}
	return alert, nil
}

func (m *Client) ListBouncers(ctx context.Context) ([]Bouncer, error) {
	release, e := m.lockOperation(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	raw, e := m.execCrowdSec(ctx, "bouncers", "list", "-o", "json")
	if e != nil {
		return nil, e
	}
	return parseBouncers(raw)
}

func parseBouncers(raw []byte) ([]Bouncer, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return []Bouncer{}, nil
	}
	var rows []struct {
		Name      string `json:"name"`
		IPAddress string `json:"ip_address"`
		Type      string `json:"type"`
		Version   string `json:"version"`
		LastPull  string `json:"last_pull"`
		Revoked   bool   `json:"revoked"`
	}
	if json.Unmarshal([]byte(text), &rows) != nil {
		return nil, ErrExecFailed
	}
	out := make([]Bouncer, 0, len(rows))
	for _, row := range rows {
		out = append(out, Bouncer{Name: row.Name, IPAddress: row.IPAddress, Type: row.Type, Version: row.Version, LastPull: row.LastPull, Revoked: row.Revoked})
	}
	return out, nil
}
