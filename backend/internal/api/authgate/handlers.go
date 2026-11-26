package authgate

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/auth"
)

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (g *Gate) routes() {
	// Public: the SPA bootstrap, first-run setup, password sign-in and the external sign-in round trip.
	g.handle("GET /api/v1/auth/status", g.status)
	g.handle("POST /api/v1/auth/setup", g.setup)
	g.handle("POST /api/v1/auth/login", g.login)
	g.handle("GET /api/v1/auth/oauth/start", g.oauthStart)
	g.handle("GET /api/v1/auth/oauth/callback", g.oauthCallback)
	// Signed in.
	g.handle("POST /api/v1/auth/logout", g.signedIn(g.logout))
	g.handle("POST /api/v1/auth/password", g.signedIn(g.changePassword))
	g.handle("GET /api/v1/auth/sessions", g.signedIn(g.listSessions))
	g.handle("POST /api/v1/auth/sessions/revoke-others", g.signedIn(g.revokeOthers))
	g.handle("POST /api/v1/auth/sessions/{id}/revoke", g.signedIn(g.revokeSession))
	g.handle("GET /api/v1/auth/activity", g.signedIn(g.activity))
	g.handle("GET /api/v1/auth/providers", g.signedIn(g.listProviders))
	g.handle("POST /api/v1/auth/providers", g.signedIn(g.createProvider))
	g.handle("PUT /api/v1/auth/providers/{id}", g.signedIn(g.updateProvider))
	g.handle("POST /api/v1/auth/providers/{id}/delete", g.signedIn(g.deleteProvider))
}

func (g *Gate) handle(pattern string, h http.HandlerFunc) {
	g.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		middleware.SetRoute(r.Context(), pattern)
		h(w, r)
	})
}

type sessionHandler func(http.ResponseWriter, *http.Request, *auth.Session)

func (g *Gate) signedIn(h sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := g.requireSession(w, r)
		if !ok {
			return
		}
		r = g.withSession(r, sess)
		h(w, r, sess)
	}
}

// decode requires a JSON content type on every body. Browsers cannot send that
// cross-site without a CORS preflight, which the host/origin policy refuses.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		response.WriteCustomError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Send the request as application/json.", nil, false)
		return false
	}
	return request.DecodeObject(w, r, dst, request.Options{MaxBytes: 16 * 1024})
}

// ---------------------------------------------------------------------------
// Session payloads
// ---------------------------------------------------------------------------

type userView struct {
	Username string `json:"username"`
	Method   string `json:"method"`
	// Provider is the display name of the external provider for "oauth" sessions.
	Provider string `json:"provider,omitempty"`
}

type sessionView struct {
	User      userView  `json:"user"`
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
}

func viewOf(s *auth.Session) sessionView {
	return sessionView{User: userView{Username: s.Username, Method: s.Method, Provider: s.ProviderName}, CSRF: s.CSRF, ExpiresAt: s.ExpiresAt.UTC()}
}

type statusView struct {
	SetupRequired bool                  `json:"setup_required"`
	Authenticated bool                  `json:"authenticated"`
	Session       *sessionView          `json:"session,omitempty"`
	Providers     []auth.PublicProvider `json:"providers"`
}

// status is the SPA's single bootstrap call: which screen to show and, when
// signed in, the CSRF value for writes.
func (g *Gate) status(w http.ResponseWriter, r *http.Request) {
	exists, err := g.svc.AdminExists(r.Context())
	if err != nil {
		writeAuthError(w, err)
		return
	}
	out := statusView{SetupRequired: !exists, Providers: []auth.PublicProvider{}}
	if exists {
		out.Providers = g.svc.PublicProviders(r.Context())
		if cookie, cerr := r.Cookie(SessionCookie); cerr == nil && cookie.Value != "" {
			sess, aerr := g.svc.Authenticate(r.Context(), cookie.Value)
			switch {
			case aerr == nil:
				v := viewOf(sess)
				out.Authenticated, out.Session = true, &v
			case errors.Is(aerr, auth.ErrSessionInvalid):
				g.clearSessionCookie(w, r)
			default:
				writeAuthError(w, aerr)
				return
			}
		}
	}
	response.WriteJSON(w, http.StatusOK, out)
}

type setupRequest struct {
	SetupToken string `json:"setup_token"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

func (g *Gate) setup(w http.ResponseWriter, r *http.Request) {
	var in setupRequest
	if !decode(w, r, &in) {
		return
	}
	issued, err := g.svc.Setup(r.Context(), auth.SetupInput{SetupToken: in.SetupToken, Username: in.Username, Password: in.Password}, g.client(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	log.Printf("INFO auth: administrator %q created through first-run setup", issued.Session.Username)
	g.setSessionCookie(w, r, issued)
	response.WriteJSON(w, http.StatusOK, viewOf(&issued.Session))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (g *Gate) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Username == "" || in.Password == "" || len(in.Username) > 64 || len(in.Password) > 4*auth.MaxPasswordLength {
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "ERR_VALIDATION_FAILED", "Enter your username and password.", nil, false)
		return
	}
	// Signing in replaces whatever session this browser held.
	if old, err := r.Cookie(SessionCookie); err == nil {
		_ = g.svc.Logout(r.Context(), old.Value, g.client(r))
	}
	issued, err := g.svc.Login(r.Context(), in.Username, in.Password, g.client(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	g.setSessionCookie(w, r, issued)
	response.WriteJSON(w, http.StatusOK, viewOf(&issued.Session))
}

func (g *Gate) logout(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	cookie, _ := r.Cookie(SessionCookie)
	if err := g.svc.Logout(r.Context(), cookie.Value, g.client(r)); err != nil {
		writeAuthError(w, err)
		return
	}
	g.clearSessionCookie(w, r)
	response.WriteJSON(w, http.StatusOK, map[string]bool{"signed_out": true})
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (g *Gate) changePassword(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	var in passwordRequest
	if !decode(w, r, &in) {
		return
	}
	issued, err := g.svc.ChangePassword(r.Context(), in.CurrentPassword, in.NewPassword, g.client(r))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			response.WriteCustomError(w, http.StatusUnprocessableEntity, "AUTH_INVALID_CREDENTIALS", "The current password is incorrect.", map[string]string{"current_password": "The current password is incorrect."}, false)
			return
		}
		writeAuthError(w, err)
		return
	}
	g.setSessionCookie(w, r, issued)
	response.WriteJSON(w, http.StatusOK, viewOf(&issued.Session))
}

func (g *Gate) listSessions(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	list, err := g.svc.ListSessions(r.Context(), sess.ID)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (g *Gate) revokeOthers(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	n, err := g.svc.RevokeOtherSessions(r.Context(), sess.ID, g.client(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}

func (g *Gate) revokeSession(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	err := g.svc.RevokeSession(r.Context(), r.PathValue("id"), sess.ID, g.client(r))
	switch {
	case errors.Is(err, auth.ErrCurrentSession):
		response.WriteCustomError(w, http.StatusConflict, "AUTH_CURRENT_SESSION", "This is the session you are using. Sign out instead.", nil, false)
	case errors.Is(err, auth.ErrSessionInvalid):
		response.WriteCustomError(w, http.StatusNotFound, "AUTH_SESSION_NOT_FOUND", "That session has already ended.", nil, false)
	case err != nil:
		writeAuthError(w, err)
	default:
		response.WriteJSON(w, http.StatusOK, map[string]bool{"revoked": true})
	}
}

func (g *Gate) activity(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	events, err := g.svc.RecentEvents(r.Context(), 25)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]any{"items": events})
}

// ---------------------------------------------------------------------------
// External sign-in settings
// ---------------------------------------------------------------------------

type providerList struct {
	Items []auth.ProviderSettings `json:"items"`
}

func (g *Gate) listProviders(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	list, err := g.svc.Providers(r.Context())
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writePlain(w, http.StatusOK, providerList{Items: list})
}

// providerRequest creates or replaces one provider. Changing how the
// administrator can sign in re-verifies the password first.
type providerRequest struct {
	CurrentPassword   string   `json:"current_password"`
	Kind              string   `json:"kind"`
	DisplayName       string   `json:"display_name"`
	Issuer            string   `json:"issuer"`
	AuthorizeURL      string   `json:"authorize_url"`
	TokenURL          string   `json:"token_url"`
	UserinfoURL       string   `json:"userinfo_url"`
	ClientID          string   `json:"client_id"`
	ClientSecret      string   `json:"client_secret"`
	ClearClientSecret bool     `json:"clear_client_secret"`
	Scopes            []string `json:"scopes"`
	IdentityClaim     string   `json:"identity_claim"`
	AllowedIdentities []string `json:"allowed_identities"`
	RedirectURL       string   `json:"redirect_url"`
}

func (g *Gate) createProvider(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	g.saveProvider(w, r, "")
}

func (g *Gate) updateProvider(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	id := r.PathValue("id")
	if !auth.ValidProviderID(id) {
		writeAuthError(w, auth.ErrProviderNotConfigured)
		return
	}
	g.saveProvider(w, r, id)
}

func (g *Gate) saveProvider(w http.ResponseWriter, r *http.Request, id string) {
	var in providerRequest
	if !decode(w, r, &in) {
		return
	}
	if !g.reauthenticate(w, r, in.CurrentPassword) {
		return
	}
	saved, err := g.svc.SaveProvider(r.Context(), id, auth.ProviderInput{
		ProviderSettings: auth.ProviderSettings{
			Kind: in.Kind, DisplayName: in.DisplayName, Issuer: in.Issuer,
			AuthorizeURL: in.AuthorizeURL, TokenURL: in.TokenURL, UserinfoURL: in.UserinfoURL, ClientID: in.ClientID,
			Scopes: in.Scopes, IdentityClaim: in.IdentityClaim, AllowedIdentities: in.AllowedIdentities, RedirectURL: in.RedirectURL,
		},
		ClientSecret: in.ClientSecret, ClearClientSecret: in.ClearClientSecret,
	}, g.client(r))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writePlain(w, status, saved)
}

type deleteProviderRequest struct {
	CurrentPassword string `json:"current_password"`
}

func (g *Gate) deleteProvider(w http.ResponseWriter, r *http.Request, _ *auth.Session) {
	var in deleteProviderRequest
	if !decode(w, r, &in) {
		return
	}
	if !g.reauthenticate(w, r, in.CurrentPassword) {
		return
	}
	if err := g.svc.DeleteProvider(r.Context(), r.PathValue("id"), g.client(r)); err != nil {
		writeAuthError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// writePlain skips the generic credential-name redaction: field names such
// as token_url are endpoint addresses, and the client secret is never part of
// the view (only credential_stored says whether one exists).
func writePlain(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		response.WriteError(w, response.ErrInternal("Response encoding failed", err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func (g *Gate) reauthenticate(w http.ResponseWriter, r *http.Request, password string) bool {
	if password == "" {
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "ERR_VALIDATION_FAILED", "Confirm with your current password.", map[string]string{"current_password": "Enter your current password."}, false)
		return false
	}
	err := g.svc.VerifyPassword(r.Context(), password, g.client(r))
	if errors.Is(err, auth.ErrInvalidCredentials) {
		response.WriteCustomError(w, http.StatusUnprocessableEntity, "AUTH_INVALID_CREDENTIALS", "The current password is incorrect.", map[string]string{"current_password": "The current password is incorrect."}, false)
		return false
	}
	if err != nil {
		writeAuthError(w, err)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// External sign-in round trip (top-level browser navigations, not XHR)
// ---------------------------------------------------------------------------

// signInRedirect sends the browser back to the SPA sign-in screen with a
// short, non-sensitive error code. Relative so it works behind any proxy.
func signInRedirect(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/#/login?"+url.Values{"error": {code}}.Encode(), http.StatusFound)
}

func (g *Gate) oauthStart(w http.ResponseWriter, r *http.Request) {
	redirect, browser, err := g.svc.BeginOAuth(r.Context(), r.URL.Query().Get("provider"), g.client(r))
	if err != nil {
		signInRedirect(w, r, oauthCode(err))
		return
	}
	g.setBrowserCookie(w, r, browser)
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (g *Gate) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	browser := ""
	if c, err := r.Cookie(browserCookie); err == nil {
		browser = c.Value
	}
	g.clearBrowserCookie(w, r)
	if q.Get("error") != "" {
		signInRedirect(w, r, "provider_denied")
		return
	}
	issued, err := g.svc.CompleteOAuth(r.Context(), q.Get("state"), q.Get("code"), browser, g.client(r))
	if err != nil {
		var oe *auth.OAuthError
		if errors.As(err, &oe) {
			log.Printf("WARN auth: external sign-in refused code=%s reason=%q", oe.Code, oe.Reason)
		}
		signInRedirect(w, r, oauthCode(err))
		return
	}
	g.setSessionCookie(w, r, issued)
	http.Redirect(w, r, "/#/", http.StatusFound)
}

func oauthCode(err error) string {
	var oe *auth.OAuthError
	var rate *auth.RateLimitError
	switch {
	case errors.As(err, &oe):
		return oe.Code
	case errors.As(err, &rate):
		return "rate_limited"
	case errors.Is(err, auth.ErrSetupRequired):
		return "setup_required"
	default:
		log.Printf("ERROR auth: external sign-in failed: %v", err)
		return "server_error"
	}
}
