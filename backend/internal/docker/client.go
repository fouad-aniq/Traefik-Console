package docker

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"time"
)

var fixedID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Client owns the Docker connection and fixed, operator-registered targets.
// It has no public arbitrary-command, container-target or raw HTTP entrypoint.
type Client struct {
	secretRoot                       string
	baseURL, traefikRef, crowdsecRef string
	allowTraefikRestart              bool
	httpClient                       *http.Client
	gate                             chan struct{}
}

// Registration names the containers the Console may act on. Names are resolved on
// every operation, so recreating a container does not break the registration.
type Registration struct {
	DockerHost, TraefikContainer, CrowdSecContainer, SecretsDir string
	AllowRestart                                                bool
}

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

var _ Operations = (*Client)(nil)

func New(reg Registration) (*Client, error) {
	if reg.DockerHost == "" {
		if reg.TraefikContainer != "" || reg.CrowdSecContainer != "" || reg.AllowRestart {
			return nil, fmt.Errorf("DOCKER_HOST is required for container operations")
		}
		return &Client{}, nil
	}
	if !containerName.MatchString(reg.TraefikContainer) || reg.CrowdSecContainer != "" && (!containerName.MatchString(reg.CrowdSecContainer) || reg.CrowdSecContainer == reg.TraefikContainer) {
		return nil, fmt.Errorf("Docker operations require valid, distinct Traefik and CrowdSec container names")
	}
	if reg.SecretsDir == "" {
		reg.SecretsDir = "/etc/traefik/secrets"
	}
	if !filepath.IsAbs(reg.SecretsDir) || filepath.Clean(reg.SecretsDir) != reg.SecretsDir || reg.SecretsDir == "/" {
		return nil, fmt.Errorf("invalid registered Traefik secret root")
	}
	base, transport, e := endpointTransport(reg.DockerHost)
	if e != nil {
		return nil, e
	}
	return &Client{secretRoot: reg.SecretsDir, traefikRef: reg.TraefikContainer, crowdsecRef: reg.CrowdSecContainer, allowTraefikRestart: reg.AllowRestart, baseURL: base, gate: make(chan struct{}, 1), httpClient: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (m *Client) Close() {
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

// lockOperation covers the complete typed operation, including multi-command
// owned cleanup. It replaces the old transport's serialization without a proxy.
func (m *Client) lockOperation(ctx context.Context) (func(), error) {
	if m.httpClient == nil {
		return nil, ErrCapabilityDisabled
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	// Operations are serialized, not refused: a page reads status, statistics and lists at the same
	// time. A caller waits its turn for a bounded time and only then reports the engine as busy.
	timer := time.NewTimer(operationQueueWait)
	defer timer.Stop()
	select {
	case m.gate <- struct{}{}:
		return func() { <-m.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrOperationBusy
	}
}

// operationQueueWait bounds how long a container operation waits behind another one.
var operationQueueWait = 20 * time.Second
