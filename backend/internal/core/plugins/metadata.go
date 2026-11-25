package plugins

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/traefik-console/internal/models"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

func catalogVersions(row models.CatalogPluginRow) []string {
	var versions []string
	_ = json.Unmarshal([]byte(row.VersionsJSON), &versions)

	var out []string
	seen := map[string]bool{}
	for _, v := range append(versions, row.LatestVersion) {
		if validation.ValidPluginVersion(v) && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}

	sort.SliceStable(out, func(i, j int) bool { c, _ := compareVersions(out[i], out[j]); return c > 0 })
	return out
}

func recommendedVersion(row models.CatalogPluginRow, current string) string {
	versions := catalogVersions(row)
	for _, v := range versions {
		if !strings.Contains(strings.SplitN(v, "+", 2)[0], "-") {
			return v
		}
	}
	if strings.Contains(current, "-") && len(versions) > 0 {
		return versions[0]
	}
	return ""
}

func (m *pluginManager) ListInstalledPluginsIfAvailable(ctx context.Context) ([]installedPlugin, error) {
	if m.files == nil {
		return nil, errInventoryUnavailable
	}
	return m.ListInstalledPlugins(ctx)
}

// ReadAvailableCatalogPage Filter before counting and slicing, with one bounded cache snapshot. The full
// catalog remains cached for update checks even when a module is hidden here.
func (m *pluginManager) ReadAvailableCatalogPage(ctx context.Context, query string, limit, offset int, descending bool) (*models.CatalogPageView, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, ErrPluginInvalidYAML
	}

	snapshot, err := m.store.ReadPluginCatalogSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	installed, inventoryErr := m.ListInstalledPluginsIfAvailable(ctx)

	out := &models.CatalogPageView{
		Items:          []models.CatalogPluginView{},
		Limit:          limit,
		Offset:         offset,
		LastSyncedAt:   snapshot.Refresh.LastSuccessAt,
		Refresh:        snapshot.Refresh,
		InventoryKnown: inventoryErr == nil,
	}
	if inventoryErr != nil {
		return out, nil
	}

	reserved := map[string]bool{}
	for _, p := range installed {
		reserved[validation.PluginModuleKey(p.Config.ModuleName)] = true
	}

	// Re-read static declarations to reserve actual disk modules even if an alias
	// is represented by a different managed row in SQLite.
	raw, err := m.files.ReadStaticConfig()
	if err != nil {
		return nil, err
	}

	disk, err := staticPluginModules(raw)
	if err != nil {
		return nil, err
	}

	for _, module := range disk {
		reserved[validation.PluginModuleKey(module)] = true
	}

	q := strings.ToLower(strings.TrimSpace(query))
	var rows []models.CatalogPluginView
	for _, row := range snapshot.Items {
		hidden := false
		for _, identity := range catalogIdentities(row) {
			hidden = hidden || reserved[validation.PluginModuleKey(identity)]
		}
		if hidden {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(row.DisplayName+" "+row.ModuleName+" "+row.Summary), q) {
			continue
		}
		rows = append(rows, catalogView(row, nil, true))
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].DisplayName, rows[j].DisplayName
		if a == b {
			a, b = rows[i].ModuleName, rows[j].ModuleName
		}
		if descending {
			return a > b
		}
		return a < b
	})

	out.Total = len(rows)
	if offset < len(rows) {
		out.Items = rows[offset:min(offset+limit, len(rows))]
	}

	return out, nil
}

// computeUpdates compares every installed plugin with the cached catalog. It is pure: the caller decides
// what to do when the catalog or the inventory is unreadable.
func computeUpdates(installed []installedPlugin, snapshot *models.PluginCatalogSnapshot) []models.PluginUpdate {
	byModule := catalogIndex(snapshot.Items)

	out := make([]models.PluginUpdate, 0, len(installed))
	for _, p := range installed {
		item := models.PluginUpdate{
			Alias:          p.Name,
			ModuleName:     p.Config.ModuleName,
			CurrentVersion: p.Config.Version,
			State:          "not_in_catalog",
			Versions:       []string{},
		}

		if row, ok := lookupCatalog(byModule, p.Config.ModuleName, p.Name); ok {
			item.Versions = catalogVersions(row)
			item.LatestVersion = recommendedVersion(row, p.Config.Version)
			item.State = "unknown"
			if c, valid := compareVersions(p.Config.Version, item.LatestVersion); valid {
				if c < 0 {
					item.State = "update_available"
				} else {
					item.State = "up_to_date"
				}
			}
		}

		if snapshot.Refresh.LastSuccessAt == "" {
			item.State = "unknown"
		}

		item.CanUpdate = item.State == "update_available" && !p.ReadOnly

		out = append(out, item)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

func (m *pluginManager) ReadPluginUpdates(ctx context.Context) (*models.PluginUpdates, error) {
	snapshot, err := m.store.ReadPluginCatalogSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	installed, err := m.ListInstalledPluginsIfAvailable(ctx)
	if err != nil {
		return nil, err
	}
	return &models.PluginUpdates{Items: computeUpdates(installed, snapshot), Refresh: snapshot.Refresh}, nil
}

// ListPluginViews is GET /plugins: the inventory plus, per plugin, how it compares with the catalog.
// An unreadable catalog never hides the inventory; the plugins just report an unknown update state.
func (m *pluginManager) ListPluginViews(ctx context.Context) ([]models.PluginView, error) {
	installed, err := m.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}

	snapshot, snapshotErr := m.store.ReadPluginCatalogSnapshot(ctx)
	byAlias := map[string]models.PluginUpdate{}
	if snapshotErr == nil {
		for _, u := range computeUpdates(installed, snapshot) {
			byAlias[u.Alias] = u
		}
	}

	views := make([]models.PluginView, 0, len(installed))
	for _, p := range installed {
		info := models.PluginUpdateInfo{State: "unknown", Versions: []string{}}
		if u, ok := byAlias[p.Name]; ok {
			info.State, info.LatestVersion, info.Versions, info.CanUpdate = u.State, u.LatestVersion, u.Versions, u.CanUpdate
			info.CheckedAt, info.LastAttemptFailed = snapshot.Refresh.LastSuccessAt, snapshot.Refresh.LastAttemptFailed
		}
		views = append(views, models.PluginView{UIResource: p, Update: info})
	}

	return views, nil
}

func (m *pluginManager) ApplyPluginUpdate(ctx context.Context, alias, version string, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	if !validation.ValidPluginVersion(version) {
		return nil, ErrPluginInvalidVersion
	}
	updates, err := m.ReadPluginUpdates(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range updates.Items {
		if item.Alias != alias {
			continue
		}
		known := false
		for _, v := range item.Versions {
			known = known || v == version
		}
		if !known {
			return nil, ErrPluginNotInCatalog
		}
		c, ok := compareVersions(item.CurrentVersion, version)
		if !ok || c >= 0 {
			return nil, ErrPluginNoUpdate
		}
		if !item.CanUpdate {
			return nil, ErrPluginNoUpdate
		}
		return m.UpsertPlugin(ctx, alias, models.StaticPluginConfig{ModuleName: item.ModuleName, Version: version}, opts)
	}
	return nil, ErrPluginNotInCatalog
}

func (m *pluginManager) syncCatalog(ctx context.Context, interval time.Duration) (int, error) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	claimed, err := m.store.ClaimPluginCatalogRefresh(ctx, time.Now(), interval)
	if err != nil {
		return 0, err
	}

	if !claimed {
		snapshot, err := m.store.ReadPluginCatalogSnapshot(ctx)
		if err != nil {
			return 0, err
		}
		if snapshot.Refresh.LastAttemptFailed {
			return 0, catalog.ErrCatalogUnavailable
		}
		return len(snapshot.Items), nil
	}

	rows, err := m.catalog.Fetch(ctx)
	if err != nil {
		return 0, err
	}
	if err = m.store.ReplacePluginCatalog(ctx, rows); err != nil {
		return 0, err
	}

	return len(rows), nil
}

// RunCatalogChecks The owner cancels and joins this worker before closing SQLite or HTTP clients.
// Metadata checks never write configuration or invoke a runtime capability.
func (m *pluginManager) RunCatalogChecks(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		snapshot, err := m.store.ReadPluginCatalogSnapshot(ctx)
		if err == nil && (snapshot.Refresh.LastAttemptAt != "" || snapshot.Refresh.LastSuccessAt != "") {
			interval := 24 * time.Hour
			if snapshot.Refresh.LastAttemptFailed {
				interval = 15 * time.Minute
			}
			check, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, _ = m.syncCatalog(check, interval)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func staticPluginModules(raw []byte) (map[string]string, error) {
	snapshot, err := status.DiscoverStatic(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for alias, p := range snapshot.StaticPlugins {
		module, _ := p.Raw["moduleName"].(string)
		out[alias] = module
	}
	return out, nil
}

func (m *pluginManager) InstallCatalogPlugin(ctx context.Context, in CatalogInstallRequest, opts models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	snapshot, err := m.store.ReadPluginCatalogSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range snapshot.Items {
		if validation.PluginModuleKey(row.ModuleName) != validation.PluginModuleKey(in.ModuleName) {
			continue
		}
		if row.PluginType != "middleware" {
			return nil, ErrPluginNotInCatalog
		}
		for _, v := range catalogVersions(row) {
			if v == in.Version {
				return m.UpsertPlugin(ctx, in.Alias, models.StaticPluginConfig{ModuleName: row.ModuleName, Version: v}, opts)
			}
		}
	}
	return nil, ErrPluginNotInCatalog
}
