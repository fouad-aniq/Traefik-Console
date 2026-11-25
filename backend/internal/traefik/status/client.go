package status

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ObservationClient struct {
	traefikAPIURL string
	httpClient    *http.Client

	// One request to Traefik at a time: callers that arrive while it runs share its result (a
	// page loading middlewares, chains and routers at once used to see the third one rejected).
	// Nothing is kept once it completes: a later observation always makes a new request.
	mu       sync.Mutex
	inflight *rawFetch
}

type rawFetch struct {
	done chan struct{}
	body []byte
	err  error
}

func NewObservationClient(traefikAPIURL string) *ObservationClient {
	if traefikAPIURL == "" {
		traefikAPIURL = "http://127.0.0.1:8080"
	}
	return &ObservationClient{
		traefikAPIURL: strings.TrimSuffix(traefikAPIURL, "/"),
		httpClient: &http.Client{
			Timeout:       3 * time.Second,
			Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxResponseHeaderBytes: 16 * 1024, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second, MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

var ErrObservationUnavailable = errors.New("Traefik runtime observation unavailable")

// FetchRawData never returns a partial snapshot or transport details on failure.
func (oc *ObservationClient) FetchRawData(ctx context.Context) (map[string]any, error) {
	raw, err := oc.fetchRawData(ctx)
	if err != nil {
		return nil, ErrObservationUnavailable
	}
	return raw, nil
}

// fetchFreshRawData always makes its own request: verification after a write must never see a
// snapshot taken (or started) before the write.
func (oc *ObservationClient) fetchFreshRawData(ctx context.Context) (map[string]any, error) {
	body, err := oc.requestBody(ctx)
	if err != nil {
		return nil, ErrObservationUnavailable
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil || data == nil {
		return nil, ErrObservationUnavailable
	}
	return data, nil
}

// fetchRawData returns a bounded raw configuration snapshot. Each caller decodes its own copy,
// so a shared body is never mutated by another caller.
func (oc *ObservationClient) fetchRawData(ctx context.Context) (map[string]any, error) {
	body, err := oc.sharedBody(ctx)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to decode rawdata: %w", err)
	}
	if data == nil {
		return nil, ErrObservationUnavailable
	}
	return data, nil
}

// sharedBody joins the request in flight or starts a new one. The request is detached from the first caller's cancellation (it is bounded by the
// HTTP client timeout); every caller still stops waiting when its own context ends.
func (oc *ObservationClient) sharedBody(ctx context.Context) ([]byte, error) {
	oc.mu.Lock()
	f := oc.inflight
	if f == nil {
		f = &rawFetch{done: make(chan struct{})}
		oc.inflight = f
		go func() {
			body, err := oc.requestBody(context.WithoutCancel(ctx))
			oc.mu.Lock()
			f.body, f.err = body, err
			oc.inflight = nil
			oc.mu.Unlock()
			close(f.done)
		}()
	}
	oc.mu.Unlock()
	select {
	case <-f.done:
		return f.body, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (oc *ObservationClient) requestBody(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf("%s/api/rawdata", oc.traefikAPIURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := oc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("traefik API unreachable: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("traefik API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4*1024*1024 {
		return nil, fmt.Errorf("traefik API response exceeds limit")
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("failed to decode rawdata")
	}
	return body, nil
}

func (oc *ObservationClient) Close() { oc.httpClient.CloseIdleConnections() }
