// Package authgate is the HTTP boundary of authentication. It serves the
// /api/v1/auth/* endpoints and requires a valid session (and, for writes, the
// session's CSRF token) on every other /api/v1 route except the liveness probe.
package authgate

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/auth"
)

const (
	SessionCookie = "tc_session"
	browserCookie = "tc_oauth"
	csrfHeader    = "X-CSRF-Token"
)

type sessionKey struct{}

// Options are fixed at startup.
type Options struct {
	// CookieSecure always marks cookies Secure (COOKIE_SECURE=true). Otherwise, with
	// CookieSecureAuto, a cookie is Secure when its request came over HTTPS: directly, or through a
	// proxy that says so (X-Forwarded-Proto). Believing the header is safe: it can only add Secure.
	CookieSecure     bool
	CookieSecureAuto bool
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed for
	// rate limiting and the audit log. Empty uses the TCP peer address only.
	TrustedProxies []*net.IPNet
}

// Gate wraps the API handler.
type Gate struct {
	svc  *auth.Service
	opts Options
	mux  *http.ServeMux
}

func New(svc *auth.Service, opts Options) *Gate {
	g := &Gate{svc: svc, opts: opts, mux: http.NewServeMux()}
	g.routes()
	return g
}

// Wrap returns the authenticated handler. Requests outside /api/v1 pass through unchanged.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/v1/health" && r.Method == http.MethodGet:
			middleware.SetRoute(r.Context(), "GET /api/v1/health")
			response.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "authentication": "session"})
		case path == "/api/v1/auth" || strings.HasPrefix(path, "/api/v1/auth/"):
			g.mux.ServeHTTP(w, r)
		case path == "/api/v1" || strings.HasPrefix(path, "/api/v1/"):
			sess, ok := g.requireSession(w, r)
			if !ok {
				return
			}
			next.ServeHTTP(w, g.withSession(r, sess))
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// requireSession resolves the session cookie and, for state-changing methods,
// checks the CSRF header. It writes the error response itself.
func (g *Gate) requireSession(w http.ResponseWriter, r *http.Request) (*auth.Session, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		response.WriteCustomError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in to continue.", nil, false)
		return nil, false
	}
	sess, err := g.svc.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		if !errors.Is(err, auth.ErrSessionInvalid) {
			response.WriteError(w, response.ErrInternal("Session check failed", err))
			return nil, false
		}
		g.clearSessionCookie(w, r)
		response.WriteCustomError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Your session has ended. Sign in again.", nil, false)
		return nil, false
	}
	if !safeMethod(r.Method) && !validCSRF(r, sess) {
		response.WriteCustomError(w, http.StatusForbidden, "CSRF_TOKEN_INVALID", "The request is missing its security token. Reload the page and retry.", nil, false)
		return nil, false
	}
	return sess, true
}

func (g *Gate) withSession(r *http.Request, sess *auth.Session) *http.Request {
	ctx := context.WithValue(r.Context(), sessionKey{}, sess)
	ctx = context.WithValue(ctx, middleware.ActorContextKey, sess.Username)
	middleware.SetActor(ctx, sess.Username)
	return r.WithContext(ctx)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func validCSRF(r *http.Request, sess *auth.Session) bool {
	got := r.Header.Get(csrfHeader)
	return got != "" && len(got) == len(sess.CSRF) && constantTimeEqual(got, sess.CSRF)
}

// ---------------------------------------------------------------------------
// Cookies
// ---------------------------------------------------------------------------

// secure tells whether this request's cookies are marked Secure.
func (g *Gate) secure(r *http.Request) bool {
	if g.opts.CookieSecure {
		return true
	}
	if !g.opts.CookieSecureAuto {
		return false
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(proto), "https")
}

func (g *Gate) setSessionCookie(w http.ResponseWriter, r *http.Request, issued *auth.Issued) {
	maxAge := int(time.Until(issued.Session.ExpiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: issued.Token, Path: "/", MaxAge: max(maxAge, 1),
		HttpOnly: true, Secure: g.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (g *Gate) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: g.secure(r), SameSite: http.SameSiteLaxMode})
}

func (g *Gate) setBrowserCookie(w http.ResponseWriter, r *http.Request, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: browserCookie, Value: value, Path: "/api/v1/auth/oauth", MaxAge: 600,
		HttpOnly: true, Secure: g.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (g *Gate) clearBrowserCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: browserCookie, Value: "", Path: "/api/v1/auth/oauth", MaxAge: -1, HttpOnly: true, Secure: g.secure(r), SameSite: http.SameSiteLaxMode})
}

// ---------------------------------------------------------------------------
// Client identity
// ---------------------------------------------------------------------------

// client derives the caller address. X-Forwarded-For is only believed when the
// TCP peer is a configured trusted proxy; the right-most untrusted hop wins.
func (g *Gate) client(r *http.Request) auth.Client {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && g.trusted(ip) {
		hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := net.ParseIP(strings.TrimSpace(hops[i]))
			if hop == nil {
				break
			}
			host = hop.String()
			if !g.trusted(hop) {
				break
			}
		}
	}
	return auth.Client{IP: host, UserAgent: r.UserAgent()}
}

func (g *Gate) trusted(ip net.IP) bool {
	for _, n := range g.opts.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func writeAuthError(w http.ResponseWriter, err error) {
	var rate *auth.RateLimitError
	var invalid *auth.ValidationError
	var unreachable *auth.ProviderUnreachableError
	switch {
	case errors.As(err, &rate):
		secs := int(rate.RetryAfter.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		response.WriteCustomError(w, http.StatusTooManyRequests, "AUTH_RATE_LIMITED", "Too many failed attempts. Try again in "+strconv.Itoa(secs)+" seconds.", nil, true)
	case errors.As(err, &invalid):
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "ERR_VALIDATION_FAILED", "Some settings need attention.", invalid.Fields, false)
	case errors.As(err, &unreachable):
		response.WriteCustomError(w, http.StatusBadGateway, "AUTH_PROVIDER_UNREACHABLE", "The identity provider could not be reached or its discovery document is invalid: "+unreachable.Err.Error(), map[string]string{"issuer": "Discovery failed: " + unreachable.Err.Error()}, true)
	case errors.Is(err, auth.ErrInvalidCredentials):
		response.WriteCustomError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "Incorrect username or password.", nil, false)
	case errors.Is(err, auth.ErrInvalidSetupToken):
		response.WriteCustomError(w, http.StatusUnauthorized, "AUTH_INVALID_SETUP_TOKEN", "The setup token is not valid. Restart Console to get a new one in its logs.", map[string]string{"setup_token": "The setup token is not valid."}, false)
	case errors.Is(err, auth.ErrSetupComplete):
		response.WriteCustomError(w, http.StatusConflict, "AUTH_SETUP_COMPLETE", "The administrator already exists. Sign in instead.", nil, false)
	case errors.Is(err, auth.ErrSetupRequired):
		response.WriteCustomError(w, http.StatusConflict, "AUTH_SETUP_REQUIRED", "Create the administrator first.", nil, false)
	case errors.Is(err, auth.ErrInvalidUsername):
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "ERR_VALIDATION_FAILED", err.Error(), map[string]string{"username": err.Error()}, false)
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong), errors.Is(err, auth.ErrSamePassword):
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "ERR_VALIDATION_FAILED", err.Error(), map[string]string{"password": err.Error()}, false)
	case errors.Is(err, auth.ErrProviderNotConfigured):
		response.WriteCustomError(w, http.StatusNotFound, "AUTH_PROVIDER_NOT_FOUND", "That sign-in provider does not exist.", nil, false)
	case errors.Is(err, auth.ErrTooManyProviders):
		response.WriteCustomError(w, http.StatusConflict, "AUTH_TOO_MANY_PROVIDERS", err.Error()+".", nil, false)
	case errors.Is(err, auth.ErrBusy):
		response.WriteCustomError(w, http.StatusServiceUnavailable, "AUTH_BUSY", "Sign-in is busy. Retry in a moment.", nil, true)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		response.WriteCustomError(w, http.StatusServiceUnavailable, "AUTH_TIMEOUT", "The request timed out. Retry.", nil, true)
	default:
		response.WriteError(w, response.ErrInternal("Authentication failed", err))
	}
}
