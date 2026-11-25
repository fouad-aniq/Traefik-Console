package plugins

import (
	"context"
	"errors"
	"github.com/traefik-console/internal/traefik/validation"
	"sort"
	"strings"
	"time"

	"github.com/traefik-console/internal/models"
)

// SyncCatalog persists a fetched inventory only after successful bounded decoding.
func (m *pluginManager) SyncCatalog(ctx context.Context) (int, error) {
	return m.syncCatalog(ctx, time.Minute)
}

func (m *pluginManager) SearchCatalog(ctx context.Context, query string) ([]models.CatalogPluginView, error) {
	rows, err := m.store.SearchPluginCatalog(ctx, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	return m.annotate(ctx, rows), nil
}

func (m *pluginManager) ReadCatalogPage(ctx context.Context, query, module string, limit, offset int, descending bool) (*models.CatalogPageView, error) {
	page, err := m.store.ReadPluginCatalogPage(ctx, strings.TrimSpace(query), strings.TrimSpace(module), limit, offset, descending)
	if err != nil {
		return nil, err
	}
	refresh, err := m.store.ReadPluginCatalogSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	_, inventoryErr := m.ListInstalledPluginsIfAvailable(ctx)
	return &models.CatalogPageView{Refresh: refresh.Refresh, InventoryKnown: inventoryErr == nil, Items: m.annotate(ctx, page.Items), Total: page.Total, Limit: page.Limit, Offset: page.Offset, LastSyncedAt: page.LastSyncedAt}, nil
}

// annotate adds the install state to each entry. An unreadable installed inventory (for example a
// broken traefik.yml) must not hide the catalog, so entries then carry the explicit "unknown" state.
func (m *pluginManager) annotate(ctx context.Context, rows []models.CatalogPluginRow) []models.CatalogPluginView {
	installed, err := []installedPlugin(nil), errInventoryUnavailable
	if m.files != nil {
		installed, err = m.ListInstalledPlugins(ctx)
	}
	views := make([]models.CatalogPluginView, len(rows))
	for i, row := range rows {
		views[i] = catalogView(row, installed, err == nil)
	}
	return views
}

var errInventoryUnavailable = errors.New("installed plugin inventory is unavailable")

type installedPlugin = models.UIResource[models.StaticPluginConfig]

func catalogView(row models.CatalogPluginRow, installed []installedPlugin, known bool) models.CatalogPluginView {
	view := models.CatalogPluginView{CatalogPluginRow: row, Versions: catalogVersions(row), InstallState: models.CatalogStateUnknown}
	if !known {
		return view
	}
	var match *installedPlugin
	for _, identity := range catalogIdentities(row) {
		if match = installedFor(identity, installed); match != nil {
			break
		}
	}
	if match == nil {
		view.InstallState = models.CatalogNotInstalled
		return view
	}
	view.InstalledAlias, view.InstalledVersion, view.InstalledOrigin, view.InstalledStatus = match.Name, match.Config.Version, match.Origin, match.Status
	view.LatestVersion = recommendedVersion(row, match.Config.Version)
	switch c, ok := compareVersions(match.Config.Version, view.LatestVersion); {
	case !ok:
		view.InstallState = models.CatalogStateUnknown
	case c < 0:
		view.InstallState = models.CatalogUpdateAvailable
	default:
		view.InstallState = models.CatalogUpToDate
	}
	view.CanUpdate = view.InstallState == models.CatalogUpdateAvailable && match.Origin == "managed"
	return view
}

// installedFor finds the installed plugin for a module (GitHub paths are case-insensitive). When
// legacy data holds several, a managed one wins over an external one.
func installedFor(module string, installed []installedPlugin) *installedPlugin {
	rank := func(p installedPlugin) int {
		switch {
		case p.Origin == "managed":
			return 0
		}
		return 1
	}
	var matches []installedPlugin
	for _, p := range installed {
		if validation.PluginModuleKey(p.Config.ModuleName) == validation.PluginModuleKey(module) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if ri, rj := rank(matches[i]), rank(matches[j]); ri != rj {
			return ri < rj
		}
		return matches[i].Name < matches[j].Name
	})
	return &matches[0]
}
