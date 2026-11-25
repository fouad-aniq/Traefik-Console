package engine

import (
	"errors"
	"testing"

	"github.com/traefik-console/internal/models"
)

func pluginMutation(name, module string) models.Mutation {
	return models.Mutation{Upserts: []models.ConfigItemRow{pluginRow(name, module)}}
}

func TestPluginModuleConflict(t *testing.T) {
	managed := []models.ConfigItemRow{pluginRow("bouncer", "github.com/org/bouncer")}
	external := map[string]string{"handmade": "github.com/org/handmade"}
	for _, tc := range []struct {
		name  string
		m     models.Mutation
		alias string
	}{
		{"same module, other alias", pluginMutation("second", "github.com/org/bouncer"), "bouncer"},
		{"module match ignores case", pluginMutation("second", "GitHub.com/Org/Bouncer"), "bouncer"},
		{"declared by hand in traefik.yml", pluginMutation("second", "github.com/org/handmade"), "handmade"},
	} {
		err := pluginModuleConflict(managed, tc.m, external)
		var conflict *PluginModuleError
		if !errors.Is(err, ErrPluginModuleInstalled) || !errors.As(err, &conflict) || conflict.Alias != tc.alias {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		name string
		m    models.Mutation
	}{
		{"re-pinning the same alias", pluginMutation("bouncer", "github.com/org/bouncer")},
		{"a different module", pluginMutation("other", "github.com/org/other")},
		{"no plugin in the mutation", models.Mutation{}},
	} {
		if err := pluginModuleConflict(managed, tc.m, external); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestPluginModuleConflictSeesTheWholeMutation(t *testing.T) {
	both := pluginMutation("one", "github.com/org/x")
	both.Upserts = append(both.Upserts, pluginRow("two", "github.com/org/x"))
	if err := pluginModuleConflict(nil, both, nil); !errors.Is(err, ErrPluginModuleInstalled) {
		t.Fatal("duplicate inside one mutation accepted", err)
	}
	// Removing the holder and declaring the module under a new alias is one valid swap.
	managed := []models.ConfigItemRow{pluginRow("bouncer", "github.com/org/bouncer")}
	swap := pluginMutation("renamed", "github.com/org/bouncer")
	swap.Deletes = []models.ItemRef{{Kind: models.KindPlugin, Name: "bouncer"}}
	if err := pluginModuleConflict(managed, swap, nil); err != nil {
		t.Fatal("swap rejected", err)
	}
	// Legacy duplicates that the mutation does not touch never block unrelated work.
	legacy := []models.ConfigItemRow{pluginRow("a", "github.com/org/dup"), pluginRow("b", "github.com/org/dup")}
	if err := pluginModuleConflict(legacy, pluginMutation("c", "github.com/org/fresh"), nil); err != nil {
		t.Fatal("unrelated legacy duplicate blocked", err)
	}
}
