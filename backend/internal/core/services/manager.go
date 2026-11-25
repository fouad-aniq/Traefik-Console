package services

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/status"
)

type ServiceManager interface {
	// ListServices lists HTTP, TCP and UDP services, managed and external; each carries its protocol.
	ListServices(ctx context.Context) ([]models.UIResource[models.ServiceConfig], error)
	// GetService finds a service by name; protocol narrows the search ("" = any, http first).
	GetService(ctx context.Context, name, protocol string) (*models.UIResource[models.ServiceConfig], error)
	DiagnoseService(ctx context.Context, name, protocol string) (status.ServiceDiagnostic, error)
	// RuntimeRouters lists the TCP or UDP routers Traefik reports (the console manages none).
	RuntimeRouters(ctx context.Context, protocol string) ([]models.UIResource[models.RouterConfig], error)

	UpsertService(ctx context.Context, name string, input models.ServiceConfig, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error)
	DeleteService(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error)
}

var resolverNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

var protocols = []string{models.ServiceHTTP, models.ServiceTCP, models.ServiceUDP}

func runtimeOf(observed *status.RuntimeSnapshot, protocol string) *status.RuntimeResources {
	switch protocol {
	case models.ServiceTCP:
		return &observed.TCP
	case models.ServiceUDP:
		return &observed.UDP
	default:
		return &observed.HTTP
	}
}

func (m *serviceManager) ListServices(ctx context.Context) ([]models.UIResource[models.ServiceConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindService)
	if err != nil {
		return nil, err
	}
	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool) // protocol:runtime key
	result := make([]models.UIResource[models.ServiceConfig], 0)

	// 1. Console-managed services from SQLite.
	for _, row := range dbRows {
		cfg, err := presentation.DecodeUIConfig[models.ServiceConfig](row)
		if err != nil {
			return nil, err
		}
		cfg.Protocol = models.ServiceProtocol(cfg)
		seen[cfg.Protocol+":"+row.Name+"@file"] = true

		health := "unknown"
		var errMsg string
		if live, ok := runtimeOf(observed, cfg.Protocol).Services[row.Name+"@file"]; ok {
			if live.Status == "enabled" {
				health = "loaded"
			}
			if live.Status == "error" || live.ErrorMsg != "" {
				health = "error"
				errMsg = live.ErrorMsg
			}
		}
		result = append(result, models.UIResource[models.ServiceConfig]{
			ID: row.ID, UpdatedAt: presentation.ParseTimestamp(row.UpdatedAt), Name: row.Name,
			Origin: "managed", Provider: "file", Status: models.StatusActive, ReadOnly: false,
			LiveHealth: health, LiveErrorMsg: errMsg, Config: cfg, Version: row.Version,
		})
	}

	// 2. External services Traefik reports (@docker, @internal, or hand-written @file), per protocol.
	for _, protocol := range protocols {
		for fullKey, ext := range runtimeOf(observed, protocol).Services {
			if seen[protocol+":"+fullKey] || (ext.Provider == "file" && seen[protocol+":"+ext.Name+"@file"]) {
				continue
			}
			cfg := parseExternalService(protocol, ext.Raw)
			result = append(result, models.UIResource[models.ServiceConfig]{
				ID: "external:" + protocol + "-service:" + fullKey, Name: fullKey,
				Origin: "external", Provider: ext.Provider, Status: models.StatusActive, ReadOnly: true,
				LiveHealth: ext.Status, LiveErrorMsg: ext.ErrorMsg, Config: cfg,
			})
		}
	}
	return result, nil
}

func (m *serviceManager) GetService(ctx context.Context, name, protocol string) (*models.UIResource[models.ServiceConfig], error) {
	all, err := m.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	// A bare name and name@file are the same file service.
	want := strings.TrimSuffix(name, "@file")
	var found *models.UIResource[models.ServiceConfig]
	for i := range all {
		item := &all[i]
		if item.Name != name && strings.TrimSuffix(item.Name, "@file") != want {
			continue
		}
		if protocol != "" && item.Config.Protocol != protocol {
			continue
		}
		if found == nil || item.Config.Protocol == models.ServiceHTTP {
			found = item
		}
	}
	if found == nil {
		return nil, files.ErrSourceFileNotFound
	}
	return found, nil
}

func (m *serviceManager) DiagnoseService(ctx context.Context, name, protocol string) (status.ServiceDiagnostic, error) {
	if protocol == "" {
		protocol = models.ServiceHTTP
	}
	if protocol != models.ServiceHTTP && protocol != models.ServiceTCP && protocol != models.ServiceUDP {
		return status.ServiceDiagnostic{}, ErrServiceInvalidProtocol
	}
	return m.inspector.DiagnoseNamedService(ctx, protocol, strings.TrimSuffix(name, "@file"))
}

func (m *serviceManager) RuntimeRouters(ctx context.Context, protocol string) ([]models.UIResource[models.RouterConfig], error) {
	if protocol != models.ServiceTCP && protocol != models.ServiceUDP {
		return nil, ErrServiceInvalidProtocol
	}
	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	return runtimeRouters(runtimeOf(observed, protocol)), nil
}

func runtimeRouters(r *status.RuntimeResources) []models.UIResource[models.RouterConfig] {
	out := make([]models.UIResource[models.RouterConfig], 0, len(r.Routers))
	for fullKey, ext := range r.Routers {
		cfg := models.RouterConfig{}
		cfg.Rule, _ = ext.Raw["rule"].(string)
		cfg.Service, _ = ext.Raw["service"].(string)
		if eps, ok := ext.Raw["entryPoints"].([]any); ok {
			for _, ep := range eps {
				if s, ok := ep.(string); ok {
					cfg.EntryPoints = append(cfg.EntryPoints, s)
				}
			}
		}
		if _, ok := ext.Raw["tls"].(map[string]any); ok {
			cfg.TLS = &models.RouterTLS{Enabled: true}
		}
		out = append(out, models.UIResource[models.RouterConfig]{
			ID: "external:router:" + fullKey, Name: fullKey, Origin: "external", Provider: ext.Provider,
			Status: models.StatusActive, ReadOnly: true, LiveHealth: ext.Status, Config: cfg,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *serviceManager) UpsertService(ctx context.Context, name string, input models.ServiceConfig, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	name = strings.TrimSpace(name)
	if !resolverNameRegex.MatchString(name) {
		return nil, ErrServiceInvalidName
	}
	input, err := normalize(input)
	if err != nil {
		return nil, err
	}
	// Traefik keeps each protocol in its own section: a service never moves between them.
	existing, err := m.store.GetItem(ctx, models.KindService, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		var stored models.ServiceConfig
		if e := json.Unmarshal(existing.Payload, &stored); e == nil && models.ServiceProtocol(stored) != input.Protocol {
			return nil, fmt.Errorf("%w: '%s' is a %s service", ErrServiceInvalidProtocol, name, models.ServiceProtocol(stored))
		}
	}
	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	row := models.ConfigItemRow{ID: "service:" + name, Kind: models.KindService, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, func(ctx context.Context) (*models.UIResource[models.ServiceConfig], error) {
		return m.committed(ctx, name)
	})
}

func (m *serviceManager) DeleteService(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	existing, err := m.store.GetItem(ctx, models.KindService, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, m.unmanaged(ctx, name)
	}
	var cfg models.ServiceConfig
	if err := json.Unmarshal(existing.Payload, &cfg); err != nil {
		return nil, err
	}
	protocol := models.ServiceProtocol(cfg)

	// Block deletion while a managed router points to the service...
	// (a router only uses services of its own protocol).
	routers, err := m.store.ListItemsByKind(ctx, models.KindRouter)
	if err != nil {
		return nil, err
	}
	for _, rRow := range routers {
		var rCfg models.RouterConfig
		if json.Unmarshal(rRow.Payload, &rCfg) == nil && models.RouterProtocol(rCfg) == protocol && qualified(rCfg.Service, "file") == name+"@file" {
			return nil, fmt.Errorf("%w: used by router '%s'", ErrServiceInUse, rRow.Name)
		}
	}
	// ...or a router of another provider (docker labels, hand-written files) that Traefik reports.
	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range runtimeRouters(runtimeOf(observed, protocol)) {
		if r.Config.Service != "" && qualified(r.Config.Service, r.Provider) == name+"@file" {
			return nil, fmt.Errorf("%w: used by router '%s'", ErrServiceInUse, r.Name)
		}
	}

	return engine.Write[models.ServiceConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindService, Name: name}}}, opts, nil)
}

// unmanaged explains why a service with no stored row cannot be changed: another provider's (or
// an unmanaged file's) service is read-only, a name nobody declares is not found.
func (m *serviceManager) unmanaged(ctx context.Context, name string) error {
	if strings.Contains(name, "@") {
		return files.ErrCannotModifyReadOnly
	}
	if observed, err := m.inspector.ObserveRuntime(ctx); err == nil {
		for _, protocol := range protocols {
			if _, ok := runtimeOf(observed, protocol).Services[name+"@file"]; ok {
				return files.ErrCannotModifyReadOnly
			}
		}
	}
	return files.ErrSourceFileNotFound
}

// parseExternalService reads the definition fields the console shows from Traefik's runtime view.
func parseExternalService(protocol string, raw map[string]any) models.ServiceConfig {
	cfg := models.ServiceConfig{Protocol: protocol}
	lb, _ := raw["loadBalancer"].(map[string]any)
	if lb == nil {
		return cfg
	}
	srvs, _ := lb["servers"].([]any)
	anyTLS := false
	for _, s := range srvs {
		sMap, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if protocol == models.ServiceHTTP {
			if u, ok := sMap["url"].(string); ok {
				cfg.Servers = append(cfg.Servers, models.ServerEndpoint{URL: u})
			}
			continue
		}
		if a, ok := sMap["address"].(string); ok {
			cfg.Servers = append(cfg.Servers, models.ServerEndpoint{Address: a})
		}
		if tls, _ := sMap["tls"].(bool); tls {
			anyTLS = true
		}
	}
	switch protocol {
	case models.ServiceHTTP:
		if v, ok := lb["passHostHeader"].(bool); ok {
			cfg.PassHostHeader = &v
		}
		if hc, ok := lb["healthCheck"].(map[string]any); ok {
			h := &models.ServiceHealth{}
			h.Path, _ = hc["path"].(string)
			h.Interval, _ = hc["interval"].(string)
			h.Timeout, _ = hc["timeout"].(string)
			cfg.HealthCheck = h
		}
	case models.ServiceTCP:
		opts := models.TCPServiceOptions{TLS: anyTLS}
		if hc, ok := lb["healthCheck"].(map[string]any); ok {
			h := &models.TCPHealthCheck{}
			h.Interval, _ = hc["interval"].(string)
			h.Timeout, _ = hc["timeout"].(string)
			h.Send, _ = hc["send"].(string)
			h.Expect, _ = hc["expect"].(string)
			if p, ok := hc["port"].(float64); ok {
				h.Port = int(p)
			}
			opts.HealthCheck = h
		}
		if opts != (models.TCPServiceOptions{}) {
			cfg.TCP = &opts
		}
	}
	return cfg
}

// committed re-reads a saved service from SQLite only; see presentation.ManagedResource.
func (m *serviceManager) committed(ctx context.Context, name string) (*models.UIResource[models.ServiceConfig], error) {
	row, err := m.store.GetItem(ctx, models.KindService, name)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, files.ErrSourceFileNotFound
	}
	res, err := presentation.ManagedResource[models.ServiceConfig](*row, "file")
	if err == nil {
		res.Config.Protocol = models.ServiceProtocol(res.Config)
	}
	return res, err
}
