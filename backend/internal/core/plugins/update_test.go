package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

// catalogStore adds a cached catalog to the shared test store.
type catalogStore struct {
	*testStore
	rows []models.CatalogPluginRow
}

func (s catalogStore) SearchPluginCatalog(context.Context, string) ([]models.CatalogPluginRow, error) {
	return s.rows, nil
}
func (s catalogStore) ReadPluginCatalogPage(_ context.Context, _, _ string, limit, offset int, _ bool) (*models.CatalogPage, error) {
	return &models.CatalogPage{Items: s.rows, Total: len(s.rows), Limit: limit, Offset: offset, LastSyncedAt: "2026-01-01 10:00:00"}, nil
}

func managedPlugin(name, module, version string) models.ConfigItemRow {
	raw, _ := json.Marshal(models.StaticPluginConfig{ModuleName: module, Version: version})
	return models.ConfigItemRow{ID: "plugin:" + name, Kind: models.KindPlugin, Name: name, Scope: models.ScopeStatic, Payload: raw}
}

const externalYAML = "experimental:\n  plugins:\n    legacy:\n      moduleName: example.invalid/legacy\n      version: v1.0.0\n"

func entry(module, version string) models.CatalogPluginRow {
	return models.CatalogPluginRow{ModuleName: module, DisplayName: module, LatestVersion: version}
}

func TestCatalogStatesCoverEveryInstallCase(t *testing.T) {
	store := &testStore{items: []models.ConfigItemRow{
		managedPlugin("old", "example.invalid/old", "v1.0.0"),
		managedPlugin("current", "example.invalid/current", "v2.0.0"),
		managedPlugin("odd", "example.invalid/odd", "latest"),
		managedPlugin("ahead", "example.invalid/ahead", "v3.0.0"),
	}}
	rows := []models.CatalogPluginRow{
		entry("example.invalid/old", "v1.1.0"), entry("example.invalid/current", "v2.0.0"),
		entry("example.invalid/odd", "v1.0.0"), entry("example.invalid/ahead", "v2.0.0"), entry("example.invalid/legacy", "v1.5.0"),
		entry("example.invalid/legacy-current", "v1.0.0"), entry("example.invalid/new", "v1.0.0"),
	}
	m := NewManager(catalogStore{store, rows}, testFiles{raw: []byte(externalYAML)}, nil, nil)
	page, err := m.ReadCatalogPage(context.Background(), "", "", 25, 0, false)
	if err != nil || page.Total != len(rows) || page.LastSyncedAt == "" {
		t.Fatal(page, err)
	}
	want := map[string]struct {
		state, alias, origin string
		canUpdate            bool
	}{
		"example.invalid/old":     {models.CatalogUpdateAvailable, "old", "managed", true},
		"example.invalid/current": {models.CatalogUpToDate, "current", "managed", false},
		"example.invalid/odd":     {models.CatalogStateUnknown, "odd", "managed", false},
		"example.invalid/ahead":   {models.CatalogUpToDate, "ahead", "managed", false},
		"example.invalid/legacy":  {models.CatalogUpdateAvailable, "legacy", "external", false},
		"example.invalid/new":     {models.CatalogNotInstalled, "", "", false},
	}
	for _, item := range page.Items {
		w, ok := want[item.ModuleName]
		if !ok {
			continue
		}
		if item.InstallState != w.state || item.InstalledAlias != w.alias || item.InstalledOrigin != w.origin || item.CanUpdate != w.canUpdate {
			t.Errorf("%s: got %+v, want %+v", item.ModuleName, item, w)
		}
	}
	search, err := m.SearchCatalog(context.Background(), "example")
	if err != nil || len(search) != len(rows) || search[0].InstallState != models.CatalogUpdateAvailable {
		t.Fatal(search, err)
	}
}

func TestCatalogStaysReadableWhenInstalledInventoryIsNot(t *testing.T) {
	m := NewManager(catalogStore{&testStore{}, []models.CatalogPluginRow{entry("example.invalid/a", "v1.0.0")}}, testFiles{err: errors.New("unreadable")}, nil, nil)
	page, err := m.ReadCatalogPage(context.Background(), "", "", 25, 0, false)
	if err != nil || len(page.Items) != 1 || page.Items[0].InstallState != models.CatalogStateUnknown || page.Items[0].CanUpdate {
		t.Fatal(page, err)
	}
}

func TestInstalledForPrefersManagedEntries(t *testing.T) {
	list := []installedPlugin{
		{Name: "external", Origin: "external", Config: models.StaticPluginConfig{ModuleName: "X/Y"}},
		{Name: "managed", Origin: "managed", Config: models.StaticPluginConfig{ModuleName: "x/y"}},
	}
	if got := installedFor("x/y", list); got == nil || got.Name != "managed" {
		t.Fatal(got)
	}
	if installedFor("x/z", list) != nil {
		t.Fatal("unrelated module matched")
	}
}

func TestUpdatePluginAppliesOnlyTheCatalogVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		alias   string
		items   []models.ConfigItemRow
		catalog []models.CatalogPluginRow
		yaml    string
		wantErr error
	}{
		{name: "newer version", alias: "demo", items: []models.ConfigItemRow{managedPlugin("demo", "example.invalid/demo", "v1.0.0")}, catalog: []models.CatalogPluginRow{entry("example.invalid/demo", "v1.2.0")}},
		{name: "already current", alias: "demo", items: []models.ConfigItemRow{managedPlugin("demo", "example.invalid/demo", "v1.2.0")}, catalog: []models.CatalogPluginRow{entry("example.invalid/demo", "v1.2.0")}, wantErr: ErrPluginNoUpdate},
		{name: "not in catalog", alias: "demo", items: []models.ConfigItemRow{managedPlugin("demo", "example.invalid/demo", "v1.0.0")}, wantErr: ErrPluginNotInCatalog},
		{name: "external is read only", alias: "legacy", catalog: []models.CatalogPluginRow{entry("example.invalid/legacy", "v2.0.0")}, yaml: externalYAML, wantErr: files.ErrCannotModifyReadOnly},
		{name: "unknown alias", alias: "ghost", wantErr: files.ErrSourceFileNotFound},
		{name: "invalid alias", alias: "Bad Alias", wantErr: ErrPluginInvalidName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &testStore{items: tc.items}
			applier := &testApplier{store: store}
			m := NewManager(catalogStore{store, tc.catalog}, testFiles{raw: []byte(tc.yaml)}, nil, applier)
			got, err := m.UpdatePlugin(context.Background(), tc.alias, models.WriteOptions{})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || len(applier.mutations) != 0 {
					t.Fatalf("err = %v, applied %d mutations", err, len(applier.mutations))
				}
				return
			}
			if err != nil || got == nil || len(applier.mutations) != 1 {
				t.Fatal(got, err, applier.mutations)
			}
			var cfg models.StaticPluginConfig
			if json.Unmarshal(applier.mutations[0].Upserts[0].Payload, &cfg) != nil || cfg.Version != "v1.2.0" || cfg.ModuleName != "example.invalid/demo" {
				t.Fatalf("applied %+v", cfg)
			}
		})
	}
}

func TestCatalogWithoutStaticConfigReaderReportsUnknown(t *testing.T) {
	m := NewManager(catalogStore{&testStore{}, []models.CatalogPluginRow{entry("example.invalid/a", "v1.0.0")}}, nil, nil, nil)
	page, err := m.ReadCatalogPage(context.Background(), "", "", 25, 0, false)
	if err != nil || page.Items[0].InstallState != models.CatalogStateUnknown {
		t.Fatal(page, err)
	}
}

func (s catalogStore) ReadPluginCatalogSnapshot(context.Context) (*models.PluginCatalogSnapshot, error) {
	return &models.PluginCatalogSnapshot{Items: s.rows, Refresh: models.CatalogRefresh{LastSuccessAt: "2026-01-01 10:00:00"}}, nil
}
