package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
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
	if err == nil {
		code := resp.StatusCode
		_ = resp.Body.Close()
		if code != http.StatusNoContent && code != http.StatusOK {
			return nil, ErrRestartUnconfirmed // refused: no permission, unknown container...
		}
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Docker answers only once Traefik is back, after its graceful stop (up to t=10s), which can
	// outlast the client's wait for an answer. A late or lost answer is not a failure: wait for
	// the container to be running again with a newer StartedAt.
	return m.waitRestarted(ctx, before)
}

// Restart confirmation window and polling interval (variables so tests can shorten them).
var (
	restartConfirmWindow = 40 * time.Second
	restartPollInterval  = 500 * time.Millisecond
)

func (m *Client) waitRestarted(ctx context.Context, before *ContainerStatus) (*ContainerStatus, error) {
	deadline := time.Now().Add(restartConfirmWindow)
	for {
		after, err := m.inspectContainer(ctx, m.traefikRef)
		if err == nil && after.Running && after.StartedAt.After(before.StartedAt) {
			return after, nil
		}
		if time.Now().After(deadline) {
			return nil, ErrRestartUnconfirmed
		}
		timer := time.NewTimer(restartPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
