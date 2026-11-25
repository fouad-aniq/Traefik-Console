package engine

import (
	"encoding/json"
	"errors"
	"github.com/traefik-console/internal/traefik/validation"
	"sort"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// checkPluginModules runs only for mutations that declare plugins. It reads traefik.yml so a plugin
// declared there by hand counts as installed.
func (c *operations) checkPluginModules(b models.Mutation, state *db.State) error {
	declares := false
	for _, item := range b.Upserts {
		declares = declares || item.Kind == models.KindPlugin
	}
	if !declares {
		return nil
	}
	raw, err := c.files.ReadStaticConfig()
	if err != nil && !errors.Is(err, files.ErrSourceFileNotFound) {
		return err
	}
	snapshot, err := status.DiscoverStatic(raw)
	if err != nil {
		return err
	}
	external := map[string]string{}
	for alias, item := range snapshot.StaticPlugins {
		module, _ := item.Raw["moduleName"].(string)
		external[alias] = module
	}
	return pluginModuleConflict(state.Items, b, external)
}

// pluginModuleConflict simulates the plugin declarations after the mutation and fails when a plugin the
// mutation writes shares its module (compared case-insensitively) with a different alias. Pre-existing
// duplicates that the mutation does not touch never block unrelated changes.
func pluginModuleConflict(existing []models.ConfigItemRow, b models.Mutation, external map[string]string) error {
	touched := map[string]bool{}
	for _, item := range b.Upserts {
		if item.Kind == models.KindPlugin {
			touched[item.Name] = true
		}
	}
	for _, ref := range b.Deletes {
		if ref.Kind == models.KindPlugin {
			touched[ref.Name] = true
		}
	}
	final := map[string]string{}
	for alias, module := range external {
		final[alias] = module
	}
	for _, row := range existing {
		if row.Kind != models.KindPlugin || touched[row.Name] {
			continue
		}
		var cfg models.StaticPluginConfig
		if json.Unmarshal(row.Payload, &cfg) != nil {
			return ErrValidationFailed
		}
		final[row.Name] = cfg.ModuleName
	}
	written := map[string]string{}
	for _, item := range b.Upserts {
		if item.Kind != models.KindPlugin {
			continue
		}
		var cfg models.StaticPluginConfig
		if json.Unmarshal(item.Payload, &cfg) != nil {
			return ErrValidationFailed
		}
		final[item.Name] = cfg.ModuleName
		written[item.Name] = cfg.ModuleName
	}
	aliases := make([]string, 0, len(final))
	for alias := range final {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	writtenAliases := make([]string, 0, len(written))
	for alias := range written {
		writtenAliases = append(writtenAliases, alias)
	}
	sort.Strings(writtenAliases)
	for _, alias := range writtenAliases {
		module := written[alias]
		for _, other := range aliases {
			if other != alias && module != "" && validation.PluginModuleKey(final[other]) == validation.PluginModuleKey(module) {
				return &PluginModuleError{Module: module, Alias: other}
			}
		}
	}
	return nil
}
