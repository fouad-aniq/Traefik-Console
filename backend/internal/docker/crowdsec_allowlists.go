package docker

import (
	"context"
	"encoding/json"
	"net"
	"regexp"
	"strings"
	"time"
)

var allowlistName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validateAllowlistName(name string) error {
	if !allowlistName.MatchString(name) {
		return ErrInvalidRequest
	}
	return nil
}

func validateAllowlistValue(value string) error {
	if net.ParseIP(value) == nil {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return ErrInvalidRequest
		}
	}
	return nil
}

func validateAllowlistDescription(value string) error {
	if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
		return ErrInvalidRequest
	}
	return nil
}

func parseAllowlist(raw []byte) (*Allowlist, error) {
	var list Allowlist
	if json.Unmarshal(raw, &list) != nil || !allowlistName.MatchString(list.Name) || len(list.Items) > 1000 {
		return nil, ErrExecFailed
	}
	if list.Items == nil {
		list.Items = []AllowlistEntry{}
	}
	for _, entry := range list.Items {
		if validateAllowlistValue(entry.Value) != nil || validateAllowlistDescription(entry.Description) != nil {
			return nil, ErrExecFailed
		}
	}
	return &list, nil
}

func (m *Client) inspectAllowlist(ctx context.Context, name string) (*Allowlist, error) {
	raw, err := m.execCrowdSec(ctx, "allowlists", "inspect", name, "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseAllowlist(raw)
}

func (m *Client) ListAllowlists(ctx context.Context) ([]Allowlist, error) {
	release, err := m.lockOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	raw, err := m.execCrowdSec(ctx, "allowlists", "list", "-o", "json")
	if err != nil {
		return nil, err
	}
	var summaries []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &summaries) != nil || len(summaries) > 100 {
		return nil, ErrExecFailed
	}
	result := make([]Allowlist, 0, len(summaries))
	for _, summary := range summaries {
		if validateAllowlistName(summary.Name) != nil {
			return nil, ErrExecFailed
		}
		list, err := m.inspectAllowlist(ctx, summary.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, *list)
	}
	return result, nil
}

func (m *Client) CheckAllowlist(ctx context.Context, value string) (bool, string, error) {
	if validateAllowlistValue(value) != nil {
		return false, "", ErrInvalidRequest
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return false, "", err
	}
	defer release()
	raw, err := m.execCrowdSec(ctx, "allowlists", "check", value, "-o", "json")
	if err != nil {
		return false, "", err
	}
	var response struct {
		Allowlisted bool   `json:"allowlisted"`
		Reason      string `json:"reason"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Reason) > 512 {
		return false, "", ErrExecFailed
	}
	return response.Allowlisted, response.Reason, nil
}

func (m *Client) CreateAllowlist(ctx context.Context, name, description string) error {
	if validateAllowlistName(name) != nil || validateAllowlistDescription(description) != nil || strings.TrimSpace(description) == "" {
		return ErrInvalidRequest
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = m.execCrowdSec(ctx, "allowlists", "create", name, "--description", description)
	return err
}

func (m *Client) AddAllowlistEntry(ctx context.Context, name, value, description, expiration string) error {
	if validateAllowlistName(name) != nil || validateAllowlistValue(value) != nil || validateAllowlistDescription(description) != nil {
		return ErrInvalidRequest
	}
	args := []string{"allowlists", "add", name, value}
	if description != "" {
		args = append(args, "--comment", description)
	}
	if expiration != "" {
		duration, err := time.ParseDuration(expiration)
		if err != nil || duration <= 0 || duration > 365*24*time.Hour {
			return ErrInvalidRequest
		}
		args = append(args, "--expiration", expiration)
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = m.execCrowdSec(ctx, args...)
	return err
}

func (m *Client) RemoveAllowlistEntry(ctx context.Context, name, value string) error {
	if validateAllowlistName(name) != nil || validateAllowlistValue(value) != nil {
		return ErrInvalidRequest
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = m.execCrowdSec(ctx, "allowlists", "remove", name, value)
	return err
}

func (m *Client) DeleteAllowlist(ctx context.Context, name string) error {
	if validateAllowlistName(name) != nil || !strings.HasPrefix(name, "traefik-console-") {
		return ErrInvalidRequest
	}
	release, err := m.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	_, err = m.execCrowdSec(ctx, "allowlists", "delete", name)
	return err
}
