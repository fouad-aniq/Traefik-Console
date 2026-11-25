package authgate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/auth"
)

const password = "correct horse battery"

type harness struct {
	t       *testing.T
	handler http.Handler
	svc     *auth.Service
	cookie  *http.Cookie
	csrf    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	// t.TempDir follows the umask (0775 under umask 002); the store requires a private parent.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := auth.OpenStore(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := auth.NewService(store, auth.Config{IdleTimeout: time.Hour, MaxLifetime: 2 * time.Hour, SecretKey: make([]byte, 32)})
	gate := New(svc, Options{})
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, _ := r.Context().Value(middleware.ActorContextKey).(string)
		w.Header().Set("X-Actor", actor)
		w.WriteHeader(http.StatusOK)
	})
	return &harness{t: t, svc: svc, handler: middleware.Chain(api, middleware.Policy{Gate: gate.Wrap})}
}

func (h *harness) do(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:8081"+path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:5000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.cookie != nil {
		req.AddCookie(h.cookie)
	}
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req.WithContext(context.Background()))
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			if c.MaxAge < 0 {
				h.cookie = nil
			} else {
				h.cookie = c
			}
		}
	}
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func (h *harness) setup() {
	h.t.Helper()
	token, err := h.svc.PrepareSetup(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	rec := h.do("POST", "/api/v1/auth/setup", `{"setup_token":"`+token+`","username":"admin","password":"`+password+`"}`, nil)
	if rec.Code != 200 || h.cookie == nil {
		h.t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	h.csrf = decodeBody(h.t, rec)["csrf"].(string)
}

func TestFirstRunFlow(t *testing.T) {
	h := newHarness(t)
	status := decodeBody(t, h.do("GET", "/api/v1/auth/status", "", nil))
	if status["setup_required"] != true || status["authenticated"] != false {
		t.Fatalf("status before setup %v", status)
	}
	// Login is impossible until setup is complete.
	if rec := h.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "AUTH_SETUP_REQUIRED") {
		t.Fatalf("login before setup: %d %s", rec.Code, rec.Body)
	}
	// Setup without the server-log token is refused.
	if rec := h.do("POST", "/api/v1/auth/setup", `{"setup_token":"guess","username":"admin","password":"`+password+`"}`, nil); rec.Code != 401 {
		t.Fatalf("setup without token: %d", rec.Code)
	}
	h.setup()
	if c := h.cookie; !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("session cookie attributes %+v", c)
	}
	status = decodeBody(t, h.do("GET", "/api/v1/auth/status", "", nil))
	if status["setup_required"] != false || status["authenticated"] != true {
		t.Fatalf("status after setup %v", status)
	}
	// Setup can never run again, signed in or not.
	if rec := h.do("POST", "/api/v1/auth/setup", `{"setup_token":"x","username":"evil","password":"`+password+`"}`, nil); rec.Code != 409 {
		t.Fatalf("second setup: %d", rec.Code)
	}
	h.cookie = nil
	if rec := h.do("POST", "/api/v1/auth/setup", `{"setup_token":"x","username":"evil","password":"`+password+`"}`, nil); rec.Code != 409 {
		t.Fatalf("second setup anonymous: %d", rec.Code)
	}
}

func TestProtectedRoutes(t *testing.T) {
	h := newHarness(t)
	if rec := h.do("GET", "/api/v1/health", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"session"`) {
		t.Fatalf("health must stay public: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/api/v1/routers", "", nil); rec.Code != 401 {
		t.Fatalf("anonymous read: %d", rec.Code)
	}
	h.setup()
	rec := h.do("GET", "/api/v1/routers", "", nil)
	if rec.Code != 200 || rec.Header().Get("X-Actor") != "admin" {
		t.Fatalf("signed-in read: %d actor %q", rec.Code, rec.Header().Get("X-Actor"))
	}
	if rec = h.do("PUT", "/api/v1/routers/web", `{}`, nil); rec.Code != 403 || !strings.Contains(rec.Body.String(), "CSRF_TOKEN_INVALID") {
		t.Fatalf("write without CSRF: %d %s", rec.Code, rec.Body)
	}
	if rec = h.do("PUT", "/api/v1/routers/web", `{}`, map[string]string{"X-CSRF-Token": strings.Repeat("0", len(h.csrf))}); rec.Code != 403 {
		t.Fatalf("write with wrong CSRF: %d", rec.Code)
	}
	if rec = h.do("PUT", "/api/v1/routers/web", `{}`, map[string]string{"X-CSRF-Token": h.csrf}); rec.Code != 200 {
		t.Fatalf("write with CSRF: %d %s", rec.Code, rec.Body)
	}
	// Logging out ends the session server-side.
	stolen := h.cookie
	if rec = h.do("POST", "/api/v1/auth/logout", "", map[string]string{"X-CSRF-Token": h.csrf}); rec.Code != 200 || h.cookie != nil {
		t.Fatalf("logout: %d", rec.Code)
	}
	h.cookie = stolen
	if rec = h.do("GET", "/api/v1/routers", "", nil); rec.Code != 401 {
		t.Fatalf("revoked cookie still works: %d", rec.Code)
	}
}

func TestLoginRequiresJSONAndCorrectPassword(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.cookie = nil
	if rec := h.do("POST", "/api/v1/auth/login", `username=admin&password=x`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); rec.Code != 415 {
		t.Fatalf("form post accepted: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong password here"}`, nil); rec.Code != 401 || !strings.Contains(rec.Body.String(), "AUTH_INVALID_CREDENTIALS") {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	rec := h.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, nil)
	if rec.Code != 200 || h.cookie == nil {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["csrf"] == "" || body["user"].(map[string]any)["username"] != "admin" {
		t.Fatalf("login body %v", body)
	}
}

func TestProviderSettingsRequirePassword(t *testing.T) {
	h := newHarness(t)
	h.setup()
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	if rec := h.do("GET", "/api/v1/auth/providers", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty providers: %d %s", rec.Code, rec.Body)
	}
	body := `{"kind":"oauth2","display_name":"GitHub","client_id":"id","client_secret":"sec","identity_claim":"login",
		"authorize_url":"https://github.com/login/oauth/authorize","token_url":"https://github.com/login/oauth/access_token","userinfo_url":"https://api.github.com/user",
		"allowed_identities":["octocat"],"redirect_url":"https://console.example.com/api/v1/auth/oauth/callback","current_password":"%s"}`
	if rec := h.do("POST", "/api/v1/auth/providers", strings.Replace(body, "%s", "wrong password!!", 1), csrf); rec.Code != 422 || !strings.Contains(rec.Body.String(), "current_password") {
		t.Fatalf("wrong password accepted: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/auth/providers", strings.Replace(body, "%s", password, 1), nil); rec.Code != 403 {
		t.Fatalf("create without CSRF: %d", rec.Code)
	}
	rec := h.do("POST", "/api/v1/auth/providers", strings.Replace(body, "%s", password, 1), csrf)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	got := rec.Body.String()
	if strings.Contains(got, `"sec"`) || !strings.Contains(got, `"credential_stored":true`) || !strings.Contains(got, `"id":"github"`) || !strings.Contains(got, `"token_url":"https://github.com/login/oauth/access_token"`) {
		t.Fatalf("provider view %s", got)
	}
	// Every configured provider becomes a sign-in button.
	status := decodeBody(t, h.do("GET", "/api/v1/auth/status", "", nil))
	if buttons := status["providers"].([]any); len(buttons) != 1 || buttons[0].(map[string]any)["display_name"] != "GitHub" {
		t.Fatalf("status providers %v", status["providers"])
	}
	update := strings.Replace(strings.Replace(body, "%s", password, 1), `"display_name":"GitHub"`, `"display_name":"GitHub SSO"`, 1)
	if rec = h.do("PUT", "/api/v1/auth/providers/github", update, csrf); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"GitHub SSO"`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if rec = h.do("PUT", "/api/v1/auth/providers/missing", update, csrf); rec.Code != 404 {
		t.Fatalf("update unknown: %d", rec.Code)
	}
	if rec = h.do("POST", "/api/v1/auth/providers/github/delete", `{"current_password":"`+password+`"}`, csrf); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	status = decodeBody(t, h.do("GET", "/api/v1/auth/status", "", nil))
	if len(status["providers"].([]any)) != 0 {
		t.Fatalf("deleted provider still offered: %v", status["providers"])
	}
}

func TestRevokeSingleSession(t *testing.T) {
	h := newHarness(t)
	h.setup()
	mine, csrf := h.cookie, map[string]string{"X-CSRF-Token": h.csrf}
	h.cookie = nil
	h.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, nil)
	other := h.cookie
	h.cookie = mine
	list := decodeBody(t, h.do("GET", "/api/v1/auth/sessions", "", nil))["items"].([]any)
	var otherID, myID string
	for _, item := range list {
		m := item.(map[string]any)
		if m["current"] == true {
			myID = m["id"].(string)
		} else {
			otherID = m["id"].(string)
		}
	}
	if rec := h.do("POST", "/api/v1/auth/sessions/"+myID+"/revoke", "", csrf); rec.Code != 409 {
		t.Fatalf("revoking own session: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/sessions/"+otherID+"/revoke", "", csrf); rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	h.cookie = other
	if rec := h.do("GET", "/api/v1/routers", "", nil); rec.Code != 401 {
		t.Fatalf("revoked session still works: %d", rec.Code)
	}
}

func TestOAuthCallbackPassesPolicyFromProvider(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.cookie = nil
	// A provider redirect is a cross-site top-level navigation; it must reach the
	// gate (which then rejects the unknown state with a redirect, not a 403).
	rec := h.do("GET", "/api/v1/auth/oauth/callback?state=x&code=y", "", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/#/login?error=invalid_state") {
		t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A cross-site sign-in attempt from a foreign page stays blocked.
	if rec = h.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+password+`"}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}); rec.Code != 403 {
		t.Fatalf("cross-site login: %d", rec.Code)
	}
	// Starting without a configured provider returns to the sign-in page.
	if rec = h.do("GET", "/api/v1/auth/oauth/start?provider=nope", "", nil); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "error=not_configured") {
		t.Fatalf("start: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestTrustedProxyAddress(t *testing.T) {
	_, network, _ := netParse("10.0.0.0/8")
	g := &Gate{opts: Options{TrustedProxies: network}}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.1.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.2.2.2")
	if got := g.client(r).IP; got != "203.0.113.9" {
		t.Fatalf("trusted proxy chain: %s", got)
	}
	r.RemoteAddr = "198.51.100.1:1234"
	if got := g.client(r).IP; got != "198.51.100.1" {
		t.Fatalf("untrusted peer forwarded header believed: %s", got)
	}
}
