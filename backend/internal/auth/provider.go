package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// CallbackPath is the only redirect path the console accepts from a provider.
const CallbackPath = "/api/v1/auth/oauth/callback"

const (
	KindOIDC   = "oidc"
	KindOAuth2 = "oauth2"
)

var (
	ErrProviderNotConfigured = errors.New("external sign-in provider not found")
	ErrTooManyProviders      = errors.New("at most 10 sign-in providers can be configured")
)

const maxProviders = 10

// ValidationError carries per-field messages for the settings form.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid external sign-in settings" }

// ProviderSettings is one external sign-in provider as shown to the
// administrator. Every configured provider is offered on the sign-in page.
// The client secret is write-only and never returned.
type ProviderSettings struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	DisplayName       string   `json:"display_name"`
	Issuer            string   `json:"issuer"`
	AuthorizeURL      string   `json:"authorize_url"`
	TokenURL          string   `json:"token_url"`
	UserinfoURL       string   `json:"userinfo_url"`
	ClientID          string   `json:"client_id"`
	Scopes            []string `json:"scopes"`
	IdentityClaim     string   `json:"identity_claim"`
	AllowedIdentities []string `json:"allowed_identities"`
	RedirectURL       string   `json:"redirect_url"`
	// CredentialStored says whether a client secret is saved (its value is never returned).
	CredentialStored bool      `json:"credential_stored"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ProviderInput is a settings change. ClientSecret empty keeps the stored
// secret; ClearClientSecret removes it (public PKCE clients).
type ProviderInput struct {
	ProviderSettings
	ClientSecret      string
	ClearClientSecret bool
}

type storedProvider struct {
	ProviderSettings
	sealedSecret []byte
	revision     int64
}

var (
	claimPattern      = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	scopePattern      = regexp.MustCompile(`^[\x21\x23-\x5B\x5D-\x7E]{1,128}$`)
	providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// ValidProviderID reports whether id can name a provider (also used for URL paths).
func ValidProviderID(id string) bool { return providerIDPattern.MatchString(id) }

const providerColumns = `id,kind,display_name,issuer,authorize_url,token_url,userinfo_url,client_id,sealed_client_secret,scopes,identity_claim,allowed_identities,redirect_url,revision,updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanProvider(row rowScanner) (*storedProvider, error) {
	var p storedProvider
	var scopes, allowed string
	var updated int64
	if err := row.Scan(&p.ID, &p.Kind, &p.DisplayName, &p.Issuer, &p.AuthorizeURL, &p.TokenURL, &p.UserinfoURL, &p.ClientID, &p.sealedSecret, &scopes, &p.IdentityClaim, &allowed, &p.RedirectURL, &p.revision, &updated); err != nil {
		return nil, err
	}
	p.CredentialStored = len(p.sealedSecret) > 0
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	if err := json.Unmarshal([]byte(scopes), &p.Scopes); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(allowed), &p.AllowedIdentities); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) loadProvider(ctx context.Context, id string) (*storedProvider, error) {
	if !ValidProviderID(id) {
		return nil, ErrProviderNotConfigured
	}
	p, err := scanProvider(s.store.db.QueryRowContext(ctx, `SELECT `+providerColumns+` FROM oauth_providers WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProviderNotConfigured
	}
	return p, err
}

// Providers lists every configured provider, in sign-in page order.
func (s *Service) Providers(ctx context.Context) ([]ProviderSettings, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT `+providerColumns+` FROM oauth_providers ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderSettings{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p.ProviderSettings)
	}
	return out, rows.Err()
}

// Provider returns one provider, or ErrProviderNotConfigured.
func (s *Service) Provider(ctx context.Context, id string) (*ProviderSettings, error) {
	p, err := s.loadProvider(ctx, id)
	if err != nil {
		return nil, err
	}
	return &p.ProviderSettings, nil
}

// PublicProvider is what the sign-in page may know: one button per provider.
type PublicProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

func (s *Service) PublicProviders(ctx context.Context) []PublicProvider {
	list, err := s.Providers(ctx)
	out := []PublicProvider{}
	if err != nil {
		return out
	}
	for _, p := range list {
		out = append(out, PublicProvider{ID: p.ID, DisplayName: p.DisplayName})
	}
	return out
}

// SaveProvider creates (id == "") or replaces a provider after validating it
// and, for OpenID Connect, checking its discovery document. The caller must
// have re-verified the administrator's password.
func (s *Service) SaveProvider(ctx context.Context, id string, in ProviderInput, client Client) (*ProviderSettings, error) {
	normalized, err := normalizeProvider(in.ProviderSettings)
	if err != nil {
		return nil, err
	}
	if len(in.ClientSecret) > 4096 {
		return nil, &ValidationError{Fields: map[string]string{"client_secret": "Client secret is too long."}}
	}
	all, err := s.Providers(ctx)
	if err != nil {
		return nil, err
	}
	var existing *storedProvider
	if id != "" {
		if existing, err = s.loadProvider(ctx, id); err != nil {
			return nil, err
		}
	} else if len(all) >= maxProviders {
		return nil, ErrTooManyProviders
	}
	for _, other := range all {
		if other.ID != id && strings.EqualFold(other.DisplayName, normalized.DisplayName) {
			return nil, &ValidationError{Fields: map[string]string{"display_name": "Another provider already uses this label."}}
		}
	}
	sealed := []byte(nil)
	if existing != nil && !in.ClearClientSecret {
		sealed = existing.sealedSecret
	}
	if in.ClientSecret != "" {
		if sealed, err = s.secrets.seal(in.ClientSecret); err != nil {
			return nil, err
		}
	}
	if normalized.Kind == KindOAuth2 && len(sealed) == 0 {
		return nil, &ValidationError{Fields: map[string]string{"client_secret": "A client secret is required for OAuth 2.0 providers."}}
	}
	if normalized.Kind == KindOIDC {
		if _, err = s.httpc.discover(ctx, normalized.Issuer); err != nil {
			return nil, &ProviderUnreachableError{Err: err}
		}
	}
	scopes, _ := json.Marshal(normalized.Scopes)
	allowed, _ := json.Marshal(normalized.AllowedIdentities)
	now := s.now().Unix()
	if existing == nil {
		id = uniqueProviderID(normalized.DisplayName, all)
		_, err = s.store.db.ExecContext(ctx, `INSERT INTO oauth_providers(`+providerColumns+`,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)`,
			id, normalized.Kind, normalized.DisplayName, normalized.Issuer, normalized.AuthorizeURL, normalized.TokenURL, normalized.UserinfoURL,
			normalized.ClientID, sealed, string(scopes), normalized.IdentityClaim, string(allowed), normalized.RedirectURL, now, now)
	} else {
		_, err = s.store.db.ExecContext(ctx, `UPDATE oauth_providers SET kind=?, display_name=?, issuer=?, authorize_url=?, token_url=?, userinfo_url=?, client_id=?,
			sealed_client_secret=?, scopes=?, identity_claim=?, allowed_identities=?, redirect_url=?, revision=revision+1, updated_at=? WHERE id=?`,
			normalized.Kind, normalized.DisplayName, normalized.Issuer, normalized.AuthorizeURL, normalized.TokenURL, normalized.UserinfoURL,
			normalized.ClientID, sealed, string(scopes), normalized.IdentityClaim, string(allowed), normalized.RedirectURL, now, id)
	}
	if err != nil {
		return nil, err
	}
	// Sign-ins started against the previous settings cannot complete.
	s.flows.dropProvider(id)
	action := "added"
	if existing != nil {
		action = "updated"
	}
	s.auditf(ctx, "external_signin_changed", "success", client.IP, normalized.DisplayName+" "+action)
	return s.Provider(ctx, id)
}

// DeleteProvider removes one provider and ends every session it opened.
func (s *Service) DeleteProvider(ctx context.Context, id string, client Client) error {
	p, err := s.loadProvider(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM oauth_providers WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE method='oauth' AND provider=?`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.flows.dropProvider(id)
	s.auditf(ctx, "external_signin_removed", "success", client.IP, p.DisplayName)
	return nil
}

// uniqueProviderID derives a readable, URL-safe id from the label.
func uniqueProviderID(label string, existing []ProviderSettings) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if len(base) > 32 {
		base = strings.Trim(base[:32], "-")
	}
	if base == "" {
		base = "provider"
	}
	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.ID] = true
	}
	id := base
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// ProviderUnreachableError reports that discovery failed while saving.
type ProviderUnreachableError struct{ Err error }

func (e *ProviderUnreachableError) Error() string {
	return "identity provider unreachable: " + e.Err.Error()
}
func (e *ProviderUnreachableError) Unwrap() error { return e.Err }

func normalizeProvider(in ProviderSettings) (ProviderSettings, error) {
	fields := map[string]string{}
	out := ProviderSettings{Kind: strings.TrimSpace(in.Kind)}
	out.DisplayName = strings.TrimSpace(in.DisplayName)
	if out.DisplayName == "" || len([]rune(out.DisplayName)) > 40 {
		fields["display_name"] = "Enter a button label of 1-40 characters."
	}
	switch out.Kind {
	case KindOIDC:
		out.Issuer = strings.TrimRight(strings.TrimSpace(in.Issuer), "/")
		if err := checkProviderURL(out.Issuer, false); err != "" {
			fields["issuer"] = err
		}
	case KindOAuth2:
		out.AuthorizeURL = strings.TrimSpace(in.AuthorizeURL)
		out.TokenURL = strings.TrimSpace(in.TokenURL)
		out.UserinfoURL = strings.TrimSpace(in.UserinfoURL)
		for field, value := range map[string]string{"authorize_url": out.AuthorizeURL, "token_url": out.TokenURL, "userinfo_url": out.UserinfoURL} {
			if err := checkProviderURL(value, true); err != "" {
				fields[field] = err
			}
		}
	default:
		fields["kind"] = "Choose OpenID Connect or OAuth 2.0."
	}
	out.ClientID = strings.TrimSpace(in.ClientID)
	if out.ClientID == "" || len(out.ClientID) > 256 {
		fields["client_id"] = "Enter the client ID issued by the provider."
	}
	out.Scopes = []string{}
	for _, raw := range in.Scopes {
		for scope := range strings.FieldsSeq(raw) {
			if !scopePattern.MatchString(scope) {
				fields["scopes"] = "Scopes are space-separated words without quotes or backslashes."
				continue
			}
			if !contains(out.Scopes, scope) {
				out.Scopes = append(out.Scopes, scope)
			}
		}
	}
	if out.Kind == KindOIDC && !contains(out.Scopes, "openid") {
		out.Scopes = append([]string{"openid"}, out.Scopes...)
	}
	if len(out.Scopes) > 16 {
		fields["scopes"] = "Use at most 16 scopes."
	}
	out.IdentityClaim = strings.TrimSpace(in.IdentityClaim)
	if out.IdentityClaim == "" && out.Kind == KindOIDC {
		out.IdentityClaim = "email"
	}
	if !claimPattern.MatchString(out.IdentityClaim) {
		fields["identity_claim"] = "Enter the claim that identifies the user, for example email or login."
	}
	out.AllowedIdentities = []string{}
	for _, raw := range in.AllowedIdentities {
		for _, id := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if len(id) > 254 {
				fields["allowed_identities"] = "Each identity must be at most 254 characters."
				continue
			}
			if !containsFold(out.AllowedIdentities, id) {
				out.AllowedIdentities = append(out.AllowedIdentities, id)
			}
		}
	}
	if len(out.AllowedIdentities) == 0 {
		fields["allowed_identities"] = "List at least one account that may sign in as the administrator."
	} else if len(out.AllowedIdentities) > 50 {
		fields["allowed_identities"] = "List at most 50 accounts."
	}
	out.RedirectURL = strings.TrimSpace(in.RedirectURL)
	if err := checkRedirectURL(out.RedirectURL); err != "" {
		fields["redirect_url"] = err
	}
	if len(fields) > 0 {
		return ProviderSettings{}, &ValidationError{Fields: fields}
	}
	return out, nil
}

// checkProviderURL requires https, except for loopback development providers.
func checkProviderURL(raw string, allowQuery bool) string {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(raw) > 2048 || (!allowQuery && u.RawQuery != "") {
		return "Enter a full https:// address."
	}
	if u.Scheme == "https" || u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return ""
	}
	return "The address must use https:// (http:// is accepted only for localhost)."
}

func checkRedirectURL(raw string) string {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != CallbackPath {
		return "The callback address must be this console's origin followed by " + CallbackPath + "."
	}
	if u.Scheme == "https" || u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return ""
	}
	return "The callback address must use https:// (http:// is accepted only for localhost)."
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
