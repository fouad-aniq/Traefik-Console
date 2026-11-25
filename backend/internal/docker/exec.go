package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxOutputBytes = 65536 // bounded Docker responses and command output

// execCrowdSec runs arguments constructed only by the typed adapter methods.
func (m *Client) execCrowdSec(ctx context.Context, args ...string) ([]byte, error) {

	csStatus, err := m.inspectContainer(ctx, m.crowdsecRef)
	if err != nil || !csStatus.Running {
		return nil, ErrContainerNotRunning
	}

	return m.execFixed(ctx, csStatus.ID, append([]string{"cscli"}, args...))
}

func (m *Client) execFixed(ctx context.Context, target string, cmd []string) ([]byte, error) {
	createPayload, _ := json.Marshal(map[string]any{
		"AttachStdout": true,
		"AttachStderr": true,
		"Cmd":          cmd,
	})

	// 1. Create Exec instance
	createURL := fmt.Sprintf("%s/containers/%s/exec", m.baseURL, url.PathEscape(target))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(createPayload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, ErrExecFailed
	}
	var execCreated struct {
		ID string `json:"Id"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&execCreated)
	_ = resp.Body.Close()
	if decodeErr != nil || !fixedID.MatchString(execCreated.ID) {
		return nil, ErrExecFailed
	}

	// 2. Start Exec and read bounded output
	startPayload, _ := json.Marshal(map[string]bool{"Detach": false, "Tty": false})
	startURL := fmt.Sprintf("%s/exec/%s/start", m.baseURL, execCreated.ID)
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(startPayload))
	if err != nil {
		return nil, err
	}
	startReq.Header.Set("Content-Type", "application/json")

	startResp, err := m.httpClient.Do(startReq)
	if err != nil || startResp.StatusCode != http.StatusOK {
		if startResp != nil {
			_ = startResp.Body.Close()
		}
		return nil, ErrExecFailed
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(startResp.Body)

	out, err := io.ReadAll(io.LimitReader(startResp.Body, maxOutputBytes+1))
	if err != nil || len(out) > maxOutputBytes {
		return nil, ErrExecFailed
	}

	checkReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/exec/%s/json", m.baseURL, execCreated.ID), nil)
	if err != nil {
		return nil, ErrExecFailed
	}
	checkRes, err := m.httpClient.Do(checkReq)
	if err != nil {
		return nil, ErrExecFailed
	}

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(checkRes.Body)

	var result struct {
		Running  *bool
		ExitCode *int
	}
	if checkRes.StatusCode != 200 || json.NewDecoder(io.LimitReader(checkRes.Body, 4096)).Decode(&result) != nil || result.Running == nil || result.ExitCode == nil || *result.Running || *result.ExitCode != 0 {
		return nil, ErrExecFailed
	}
	return []byte(stripDockerLogHeaders(out)), nil
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream header from Docker stdout/stderr frames
func stripDockerLogHeaders(b []byte) string {
	var clean bytes.Buffer
	for len(b) >= 8 {
		frameSize := int(b[4])<<24 | int(b[5])<<16 | int(b[6])<<8 | int(b[7])
		if frameSize <= 0 || 8+frameSize > len(b) {
			clean.Write(b)
			break
		}
		clean.Write(b[8 : 8+frameSize])
		b = b[8+frameSize:]
	}
	return strings.TrimSpace(clean.String())
}
