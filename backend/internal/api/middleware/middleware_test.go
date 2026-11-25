package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traefik-console/internal/api/response"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &logs
}
func TestRequestTraceSurvivesPanicAndMatchesError(t *testing.T) {
	logs := captureLogs(t)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Context().Value(traceContextKey{}).(*requestTrace).route = "GET /items/{name}"
		panic("synthetic-sensitive")
	}), Policy{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/items/private?token=synthetic-sensitive", nil))
	var got response.Error
	if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.OperationID == "" || !strings.Contains(logs.String(), "operation_id="+got.OperationID) || !strings.Contains(logs.String(), "status=500") || strings.Contains(logs.String(), "synthetic-sensitive") || strings.Contains(logs.String(), "/private") {
		t.Fatal(w.Code, w.Body.String(), logs.String())
	}
}
func TestPanicAfterCommitDoesNotAppendError(t *testing.T) {
	logs := captureLogs(t)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response.WriteJSON(w, 200, map[string]bool{"committed": true})
		panic("private")
	}), Policy{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api", nil))
	if w.Code != 200 || w.Body.String() != "{\"committed\":true}\n" || !strings.Contains(logs.String(), "ERR_PANIC_AFTER_COMMIT") || !strings.Contains(logs.String(), "status=200") {
		t.Fatal(w.Body.String(), logs.String())
	}
}

func TestPolicyIsFixedAtConstructionAndRetainsOrigins(t *testing.T) {
	policy := Policy{AllowedHost: "console.invalid:8081", AllowedOrigin: "http://ui.invalid"}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), policy)
	policy.AllowedOrigin = "http://changed.invalid"
	t.Setenv("CONSOLE_ORIGIN", "http://changed.invalid")
	for _, tc := range []struct {
		origin, site string
		want         int
	}{{"http://ui.invalid", "", 204}, {"http://changed.invalid", "", 403}, {"null", "", 403}, {"http://console.invalid:8081", "", 204}, {"http://ui.invalid", "cross-site", 204}, {"", "cross-site", 403}} {
		r := httptest.NewRequest("POST", "http://console.invalid:8081/api", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
	r := httptest.NewRequest("OPTIONS", "http://console.invalid:8081/api", nil)
	r.Header.Set("Origin", "http://ui.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "If-None-Match") {
		t.Fatal("preflight changed")
	}
}
func TestAdmissionUsesOperationBudgetOnlyForRealMutations(t *testing.T) {
	const applyTimeout = 120 * time.Second
	remaining := make(chan time.Duration, 1)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("deadline absent")
			return
		}
		remaining <- time.Until(deadline)
	}), Policy{ApplyTimeout: applyTimeout})

	for _, tc := range []struct {
		name, method, target string
		apply                bool
	}{
		{name: "read", method: http.MethodGet, target: "http://localhost/api/v1/crowdsec/config"},
		{name: "preview true", method: http.MethodPut, target: "http://localhost/api/v1/crowdsec/config?dry_run=true"},
		{name: "preview one", method: http.MethodPut, target: "http://localhost/api/v1/crowdsec/config?dry_run=1"},
		{name: "crowdsec apply", method: http.MethodPut, target: "http://localhost/api/v1/crowdsec/config", apply: true},
		{name: "recovery", method: http.MethodPost, target: "http://localhost/api/v1/operations/22/recover", apply: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.target, nil))
			got := <-remaining
			if tc.apply {
				if got <= requestTimeout || got > applyTimeout {
					t.Fatalf("real mutation did not receive apply budget: %v", got)
				}
			} else if got <= 0 || got > requestTimeout {
				t.Fatalf("read or preview received the wrong budget: %v", got)
			}
		})
	}
}

func TestAdmissionIsBoundedAndPreservesCancellation(t *testing.T) {
	entered := make(chan struct{}, requestCapacity)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	h := admissionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(204)
			return
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > requestTimeout {
			t.Error("deadline absent")
		}
		if r.URL.Path == "/cancelled" {
			if r.Context().Err() != context.Canceled {
				t.Error("cancellation lost")
			}
			return
		}
		entered <- struct{}{}
		<-release
	}), 0)
	var wg sync.WaitGroup
	for range requestCapacity {
		wg.Go(func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/api", nil)) })
	}
	for range requestCapacity {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("admission did not fill")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api", nil))
	if w.Code != 503 {
		t.Fatal("capacity unbounded", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/health", nil))
	if w.Code != 204 {
		t.Fatal("health admission changed")
	}
	once.Do(func() { close(release) })
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/cancelled", nil).WithContext(ctx))
}
