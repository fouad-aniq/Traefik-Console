package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxProviderBody = 1 << 20

// providerHTTP talks to the identity provider: discovery, keys, token
// exchange and userinfo. Redirects are never followed, bodies are bounded and
// discovery/key documents are cached for an hour.
type providerHTTP struct {
	client *http.Client
	mu     sync.Mutex
	disc   map[string]cachedDiscovery
	keys   map[string]cachedKeys
	now    func() time.Time
}

type discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

type cachedDiscovery struct {
	doc     *discovery
	fetched time.Time
}

type cachedKeys struct {
	set     *jwkSet
	fetched time.Time
}

func newProviderHTTP() providerHTTP {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxResponseHeaderBytes = 64 << 10
	return providerHTTP{
		client: &http.Client{
			Transport:     transport,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		disc: map[string]cachedDiscovery{},
		keys: map[string]cachedKeys{},
		now:  time.Now,
	}
}

func (p *providerHTTP) getJSON(ctx context.Context, rawURL, bearer string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s failed", hostOf(rawURL))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody+1))
	if err != nil || len(body) > maxProviderBody {
		return fmt.Errorf("response from %s is unreadable or too large", hostOf(rawURL))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered HTTP %d", hostOf(rawURL), resp.StatusCode)
	}
	if err = json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%s returned invalid JSON", hostOf(rawURL))
	}
	return nil
}

// discover fetches the issuer's OpenID configuration. Every endpoint must be
// an https (or loopback http) address and the issuer must match exactly.
func (p *providerHTTP) discover(ctx context.Context, issuer string) (*discovery, error) {
	p.mu.Lock()
	if c, ok := p.disc[issuer]; ok && p.now().Sub(c.fetched) < time.Hour {
		p.mu.Unlock()
		return c.doc, nil
	}
	p.mu.Unlock()
	var doc discovery
	if err := p.getJSON(ctx, issuer+"/.well-known/openid-configuration", "", &doc); err != nil {
		return nil, err
	}
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return nil, errors.New("the discovery document names a different issuer")
	}
	for name, endpoint := range map[string]string{"authorization": doc.AuthorizationEndpoint, "token": doc.TokenEndpoint, "keys": doc.JWKSURI} {
		if checkProviderURL(endpoint, true) != "" {
			return nil, fmt.Errorf("the discovery document has no valid %s endpoint", name)
		}
	}
	if doc.UserinfoEndpoint != "" && checkProviderURL(doc.UserinfoEndpoint, true) != "" {
		doc.UserinfoEndpoint = ""
	}
	p.mu.Lock()
	p.disc[issuer] = cachedDiscovery{doc: &doc, fetched: p.now()}
	p.mu.Unlock()
	return &doc, nil
}

// keySet returns the provider's signing keys; refresh forces a re-fetch when a
// token names an unknown key (rotation), at most once every ten seconds.
func (p *providerHTTP) keySet(ctx context.Context, uri string, refresh bool) (*jwkSet, error) {
	p.mu.Lock()
	c, ok := p.keys[uri]
	age := p.now().Sub(c.fetched)
	p.mu.Unlock()
	if ok && (age < time.Hour && !refresh || refresh && age < 10*time.Second) {
		return c.set, nil
	}
	var set jwkSet
	if err := p.getJSON(ctx, uri, "", &set); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.keys[uri] = cachedKeys{set: &set, fetched: p.now()}
	p.mu.Unlock()
	return &set, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
}

// exchange redeems the authorization code. basicAuth selects
// client_secret_basic; otherwise the credentials travel in the form body.
func (p *providerHTTP) exchange(ctx context.Context, tokenURL string, form url.Values, clientID, secret string, basicAuth bool) (*tokenResponse, error) {
	if secret != "" && !basicAuth {
		form.Set("client_secret", secret)
	}
	if secret == "" || !basicAuth {
		form.Set("client_id", clientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if secret != "" && basicAuth {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request to %s failed", hostOf(tokenURL))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody+1))
	if err != nil || len(body) > maxProviderBody {
		return nil, errors.New("token response is unreadable or too large")
	}
	var out tokenResponse
	// Some providers label JSON as text/plain, others (classic GitHub) answer
	// form-encoded: decide by the body, falling back to the declared type.
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") && (mediaType == "application/x-www-form-urlencoded" || mediaType == "text/plain" || strings.Contains(trimmed, "=")) {
		values, perr := url.ParseQuery(string(body))
		if perr != nil {
			return nil, errors.New("token response is malformed")
		}
		out = tokenResponse{AccessToken: values.Get("access_token"), IDToken: values.Get("id_token"), TokenType: values.Get("token_type"), Error: values.Get("error")}
	} else if err = json.Unmarshal(body, &out); err != nil {
		return nil, errors.New("token response is malformed")
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		code := out.Error
		if code == "" {
			code = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("the provider rejected the authorization code (%s)", truncate(code, 64))
	}
	if out.AccessToken == "" && out.IDToken == "" {
		return nil, errors.New("the provider returned no token")
	}
	return &out, nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "the provider"
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
