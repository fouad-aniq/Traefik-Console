package docker

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
)

// Only this container adapter knows cscli argument order and JSON layout.
func (m *Client) ListDecisions(ctx context.Context) ([]Decision, error) {
	release, e := m.lockOperation(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	raw, e := m.execCrowdSec(ctx, "decisions", "list", "-o", "json")
	if e != nil {
		return nil, e
	}
	return parseDecisions(raw)
}
func parseDecisions(raw []byte) ([]Decision, error) {
	result := []Decision{}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return nil, ErrExecFailed
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			return nil, ErrExecFailed
		}
		if nested, ok := fields["decisions"]; ok {
			var decisions []Decision
			if json.Unmarshal(nested, &decisions) != nil {
				return nil, ErrExecFailed
			}
			result = append(result, decisions...)
		} else {
			var d Decision
			if json.Unmarshal(row, &d) != nil || strings.TrimSpace(d.Value) == "" {
				return nil, ErrExecFailed
			}
			result = append(result, d)
		}
	}
	return result, nil
}
func decisionFlag(ip string) string {
	if net.ParseIP(ip) == nil {
		return "--range"
	}
	return "--ip"
}
func (m *Client) AddDecision(ctx context.Context, ip, duration, reason string) error {
	if e := ValidateDecisionInput(ip, duration, reason, true); e != nil {
		return e
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	_, e = m.execCrowdSec(ctx, "decisions", "add", decisionFlag(ip), ip, "--duration", duration, "--reason", reason)
	return e
}
func (m *Client) DeleteDecision(ctx context.Context, ip string) error {
	if e := ValidateDecisionInput(ip, "", "", false); e != nil {
		return e
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	_, e = m.execCrowdSec(ctx, "decisions", "delete", decisionFlag(ip), ip)
	return e
}
func (m *Client) DeleteDecisionByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidRequest
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	_, e = m.execCrowdSec(ctx, "decisions", "delete", "--id", strconv.FormatInt(id, 10))
	return e
}
func (m *Client) RegisterBouncer(ctx context.Context, id int64, key string) error {
	if e := ValidateBouncerInput(id, key, true); e != nil {
		return e
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	_, e = m.execCrowdSec(ctx, "bouncers", "add", "traefik-console-op-"+strconv.FormatInt(id, 10), "--key", key, "-o", "raw")
	return e
}
func (m *Client) RevokeBouncer(ctx context.Context, id int64) error {
	if e := ValidateBouncerInput(id, "", false); e != nil {
		return e
	}
	release, e := m.lockOperation(ctx)
	if e != nil {
		return e
	}
	defer release()
	raw, e := m.execCrowdSec(ctx, "bouncers", "list", "-o", "json")
	if e != nil {
		return e
	}
	var list []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ErrExecFailed
	}
	name := "traefik-console-op-" + strconv.FormatInt(id, 10)
	for _, entry := range list {
		if entry.Name == name {
			_, e = m.execCrowdSec(ctx, "bouncers", "delete", name)
			return e
		}
	}
	return nil
}
