package routers

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"errors"
	"fmt"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/routing"
	"github.com/traefik-console/internal/traefik/status"
)

type RouterManager interface {
	ListRouters(ctx context.Context) ([]models.UIResource[models.RouterConfig], error)
	GetRouter(ctx context.Context, name string) (*models.UIResource[models.RouterConfig], error)

	UpsertRouter(ctx context.Context, name string, input models.RouterConfig, opts models.WriteOptions) (*models.WriteResult[models.RouterConfig], error)
	DeleteRouter(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.RouterConfig], error)
}

var resolverNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func (m *routerManager) ListRouters(ctx context.Context) ([]models.UIResource[models.RouterConfig], error) {
	view, err := m.loadView(ctx)
	if err != nil {
		return nil, err
	}
	var dbRows []models.ConfigItemRow
	for _, row := range view.rows {
		if row.Kind == models.KindRouter {
			dbRows = append(dbRows, row)
		}
	}

	observed, err := m.inspector.ObserveRuntime(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool) // protocol:runtime key
	result := make([]models.UIResource[models.RouterConfig], 0)

	// 1. Console-Managed Routers from SQLite
	for _, row := range dbRows {
		item, err := view.resource(row)
		if err != nil {
			return nil, err
		}
		protocol := models.RouterProtocol(item.Config)
		seen[protocol+":"+row.Name+"@file"] = true

		health := item.LiveHealth
		errMsg := item.LiveErrorMsg
		if item.LiveHealth != "error" {
			if liveItem, ok := runtimeOf(observed, protocol).Routers[row.Name+"@file"]; ok {
				if liveItem.Status == "enabled" {
					health = "loaded"
				}
				if liveItem.Status == "error" || liveItem.ErrorMsg != "" {
					health = "error"
					errMsg = liveItem.ErrorMsg
				}
			}
		}

		item.LiveHealth, item.LiveErrorMsg = health, errMsg
		result = append(result, *item)
	}

	// 2. External Routers from Traefik (/api/rawdata), per protocol
	for _, protocol := range Protocols {
		for fullKey, extItem := range runtimeOf(observed, protocol).Routers {
			if seen[protocol+":"+fullKey] || (extItem.Provider == "file" && seen[protocol+":"+extItem.Name+"@file"]) {
				continue
			}
			id := "external:router:" + fullKey
			if protocol != models.ServiceHTTP {
				id = "external:" + protocol + "-router:" + fullKey
			}
			cfg := parseExternalRouter(extItem.Raw)
			cfg.Protocol = protocol
			result = append(result, models.UIResource[models.RouterConfig]{
				ID:           id,
				Name:         fullKey,
				Origin:       "external",
				Provider:     extItem.Provider,
				Status:       models.StatusActive,
				ReadOnly:     true,
				LiveHealth:   extItem.Status,
				LiveErrorMsg: extItem.ErrorMsg,
				Config:       cfg,
			})
		}
	}

	return result, nil
}

// Protocols are the three router sections Traefik keeps apart.
var Protocols = []string{models.ServiceHTTP, models.ServiceTCP, models.ServiceUDP}

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

func (m *routerManager) GetRouter(ctx context.Context, name string) (*models.UIResource[models.RouterConfig], error) {
	all, err := m.ListRouters(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range all {
		if item.Name == name || strings.TrimSuffix(item.Name, "@file") == name {
			return &item, nil
		}
	}
	return nil, files.ErrSourceFileNotFound
}

func (m *routerManager) UpsertRouter(ctx context.Context, name string, input models.RouterConfig, opts models.WriteOptions) (*models.WriteResult[models.RouterConfig], error) {
	name = strings.TrimSpace(name)
	if input.Priority > 9007199254740991 || input.Priority < -9007199254740991 {
		return nil, ErrRouterRuleEmpty
	}
	if !resolverNameRegex.MatchString(name) {
		return nil, ErrRouterInvalidName
	}
	if strings.TrimSpace(input.Service) == "" {
		return nil, ErrRouterNoService
	}
	protocol := models.RouterProtocol(input)
	if protocol != models.ServiceHTTP && protocol != models.ServiceTCP && protocol != models.ServiceUDP {
		return nil, ErrRouterInvalidProtocol
	}
	if protocol == models.ServiceHTTP && strings.TrimSpace(input.Service) == models.NoopService && !hasMiddleware(input.Middlewares) {
		return nil, ErrRouterNoBackend
	}
	// Traefik keeps each protocol in its own section: a router never moves between them.
	if existing, err := m.store.GetItem(ctx, models.KindRouter, name); err != nil {
		return nil, err
	} else if existing != nil {
		var stored models.RouterConfig
		if json.Unmarshal(existing.Payload, &stored) == nil && models.RouterProtocol(stored) != protocol {
			return nil, fmt.Errorf("%w: '%s' is a %s router", ErrRouterInvalidProtocol, name, models.RouterProtocol(stored))
		}
	}
	view, err := m.loadView(ctx)
	if err != nil {
		return nil, err
	}
	input, err = view.project(input)
	if errors.Is(err, routing.ErrDomainMissing) {
		return nil, ErrRouterDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRouterRuleEmpty, err)
	}

	rawJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	row := models.ConfigItemRow{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: rawJSON}
	return engine.Write(ctx, m.applier, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, func(ctx context.Context) (*models.UIResource[models.RouterConfig], error) {
		return m.committed(ctx, name)
	})
}

// committed re-reads a saved router from SQLite only. A write that already succeeded must never
// fail because Traefik's runtime API is unreachable, so no live-health overlay is applied here.
func (m *routerManager) committed(ctx context.Context, name string) (*models.UIResource[models.RouterConfig], error) {
	view, err := m.loadView(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range view.rows {
		if row.Kind == models.KindRouter && row.Name == name {
			return view.resource(row)
		}
	}
	return nil, files.ErrSourceFileNotFound
}

func (m *routerManager) DeleteRouter(ctx context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.RouterConfig], error) {
	existing, err := m.store.GetItem(ctx, models.KindRouter, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, files.ErrSourceFileNotFound
	}
	return engine.Write[models.RouterConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindRouter, Name: name}}}, opts, nil)
}

func hasMiddleware(names []string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			return true
		}
	}
	return false
}

func parseExternalRouter(raw map[string]any) models.RouterConfig {
	var cfg models.RouterConfig
	if raw == nil {
		return cfg
	}
	if rule, ok := raw["rule"].(string); ok {
		cfg.Rule = rule
	}
	if svc, ok := raw["service"].(string); ok {
		cfg.Service = svc
	}
	if p, ok := raw["priority"].(float64); ok && p > 0 && p < 1<<31 {
		cfg.Priority = int(p)
	}
	if eps, ok := raw["entryPoints"].([]any); ok {
		for _, ep := range eps {
			if s, ok := ep.(string); ok {
				cfg.EntryPoints = append(cfg.EntryPoints, s)
			}
		}
	}
	if mws, ok := raw["middlewares"].([]any); ok {
		for _, mw := range mws {
			if s, ok := mw.(string); ok {
				cfg.Middlewares = append(cfg.Middlewares, s)
			}
		}
	}
	if tlsMap, ok := raw["tls"].(map[string]any); ok {
		cfg.TLS = &models.RouterTLS{Enabled: true}
		if cr, ok := tlsMap["certResolver"].(string); ok {
			cfg.TLS.CertResolver = cr
		}
		if pass, ok := tlsMap["passthrough"].(bool); ok {
			cfg.TLS.Passthrough = pass
		}
	}
	return cfg
}
