package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTraefik answers inspect with a StartedAt that moves forward once the restart took effect.
func fakeTraefik(t *testing.T, restart func(*int32) (*http.Response, error)) (*Client, *int32) {
	t.Helper()
	oldWindow, oldPoll := restartConfirmWindow, restartPollInterval
	restartConfirmWindow, restartPollInterval = 300*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { restartConfirmWindow, restartPollInterval = oldWindow, oldPoll })
	c, _ := New(registration("http://proxy:2375"))
	var restarted int32
	c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return restart(&restarted)
		}
		started := "2026-01-01T00:00:00Z"
		if atomic.LoadInt32(&restarted) > 0 {
			started = "2026-01-01T00:01:00Z"
		}
		raw, _ := json.Marshal(map[string]any{"Id": strings.Repeat("a", 64), "Name": "/traefik", "State": map[string]any{"Running": true, "StartedAt": started}})
		return response(200, string(raw)), nil
	})
	return c, &restarted
}

func TestRestartConfirmedEvenWhenDockerAnswersLate(t *testing.T) {
	// Docker restarts Traefik but its answer outlasts the client's wait (graceful stop up to 10s).
	c, _ := fakeTraefik(t, func(restarted *int32) (*http.Response, error) {
		go func() { time.Sleep(20 * time.Millisecond); atomic.StoreInt32(restarted, 1) }()
		return nil, errors.New("net/http: timeout awaiting response headers")
	})
	st, err := c.RestartTraefikContainerAndConfirm(context.Background())
	if err != nil || st == nil || !st.Running {
		t.Fatalf("a late answer must still confirm the restart: %v", err)
	}
}

func TestRestartConfirmedOnTheNormalAnswer(t *testing.T) {
	c, _ := fakeTraefik(t, func(restarted *int32) (*http.Response, error) {
		atomic.StoreInt32(restarted, 1)
		return response(204, ""), nil
	})
	if _, err := c.RestartTraefikContainerAndConfirm(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRestartRefusedOrNeverHappening(t *testing.T) {
	refused, _ := fakeTraefik(t, func(*int32) (*http.Response, error) { return response(403, ""), nil })
	start := time.Now()
	if _, err := refused.RestartTraefikContainerAndConfirm(context.Background()); !errors.Is(err, ErrRestartUnconfirmed) || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("a refusal fails at once: %v after %v", err, time.Since(start))
	}
	lost, _ := fakeTraefik(t, func(*int32) (*http.Response, error) { return nil, errors.New("connection reset") })
	if _, err := lost.RestartTraefikContainerAndConfirm(context.Background()); !errors.Is(err, ErrRestartUnconfirmed) {
		t.Fatalf("no new start within the window is unconfirmed: %v", err)
	}
}
