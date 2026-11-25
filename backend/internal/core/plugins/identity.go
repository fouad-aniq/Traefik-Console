package plugins

import (
	"encoding/json"
	"strings"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

// catalogIdentities lists every module path a catalog entry is known by: its import path first, then the
// other paths the catalog publishes for the same entry (its name, the module inside its snippet).
func catalogIdentities(row models.CatalogPluginRow) []string {
	out := []string{row.ModuleName}
	var aliases []string
	_ = json.Unmarshal([]byte(row.AliasesJSON), &aliases)
	return append(out, aliases...)
}

// catalogIndex maps a module key to its catalog entry. An entry's own import path always wins over an
// alias of another entry, so an alias can never shadow a real module.
func catalogIndex(rows []models.CatalogPluginRow) map[string]models.CatalogPluginRow {
	index := make(map[string]models.CatalogPluginRow, len(rows))
	for _, row := range rows {
		index[validation.PluginModuleKey(row.ModuleName)] = row
	}
	for _, row := range rows {
		for _, alias := range catalogIdentities(row)[1:] {
			if _, taken := index[validation.PluginModuleKey(alias)]; !taken {
				index[validation.PluginModuleKey(alias)] = row
			}
		}
	}
	// A plugin can be installed under a short name ("crowdsec-bouncer-traefik-plugin") while the catalog
	// lists the full path. The last path segment is a fallback key, kept only when it is unambiguous.
	segments := map[string][]models.CatalogPluginRow{}
	for _, row := range rows {
		seen := map[string]bool{}
		for _, identity := range catalogIdentities(row) {
			segment := lastSegment(identity)
			if !seen[segment] {
				seen[segment] = true
				segments[segment] = append(segments[segment], row)
			}
		}
	}
	for segment, owners := range segments {
		if _, taken := index[segment]; !taken && len(owners) == 1 {
			index[segment] = owners[0]
		}
	}
	return index
}

func lastSegment(module string) string {
	key := validation.PluginModuleKey(module)
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// lookupCatalog finds the catalog entry for an installed plugin: by module path first, then by the
// short name the plugin was installed under (its module's last segment, then its alias).
func lookupCatalog(index map[string]models.CatalogPluginRow, module, alias string) (models.CatalogPluginRow, bool) {
	for _, key := range []string{validation.PluginModuleKey(module), lastSegment(module), validation.PluginModuleKey(alias)} {
		if row, ok := index[key]; ok {
			return row, true
		}
	}
	return models.CatalogPluginRow{}, false
}
