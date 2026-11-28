package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/core/resolvers/providers"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/project"
	"github.com/traefik-console/internal/traefik/status"
)

var resolverNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type ResolverManager interface {
	ListProviders() ([]providers.Provider, []string)

	ListResolvers(ctx context.Context) ([]models.UIResource[models.ResolverInput], error)
	GetResolver(ctx context.Context, name string) (*models.UIResource[models.ResolverInput], error)

	UpsertResolver(ctx context.Context, input models.ResolverInput, opts models.WriteOptions) (*models.WriteResult[models.ResolverInput], error)
	DeleteResolver(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ResolverInput], error)
}

// ListProviders returns the 8 form-supported DNS providers + 170+ custom provider codes
func (m *resolverManager) ListProviders() ([]providers.Provider, []string) {
	return m.registry.Providers(), m.registry.CustomProviderCodes()
}

// ListResolvers returns both Console-Managed resolvers (from SQLite) and External resolvers (from traefik.yml)
func (m *resolverManager) ListResolvers(ctx context.Context) ([]models.UIResource[models.ResolverInput], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindResolver)
	if err != nil {
		return nil, err
	}

	rawStatic, err := m.files.ReadStaticConfig()
	if err != nil {
		return nil, err
	}
	extSnap, err := status.DiscoverStatic(rawStatic)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	result := make([]models.UIResource[models.ResolverInput], 0)

	// 1. Add Managed Resolvers from SQLite
	for _, row := range dbRows {
		seen[row.Name] = true
		cfg, err := presentation.DecodeUIConfig[models.ResolverInput](row)
		if err != nil {
			return nil, err
		}

		res := models.UIResource[models.ResolverInput]{
			ID:         row.ID,
			UpdatedAt:  presentation.ParseTimestamp(row.UpdatedAt),
			Name:       row.Name,
			Origin:     "managed",
			Provider:   "static",
			Status:     models.StatusActive,
			ReadOnly:   false,
			LiveHealth: "unknown",
			Config:     cfg,
			Version:    row.Version,
		}
		result = append(result, res)
	}

	// 2. Append External Resolvers discovered in traefik.yml

	for name, extItem := range extSnap.Resolvers {
		if seen[name] {
			continue
		}
		extInput := parseExternalResolver(name, extItem.Raw)
		result = append(result, models.UIResource[models.ResolverInput]{
			ID:         "external:resolver:" + name,
			Name:       name,
			Origin:     "external",
			Provider:   "static",
			Status:     models.StatusActive,
			ReadOnly:   true,
			LiveHealth: "unknown",
			Config:     extInput,
		})
	}

	return result, nil
}

func (m *resolverManager) GetResolver(ctx context.Context, name string) (*models.UIResource[models.ResolverInput], error) {
	all, err := m.ListResolvers(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range all {
		if item.Name == name {
			return &item, nil
		}
	}

	return nil, files.ErrSourceFileNotFound
}

// UpsertResolver validates the resolver (ACME / Tailscale / Custom YAML) and applies it, with any 0600
// DNS secrets, as one operation. A static change restarts Traefik (or reports a manual restart).
func (m *resolverManager) UpsertResolver(ctx context.Context, in models.ResolverInput, opts models.WriteOptions) (*models.WriteResult[models.ResolverInput], error) {
	in.Name = strings.TrimSpace(in.Name)
	if !resolverNameRegex.MatchString(in.Name) {
		return nil, ErrResolverInvalidName
	}

	rawStatic, err := m.files.ReadStaticConfig()
	if err != nil {
		return nil, err
	}
	extSnap, err := status.DiscoverStatic(rawStatic)
	if err != nil {
		return nil, err
	}

	existingRow, err := m.store.GetItem(ctx, models.KindResolver, in.Name)
	if err != nil {
		return nil, err
	}

	// Block overwriting an external resolver unless imported
	if existingRow == nil && extSnap != nil {
		if _, isExt := extSnap.Resolvers[in.Name]; isExt {
			return nil, files.ErrCannotModifyReadOnly
		}
	}

	// Staging, production and a custom CA each keep their own storage file (<name>-<environment>.json),
	// so switching environment points Traefik at a fresh account and certificate store: the certificates
	// of the old CA are never served by the new one, and switching back finds them again. Two different
	// custom CAs would share <name>-custom.json, so a custom CA address cannot be changed in place.
	if existingRow != nil && len(existingRow.Payload) > 0 {
		var prev models.ResolverInput
		if json.Unmarshal(existingRow.Payload, &prev) != nil {
			return nil, fmt.Errorf("%w: stored resolver", engine.ErrValidationFailed)
		}
		if prev.Environment == "custom" && in.Environment == "custom" && strings.TrimSpace(prev.CAURL) != strings.TrimSpace(in.CAURL) {
			return nil, ErrEnvChangeForbidden
		}
	}

	// Build & validate according to mode
	var pending []models.SecretRow
	if err := m.validateAndBuild(ctx, &in, extSnap, &pending); err != nil {
		return nil, fmt.Errorf("%w: resolver input", engine.ErrValidationFailed)
	}

	rawJSON, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}

	row := models.ConfigItemRow{
		ID:      "resolver:" + in.Name,
		Kind:    models.KindResolver,
		Name:    in.Name,
		Scope:   models.ScopeStatic,
		Payload: rawJSON,
	}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}, Secrets: pending}, opts, func(ctx context.Context) (*models.UIResource[models.ResolverInput], error) {
		return m.GetResolver(ctx, in.Name)
	})
}

func (m *resolverManager) DeleteResolver(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ResolverInput], error) {
	existing, err := m.store.GetItem(ctx, models.KindResolver, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, files.ErrCannotModifyReadOnly
	}

	// Ensure no Router references this certResolver
	routers, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return nil, err
	}
	for _, rRow := range routers {
		var rCfg models.RouterConfig
		if json.Unmarshal(rRow.Payload, &rCfg) == nil && rCfg.TLS != nil {
			if rCfg.TLS.CertResolver == name {
				return nil, fmt.Errorf("%w: used by router '%s'", ErrResolverInUse, rRow.Name)
			}
		}
	}

	// Ensure no entrypoint uses it as its default certificate resolver
	entryPoints, err := m.store.ListItemsByKind(ctx, models.KindEntryPoint)
	if err != nil {
		return nil, err
	}
	for _, eRow := range entryPoints {
		var eCfg models.EntryPointConfig
		if json.Unmarshal(eRow.Payload, &eCfg) == nil && eCfg.HTTP != nil && eCfg.HTTP.TLS != nil && eCfg.HTTP.TLS.CertResolver == name {
			return nil, fmt.Errorf("%w: used by entrypoint '%s'", ErrResolverInUse, eRow.Name)
		}
	}

	// Ensure no Domain binds to this resolver
	domains, err := m.store.ListItemsByKind(ctx, models.KindDomain)
	if err != nil {
		return nil, err
	}
	for _, dRow := range domains {
		var dCfg models.DomainConfig
		if json.Unmarshal(dRow.Payload, &dCfg) == nil {
			if dCfg.Method == "resolver" && dCfg.Resolver == name {
				return nil, fmt.Errorf("%w: used by domain '%s'", ErrResolverInUse, dRow.Name)
			}
		}
	}

	return engine.Write[models.ResolverInput](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindResolver, Name: name}}}, opts, nil)
}

// ============================================================================
// VALIDATION & BUILDER LOGIC (Adapted from build.go & custom.go)
// ============================================================================

func (m *resolverManager) validateAndBuild(ctx context.Context, r *models.ResolverInput, extSnap *status.StaticSnapshot, pending *[]models.SecretRow) error {
	r.Warnings = nil
	r.RenderedNode = nil
	if r.Type != "acme" || r.Challenge != "dns01" {
		r.Values = nil
	}

	if r.Type == "tailscale" {
		return errors.New("tailscale resolver support is deferred")
	}

	if r.Type == "custom" {
		return m.buildCustomYAMLResolver(r)
	}

	if r.Type != "acme" {
		return errors.New("resolver type must be 'acme' or 'custom'")
	}

	if _, err := mail.ParseAddress(r.Email); err != nil || strings.ContainsAny(r.Email, "\r\n") {
		return errors.New("a valid ACME account email address is required")
	}

	switch r.Environment {
	case "staging":
		r.CAURL = project.LetsEncryptStagingURL
	case "production", "":
		r.Environment = "production"
		r.CAURL = project.LetsEncryptProductionURL
	case "custom":
		u, err := url.Parse(r.CAURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("custom ACME directory URL must be a valid https:// URL")
		}
	default:
		return errors.New("environment must be 'production', 'staging', or 'custom'")
	}

	if r.KeyType != "" && !map[string]bool{"EC256": true, "EC384": true, "RSA2048": true, "RSA4096": true, "RSA8192": true}[r.KeyType] {
		return errors.New("unsupported ACME keyType (use EC256, EC384, RSA2048, RSA4096, or RSA8192)")
	}

	r.Storage = filepath.Join(m.acmeStorageDir, fmt.Sprintf("%s-%s.json", r.Name, r.Environment))
	acmeMap := map[string]any{
		"email":    r.Email,
		"caServer": r.CAURL,
		"storage":  r.Storage,
	}
	if r.KeyType != "" {
		acmeMap["keyType"] = r.KeyType
	}

	switch r.Challenge {
	case "http01":
		if r.EntryPoint == "" {
			return errors.New("HTTP-01 challenge requires an entryPoint (e.g., 'web')")
		}

		if extSnap != nil && len(extSnap.EntryPoints) > 0 {
			found := slices.Contains(extSnap.EntryPoints, r.EntryPoint)

			if !found {
				return fmt.Errorf("entryPoint '%s' does not exist in traefik.yml", r.EntryPoint)
			}
		}

		acmeMap["httpChallenge"] = map[string]any{"entryPoint": r.EntryPoint}
		r.Warnings = append(r.Warnings, "HTTP-01 requires public reachability on port 80.")

	case "tlsalpn01":
		acmeMap["tlsChallenge"] = map[string]any{}
		r.Warnings = append(r.Warnings, "TLS-ALPN-01 requires public reachability on port 443.")

	case "dns01":
		dnsNode, err := m.buildDNS01Challenge(ctx, r, pending)
		if err != nil {
			return err
		}
		acmeMap["dnsChallenge"] = dnsNode

	default:
		return errors.New("challenge must be 'dns01', 'http01', or 'tlsalpn01'")
	}

	r.RenderedNode = map[string]any{"acme": acmeMap}
	return nil
}

func (m *resolverManager) buildDNS01Challenge(ctx context.Context, r *models.ResolverInput, pending *[]models.SecretRow) (map[string]any, error) {
	if r.ExternalCredentials {
		r.Values = nil
		if !m.registry.IsCustomProvider(r.Provider) {
			return nil, fmt.Errorf("unsupported or disallowed DNS provider code '%s'", r.Provider)
		}

		r.Warnings = append(r.Warnings, "External DNS credentials mode selected; ensure provider env vars are passed to the Traefik container.")
	} else {
		prov, ok := m.registry.Provider(r.Provider)
		if !ok || !prov.Supported {
			return nil, fmt.Errorf("DNS provider '%s' requires external_credentials=true or is not form-supported", r.Provider)
		}

		if r.Alternative < 0 || r.Alternative >= len(prov.Alternatives) {
			return nil, errors.New("invalid provider authentication alternative selected")
		}

		alt := prov.Alternatives[r.Alternative]
		allFields := append(append([]providers.Field{}, alt.Required...), alt.Optional...)
		allFields = append(allFields, prov.Options...)

		if r.Values == nil {
			r.Values = make(map[string]string)
		}

		// Reject unknown inputs and export BOTH credentials and non-secret required options.
		allowed := map[string]bool{}
		required := map[string]bool{}
		for _, f := range allFields {
			allowed[f.Env] = true
		}
		for _, f := range alt.Required {
			required[f.Env] = true
		}
		for k := range r.Values {
			if !allowed[k] {
				return nil, errors.New("unknown provider field")
			}
		}
		maskedValues := map[string]string{}
		for _, f := range allFields {
			val := strings.TrimSpace(r.Values[f.Env])
			if val == "" {
				val = f.DefaultValue
			}
			if val == "" || strings.HasPrefix(val, "••••••••") {
				old, e := m.store.GetSecret(ctx, strings.ToLower(f.Env))
				if e != nil {
					return nil, e
				}
				if old == nil {
					if required[f.Env] || val != "" {
						return nil, errors.New("required DNS provider value is missing")
					}
					continue
				}
				if f.Secret {
					maskedValues[f.Env] = "••••••••"
				} else {
					existing, e := m.secrets.DecryptSecret(ctx, strings.ToLower(f.Env))
					if e != nil {
						return nil, e
					}
					maskedValues[f.Env] = existing
				}
				continue
			}
			if err := validateProviderFieldValue(f, val); err != nil {
				return nil, err
			}
			if !f.File {
				return nil, errors.New("provider field has no audited file delivery")
			}
			delivery := val
			if f.Type == "duration" {
				duration, _ := time.ParseDuration(val)
				if duration%time.Second != 0 {
					return nil, errors.New("provider duration must use whole seconds")
				}
				delivery = strconv.FormatInt(int64(duration/time.Second), 10)
			}
			row, err := m.secrets.PrepareSecret(ctx, strings.ToLower(f.Env), "dns_challenge", f.Env+"_FILE", delivery)
			if err != nil {
				return nil, err
			}
			*pending = append(*pending, *row)
			if f.Secret {
				maskedValues[f.Env] = "••••••••"
			} else {
				maskedValues[f.Env] = val
			}
		}

		r.Values = maskedValues
	}

	// Validate recursive nameservers (IP:port)
	if len(r.Nameservers) > 8 {
		return nil, errors.New("maximum 8 recursive nameservers allowed")
	}

	for _, ns := range r.Nameservers {
		host, port, err := net.SplitHostPort(ns)
		pNum, pErr := strconv.Atoi(port)
		if err != nil || pErr != nil || pNum < 1 || pNum > 65535 || net.ParseIP(host) == nil {
			return nil, fmt.Errorf("nameserver '%s' must be a valid IP:port (e.g., 1.1.1.1:53)", ns)
		}
	}

	dnsMap := map[string]any{"provider": r.Provider}
	if len(r.Nameservers) > 0 {
		dnsMap["resolvers"] = r.Nameservers
	}

	propMap := make(map[string]any)
	if r.Delay != "" && r.Delay != "0s" {
		d, err := time.ParseDuration(r.Delay)
		if err != nil || d < 0 || d > 10*time.Minute {
			return nil, errors.New("propagation delay must be between 0s and 10m")
		}
		propMap["delayBeforeChecks"] = r.Delay
	}
	if r.Propagation != nil {
		if r.Propagation.DisableChecks {
			propMap["disableChecks"] = true
		}
		if r.Propagation.DisableANSChecks {
			propMap["disableANSChecks"] = true
		}
		if r.Propagation.RequireAllRNS {
			propMap["requireAllRNS"] = true
		}
	}
	if len(propMap) > 0 {
		dnsMap["propagation"] = propMap
	}

	return dnsMap, nil
}

func (m *resolverManager) buildCustomYAMLResolver(r *models.ResolverInput) error {
	if len(r.CustomYAML) == 0 || len(r.CustomYAML) > 65536 {
		return errors.New("custom resolver YAML must be between 1 and 65536 bytes")
	}

	if _, _, err := yaml.ParseBoundedYAML(strings.NewReader(r.CustomYAML), yaml.DefaultParserOptions()); err != nil {
		return fmt.Errorf("invalid custom YAML structure: %w", err)
	}

	// Enforce strict schema via KnownFields(true) as in old custom.go
	var doc struct {
		ACME map[string]any `yaml:"acme"`
	}
	dec := yamlv3.NewDecoder(bytes.NewReader([]byte(r.CustomYAML)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil || doc.ACME == nil {
		return errors.New("custom YAML must contain a top-level 'acme:' block")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return errors.New("custom YAML must contain only a single YAML document")
	}

	if err := validateCustomACME(doc.ACME, r.ExternalCredentials, m.registry); err != nil {
		return err
	}
	if dns, ok := doc.ACME["dnsChallenge"].(map[string]any); ok {
		r.Challenge = "dns01"
		r.Provider, _ = dns["provider"].(string)
	} else if _, ok := doc.ACME["httpChallenge"]; ok {
		r.Challenge = "http01"
	} else {
		r.Challenge = "tlsalpn01"
	}
	r.Storage = filepath.Join(m.acmeStorageDir, fmt.Sprintf("%s-custom.json", r.Name))
	doc.ACME["storage"] = r.Storage
	r.RenderedNode = map[string]any{"acme": doc.ACME}
	r.Warnings = append(r.Warnings, "Custom YAML mode active; ensure any required DNS environment variables are mounted on Traefik.")
	return nil
}

func validateProviderFieldValue(f providers.Field, v string) error {
	if len(v) > 65536 || strings.ContainsRune(v, 0) {
		return fmt.Errorf("%s exceeds maximum length", f.Env)
	}

	if f.Bounds != "" && strings.Contains(f.Bounds, "|") {
		found := false
		for allowed := range strings.SplitSeq(f.Bounds, "|") {
			found = found || v == allowed
		}
		if !found {
			return errors.New("provider value is outside the audited choices")
		}
	}

	switch f.Type {
	case "duration":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > time.Hour {
			return fmt.Errorf("%s must be a positive duration (e.g., '120s')", f.Env)
		}
	case "integer":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 86400 {
			return fmt.Errorf("%s must be an integer between 0 and 86400", f.Env)
		}
		if f.Bounds == "120-86400" && n < 120 {
			return fmt.Errorf("%s minimum value is 120", f.Env)
		}
	case "boolean":
		if v != "true" && v != "false" {
			return fmt.Errorf("%s must be 'true' or 'false'", f.Env)
		}
	}

	return nil
}

func parseExternalResolver(name string, raw map[string]any) models.ResolverInput {
	res := models.ResolverInput{
		Name: name,
		Type: "acme",
	}
	if raw == nil {
		return res
	}
	if email, ok := raw["email"].(string); ok {
		res.Email = email
	}
	if storage, ok := raw["storage"].(string); ok {
		res.Storage = storage
	}
	if dns, ok := raw["dnsChallenge"].(map[string]any); ok {
		res.Challenge = "dns01"
		if prov, ok := dns["provider"].(string); ok {
			res.Provider = prov
		}
	} else if httpChal, ok := raw["httpChallenge"].(map[string]any); ok {
		res.Challenge = "http01"
		if ep, ok := httpChal["entryPoint"].(string); ok {
			res.EntryPoint = ep
		}
	} else if _, ok := raw["tlsChallenge"]; ok {
		res.Challenge = "tlsalpn01"
	}
	return res
}
