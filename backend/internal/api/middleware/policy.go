package middleware

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/traefik-console/internal/api/response"
)

// Policy is copied at construction. Operator settings are never read from the environment per request.
type Policy struct {
	AllowedHost, AllowedOrigin string
	// ApplyTimeout covers a real mutation, including publication and runtime verification.
	// Zero retains the standard request timeout, which is useful for lightweight test servers.
	ApplyTimeout time.Duration
	// Gate authenticates requests. The server always sets it; nil keeps the
	// development actor for isolated handler tests.
	Gate func(http.Handler) http.Handler
}

// oauthCallbackPath is reached by a top-level navigation from the identity
// provider, which browsers label cross-site. It carries no ambient authority:
// the gate accepts it only with the single-use state bound to this browser.
const oauthCallbackPath = "/api/v1/auth/oauth/callback"

func (policy Policy) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		host, _, e := net.SplitHostPort(r.Host)
		if e != nil {
			host = r.Host
		}
		allowedHost := policy.AllowedHost
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && r.Host != allowedHost {
			response.WriteCustomError(w, 403, "ERR_HOST", "Unapproved request host", nil, false)
			return
		}
		origin := r.Header.Get("Origin")
		approved := policy.AllowedOrigin
		if origin != "" {
			u, e := url.Parse(origin)
			same := e == nil && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host
			if origin == "null" || (!same && origin != approved) {
				response.WriteCustomError(w, 403, "ERR_ORIGIN", "Unapproved request origin", nil, false)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		navigation := r.Method == http.MethodGet && r.URL.Path == oauthCallbackPath && origin == ""
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" && origin != approved && !navigation {
			response.WriteCustomError(w, 403, "ERR_ORIGIN", "Cross-site request denied", nil, false)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, If-Match, If-None-Match, X-CSRF-Token")
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
