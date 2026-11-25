package crowdsec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

type pluginRows struct {
	db.ItemReader
	db.SecretReader
	rows []models.ConfigItemRow
}

func (s pluginRows) ListItemsByKind(context.Context, models.ItemKind) ([]models.ConfigItemRow, error) {
	return s.rows, nil
}

func bouncerRow(name, module string) models.ConfigItemRow {
	raw, _ := json.Marshal(models.StaticPluginConfig{ModuleName: module, Version: "v1.3.5"})
	return models.ConfigItemRow{Kind: models.KindPlugin, Name: name, Payload: raw}
}

func TestInstalledPluginAliasReusesAnotherManagedDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []models.ConfigItemRow
		want string
	}{
		{"nothing installed", nil, ""},
		{"default alias already holds it", []models.ConfigItemRow{bouncerRow(crowdsecPluginShortName, crowdsecPluginModule)}, ""},
		{"installed by hand under another alias", []models.ConfigItemRow{bouncerRow("my-bouncer", "GitHub.com/MaxLerebourg/crowdsec-bouncer-traefik-plugin")}, "my-bouncer"},
		{"unrelated plugin", []models.ConfigItemRow{bouncerRow("other", "github.com/org/other")}, ""},
		{"default alias wins over another", []models.ConfigItemRow{bouncerRow("zzz", crowdsecPluginModule), bouncerRow(crowdsecPluginShortName, crowdsecPluginModule)}, ""},
	} {
		m := &crowdsecManager{store: pluginRows{rows: tc.rows}}
		got, err := m.installedPluginAlias(context.Background())
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}
