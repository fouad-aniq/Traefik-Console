package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// InspectTraefikContainer retrieves the live container state, start timestamp, and mounted env vars
func (m *Client) InspectTraefikContainer(ctx context.Context) (*ContainerStatus, error) {
	release, e := m.lockOperation(ctx)
	if e != nil {
		return nil, e
	}
	defer release()

	return m.inspectContainer(ctx, m.traefikRef)
}

func (m *Client) inspectContainer(ctx context.Context, ref string) (*ContainerStatus, error) {
	if ref == "" {
		return nil, ErrContainerNotFound
	}

	reqURL := fmt.Sprintf("%s/containers/%s/json", m.baseURL, url.PathEscape(ref))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, ErrContainerNotFound
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOutputBytes+1))
	if err != nil || len(body) > maxOutputBytes {
		return nil, ErrExecFailed
	}

	var raw struct {
		ID           string `json:"Id"`
		Name         string `json:"Name"`
		RestartCount int    `json:"RestartCount"`
		Config       struct {
			Image  string            `json:"Image"`
			Env    []string          `json:"Env"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Running   bool   `json:"Running"`
			Status    string `json:"Status"`
			StartedAt string `json:"StartedAt"`
		} `json:"State"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	// The container must be exactly the registered one (by name, or by full ID if one was
	// registered); later operations use the full ID Docker returned here.
	if !fixedID.MatchString(raw.ID) || strings.TrimPrefix(raw.Name, "/") != ref && raw.ID != ref {
		return nil, ErrContainerNotFound
	}
	startedAt, _ := time.Parse(time.RFC3339Nano, raw.State.StartedAt)
	envMap := make(map[string]string, len(raw.Config.Env))
	for _, entry := range raw.Config.Env {
		if k, v, ok := strings.Cut(entry, "="); ok {
			envMap[k] = v
		}
	}

	return &ContainerStatus{
		ID:            raw.ID,
		Name:          strings.TrimPrefix(raw.Name, "/"),
		Image:         raw.Config.Image,
		Running:       raw.State.Running,
		Status:        raw.State.Status,
		RestartCount:  raw.RestartCount,
		StartedAt:     startedAt.UTC(),
		SecretFileEnv: m.secretFileEnv(envMap), RestartAllowed: m.allowTraefikRestart,
	}, nil
}

func (m *Client) secretFileEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		if strings.HasSuffix(k, "_FILE") && env[strings.TrimSuffix(k, "_FILE")] == "" && strings.HasPrefix(v, m.secretRoot+"/") && !strings.Contains(v, "..") {
			out[k] = v
		}
	}
	return out
}
