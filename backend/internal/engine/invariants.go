package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/routing"
	"github.com/traefik-console/internal/traefik/validation"
)

var resourceName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// validateMutation checks one mutation against the committed state: names, scopes,
// kinds, collisions with resources the console does not own, plugin module
// uniqueness and shared-credential safety. Cross-resource references are checked
// on the rendered candidate by validateEffectiveAgainst.
func (c *operations) validateMutation(ctx context.Context, m models.Mutation, state *db.State) error {
	seen := map[string]bool{}
	mutating := map[string]bool{}
	for _, v := range m.Upserts {
		mutating[string(v.Kind)+":"+v.Name] = true
	}
	for _, v := range m.Deletes {
		mutating[string(v.Kind)+":"+v.Name] = true
	}
	if e := c.checkPluginModules(m, state); e != nil {
		return e
	}
	existing := rowsByKey(state.Items)
	for _, v := range m.Upserts {
		if !resourceName.MatchString(v.Name) || !json.Valid(v.Payload) {
			return ErrValidationFailed
		}
		scope := models.ScopeDynamic
		if defaultScope(v.Kind) == models.ScopeStatic {
			scope = models.ScopeStatic
		}
		switch v.Kind {
		case models.KindEntryPoint, models.KindResolver, models.KindPlugin, models.KindRouter, models.KindService, models.KindMiddleware, models.KindChain, models.KindTLS, models.KindDomain, models.KindCrowdSec:
		default:
			return ErrValidationFailed
		}
		if v.Scope != scope {
			return ErrValidationFailed
		}
		id := string(v.Kind) + ":" + v.Name
		if seen[id] {
			return files.ErrResourceConflict
		}
		seen[id] = true
		// An entrypoint declared in traefik.yml may be taken over explicitly (see adoptsEntryPoint).
		if _, exists := existing[id]; !exists && !adoptsEntryPoint(v) {
			if e := c.checkSourceCollision(v.Kind, v.Name); e != nil {
				return e
			}
		}
	}
	for _, ref := range m.Deletes {
		if seen[ref.Key()] {
			return files.ErrResourceConflict
		}
		if _, exists := existing[ref.Key()]; !exists {
			return files.ErrSourceFileNotFound
		}
	}
	for _, v := range m.Secrets {
		for _, old := range state.Secrets {
			if old.Metadata.Name != v.Name || len(old.Active) == 0 {
				continue
			}
			same, e := c.secrets.SameValue(old.Active, v.EncryptedValue)
			if e != nil {
				return e
			}
			if same {
				continue
			}
			for _, row := range state.Items {
				if row.Kind != models.KindResolver || mutating[string(row.Kind)+":"+row.Name] {
					continue
				}
				var cfg models.ResolverInput
				if e = json.Unmarshal(row.Payload, &cfg); e != nil {
					return e
				}
				env := strings.TrimSuffix(v.EnvVarName, "_FILE")
				if cfg.Values[env] != "" {
					return fmt.Errorf("%w: shared DNS credential is used by another resolver", files.ErrResourceConflict)
				}
			}
		}
	}
	return nil
}

// adoptsEntryPoint reports an entrypoint row that takes over a traefik.yml declaration. Its payload
// carries the original definition, which Render puts back when the row is released.
func adoptsEntryPoint(row models.ConfigItemRow) bool {
	if row.Kind != models.KindEntryPoint {
		return false
	}
	var cfg models.EntryPointConfig
	return json.Unmarshal(row.Payload, &cfg) == nil && cfg.Adopted
}

func (c *operations) checkSourceCollision(kind models.ItemKind, name string) error {
	raw, e := c.files.ReadStaticConfig()
	if e != nil && !errors.Is(e, files.ErrSourceFileNotFound) {
		return e
	}
	doc, _, e := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
	if e != nil {
		return e
	}
	root := yaml.GetRootMapping(doc)
	var category *yamlNode
	switch kind {
	case models.KindResolver:
		category = yaml.GetMappingValue(root, "certificatesResolvers")
	case models.KindPlugin:
		category = yaml.GetMappingValue(yaml.GetMappingValue(root, "experimental"), "plugins")
	case models.KindEntryPoint:
		category = yaml.GetMappingValue(root, "entryPoints")
	}
	if yaml.GetMappingValue(category, name) != nil {
		return files.ErrCannotModifyReadOnly
	}
	external, e := c.files.ListExternalDynamicFiles()
	if e != nil {
		return e
	}
	for _, f := range external {
		raw, e := c.files.ReadExternalDynamicFile(f.Filename)
		if e != nil {
			return e
		}
		doc, _, e := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
		if e != nil {
			return e
		}
		root := yaml.GetRootMapping(doc)
		section := func(protocol, category string) *yamlNode {
			return yaml.GetMappingValue(yaml.GetMappingValue(root, protocol), category)
		}
		switch kind {
		case models.KindRouter:
			// One managed name per router whatever its protocol.
			for _, protocol := range []string{"http", "tcp", "udp"} {
				if yaml.GetMappingValue(section(protocol, "routers"), name) != nil {
					return files.ErrCannotModifyReadOnly
				}
			}
		case models.KindMiddleware, models.KindChain:
			// One managed name per middleware whatever its protocol.
			for _, protocol := range []string{"http", "tcp"} {
				if yaml.GetMappingValue(section(protocol, "middlewares"), name) != nil {
					return files.ErrCannotModifyReadOnly
				}
			}
		case models.KindService:
			// One managed name per service whatever its protocol, and the transports it may create.
			for _, protocol := range []string{"http", "tcp", "udp"} {
				if yaml.GetMappingValue(section(protocol, "services"), name) != nil {
					return files.ErrCannotModifyReadOnly
				}
			}
			if yaml.GetMappingValue(section("http", "serversTransports"), httpTransportName(name)) != nil ||
				yaml.GetMappingValue(section("tcp", "serversTransports"), tcpTransportName(name)) != nil {
				return files.ErrCannotModifyReadOnly
			}
		}
	}
	return nil
}

// validateEffectiveAgainst validates cross-resource references of a candidate
// resource set. staticYAML is the traefik.yml the candidate will run with.
func (c *operations) validateEffectiveAgainst(ctx context.Context, rows []models.ConfigItemRow, secrets []models.SecretRow, staticYAML []byte) error {
	readStatic := func() ([]byte, error) {
		if staticYAML != nil {
			return staticYAML, nil
		}
		raw, err := c.files.ReadStaticConfig()
		if err == nil {
			staticYAML = raw
		}
		return raw, err
	}
	reference := func(kind, name string) error {
		raw, err := readStatic()
		if err != nil {
			return err
		}
		return staticReference(raw, kind, name)
	}
	pool := map[string]models.ConfigItemRow{}
	for _, v := range rows {
		pool[string(v.Kind)+":"+v.Name] = v
	}
	var routerView *routing.View
	for _, v := range rows {
		switch v.Kind {
		case models.KindResolver:
			var cfg models.ResolverInput
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			if cfg.Challenge == "dns01" {
				for _, other := range rows {
					if other.Kind != models.KindResolver || other.Name == v.Name {
						continue
					}
					var sibling models.ResolverInput
					if json.Unmarshal(other.Payload, &sibling) != nil {
						return ErrValidationFailed
					}
					if sibling.Challenge == "dns01" && sibling.Provider == cfg.Provider && (sibling.Alternative != cfg.Alternative || sibling.ExternalCredentials != cfg.ExternalCredentials) {
						return fmt.Errorf("%w: process-wide DNS authentication alternatives conflict", files.ErrResourceConflict)
					}
				}
			}

		case models.KindEntryPoint:
			// What an entrypoint points at must still exist in the candidate.
			var cfg models.EntryPointConfig
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			if cfg.HTTP == nil {
				continue
			}
			if tls := cfg.HTTP.TLS; tls != nil && tls.CertResolver != "" {
				if _, ok := pool[string(models.KindResolver)+":"+tls.CertResolver]; !ok {
					if e := reference("certificatesResolvers", tls.CertResolver); e != nil {
						return e
					}
				}
			}
			if r := cfg.HTTP.Redirect; r != nil && !strings.HasPrefix(r.To, ":") {
				if e := reference("entryPoints", r.To); e != nil {
					return e
				}
			}

		case models.KindDomain:
			var d models.DomainConfig
			if json.Unmarshal(v.Payload, &d) != nil {
				return ErrValidationFailed
			}
			if d.Method == "resolver" {
				if r, ok := pool[string(models.KindResolver)+":"+d.Resolver]; ok {
					var cfg models.ResolverInput
					if json.Unmarshal(r.Payload, &cfg) != nil {
						return ErrValidationFailed
					}
					for _, host := range append([]string{d.Main}, d.SANs...) {
						if strings.HasPrefix(host, "*.") && cfg.Challenge != "dns01" {
							return ErrValidationFailed
						}
					}
				} else if e := reference("certificatesResolvers", d.Resolver); e != nil {
					return e
				}
			} else {
				row, ok := pool[string(models.KindTLS)+":"+d.Certificate]
				if !ok {
					return ErrValidationFailed
				}
				var cert models.CertificateConfig
				if json.Unmarshal(row.Payload, &cert) != nil || cert.Purpose != "server" {
					return ErrValidationFailed
				}
				found := false
				for _, s := range secrets {
					if s.FilePath != cert.CertFile {
						continue
					}
					pem, e := c.secrets.Decrypt(s.EncryptedValue)
					if e != nil {
						return e
					}
					if e = validation.CoversDomains(pem, append([]string{d.Main}, d.SANs...)); e != nil {
						return ErrValidationFailed
					}
					found = true
				}
				if !found {
					return ErrValidationFailed
				}
			}
		case models.KindRouter:
			var cfg models.RouterConfig
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			if routerView == nil {
				raw, err := readStatic()
				if err != nil {
					return err
				}
				view, err := routing.NewView(rows, raw)
				if err != nil {
					return err
				}
				routerView = &view
			}
			if _, err := routerView.Project(cfg); err != nil {
				return fmt.Errorf("%w: %v", ErrValidationFailed, err)
			}

		case models.KindMiddleware:
			var m models.MiddlewareConfig
			if json.Unmarshal(v.Payload, &m) != nil {
				return ErrValidationFailed
			}
			if e := validateFileReferences(secrets, m.Spec); e != nil {
				return e
			}
			if m.Type == "plugin" {
				if _, ok := pool[string(models.KindPlugin)+":"+m.PluginName]; !ok {
					if e := reference("experimental", m.PluginName); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
func staticReference(b []byte, kind, name string) error {
	doc, _, e := yaml.ParseBoundedYAML(bytes.NewReader(b), yaml.DefaultParserOptions())
	if e != nil {
		return e
	}
	cat := yaml.GetMappingValue(yaml.GetRootMapping(doc), kind)
	if kind == "experimental" {
		cat = yaml.GetMappingValue(cat, "plugins")
	}
	if yaml.GetMappingValue(cat, name) == nil {
		return ErrValidationFailed
	}
	return nil
}

func assertOwned(doc *yamlNode, rows []models.ConfigItemRow) error {
	// Keys are protocol:category:name; a service owns its section and the transport it creates.
	owned := map[string]bool{}
	hasCert := false
	for _, v := range rows {
		if len(v.Payload) == 0 {
			continue
		}
		switch v.Kind {
		case models.KindRouter:
			var cfg models.RouterConfig
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			owned[routerOwnedKey(v.Name, cfg)] = true
		case models.KindService:
			var cfg models.ServiceConfig
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			protocol, transports := serviceTransports(v.Name, cfg)
			owned[protocol+":services:"+v.Name] = true
			for _, t := range transports {
				owned[protocol+":serversTransports:"+t] = true
			}
		case models.KindMiddleware:
			var cfg models.MiddlewareConfig
			if json.Unmarshal(v.Payload, &cfg) != nil {
				return ErrValidationFailed
			}
			owned[models.MiddlewareProtocol(cfg)+":middlewares:"+v.Name] = true
		case models.KindChain:
			owned["http:middlewares:"+v.Name] = true
		case models.KindTLS:
			hasCert = true
		}
	}
	root := yaml.GetRootMapping(doc)
	if root == nil {
		return ErrValidationFailed
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		switch protocol := root.Content[i].Value; protocol {
		case "http", "tcp", "udp":
			node := root.Content[i+1]
			for j := 0; j+1 < len(node.Content); j += 2 {
				category := node.Content[j].Value
				objects := node.Content[j+1]
				for k := 0; k+1 < len(objects.Content); k += 2 {
					if !owned[protocol+":"+category+":"+objects.Content[k].Value] {
						return files.ErrCannotModifyReadOnly
					}
				}
			}
		case "tls":
			if !hasCert {
				return files.ErrCannotModifyReadOnly
			}
			node := root.Content[i+1]
			for j := 0; j+1 < len(node.Content); j += 2 {
				if node.Content[j].Value != "certificates" {
					return files.ErrCannotModifyReadOnly
				}
			}
		default:
			return files.ErrCannotModifyReadOnly
		}
	}
	return nil
}

func validateFileReferences(secrets []models.SecretRow, v any) error {
	allowed := map[string]bool{}
	for _, row := range secrets {
		allowed[row.FilePath] = true
	}
	fields := map[string]bool{"usersFile": true, "certFile": true, "keyFile": true, "ca": true, "rootCAs": true, "crowdsecLapiKeyFile": true, "crowdsecAppsecKeyFile": true}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				if fields[k] {
					switch paths := y.(type) {
					case string:
						if paths != "" && !allowed[paths] {
							return ErrValidationFailed
						}
					case []any:
						for _, path := range paths {
							p, ok := path.(string)
							if !ok || !allowed[p] {
								return ErrValidationFailed
							}
						}
					default:
						return ErrValidationFailed
					}
				}
				if e := walk(y); e != nil {
					return e
				}
			}
		case []any:
			for _, y := range x {
				if e := walk(y); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(v)
}
