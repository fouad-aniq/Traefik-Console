package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a minimal OpenID Connect provider: discovery, JWKS, token
// endpoint with PKCE and client_secret_basic checks.
type fakeIdP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	codes    map[string]codeGrant
	email    string
	verified bool
	badNonce bool
	audience string
}

type codeGrant struct{ challenge, nonce string }

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key, codes: map[string]codeGrant{}, email: "owner@example.com", verified: true}
	mux := http.NewServeMux()
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	idp.audience = "console-client"
	issuer := idp.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
			"jwks_uri": issuer + "/keys", "token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "console-client" || secret != "s3cret" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		r.ParseForm()
		idp.mu.Lock()
		grant, found := idp.codes[r.Form.Get("code")]
		delete(idp.codes, r.Form.Get("code"))
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !found || base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge || r.Form.Get("redirect_uri") != "http://localhost:5173/api/v1/auth/oauth/callback" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		nonce := grant.nonce
		if idp.badNonce {
			nonce = "forged"
		}
		now := time.Now().Unix()
		token := idp.sign(t, map[string]any{
			"iss": issuer, "sub": "user-1", "aud": idp.audience, "exp": now + 300, "iat": now,
			"nonce": nonce, "email": idp.email, "email_verified": idp.verified,
		})
		json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "id_token": token, "token_type": "Bearer"})
	})
	return idp
}

func (idp *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize plays the browser + provider consent: it records the challenge
// and returns the code the provider would send to the callback.
func (idp *fakeIdP) authorize(t *testing.T, redirect string) (state, code string) {
	u, err := url.Parse(redirect)
	if err != nil || !strings.HasPrefix(redirect, idp.srv.URL+"/authorize?") {
		t.Fatalf("unexpected redirect %q", redirect)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != "console-client" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorization request is missing PKCE/OIDC parameters: %v", q)
	}
	code = randomToken(8)
	idp.mu.Lock()
	idp.codes[code] = codeGrant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	idp.mu.Unlock()
	return q.Get("state"), code
}

func configureOIDC(t *testing.T, svc *Service, idp *fakeIdP, id string) string {
	t.Helper()
	saved, err := svc.SaveProvider(context.Background(), id, ProviderInput{
		ProviderSettings: ProviderSettings{
			Kind: KindOIDC, DisplayName: "Test SSO", Issuer: idp.srv.URL, ClientID: "console-client",
			Scopes: []string{"email profile"}, AllowedIdentities: []string{"Owner@Example.com"},
			RedirectURL: "http://localhost:5173/api/v1/auth/oauth/callback",
		},
		ClientSecret: "s3cret",
	}, Client{})
	if err != nil {
		t.Fatal(err)
	}
	return saved.ID
}

func TestOIDCSignIn(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, svc)
	idp := newFakeIdP(t)
	id := configureOIDC(t, svc, idp, "")
	if id != "test-sso" {
		t.Fatalf("provider id %q", id)
	}

	p, _ := svc.Provider(ctx, id)
	if !p.CredentialStored || p.IdentityClaim != "email" || p.Scopes[0] != "openid" {
		t.Fatalf("normalized settings %+v", p)
	}
	if pub := svc.PublicProviders(ctx); len(pub) != 1 || pub[0].DisplayName != "Test SSO" || pub[0].ID != id {
		t.Fatalf("public providers %+v", pub)
	}

	redirect, browser, err := svc.BeginOAuth(ctx, id, Client{IP: "4.4.4.4"})
	if err != nil {
		t.Fatal(err)
	}
	state, code := idp.authorize(t, redirect)
	issued, err := svc.CompleteOAuth(ctx, state, code, browser, Client{IP: "4.4.4.4"})
	if err != nil {
		t.Fatalf("sign-in failed: %v", err)
	}
	sess, err := svc.Authenticate(ctx, issued.Token)
	if err != nil || sess.Method != "oauth" || sess.Username != "admin" || sess.Provider != id || sess.ProviderName != "Test SSO" {
		t.Fatalf("session %+v %v", sess, err)
	}
	// A state can be used once.
	if _, err = svc.CompleteOAuth(ctx, state, code, browser, Client{IP: "4.4.4.5"}); !isOAuthCode(err, "invalid_state") {
		t.Fatalf("replayed state: %v", err)
	}
}

func TestOIDCRejections(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, svc)
	idp := newFakeIdP(t)
	id := configureOIDC(t, svc, idp, "")

	attempt := func(mutate func(), browserOverride string) error {
		redirect, browser, err := svc.BeginOAuth(ctx, id, Client{IP: randomToken(4)})
		if err != nil {
			t.Fatal(err)
		}
		state, code := idp.authorize(t, redirect)
		mutate()
		if browserOverride != "" {
			browser = browserOverride
		}
		_, err = svc.CompleteOAuth(ctx, state, code, browser, Client{IP: randomToken(4)})
		return err
	}
	reset := func() {
		idp.email, idp.verified, idp.badNonce, idp.audience = "owner@example.com", true, false, "console-client"
	}

	cases := []struct {
		name    string
		mutate  func()
		browser string
		code    string
	}{
		{"identity not on the allow list", func() { idp.email = "intruder@example.com" }, "", "not_allowed"},
		{"unverified email", func() { idp.verified = false }, "", "no_identity"},
		{"forged nonce", func() { idp.badNonce = true }, "", "invalid_token"},
		{"token for another client", func() { idp.audience = "other-client" }, "", "invalid_token"},
		{"callback in another browser", func() {}, "someone-else", "invalid_state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset()
			if err := attempt(tc.mutate, tc.browser); !isOAuthCode(err, tc.code) {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
	reset()

	// Changing the settings invalidates sign-ins already in progress.
	redirect, browser, _ := svc.BeginOAuth(ctx, id, Client{})
	state, code := idp.authorize(t, redirect)
	configureOIDC(t, svc, idp, id)
	if _, err := svc.CompleteOAuth(ctx, state, code, browser, Client{}); !isOAuthCode(err, "invalid_state") {
		t.Fatalf("stale flow: %v", err)
	}

	// Removing the provider ends sessions opened through it.
	redirect, browser, _ = svc.BeginOAuth(ctx, id, Client{})
	state, code = idp.authorize(t, redirect)
	issued, err := svc.CompleteOAuth(ctx, state, code, browser, Client{})
	if err != nil {
		t.Fatal(err)
	}
	password, _ := svc.Login(ctx, "admin", goodPassword, Client{})
	if err = svc.DeleteProvider(ctx, id, Client{}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(ctx, issued.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("external session survived provider removal")
	}
	if _, err = svc.Authenticate(ctx, password.Token); err != nil {
		t.Fatal("password session ended with the provider")
	}
	if _, _, err = svc.BeginOAuth(ctx, id, Client{}); !isOAuthCode(err, "not_configured") {
		t.Fatalf("begin without provider: %v", err)
	}
}

func isOAuthCode(err error, code string) bool {
	var oe *OAuthError
	return errors.As(err, &oe) && oe.Code == code
}

func TestProviderValidation(t *testing.T) {
	base := ProviderSettings{
		Kind: KindOIDC, DisplayName: "SSO", Issuer: "https://id.example.com", ClientID: "c",
		AllowedIdentities: []string{"a@example.com"}, RedirectURL: "https://console.example.com/api/v1/auth/oauth/callback",
	}
	if _, err := normalizeProvider(base); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	cases := map[string]func(*ProviderSettings){
		"issuer":             func(p *ProviderSettings) { p.Issuer = "http://id.example.com" },
		"redirect_url":       func(p *ProviderSettings) { p.RedirectURL = "https://console.example.com/callback" },
		"allowed_identities": func(p *ProviderSettings) { p.AllowedIdentities = []string{" ", ""} },
		"kind":               func(p *ProviderSettings) { p.Kind = "saml" },
		"display_name":       func(p *ProviderSettings) { p.DisplayName = "" },
	}
	for field, mutate := range cases {
		p := base
		mutate(&p)
		_, err := normalizeProvider(p)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields[field] == "" {
			t.Errorf("%s: expected a field error, got %v", field, err)
		}
	}
	oauth2 := base
	oauth2.Kind, oauth2.Issuer, oauth2.IdentityClaim = KindOAuth2, "", "login"
	oauth2.AuthorizeURL, oauth2.TokenURL, oauth2.UserinfoURL = "https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token", "https://api.github.com/user"
	if _, err := normalizeProvider(oauth2); err != nil {
		t.Fatalf("GitHub-style settings rejected: %v", err)
	}
	local := base
	local.Issuer, local.RedirectURL = "http://localhost:8080/realms/home", "http://127.0.0.1:8081/api/v1/auth/oauth/callback"
	if _, err := normalizeProvider(local); err != nil {
		t.Fatalf("loopback development settings rejected: %v", err)
	}
}

func TestOAuth2SecretRequiredAndSealed(t *testing.T) {
	svc, store := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, svc)
	in := ProviderInput{ProviderSettings: ProviderSettings{
		Kind: KindOAuth2, DisplayName: "GitHub", ClientID: "id", IdentityClaim: "login",
		AuthorizeURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", UserinfoURL: "https://api.github.com/user",
		AllowedIdentities: []string{"octocat"}, RedirectURL: "https://console.example.com/api/v1/auth/oauth/callback",
	}}
	var ve *ValidationError
	if _, err := svc.SaveProvider(ctx, "", in, Client{}); !errors.As(err, &ve) || ve.Fields["client_secret"] == "" {
		t.Fatalf("missing secret accepted: %v", err)
	}
	in.ClientSecret = "very-secret-value"
	saved, err := svc.SaveProvider(ctx, "", in, Client{})
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	store.db.QueryRow(`SELECT sealed_client_secret FROM oauth_providers`).Scan(&sealed)
	if len(sealed) == 0 || strings.Contains(string(sealed), "very-secret-value") {
		t.Fatal("client secret stored in clear")
	}
	// Saving again without a secret keeps the stored one.
	in.ClientSecret = ""
	in.DisplayName = "GitHub SSO"
	if p, err := svc.SaveProvider(ctx, saved.ID, in, Client{}); err != nil || !p.CredentialStored || p.ID != "github" {
		t.Fatalf("secret lost on update: %v", err)
	}
}

func TestJWTAlgorithms(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","kid":"e"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x"}`))
	input := header + "." + payload
	digest := sha256.Sum256([]byte(input))
	r, s, _ := ecdsa.Sign(rand.Reader, ec, digest[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	key := jwk{Kty: "EC", Kid: "e", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(ec.X.FillBytes(make([]byte, 32))), Y: base64.RawURLEncoding.EncodeToString(ec.Y.FillBytes(make([]byte, 32)))}
	if err := verifySignature("ES256", key, []byte(input), sig); err != nil {
		t.Fatalf("valid ES256 rejected: %v", err)
	}
	sig[10] ^= 1
	if err := verifySignature("ES256", key, []byte(input), sig); err == nil {
		t.Fatal("tampered ES256 accepted")
	}
	for _, alg := range []string{"none", "HS256", ""} {
		if err := verifySignature(alg, key, []byte(input), nil); err == nil {
			t.Fatalf("%q accepted", alg)
		}
	}
}

func TestSeveralProviders(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	setupAdmin(t, svc)
	github := func(label string) ProviderInput {
		return ProviderInput{ClientSecret: "s", ProviderSettings: ProviderSettings{
			Kind: KindOAuth2, DisplayName: label, ClientID: "id", IdentityClaim: "login",
			AuthorizeURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", UserinfoURL: "https://api.github.com/user",
			AllowedIdentities: []string{"octocat"}, RedirectURL: "https://console.example.com/api/v1/auth/oauth/callback",
		}}
	}
	a, err := svc.SaveProvider(ctx, "", github("GitHub Work"), Client{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.SaveProvider(ctx, "", github("GitHub Home!"), Client{})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "github-work" || b.ID != "github-home" {
		t.Fatalf("ids %q %q", a.ID, b.ID)
	}
	var ve *ValidationError
	if _, err = svc.SaveProvider(ctx, "", github("github work"), Client{}); !errors.As(err, &ve) || ve.Fields["display_name"] == "" {
		t.Fatalf("duplicate label accepted: %v", err)
	}
	if pub := svc.PublicProviders(ctx); len(pub) != 2 || pub[0].ID != "github-work" {
		t.Fatalf("sign-in buttons %+v", pub)
	}
	if _, err = svc.SaveProvider(ctx, "nope", github("X"), Client{}); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("update of unknown provider: %v", err)
	}
	if got := uniqueProviderID("Ünïcode ☃ ID", nil); got != "n-code-id" {
		t.Fatalf("slug %q", got)
	}
	if got := uniqueProviderID("☃☃", nil); got != "provider" {
		t.Fatalf("empty slug %q", got)
	}
}
