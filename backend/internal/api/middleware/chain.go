package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/traefik-console/internal/api/response"
)

type contextKey string

const ActorContextKey contextKey = "actor"
const requestCapacity = 8
const requestTimeout = 30 * time.Second

// Chain composes logging → host/origin policy → admission → authentication → routes.
// Without a Gate (unit tests only) every caller gets the explicit development actor;
// the server always installs the session gate.
func Chain(next http.Handler, policy Policy) http.Handler {
	inner := developmentContext(next)
	if policy.Gate != nil {
		inner = policy.Gate(next)
	}
	return requestMiddleware(policy.middleware(admissionMiddleware(inner, policy.ApplyTimeout)))
}

// This actor is an explicit development label, not authentication.
func developmentContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetActor(r.Context(), "development")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ActorContextKey, "development")))
	})
}

func admissionMiddleware(next http.Handler, applyTimeout time.Duration) http.Handler {
	slots := make(chan struct{}, requestCapacity)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			response.WriteCustomError(w, 503, "ERR_CAPACITY", "Request capacity reached", nil, true)
			return
		}
		timeout := requestTimeout
		isPreview := r.URL.Query().Get("dry_run") == "true" || r.URL.Query().Get("dry_run") == "1"
		if applyTimeout > 0 && !isPreview && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			timeout = applyTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
