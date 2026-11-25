package plugins

import (
	"testing"

	"github.com/traefik-console/internal/models"
)

func pluginAt(name, module, version string, readOnly bool) installedPlugin {
	return installedPlugin{Name: name, ReadOnly: readOnly, Config: models.StaticPluginConfig{ModuleName: module, Version: version}}
}

func TestComputeUpdates(t *testing.T) {
	snapshot := &models.PluginCatalogSnapshot{
		Refresh: models.CatalogRefresh{LastSuccessAt: "2026-11-09 10:00:00"},
		Items: []models.CatalogPluginRow{
			{ModuleName: "github.com/org/a", LatestVersion: "v3.0.0-rc.1", VersionsJSON: `["v3.0.0-rc.1","v2.0.0","v1.0.0"]`},
			{ModuleName: "github.com/org/b", LatestVersion: "v1.0.0", VersionsJSON: `["v1.0.0"]`},
		},
	}
	got := computeUpdates([]installedPlugin{
		pluginAt("managed-old", "github.com/org/a", "v1.0.0", false),
		pluginAt("external-old", "GitHub.com/Org/A", "v1.0.0", true),
		pluginAt("current", "github.com/org/b", "v1.0.0", false),
		pluginAt("manual", "github.com/org/missing", "v1.0.0", false),
	}, snapshot)
	by := map[string]models.PluginUpdate{}
	for _, u := range got {
		by[u.Alias] = u
	}
	// A prerelease is never recommended to a plugin on a stable release.
	if u := by["managed-old"]; u.State != "update_available" || u.LatestVersion != "v2.0.0" || !u.CanUpdate || len(u.Versions) != 3 || u.Versions[0] != "v3.0.0-rc.1" {
		t.Fatalf("managed: %+v", u)
	}
	if u := by["external-old"]; u.State != "update_available" || u.CanUpdate {
		t.Fatalf("external plugins are reported but never updatable: %+v", u)
	}
	if u := by["current"]; u.State != "up_to_date" || u.CanUpdate {
		t.Fatalf("current: %+v", u)
	}
	if u := by["manual"]; u.State != "not_in_catalog" || u.CanUpdate {
		t.Fatalf("manual: %+v", u)
	}
	snapshot.Refresh.LastSuccessAt = ""
	for _, u := range computeUpdates([]installedPlugin{pluginAt("managed-old", "github.com/org/a", "v1.0.0", false)}, snapshot) {
		if u.State != "unknown" || u.CanUpdate {
			t.Fatalf("an unsynced catalog must not claim an answer: %+v", u)
		}
	}
}

func TestComputeUpdatesMatchesCatalogAliasesAndShortNames(t *testing.T) {
	snapshot := &models.PluginCatalogSnapshot{
		Refresh: models.CatalogRefresh{LastSuccessAt: "2026-11-09 10:00:00"},
		Items: []models.CatalogPluginRow{
			{ModuleName: "github.com/david-garcia-garcia/crowdsec-bouncer-traefik-plugin", LatestVersion: "v2.0.0", VersionsJSON: `["v2.0.0","v1.0.0"]`, AliasesJSON: `["github.com/legacy/crowdsec"]`},
			{ModuleName: "github.com/a/shared", LatestVersion: "v2.0.0", VersionsJSON: `["v2.0.0"]`},
			{ModuleName: "github.com/b/shared", LatestVersion: "v2.0.0", VersionsJSON: `["v2.0.0"]`},
		},
	}
	got := computeUpdates([]installedPlugin{
		pluginAt("short", "crowdsec-bouncer-traefik-plugin", "v1.0.0", false),
		pluginAt("alias", "github.com/legacy/crowdsec", "v1.0.0", false),
		pluginAt("ambiguous", "shared", "v1.0.0", false),
	}, snapshot)
	by := map[string]models.PluginUpdate{}
	for _, u := range got {
		by[u.Alias] = u
	}
	for _, name := range []string{"short", "alias"} {
		if u := by[name]; u.State != "update_available" || u.LatestVersion != "v2.0.0" || !u.CanUpdate {
			t.Fatalf("%s: %+v", name, u)
		}
	}
	if u := by["ambiguous"]; u.State != "not_in_catalog" {
		t.Fatalf("a short name shared by two entries must not guess: %+v", u)
	}
}
