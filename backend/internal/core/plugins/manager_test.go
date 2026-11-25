package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

type testFiles struct {
	raw []byte
	err error
}

func (f testFiles) ReadStaticConfig() ([]byte, error) { return f.raw, f.err }

type testCatalog struct {
	rows []models.CatalogPluginRow
	err  error
}

func (f testCatalog) Fetch(context.Context) ([]models.CatalogPluginRow, error) { return f.rows, f.err }

type testStore struct {
	db.Store
	items        []models.ConfigItemRow
	replacements int
	err          error
}

func (s *testStore) ListItemsByKind(context.Context, models.ItemKind) ([]models.ConfigItemRow, error) {
	return s.items, nil
}
func (s *testStore) GetItem(_ context.Context, kind models.ItemKind, name string) (*models.ConfigItemRow, error) {
	for i := range s.items {
		if s.items[i].Kind == kind && s.items[i].Name == name {
			return &s.items[i], nil
		}
	}
	return nil, nil
}
func (s *testStore) ReplacePluginCatalog(context.Context, []models.CatalogPluginRow) error {
	s.replacements++
	return s.err
}

// testApplier records mutations and mirrors plugin rows into the stub store.
type testApplier struct {
	store     *testStore
	mutations []models.Mutation
	err       error
}

func (a *testApplier) Apply(_ context.Context, m models.Mutation) (*models.OperationResult, error) {
	if a.err != nil {
		return nil, a.err
	}
	a.mutations = append(a.mutations, m)
	for _, r := range m.Upserts {
		if r.Kind == models.KindPlugin {
			r.Version = 1
			replaced := false
			for i := range a.store.items {
				if a.store.items[i].Kind == r.Kind && a.store.items[i].Name == r.Name {
					r.Version = a.store.items[i].Version + 1
					a.store.items[i], replaced = r, true
				}
			}
			if !replaced {
				a.store.items = append(a.store.items, r)
			}
		}
	}
	return &models.OperationResult{Operation: &models.OperationRow{Status: models.OpStatusSucceeded}}, nil
}

func (a *testApplier) Preview(context.Context, models.Mutation) (*models.OperationPreview, error) {
	return &models.OperationPreview{}, nil
}

func TestStaticPluginsNeedNoRuntimeAndSurfaceReadErrors(t *testing.T) {
	store := &testStore{}
	m := NewManager(store, testFiles{raw: []byte("experimental:\n  plugins:\n    external:\n      moduleName: example.invalid/plugin\n      version: v1.0.0\n")}, nil, nil)
	rows, err := m.ListInstalledPlugins(context.Background())
	if err != nil || len(rows) != 1 || !rows[0].ReadOnly || rows[0].LiveHealth != "unknown" {
		t.Fatalf("static-only listing: %+v %v", rows, err)
	}
	for _, f := range []testFiles{{err: errors.New("unreadable")}, {raw: []byte("experimental: [")}} {
		if _, err := NewManager(store, f, nil, nil).ListInstalledPlugins(context.Background()); err == nil {
			t.Fatal("static error swallowed")
		}
	}
}

func TestCatalogFailureNeverReplacesPriorInventory(t *testing.T) {
	store := &testStore{}
	if _, err := NewManager(store, nil, testCatalog{err: errors.New("offline")}, nil).SyncCatalog(context.Background()); err == nil || store.replacements != 0 {
		t.Fatal("fetch failure replaced inventory")
	}
	store.err = errors.New("database failure")
	if _, err := NewManager(store, nil, testCatalog{}, nil).SyncCatalog(context.Background()); err == nil {
		t.Fatal("persistence failure hidden")
	}
	store.err = nil
	n, err := NewManager(store, nil, testCatalog{rows: []models.CatalogPluginRow{{ModuleName: "example.invalid/plugin", LatestVersion: "v1.0.0"}}}, nil).SyncCatalog(context.Background())
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestPluginAndMiddlewareUseOneOperation(t *testing.T) {
	store := &testStore{}
	applier := &testApplier{store: store}
	m := NewManager(store, testFiles{}, nil, applier)
	input := InstallFromYAMLRequest{RawYAML: "experimental:\n  plugins:\n    example:\n      moduleName: example.invalid/plugin\n      version: v1.0.0\nhttp:\n  middlewares:\n    example-mw:\n      plugin:\n        example:\n          enabled: true\n"}
	if _, err := m.InstallFromYAML(context.Background(), input, models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(applier.mutations) != 1 || len(applier.mutations[0].Upserts) != 2 {
		t.Fatal("plugin/middleware split across operations")
	}
	applier.err = errors.New("operation failed")
	if _, err := m.InstallFromYAML(context.Background(), input, models.WriteOptions{}); err == nil {
		t.Fatal("operation failure hidden")
	}
}

func TestPluginSaveDoesNotReadStaticFilesAfterCommit(t *testing.T) {
	store := &testStore{}
	applier := &testApplier{store: store}
	manager := NewManager(store, testFiles{err: errors.New("static files unavailable")}, nil, applier)
	got, err := manager.UpsertPlugin(context.Background(), "demo", models.StaticPluginConfig{ModuleName: "example.invalid/demo", Version: "v1.0.0"}, models.WriteOptions{})
	if err != nil || got.Resource.Name != "demo" || got.Resource.Version != 1 {
		t.Fatal("save required a post-commit filesystem read", err)
	}
	input := InstallFromYAMLRequest{RawYAML: "plugins:\n  next:\n    moduleName: example.invalid/next\n    version: v1.0.0\n"}
	got, err = manager.InstallFromYAML(context.Background(), input, models.WriteOptions{})
	if err != nil || got.Resource.Name != "next" {
		t.Fatal("YAML save required a post-commit filesystem read", err)
	}
}

func TestPluginDeleteIsBlockedWhileAMiddlewareUsesIt(t *testing.T) {
	store := &testStore{items: []models.ConfigItemRow{{Kind: models.KindMiddleware, Name: "mw", Payload: []byte(`{"type":"plugin","plugin_name":"demo"}`)}}}
	applier := &testApplier{store: store}
	m := NewManager(store, testFiles{}, nil, applier)
	if _, err := m.DeletePlugin(context.Background(), "demo", models.WriteOptions{}); !errors.Is(err, ErrPluginInUse) || len(applier.mutations) != 0 {
		t.Fatal("plugin in use was deleted", err)
	}
	if _, err := m.DeletePlugin(context.Background(), "other", models.WriteOptions{}); err != nil || len(applier.mutations) != 1 || len(applier.mutations[0].Deletes) != 1 {
		t.Fatal("unused plugin delete failed", err)
	}
}
func (s *testStore) ClaimPluginCatalogRefresh(context.Context, time.Time, time.Duration) (bool, error) {
	return true, nil
}
func (s *testStore) ReadPluginCatalogSnapshot(context.Context) (*models.PluginCatalogSnapshot, error) {
	return &models.PluginCatalogSnapshot{Items: []models.CatalogPluginRow{}}, nil
}
