package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RestartTraefikContainerAndConfirm verifies Traefik is running, issues a 10s graceful restart,
// and confirms the new container instance has a newer StartedAt timestamp.
func (m *Client) RestartTraefikContainerAndConfirm(ctx context.Context) (*ContainerStatus, error) {
	if m.httpClient == nil {
		return nil, ErrCapabilityDisabled
	}
	if !m.allowTraefikRestart {
		return nil, ErrRestartDisabled
	}

	release, e := m.lockOperation(ctx)
	if e != nil {
		return nil, e
	}
	defer release()

	before, err := m.inspectContainer(ctx, m.traefikRef)
	if err != nil {
		return nil, err
	}
	if !before.Running {
		return nil, ErrContainerNotRunning
	}

	reqURL := fmt.Sprintf("%s/containers/%s/restart?t=10", m.baseURL, url.PathEscape(before.ID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil || (resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, ErrRestartUnconfirmed
	}
	_ = resp.Body.Close()

	// Confirm new StartedAt timestamp is strictly after `before.StartedAt`
	after, err := m.inspectContainer(ctx, m.traefikRef)
	if err != nil || !after.Running || !after.StartedAt.After(before.StartedAt) {
		return nil, ErrRestartUnconfirmed
	}

	return after, nil
}
