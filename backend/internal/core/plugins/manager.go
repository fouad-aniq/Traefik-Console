package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/traefik-console/internal/traefik/validation"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/status"
)

var (
	shortNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
)

// InstallFromYAMLRequest allows users to paste a static/dynamic YAML snippet from the Traefik Plugin Shop
type InstallFromYAMLRequest struct {
	ShortName      string `json:"short_name"`                // Optional override
	RawYAML        string `json:"raw_yaml"`                  // Pasted YAML snippet
	MiddlewareName string `json:"middleware_name,omitempty"` // Optional: also create a middleware
}

type UpdatePluginRequest struct {
	Version string `json:"version"`
}
type CatalogInstallRequest struct {
	Alias      string `json:"alias"`
	ModuleName string `json:"module_name"`
	Version    string `json:"version"`
}

type PluginManager interface {
	InstallCatalogPlugin(context.Context, CatalogInstallRequest, models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
	ReadAvailableCatalogPage(context.Context, string, int, int, bool) (*models.CatalogPageView, error)
	ReadPluginUpdates(context.Context) (*models.PluginUpdates, error)
	ListPluginViews(context.Context) ([]models.PluginView, error)
	RunCatalogChecks(context.Context)
	ApplyPluginUpdate(context.Context, string, string, models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
	ReadCatalogPage(context.Context, string, string, int, int, bool) (*models.CatalogPageView, error)
	SearchCatalog(ctx context.Context, query string) ([]models.CatalogPluginView, error)
	SyncCatalog(ctx context.Context) (int, error)
	ListInstalledPlugins(ctx context.Context) ([]models.UIResource[models.StaticPluginConfig], error)
	UpsertPlugin(ctx context.Context, shortName string, cfg models.StaticPluginConfig, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
	InstallFromYAML(ctx context.Context, req InstallFromYAMLRequest, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
	UpdatePlugin(ctx context.Context, alias string, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
	DeletePlugin(ctx context.Context, shortName string, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error)
}

type pluginManager struct {
	syncMu  sync.Mutex
	store   pluginStore
	files   staticConfigReader
	catalog catalogReader
	applier engine.Applier
}

func NewManager(store pluginStore, filesMgr staticConfigReader, catalog catalogReader, applier engine.Applier) PluginManager {
	return &pluginManager{
		store:   store,
		files:   filesMgr,
		catalog: catalog,
		applier: applier,
	}
}

func (m *pluginManager) ListInstalledPlugins(ctx context.Context) ([]models.UIResource[models.StaticPluginConfig], error) {
	dbRows, err := m.store.ListItemsByKind(ctx, models.KindPlugin)
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
	result := make([]models.UIResource[models.StaticPluginConfig], 0)

	// 1. Managed Plugins from SQLite
	for _, row := range dbRows {
		seen[row.Name] = true
		cfg, err := presentation.DecodeUIConfig[models.StaticPluginConfig](row)
		if err != nil {
			return nil, err
		}

		health := "unknown"

		result = append(result, models.UIResource[models.StaticPluginConfig]{
			ID:         row.ID,
			UpdatedAt:  presentation.ParseTimestamp(row.UpdatedAt),
			Name:       row.Name,
			Origin:     "managed",
			Provider:   "static",
			Status:     models.StatusActive,
			ReadOnly:   false,
			LiveHealth: health,
			Config:     cfg,
			Version:    row.Version,
		})
	}

	// 2. External Static Plugins found in traefik.yml
	for name, extItem := range extSnap.StaticPlugins {
		if seen[name] {
			continue
		}

		var cfg models.StaticPluginConfig
		if mod, ok := extItem.Raw["moduleName"].(string); ok {
			cfg.ModuleName = mod
		}
		if ver, ok := extItem.Raw["version"].(string); ok {
			cfg.Version = ver
		}

		result = append(result, models.UIResource[models.StaticPluginConfig]{
			ID:         "external:plugin:" + name,
			Name:       name,
			Origin:     "external",
			Provider:   "static",
			Status:     models.StatusActive,
			ReadOnly:   true,
			LiveHealth: "unknown",
			Config:     cfg,
		})
	}

	return result, nil
}

func (m *pluginManager) UpsertPlugin(ctx context.Context, shortName string, cfg models.StaticPluginConfig, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	shortName = strings.TrimSpace(shortName)
	cfg.ModuleName = strings.TrimSpace(cfg.ModuleName)
	cfg.Version = strings.TrimSpace(cfg.Version)

	if !shortNameRegex.MatchString(shortName) {
		return nil, ErrPluginInvalidName
	}
	if !validation.ValidPluginModule(cfg.ModuleName) {
		return nil, ErrPluginInvalidModule
	}
	if !validation.ValidPluginVersion(cfg.Version) {
		return nil, ErrPluginInvalidVersion
	}

	rawJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	row := models.ConfigItemRow{ID: "plugin:" + shortName, Kind: models.KindPlugin, Name: shortName, Scope: models.ScopeStatic, Payload: rawJSON}
	return m.write(ctx, models.Mutation{Upserts: []models.ConfigItemRow{row}}, opts, shortName)
}

func (m *pluginManager) write(ctx context.Context, mut models.Mutation, opts models.WriteOptions, name string) (*models.WriteResult[models.StaticPluginConfig], error) {
	return engine.Write(ctx, m.applier, mut, opts, func(ctx context.Context) (*models.UIResource[models.StaticPluginConfig], error) {
		return m.getPlugin(ctx, name)
	})
}

func (m *pluginManager) getPlugin(ctx context.Context, name string) (*models.UIResource[models.StaticPluginConfig], error) {
	row, err := m.store.GetItem(ctx, models.KindPlugin, name)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("persisted plugin missing after commit")
	}
	cfg, err := presentation.DecodeUIConfig[models.StaticPluginConfig](*row)
	if err != nil {
		return nil, err
	}
	return &models.UIResource[models.StaticPluginConfig]{ID: row.ID, Name: row.Name, UpdatedAt: presentation.ParseTimestamp(row.UpdatedAt), Origin: "managed", Provider: "static", Status: models.StatusActive, LiveHealth: "unknown", Config: cfg, Version: row.Version}, nil
}

// InstallFromYAML parses a pasted Traefik Shop YAML snippet (either static experimental.plugins,
// dynamic http.middlewares, or both) and commits them as one operation.
func (m *pluginManager) InstallFromYAML(ctx context.Context, req InstallFromYAMLRequest, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	doc, _, err := yaml.ParseBoundedYAML(strings.NewReader(req.RawYAML), yaml.DefaultParserOptions())
	if err != nil {
		return nil, ErrPluginInvalidYAML
	}
	var root map[string]any
	if doc.Decode(&root) != nil {
		return nil, ErrPluginInvalidYAML
	}
	for key := range root {
		if key != "experimental" && key != "plugins" && key != "moduleName" && key != "version" && key != "http" {
			return nil, ErrPluginInvalidYAML
		}
	}
	var defs map[string]any
	if exp, ok := root["experimental"].(map[string]any); ok {
		if len(exp) != 1 {
			return nil, ErrPluginInvalidYAML
		}
		defs, _ = exp["plugins"].(map[string]any)
	} else if v, ok := root["plugins"].(map[string]any); ok {
		defs = v
	} else if _, ok := root["moduleName"]; ok {
		defs = map[string]any{req.ShortName: root}
	}
	if len(defs) != 1 {
		return nil, ErrPluginInvalidYAML
	}
	var alias string
	var cfg models.StaticPluginConfig
	for name, v := range defs {
		alias = name
		spec, ok := v.(map[string]any)
		if !ok || len(spec) != 2 {
			return nil, ErrPluginInvalidYAML
		}
		cfg.ModuleName, _ = spec["moduleName"].(string)
		cfg.Version, _ = spec["version"].(string)
	}
	originalAlias := alias
	if req.ShortName != "" {
		alias = req.ShortName
	}
	if !shortNameRegex.MatchString(alias) || !validation.ValidPluginModule(cfg.ModuleName) || !validation.ValidPluginVersion(cfg.Version) {
		return nil, ErrPluginInvalidYAML
	}
	raw, _ := json.Marshal(cfg)
	mut := models.Mutation{Upserts: []models.ConfigItemRow{{ID: "plugin:" + alias, Kind: models.KindPlugin, Name: alias, Scope: models.ScopeStatic, Payload: raw}}}
	if h, ok := root["http"].(map[string]any); ok {
		if len(h) != 1 {
			return nil, ErrPluginInvalidYAML
		}
		mws, ok := h["middlewares"].(map[string]any)
		if !ok || len(mws) != 1 {
			return nil, ErrPluginInvalidYAML
		}
		for name, v := range mws {
			obj, ok := v.(map[string]any)
			if !ok || len(obj) != 1 {
				return nil, ErrPluginInvalidYAML
			}
			plugins, ok := obj["plugin"].(map[string]any)
			if !ok || len(plugins) != 1 {
				return nil, ErrPluginInvalidYAML
			}
			for plugin, spec := range plugins {
				if plugin != originalAlias {
					return nil, ErrPluginInvalidYAML
				}
				values, ok := spec.(map[string]any)
				if !ok {
					return nil, ErrPluginInvalidYAML
				}
				if req.MiddlewareName != "" {
					name = req.MiddlewareName
				}
				if !shortNameRegex.MatchString(name) {
					return nil, ErrPluginInvalidName
				}
				raw, _ := json.Marshal(models.MiddlewareConfig{Type: "plugin", PluginName: alias, Spec: values})
				mut.Upserts = append(mut.Upserts, models.ConfigItemRow{ID: "middleware:" + name, Kind: models.KindMiddleware, Name: name, Scope: models.ScopeDynamic, Payload: raw})
			}
		}
	}
	return m.write(ctx, mut, opts, alias)
}

// UpdatePlugin applies the newest cataloged version of an installed, managed plugin. The target comes
// from the cached catalog, never from the caller, so an update can only move to a known release.
func (m *pluginManager) UpdatePlugin(ctx context.Context, alias string, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	alias = strings.TrimSpace(alias)
	if !shortNameRegex.MatchString(alias) {
		return nil, ErrPluginInvalidName
	}
	installed, err := m.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	var current *installedPlugin
	for i := range installed {
		if installed[i].Name == alias {
			current = &installed[i]
			break
		}
	}
	switch {
	case current == nil:
		return nil, files.ErrSourceFileNotFound
	case current.ReadOnly:
		return nil, files.ErrCannotModifyReadOnly
	}
	rows, err := m.store.SearchPluginCatalog(ctx, current.Config.ModuleName)
	if err != nil {
		return nil, err
	}
	var latest string
	for _, row := range rows {
		if validation.PluginModuleKey(row.ModuleName) == validation.PluginModuleKey(current.Config.ModuleName) {
			latest = row.LatestVersion
			break
		}
	}
	if latest == "" {
		return nil, ErrPluginNotInCatalog
	}
	if c, ok := compareVersions(current.Config.Version, latest); !ok || c >= 0 {
		return nil, ErrPluginNoUpdate
	}
	return m.UpsertPlugin(ctx, alias, models.StaticPluginConfig{ModuleName: current.Config.ModuleName, Version: latest}, opts)
}

func (m *pluginManager) DeletePlugin(ctx context.Context, shortName string, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	// Ensure no middleware references this plugin
	mws, err := m.store.ListItemsByKind(ctx, models.KindMiddleware)
	if err != nil {
		return nil, err
	}
	for _, row := range mws {
		var cfg models.MiddlewareConfig
		if json.Unmarshal(row.Payload, &cfg) == nil {
			if cfg.Type == "plugin" && cfg.PluginName == shortName {
				return nil, fmt.Errorf("%w: middleware '%s'", ErrPluginInUse, row.Name)
			}
		}
	}
	return engine.Write[models.StaticPluginConfig](ctx, m.applier, models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindPlugin, Name: shortName}}}, opts, nil)
}

type pluginStore interface {
	db.ItemReader
	db.CatalogStore
}
