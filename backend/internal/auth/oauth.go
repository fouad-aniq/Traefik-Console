package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// OAuthError is a failed external sign-in. Code is a stable, non-sensitive
// value the sign-in page can show; Reason is for the server log only.
type OAuthError struct {
	Code   string
	Reason string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Reason }

func oauthFail(code, format string, args ...any) error {
	return &OAuthError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

const flowTTL = 10 * time.Minute

type oauthFlow struct {
	browserHash string
	verifier    string
	nonce       string
	providerID  string
	revision    int64
	expires     time.Time
}

// flowTable holds pending sign-ins in memory. Entries are single-use, expire
// after ten minutes and are bounded so unauthenticated starts cannot grow memory.
type flowTable struct {
	mu    sync.Mutex
	flows map[string]oauthFlow
	now   func() time.Time
}

func newFlowTable() *flowTable { return &flowTable{flows: map[string]oauthFlow{}, now: time.Now} }

func (t *flowTable) put(stateHash string, f oauthFlow) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, v := range t.flows {
		if now.After(v.expires) {
			delete(t.flows, k)
		}
	}
	if len(t.flows) >= 64 {
		return false
	}
	t.flows[stateHash] = f
	return true
}

func (t *flowTable) take(stateHash string) (oauthFlow, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.flows[stateHash]
	delete(t.flows, stateHash)
	if !ok || t.now().After(f.expires) {
		return oauthFlow{}, false
	}
	return f, true
}

func (t *flowTable) clear() {
	t.mu.Lock()
	t.flows = map[string]oauthFlow{}
	t.mu.Unlock()
}

// dropProvider cancels pending sign-ins of one provider (its settings changed).
func (t *flowTable) dropProvider(id string) {
	t.mu.Lock()
	for k, f := range t.flows {
		if f.providerID == id {
			delete(t.flows, k)
		}
	}
	t.mu.Unlock()
}

// BeginOAuth starts an external sign-in. It returns the provider address to
// redirect to and a browser-binding value the caller must place in an
// HttpOnly cookie; the callback is accepted only from the same browser.
func (s *Service) BeginOAuth(ctx context.Context, providerID string, client Client) (redirect, browser string, err error) {
	if blocked, wait := s.byIP.blocked("oauth:" + client.IP); blocked {
		return "", "", &RateLimitError{RetryAfter: wait}
	}
	exists, err := s.AdminExists(ctx)
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", ErrSetupRequired
	}
	p, err := s.loadProvider(ctx, providerID)
	if err != nil {
		return "", "", oauthFail("not_configured", "unknown provider %q", providerID)
	}
	authorizeURL := p.AuthorizeURL
	if p.Kind == KindOIDC {
		doc, derr := s.httpc.discover(ctx, p.Issuer)
		if derr != nil {
			return "", "", oauthFail("provider_unreachable", "discovery: %v", derr)
		}
		authorizeURL = doc.AuthorizationEndpoint
	}
	state, verifier, nonce := randomToken(24), randomToken(32), randomToken(24)
	browser = randomToken(24)
	if !s.flows.put(hashToken(state), oauthFlow{browserHash: hashToken(browser), verifier: verifier, nonce: nonce, providerID: p.ID, revision: p.revision, expires: s.now().Add(flowTTL)}) {
		return "", "", oauthFail("busy", "too many pending external sign-ins")
	}
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", "", oauthFail("not_configured", "invalid authorization endpoint")
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURL)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	if p.Kind == KindOIDC {
		q.Set("nonce", nonce)
	}
	u.RawQuery = q.Encode()
	return u.String(), browser, nil
}

// CompleteOAuth finishes the sign-in at the callback: it checks the state and
// browser binding, redeems the code, verifies the identity and, when the
// identity is on the allow list, opens an administrator session.
func (s *Service) CompleteOAuth(ctx context.Context, state, code, browser string, client Client) (*Issued, error) {
	ipKey := "oauth:" + client.IP
	if blocked, wait := s.byIP.blocked(ipKey); blocked {
		return nil, &RateLimitError{RetryAfter: wait}
	}
	issued, identity, err := s.completeOAuth(ctx, state, code, browser, client)
	if err != nil {
		s.byIP.fail(ipKey)
		var oe *OAuthError
		if errors.As(err, &oe) {
			s.auditf(ctx, "login", "failure", client.IP, "external: "+oe.Code)
		}
		return nil, err
	}
	s.byIP.reset(ipKey)
	s.auditf(ctx, "login", "success", client.IP, issued.Session.ProviderName+": "+identity)
	return issued, nil
}

func (s *Service) completeOAuth(ctx context.Context, state, code, browser string, client Client) (*Issued, string, error) {
	if state == "" || len(state) > 128 || browser == "" || len(browser) > 128 {
		return nil, "", oauthFail("invalid_state", "missing state or browser binding")
	}
	flow, ok := s.flows.take(hashToken(state))
	if !ok {
		return nil, "", oauthFail("invalid_state", "unknown or expired state")
	}
	if !equalConstantTime(flow.browserHash, hashToken(browser)) {
		return nil, "", oauthFail("invalid_state", "callback arrived in a different browser")
	}
	if code == "" || len(code) > 4096 {
		return nil, "", oauthFail("provider_error", "no authorization code")
	}
	p, err := s.loadProvider(ctx, flow.providerID)
	if err != nil || p.revision != flow.revision {
		return nil, "", oauthFail("not_configured", "settings changed during sign-in")
	}
	secret, err := s.secrets.open(p.sealedSecret)
	if err != nil {
		return nil, "", oauthFail("not_configured", "client secret: %v", err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.RedirectURL}, "code_verifier": {flow.verifier}}

	var claims map[string]any
	switch p.Kind {
	case KindOIDC:
		doc, derr := s.httpc.discover(ctx, p.Issuer)
		if derr != nil {
			return nil, "", oauthFail("provider_unreachable", "discovery: %v", derr)
		}
		basic := len(doc.TokenAuthMethods) == 0 || slices.Contains(doc.TokenAuthMethods, "client_secret_basic")
		tokens, terr := s.httpc.exchange(ctx, doc.TokenEndpoint, form, p.ClientID, secret, basic)
		if terr != nil {
			return nil, "", oauthFail("provider_error", "%v", terr)
		}
		if claims, err = s.verifyIDToken(ctx, doc, p, tokens.IDToken, flow.nonce); err != nil {
			return nil, "", oauthFail("invalid_token", "%v", err)
		}
		// Some providers keep the identity claim (often email) out of the ID token.
		if _, present := claims[p.IdentityClaim]; !present && doc.UserinfoEndpoint != "" && tokens.AccessToken != "" {
			var info map[string]any
			if err = s.httpc.getJSON(ctx, doc.UserinfoEndpoint, tokens.AccessToken, &info); err != nil {
				return nil, "", oauthFail("provider_error", "userinfo: %v", err)
			}
			if sub, _ := info["sub"].(string); sub != claims["sub"] {
				return nil, "", oauthFail("invalid_token", "userinfo subject does not match the ID token")
			}
			for k, v := range info {
				if _, exists := claims[k]; !exists {
					claims[k] = v
				}
			}
		}
	case KindOAuth2:
		tokens, terr := s.httpc.exchange(ctx, p.TokenURL, form, p.ClientID, secret, false)
		if terr != nil {
			return nil, "", oauthFail("provider_error", "%v", terr)
		}
		if tokens.AccessToken == "" {
			return nil, "", oauthFail("provider_error", "no access token")
		}
		if err = s.httpc.getJSON(ctx, p.UserinfoURL, tokens.AccessToken, &claims); err != nil {
			return nil, "", oauthFail("provider_error", "userinfo: %v", err)
		}
	default:
		return nil, "", oauthFail("not_configured", "unknown provider kind")
	}

	identity, err := identityFrom(claims, p.IdentityClaim)
	if err != nil {
		return nil, "", oauthFail("no_identity", "%v", err)
	}
	if !containsFold(p.AllowedIdentities, identity) {
		return nil, identity, oauthFail("not_allowed", "identity %q is not on the allow list", identity)
	}

	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	var username string
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT username, credential_epoch FROM admin WHERE id=1`).Scan(&username, &epoch); err != nil {
		return nil, "", oauthFail("not_configured", "no administrator")
	}
	issued, err := s.insertSession(ctx, tx, username, epoch, "oauth", p.ID, client)
	if err != nil {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	issued.Session.ProviderName = p.DisplayName
	return issued, identity, nil
}

// verifyIDToken validates signature, issuer, audience, time window and nonce.
func (s *Service) verifyIDToken(ctx context.Context, doc *discovery, p *storedProvider, raw, nonce string) (map[string]any, error) {
	if raw == "" {
		return nil, errors.New("the provider returned no ID token")
	}
	header, payload, signingInput, sig, err := parseJWS(raw)
	if err != nil {
		return nil, err
	}
	keys, err := s.httpc.keySet(ctx, doc.JWKSURI, false)
	if err != nil {
		return nil, err
	}
	candidates := keys.candidateKeys(header)
	if len(candidates) == 0 {
		// The provider may have rotated its keys since they were cached.
		if keys, err = s.httpc.keySet(ctx, doc.JWKSURI, true); err != nil {
			return nil, err
		}
		candidates = keys.candidateKeys(header)
	}
	verified := false
	for _, k := range candidates {
		if verifySignature(header.Alg, k, signingInput, sig) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, errTokenSignature
	}
	var claims map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err = decoder.Decode(&claims); err != nil {
		return nil, errors.New("the ID token claims are malformed")
	}
	if iss, _ := claims["iss"].(string); strings.TrimRight(iss, "/") != p.Issuer {
		return nil, errors.New("the ID token was issued by a different issuer")
	}
	if !audienceContains(claims["aud"], p.ClientID) {
		return nil, errors.New("the ID token is meant for a different client")
	}
	if azp, ok := claims["azp"].(string); ok && azp != p.ClientID {
		return nil, errors.New("the ID token was authorized for a different client")
	}
	now := s.now().Unix()
	exp, okExp := numericClaim(claims["exp"])
	if !okExp || now > exp+60 {
		return nil, errors.New("the ID token has expired")
	}
	if iat, ok := numericClaim(claims["iat"]); ok && iat > now+300 {
		return nil, errors.New("the ID token is issued in the future")
	}
	if got, _ := claims["nonce"].(string); !equalConstantTime(got, nonce) {
		return nil, errors.New("the ID token nonce does not match")
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return nil, errors.New("the ID token has no subject")
	}
	return claims, nil
}

func audienceContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []any:
		for _, a := range v {
			if s, _ := a.(string); s == clientID {
				return true
			}
		}
	}
	return false
}

func numericClaim(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(n), true
	}
	return 0, false
}

// identityFrom reads the configured claim. An email claim explicitly marked
// unverified is refused, so an unverified address can never match the allow list.
func identityFrom(claims map[string]any, claim string) (string, error) {
	var identity string
	switch v := claims[claim].(type) {
	case string:
		identity = strings.TrimSpace(v)
	case json.Number:
		identity = v.String()
	case float64:
		identity = fmt.Sprintf("%.0f", v)
	}
	if identity == "" {
		return "", fmt.Errorf("the provider did not return the %q claim", claim)
	}
	if claim == "email" {
		if verified, present := claims["email_verified"]; present {
			if b, ok := verified.(bool); ok && !b {
				return "", errors.New("the email address is not verified at the provider")
			}
			if s, ok := verified.(string); ok && s == "false" {
				return "", errors.New("the email address is not verified at the provider")
			}
		}
	}
	return identity, nil
}
