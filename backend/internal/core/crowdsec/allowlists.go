package crowdsec

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/traefik-console/internal/docker"
)

const consoleAllowlistPrefix = "traefik-console-"

var allowlistNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,55}$`)

func validAllowlistName(name string) bool {
	return allowlistNamePattern.MatchString(name)
}

func validAllowlistText(value string, max int) bool {
	return len(value) <= max && !strings.ContainsAny(value, "\x00\r\n")
}

func (m *crowdsecManager) allowlistCapability() (docker.AllowlistManager, error) {
	capability, ok := m.decisions.(docker.AllowlistManager)
	if !ok {
		return nil, ErrCrowdSecUnavailableCapability
	}
	return capability, nil
}

func (m *crowdsecManager) listAllowlistsContainer(ctx context.Context) ([]Allowlist, error) {
	capability, err := m.allowlistCapability()
	if err != nil {
		return nil, err
	}
	return capability.ListAllowlists(ctx)
}

func (m *crowdsecManager) checkAllowlistContainer(ctx context.Context, value string) (bool, string, error) {
	capability, err := m.allowlistCapability()
	if err != nil {
		return false, "", err
	}
	return capability.CheckAllowlist(ctx, value)
}

func (m *crowdsecManager) ListAllowlists(ctx context.Context) ([]Allowlist, error) {
	return m.listAllowlists(ctx)
}

func (m *crowdsecManager) CheckAllowlist(ctx context.Context, value string) (bool, string, error) {
	return m.checkAllowlist(ctx, strings.TrimSpace(value))
}

func (m *crowdsecManager) CreateAllowlist(ctx context.Context, name, description string) error {
	if !validAllowlistName(name) || !strings.HasPrefix(name, consoleAllowlistPrefix) || !validAllowlistText(description, 512) || strings.TrimSpace(description) == "" {
		return ErrCrowdSecInvalidIP
	}
	capability, err := m.allowlistCapability()
	if err != nil {
		return err
	}
	return capability.CreateAllowlist(ctx, name, strings.TrimSpace(description))
}

func (m *crowdsecManager) AddAllowlistEntry(ctx context.Context, name, value, description, expiration string) error {
	value = strings.TrimSpace(value)
	if !validAllowlistName(name) || !isValidIPOrCIDR(value) || !validAllowlistText(description, 512) {
		return ErrCrowdSecInvalidIP
	}
	if expiration != "" {
		duration, err := time.ParseDuration(expiration)
		if err != nil || duration <= 0 || duration > 365*24*time.Hour {
			return ErrCrowdSecInvalidIP
		}
	}
	capability, err := m.allowlistCapability()
	if err != nil {
		return err
	}
	return capability.AddAllowlistEntry(ctx, name, value, description, expiration)
}

func (m *crowdsecManager) RemoveAllowlistEntry(ctx context.Context, name, value string) error {
	if !validAllowlistName(name) || !isValidIPOrCIDR(strings.TrimSpace(value)) {
		return ErrCrowdSecInvalidIP
	}
	capability, err := m.allowlistCapability()
	if err != nil {
		return err
	}
	return capability.RemoveAllowlistEntry(ctx, name, strings.TrimSpace(value))
}

func (m *crowdsecManager) DeleteAllowlist(ctx context.Context, name string) error {
	if !validAllowlistName(name) || !strings.HasPrefix(name, consoleAllowlistPrefix) {
		return ErrCrowdSecInvalidIP
	}
	capability, err := m.allowlistCapability()
	if err != nil {
		return err
	}
	return capability.DeleteAllowlist(ctx, name)
}
