package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/traefik/status"
)

func TestErrorAndHealthContracts(t *testing.T) {
	for _, tc := range []struct {
		e      error
		status int
	}{{files.ErrResourceConflict, 409}, {files.ErrSourceFileNotFound, 404}, {engine.ErrValidationFailed, 422}, {engine.ErrPreconditionRequired, 428}, {status.ErrObservationUnavailable, 502}, {errors.New("synthetic-sensitive"), 500}} {
		w := httptest.NewRecorder()
		response.WriteError(w, tc.e)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "synthetic-sensitive") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	h := (&Server{}).routes()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/health", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"authentication":"deferred"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestOriginPolicyDoesNotImplementAuthentication(t *testing.T) {
	handler := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(middleware.ActorContextKey) != "development" {
			t.Error("auth changed")
		}
		w.WriteHeader(204)
	}), middleware.Policy{AllowedOrigin: "http://localhost:5173"})
	for _, tc := range []struct {
		url, origin string
		status      int
	}{{"http://localhost:8081/api", "", 204}, {"http://localhost:8081/api", "http://localhost:5173", 204}, {"http://localhost:8081/api", "https://untrusted.invalid", 403}, {"http://untrusted.invalid/api", "", 403}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", tc.url, nil)
		r.Header.Set("Origin", tc.origin)
		handler.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Access-Control-Allow-Origin") == "*" {
			t.Fatal(w.Code)
		}
	}
}
func TestWriteJSONRedactsOpaquePluginAndCredentials(t *testing.T) {
	w := httptest.NewRecorder()
	response.WriteJSON(w, 200, map[string]any{"config": map[string]any{"type": "plugin", "spec": map[string]any{"license": "synthetic-canary"}}, "password": "synthetic-canary"})
	if strings.Contains(w.Body.String(), "synthetic-canary") {
		t.Fatal("response leaked credential")
	}
}

func TestJSONDecoderRejectsDuplicateKeysAndExcessDepth(t *testing.T) {
	for _, body := range []string{`{"value":1,"value":2}`, `{"value":` + strings.Repeat("[", 40) + "1" + strings.Repeat("]", 40) + "}"} {
		w := httptest.NewRecorder()
		var out map[string]any
		if request.DecodeObject(w, httptest.NewRequest("POST", "http://localhost/api", strings.NewReader(body)), &out, request.Options{MaxBytes: 1024}) || w.Code != 400 {
			t.Fatal("invalid JSON accepted")
		}
	}
}
