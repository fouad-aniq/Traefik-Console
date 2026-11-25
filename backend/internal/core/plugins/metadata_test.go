package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	catalog "github.com/traefik-console/internal/traefik/plugins"
)

func metadataStore(t *testing.T) db.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := db.InitDB(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return db.NewStore(conn, make([]byte, 32))
}

func TestCatalogSyncReplacesLegacyTypesAndClearsFailure(t *testing.T) {
	ctx := context.Background()
	store := metadataStore(t)
	legacy := []models.CatalogPluginRow{{ModuleName: "github.com/org/demo", LatestVersion: "v1.0.0", VersionsJSON: "[]", AliasesJSON: "[]"}}
	if err := store.ReplacePluginCatalog(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(store, nil, testCatalog{err: catalog.ErrCatalogUnavailable}, nil).(*pluginManager).syncCatalog(ctx, 0); !errors.Is(err, catalog.ErrCatalogUnavailable) {
		t.Fatal(err)
	}
	// A throttled retry must not report a failed refresh as successful.
	if _, err := NewManager(store, nil, testCatalog{}, nil).SyncCatalog(ctx); !errors.Is(err, catalog.ErrCatalogUnavailable) {
		t.Fatal("failed refresh hidden by throttling", err)
	}
	// Advance eligibility without changing the real clock or sleeping.
	manager := NewManager(store, nil, testCatalog{rows: []models.CatalogPluginRow{
		{ModuleName: "github.com/org/demo", LatestVersion: "v1.1.0", PluginType: "middleware", VersionsJSON: `["v1.1.0","v1.0.0"]`, AliasesJSON: "[]"},
		{ModuleName: "github.com/org/provider", LatestVersion: "v1.0.0", PluginType: "provider", VersionsJSON: `["v1.0.0"]`, AliasesJSON: "[]"},
	}}, nil).(*pluginManager)
	if count, err := manager.syncCatalog(ctx, 0); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	snapshot, err := store.ReadPluginCatalogSnapshot(ctx)
	if err != nil || snapshot.Refresh.LastAttemptFailed || snapshot.Refresh.LastSuccessAt == "" || len(snapshot.Items) != 2 {
		t.Fatal(snapshot, err)
	}
	byModule := map[string]models.CatalogPluginRow{}
	for _, row := range snapshot.Items {
		byModule[row.ModuleName] = row
	}
	if byModule["github.com/org/demo"].PluginType != "middleware" || byModule["github.com/org/provider"].PluginType != "provider" {
		t.Fatal("types not persisted", byModule)
	}
}

func TestAvailableCatalogReservesInstalledAndExternalModulesBeforePagination(t *testing.T) {
	ctx := context.Background()
	store := metadataStore(t)
	rows := []models.CatalogPluginRow{}
	for i := 0; i < 610; i++ {
		rows = append(rows, models.CatalogPluginRow{ModuleName: fmt.Sprintf("github.com/org/p%03d", i), DisplayName: fmt.Sprintf("Plugin%03d", i), LatestVersion: "v1.0.0", PluginType: "middleware"})
	}
	if err := store.ReplacePluginCatalog(ctx, rows); err != nil {
		t.Fatal(err)
	}
	commit := func(name string, module string) {
		raw, _ := json.Marshal(models.StaticPluginConfig{ModuleName: module, Version: "v1.0.0"})
		if _, err := store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{Kind: models.KindPlugin, Name: name, Scope: models.ScopeStatic, Payload: raw}}}}); err != nil {
			t.Fatal(err)
		}
	}
	commit("replace", rows[1].ModuleName)
	commit("second", rows[2].ModuleName)
	commit("pending", rows[3].ModuleName)
	files := testFiles{raw: []byte("experimental:\n  plugins:\n    external:\n      moduleName: github.com/org/p000\n      version: v1.0.0\n    replace:\n      moduleName: github.com/org/p001\n      version: v1.0.0\n")}
	manager := NewManager(store, files, nil, nil)
	page, err := manager.ReadAvailableCatalogPage(ctx, "", 25, 500, false)
	if err != nil || page.Total != 606 || len(page.Items) != 25 || page.Items[0].ModuleName != "github.com/org/p504" {
		t.Fatal(page, err)
	}
	if _, err = store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindPlugin, Name: "pending"}}}}); err != nil {
		t.Fatal(err)
	}
	// A deleted plugin's module becomes available again.
	page, err = manager.ReadAvailableCatalogPage(ctx, "p003", 25, 0, false)
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	bad := NewManager(store, testFiles{err: errors.New("unreadable")}, nil, nil)
	page, err = bad.ReadAvailableCatalogPage(ctx, "", 25, 0, false)
	if err != nil || page.InventoryKnown || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
}

func TestUpdateSummaryBeyond500AndPinnedTarget(t *testing.T) {
	ctx := context.Background()
	store := metadataStore(t)
	rows := []models.CatalogPluginRow{}
	for i := 0; i < 605; i++ {
		rows = append(rows, models.CatalogPluginRow{ModuleName: fmt.Sprintf("github.com/org/p%03d", i), DisplayName: fmt.Sprintf("Plugin%03d", i), LatestVersion: "v3.0.0-rc.1", VersionsJSON: `["v3.0.0-rc.1","v2.0.0","v1.5.0","v1.0.0"]`, PluginType: "middleware"})
	}
	if err := store.ReplacePluginCatalog(ctx, rows); err != nil {
		t.Fatal(err)
	}
	fake := &testStore{items: []models.ConfigItemRow{managedPlugin("demo", "github.com/org/p604", "v1.0.0")}}
	manager := NewManager(struct {
		db.ItemReader
		db.CatalogStore
	}{fake, store}, testFiles{}, nil, &testApplier{store: fake})
	updates, err := manager.ReadPluginUpdates(ctx)
	if err != nil || len(updates.Items) != 1 || updates.Items[0].LatestVersion != "v2.0.0" || !updates.Items[0].CanUpdate {
		t.Fatal(updates, err)
	}
	got, err := manager.ApplyPluginUpdate(ctx, "demo", "v1.5.0", models.WriteOptions{})
	if err != nil || got.Resource.Config.Version != "v1.5.0" {
		t.Fatal(got, err)
	}
	if _, err = manager.ApplyPluginUpdate(ctx, "demo", "v9.9.9", models.WriteOptions{}); !errors.Is(err, ErrPluginNotInCatalog) {
		t.Fatal("unknown version accepted", err)
	}
	if _, err = manager.InstallCatalogPlugin(ctx, CatalogInstallRequest{Alias: "fresh", ModuleName: rows[0].ModuleName, Version: "v1.5.0"}, models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	rows[0].PluginType = "provider"
	if err = store.ReplacePluginCatalog(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.InstallCatalogPlugin(ctx, CatalogInstallRequest{Alias: "provider", ModuleName: rows[0].ModuleName, Version: "v1.5.0"}, models.WriteOptions{}); !errors.Is(err, ErrPluginNotInCatalog) {
		t.Fatal("provider guided install accepted", err)
	}
}

func TestMetadataFailureCadenceAndWorkerCancellation(t *testing.T) {
	ctx := context.Background()
	store := metadataStore(t)
	rows := []models.CatalogPluginRow{{ModuleName: "github.com/org/demo", LatestVersion: "v1.0.0"}}
	if err := store.ReplacePluginCatalog(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// The claim survives reopening the database and rejects immediate repeats.
	now := time.Now().Add(25 * time.Hour)
	claimed, err := store.ClaimPluginCatalogRefresh(ctx, now, time.Minute)
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	claimed, err = store.ClaimPluginCatalogRefresh(ctx, now.Add(30*time.Second), time.Minute)
	if err != nil || claimed {
		t.Fatal(claimed, err)
	}
	snapshot, err := store.ReadPluginCatalogSnapshot(ctx)
	if err != nil || !snapshot.Refresh.LastAttemptFailed || snapshot.Refresh.LastSuccessAt == "" || len(snapshot.Items) != 1 {
		t.Fatal(snapshot, err)
	}
	manager := NewManager(store, testFiles{}, testCatalog{err: errors.New("offline")}, nil)
	canceled, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); manager.RunCatalogChecks(canceled) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel")
	}
}

func TestSnippetRejectsIgnoredConfiguration(t *testing.T) {
	manager := NewManager(&testStore{}, testFiles{}, nil, nil)
	for _, raw := range []string{"plugins:\n  demo: {moduleName: github.com/org/demo, version: v1.0.0}\nentryPoints: {}", "experimental:\n  plugins:\n    demo: {moduleName: github.com/org/demo, version: v1.0.0}\n  localPlugins: {}"} {
		if _, err := manager.InstallFromYAML(context.Background(), InstallFromYAMLRequest{RawYAML: raw}, models.WriteOptions{}); !errors.Is(err, ErrPluginInvalidYAML) {
			t.Fatal(err)
		}
	}
}
