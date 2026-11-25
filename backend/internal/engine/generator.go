package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/routing"
	"github.com/traefik-console/internal/traefik/validation"
)

// GeneratedBundle holds the rendered YAML bytes and AST nodes for both Static and Dynamic configs
type GeneratedBundle struct {
	StaticYAML      []byte
	DynamicYAML     []byte
	BeforeDynNode   *yamlv3.Node
	AfterDynNode    *yamlv3.Node
	RequiresRestart bool
	RestartReasons  []string
	RouterErrors    []models.ValidationError
	RouterWarnings  []models.ValidationError
}

// ValidationInput is everything Validate needs to judge a rendered candidate.
type ValidationInput struct {
	Bundle    *GeneratedBundle
	Candidate []models.ConfigItemRow
	Previous  []models.ConfigItemRow
	Secrets   []models.SecretRow
}

// Validation is the outcome of Validate: blocking errors, advisories and env-mount instructions.
type Validation struct {
	Errors   []models.ValidationError
	Warnings []models.ValidationError
	EnvVars  []models.EnvMountInstruction
}

// Render is a pure function: it turns the candidate resource set into the three
// documents without touching the store, the files or Traefik. `previous` is the
// committed set the files on disk currently reflect; it lets Render remove
// managed keys that are gone from `items` and decide whether traefik.yml changes.
func (g *generator) Render(items, previous []models.ConfigItemRow, rawStatic, rawDynamic []byte) (*GeneratedBundle, error) {
	if len(items) > 1000 {
		return nil, ErrValidationFailed
	}
	beforeDynDoc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(rawDynamic), yaml.DefaultParserOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to parse existing dynamic config: %w", err)
	}
	staticDoc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(rawStatic), yaml.DefaultParserOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to parse existing static config: %w", err)
	}
	if err := assertOwned(beforeDynDoc, previous); err != nil {
		return nil, err
	}
	afterDynDoc, _, _ := yaml.ParseBoundedYAML(strings.NewReader(""), yaml.DefaultParserOptions())

	requiresRestart, restartReasons := staticChanges(previous, items)
	var tlsCertList []map[string]string
	type routerBinding struct {
		name string
		cfg  models.RouterConfig
	}
	var routerBindings []routerBinding

	// Deletion is absence: managed static keys that are no longer in the candidate are removed.
	present := map[string]bool{}
	for _, item := range items {
		present[string(item.Kind)+":"+item.Name] = true
	}
	pluginRemoved := false
	for _, item := range previous {
		if present[string(item.Kind)+":"+item.Name] {
			continue
		}
		switch item.Kind {
		case models.KindResolver:
			yaml.DeleteNestedKey(staticDoc, "certificatesResolvers", item.Name)
		case models.KindPlugin:
			yaml.DeleteNestedKey(staticDoc, "experimental", "plugins", item.Name)
			pluginRemoved = true
		case models.KindEntryPoint:
			if err := releaseEntryPoint(staticDoc, item); err != nil {
				return nil, err
			}
		}
	}

	// Pass 1: Render Static items, Routers, Services, Middlewares, Chains, and collect TLS certs & Domains
	for _, item := range items {
		payload := item.Payload
		if len(payload) == 0 {
			continue
		}
		switch item.Kind {
		case models.KindTLS:
			var certCfg models.CertificateConfig
			if err := json.Unmarshal(payload, &certCfg); err != nil {
				return nil, fmt.Errorf("invalid certificate '%s': %w", item.Name, err)
			}
			if certCfg.Purpose == "server" && certCfg.CertFile != "" && certCfg.KeyFile != "" {
				tlsCertList = append(tlsCertList, map[string]string{"certFile": certCfg.CertFile, "keyFile": certCfg.KeyFile})
			}
		case models.KindCrowdSec:
			continue
		case models.KindDomain:
			var domCfg models.DomainConfig
			if err := json.Unmarshal(payload, &domCfg); err != nil {
				return nil, fmt.Errorf("invalid domain '%s': %w", item.Name, err)
			}
		case models.KindRouter:
			var rCfg models.RouterConfig
			if err := json.Unmarshal(payload, &rCfg); err != nil {
				return nil, fmt.Errorf("invalid router '%s': %w", item.Name, err)
			}
			routerBindings = append(routerBindings, routerBinding{name: item.Name, cfg: rCfg})
		default:
			if err := injectItemIntoAST(staticDoc, afterDynDoc, item.Kind, item.Name, payload); err != nil {
				return nil, fmt.Errorf("failed to render %s '%s': %w", item.Kind, item.Name, err)
			}
		}
	}

	// Empty plugin declarations prevent Traefik from starting. Prune after all
	// projections so a replacement in the same operation is preserved.
	if pluginRemoved {
		pruneEmptyPluginDeclarations(staticDoc)
	}

	// Pass 2: Inject collected custom TLS certificates into `tls.certificates`
	if len(tlsCertList) > 0 {
		if err := yaml.SetNestedValue(afterDynDoc, tlsCertList, "tls", "certificates"); err != nil {
			return nil, fmt.Errorf("failed to render tls.certificates: %w", err)
		}
	}

	// Pass 3: Re-derive domain-bound routers from their CURRENT domain items so
	// domain edits (new SANs, changed resolver, changed main host) propagate
	// without re-saving every router.
	renderedForRouting, err := yaml.RenderYAML(staticDoc)
	if err != nil {
		return nil, err
	}
	view, err := routing.NewView(items, renderedForRouting)
	if err != nil {
		return nil, err
	}
	var routerErrors, routerWarnings []models.ValidationError
	for _, rb := range routerBindings {
		cfg, err := view.Project(rb.cfg)
		if err != nil {
			routerErrors = append(routerErrors, models.ValidationError{ItemID: "router:" + rb.name, Field: "ROUTER_CONFIGURATION", Message: err.Error()})
		}
		if cfg.CustomRule != "" && cfg.TLS != nil && models.RouterProtocol(cfg) == models.ServiceHTTP {
			routerWarnings = append(routerWarnings, models.ValidationError{ItemID: "router:" + rb.name, Field: "ADVANCED_RULE_TLS_UNVERIFIED", Message: "Custom rule wins over domain Host matching; certificate coverage for arbitrary matched hosts is not verified"})
		}
		if err = renderRouter(afterDynDoc, rb.name, cfg); err != nil {
			return nil, err
		}
	}

	renderedStatic, err := yaml.RenderYAML(staticDoc)
	if err != nil {
		return nil, err
	}
	if !requiresRestart {
		// traefik.yml is only rewritten when a console-owned static resource changed.
		renderedStatic = rawStatic
	}
	renderedDynamic, err := yaml.RenderYAML(afterDynDoc)
	if err != nil {
		return nil, err
	}
	if len(renderedStatic) > yaml.DefaultMaxYAMLSize || len(renderedDynamic) > yaml.DefaultMaxYAMLSize {
		return nil, ErrValidationFailed
	}
	return &GeneratedBundle{
		StaticYAML:      renderedStatic,
		DynamicYAML:     renderedDynamic,
		BeforeDynNode:   beforeDynDoc,
		AfterDynNode:    afterDynDoc,
		RequiresRestart: requiresRestart,
		RestartReasons:  restartReasons,
		RouterErrors:    routerErrors,
		RouterWarnings:  routerWarnings,
	}, nil
}

// releaseEntryPoint handles an entrypoint that left the managed set. One the console created is
// removed from traefik.yml. One it adopted gets its original definition back, so deleting it, or
// restoring a snapshot taken before the adoption, never drops a listener the operator declared.
func releaseEntryPoint(staticDoc *yamlv3.Node, item models.ConfigItemRow) error {
	var cfg models.EntryPointConfig
	if len(item.Payload) > 0 {
		if err := json.Unmarshal(item.Payload, &cfg); err != nil {
			return fmt.Errorf("invalid entrypoint '%s': %w", item.Name, err)
		}
	}
	if !cfg.Adopted {
		yaml.DeleteNestedKey(staticDoc, "entryPoints", item.Name)
		return nil
	}
	original := cfg.Original
	if original == nil {
		original = map[string]any{}
	}
	return yaml.SetNestedValue(staticDoc, original, "entryPoints", item.Name)
}

// staticChanges compares the static-scope resources (resolvers, plugins, entrypoints) of two
// resource sets. Any difference means traefik.yml changes and Traefik restarts.
func staticChanges(previous, next []models.ConfigItemRow) (bool, []string) {
	before := map[string]models.ConfigItemRow{}
	for _, item := range previous {
		if item.Scope == models.ScopeStatic {
			before[string(item.Kind)+":"+item.Name] = item
		}
	}
	var reasons []string
	seen := map[string]bool{}
	for _, item := range next {
		if item.Scope != models.ScopeStatic {
			continue
		}
		key := string(item.Kind) + ":" + item.Name
		seen[key] = true
		old, existed := before[key]
		switch {
		case !existed:
			reasons = append(reasons, fmt.Sprintf("%s '%s' was added", item.Kind, item.Name))
		case !bytes.Equal(old.Payload, item.Payload):
			reasons = append(reasons, fmt.Sprintf("%s '%s' was updated", item.Kind, item.Name))
		}
	}
	for key, item := range before {
		if !seen[key] {
			reasons = append(reasons, fmt.Sprintf("%s '%s' was removed", item.Kind, item.Name))
		}
	}
	sort.Strings(reasons)
	return len(reasons) > 0, reasons
}

// Validate checks the rendered candidate: its references against what Traefik
// reports for resources the console does not own, and the credential mounts.
func (g *generator) Validate(ctx context.Context, in ValidationInput) (*Validation, error) {
	observed, err := g.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	extServices, extMiddlewares := observed.HTTP.ValidationMaps()
	extTCPServices, extTCPMiddlewares := observed.TCP.ValidationMaps()
	extUDPServices, _ := observed.UDP.ValidationMaps()
	// Managed names (candidate and previous, so a deleted resource is not still "found" in the old runtime view)
	// are judged from the candidate itself, never from Traefik's current view of them.
	for _, set := range [][]models.ConfigItemRow{in.Candidate, in.Previous} {
		for _, item := range set {
			var targets []map[string]bool
			switch item.Kind {
			case models.KindService:
				targets = []map[string]bool{extServices, extTCPServices, extUDPServices}
			case models.KindMiddleware, models.KindChain:
				targets = []map[string]bool{extMiddlewares, extTCPMiddlewares}
			}
			for _, target := range targets {
				delete(target, item.Name)
				delete(target, item.Name+"@file")
			}
		}
	}
	result := g.validator.ValidateDynamicASTWithProtocolReferences(in.Bundle.AfterDynNode, validation.ExternalReferences{
		HTTPServices: extServices, HTTPMiddlewares: extMiddlewares, TCPServices: extTCPServices, TCPMiddlewares: extTCPMiddlewares, UDPServices: extUDPServices,
	})
	out := &Validation{
		Errors:   append([]models.ValidationError(nil), in.Bundle.RouterErrors...),
		Warnings: append([]models.ValidationError(nil), in.Bundle.RouterWarnings...),
	}
	for _, issue := range result.Warnings {
		out.Warnings = append(out.Warnings, models.ValidationError{ItemID: issue.Path, Field: issue.RuleCode, Message: issue.Message})
	}
	for _, issue := range result.Errors {
		out.Errors = append(out.Errors, models.ValidationError{ItemID: issue.Path, Field: issue.RuleCode, Message: issue.Message})
	}
	out.EnvVars = checkContainerSecretEnvVars(ctx, in.Candidate, in.Secrets, g.runtime, g.presetEnv)
	return out, nil
}

// checkContainerSecretEnvVars lists the credential variables the candidate resolvers need and whether
// Traefik already has each one. The live container (Docker inspect) is authoritative; without Docker
// access the variables declared in Traefik's shipped env file (presetEnv) are used instead.
func checkContainerSecretEnvVars(ctx context.Context, items []models.ConfigItemRow, allSecrets []models.SecretRow, d docker.TraefikContainerInspector, presetEnv map[string]string) []models.EnvMountInstruction {
	if len(allSecrets) == 0 {
		return nil
	}
	used := map[string]bool{}
	for _, item := range items {
		if item.Kind != models.KindResolver {
			continue
		}
		var cfg models.ResolverInput
		if json.Unmarshal(item.Payload, &cfg) != nil {
			return []models.EnvMountInstruction{{EnvVarName: "unknown", AlreadySet: false}}
		}
		for env := range cfg.Values {
			used[env+"_FILE"] = true
		}
	}
	var containerEnv map[string]string
	if d != nil {
		if containerStatus, err := d.InspectTraefikContainer(ctx); err == nil && containerStatus != nil {
			containerEnv = containerStatus.SecretFileEnv
		}
	}
	if containerEnv == nil {
		containerEnv = presetEnv
	}
	var instructions []models.EnvMountInstruction
	for _, sec := range allSecrets {
		if sec.EnvVarName == "" || !used[sec.EnvVarName] {
			continue
		}
		instructions = append(instructions, models.EnvMountInstruction{
			EnvVarName: sec.EnvVarName,
			FilePath:   sec.FilePath,
			AlreadySet: containerEnv != nil && containerEnv[sec.EnvVarName] == sec.FilePath,
		})
	}
	return instructions
}

// injectItemIntoAST converts a single typed JSON payload from SQLite into the proper Traefik YAML structure
func injectItemIntoAST(staticDoc, dynamicDoc *yamlv3.Node, kind models.ItemKind, name string, rawJSON json.RawMessage) error {
	switch kind {
	case models.KindResolver:
		var cfg models.ResolverInput
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		return yaml.SetNestedValue(staticDoc, cfg.RenderedNode, "certificatesResolvers", name)

	case models.KindPlugin:
		var cfg models.StaticPluginConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		return yaml.SetNestedValue(staticDoc, cfg, "experimental", "plugins", name)

	case models.KindEntryPoint:
		var cfg models.EntryPointConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		if len(cfg.RenderedNode) == 0 {
			return fmt.Errorf("entrypoint has no rendered configuration")
		}
		return yaml.SetNestedValue(staticDoc, cfg.RenderedNode, "entryPoints", name)

	case models.KindRouter:
		var cfg models.RouterConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		return renderRouter(dynamicDoc, name, cfg)

	case models.KindService:
		var cfg models.ServiceConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		return renderService(dynamicDoc, name, cfg)

	case models.KindMiddleware:
		var cfg models.MiddlewareConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		protocol := models.MiddlewareProtocol(cfg)
		if cfg.Type == "plugin" && cfg.PluginName != "" {
			return yaml.SetNestedValue(dynamicDoc, cfg.Spec, protocol, "middlewares", name, "plugin", cfg.PluginName)
		}
		return yaml.SetNestedValue(dynamicDoc, cfg.Spec, protocol, "middlewares", name, cfg.Type)

	case models.KindChain:
		var cfg models.ChainConfig
		if err := json.Unmarshal(rawJSON, &cfg); err != nil {
			return err
		}
		return yaml.SetNestedValue(dynamicDoc, cfg, "http", "middlewares", name, "chain")
	}
	return nil
}
