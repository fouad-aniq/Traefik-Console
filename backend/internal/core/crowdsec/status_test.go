package crowdsec

import (
	"context"
	"errors"
	"testing"
	"time"

	configsecrets "github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
)

type noIntegration struct {
	db.ItemReader
	db.SecretReader
}

func (noIntegration) GetItem(context.Context, models.ItemKind, string) (*models.ConfigItemRow, error) {
	return nil, nil
}

type noSecret struct{}

func (noSecret) PrepareSecret(context.Context, string, string, string, string) (*models.SecretRow, error) {
	return nil, errors.New("unused")
}
func (noSecret) DecryptSecret(context.Context, string) (string, error) {
	return "", configsecrets.ErrSecretNotFound
}

func plug(name, module, origin string) models.UIResource[models.StaticPluginConfig] {
	return models.UIResource[models.StaticPluginConfig]{Name: name, Origin: origin, Status: models.StatusActive, Config: models.StaticPluginConfig{ModuleName: module, Version: "v1.8.1"}}
}

func TestStatusReportsEachPartSeparately(t *testing.T) {
	m := &crowdsecManager{store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: "http://crowdsec:8080"}
	got, err := m.Status(context.Background(), Inventory{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Plugin.State != "missing" || got.Middleware.State != "missing" || got.Key.Present || got.LAPI.State != "unchecked" || got.Complete || got.Engine.State != "unavailable" {
		t.Fatalf("an empty configuration reports everything missing: %+v", got)
	}

	got, _ = m.Status(context.Background(), Inventory{
		Plugins: []models.UIResource[models.StaticPluginConfig]{plug("my-bouncer", "GitHub.com/MaxLerebourg/crowdsec-bouncer-traefik-plugin", "external")},
		Middlewares: []models.UIResource[models.MiddlewareConfig]{
			{Name: "guard", Status: models.StatusActive, Origin: "managed", Config: models.MiddlewareConfig{Type: "plugin", PluginName: "my-bouncer"}},
		},
		Routers: []models.UIResource[models.RouterConfig]{
			{Name: "app", Status: models.StatusActive, Config: models.RouterConfig{Middlewares: []string{"guard"}}},
			{Name: "api", Status: models.StatusActive, Config: models.RouterConfig{}},
		},
	})
	if got.Plugin.State != "installed" || got.Plugin.Alias != "my-bouncer" || got.Plugin.Origin != "external" {
		t.Fatalf("plugin under another alias and path casing is detected: %+v", got.Plugin)
	}
	if got.Middleware.State != "installed" || got.Middleware.Name != "guard" {
		t.Fatalf("a middleware using the plugin is detected under its own name: %+v", got.Middleware)
	}
	if len(got.Routers) != 2 || !got.Routers[0].Protected || got.Routers[1].Protected {
		t.Fatalf("routers: %+v", got.Routers)
	}
	if got.Complete {
		t.Fatal("without a key the integration is not complete")
	}
}

func TestFindPluginPrefersTheDefaultAliasThenManagedThenName(t *testing.T) {
	p, ok := findPlugin([]models.UIResource[models.StaticPluginConfig]{
		plug("zzz", crowdsecPluginModule, "managed"),
		plug("aaa", crowdsecPluginModule, "external"),
	})
	if !ok || p.Name != "zzz" {
		t.Fatalf("got %q %v", p.Name, ok)
	}
	if _, ok := findPlugin([]models.UIResource[models.StaticPluginConfig]{plug("other", "github.com/org/other", "managed")}); ok {
		t.Fatal("an unrelated plugin is not the bouncer")
	}
}

func TestSummarizeAlerts(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 30, 0, 0, time.UTC)
	alerts := []docker.Alert{
		{Reason: "crowdsecurity/ssh-bf", Country: "fr", Value: "203.0.113.9", CreatedAt: "2026-11-10T12:10:00Z"},
		{Reason: "crowdsecurity/ssh-bf", Country: "FR", Value: "203.0.113.9", CreatedAt: "2026-11-10T11:50:00Z"},
		{Reason: "crowdsecurity/http-probing", Country: "", Value: "198.51.100.4", CreatedAt: "2026-11-09T01:00:00Z"},
		{Reason: "old", Country: "US", Value: "192.0.2.1", CreatedAt: "2026-10-01T00:00:00Z"},
	}
	countries, scenarios, sources, timeline, bucket := summarizeAlerts(alerts, now, 24)
	if bucket != 1 || len(timeline) != 24 {
		t.Fatalf("a day is shown by the hour: %d %d", bucket, len(timeline))
	}
	if countries[0].Key != "FR" || countries[0].Count != 2 || scenarios[0].Key != "crowdsecurity/ssh-bf" || sources[0].Key != "203.0.113.9" {
		t.Fatalf("%v %v %v", countries, scenarios, sources)
	}
	hasUnknown := false
	for _, c := range countries {
		hasUnknown = hasUnknown || c.Key == "??"
	}
	if !hasUnknown {
		t.Fatal("alerts without a country are grouped, not dropped")
	}
	total := 0
	for _, p := range timeline {
		total += p.Count
	}
	if total != 2 || timeline[len(timeline)-1].Count != 1 || timeline[len(timeline)-2].Count != 1 {
		t.Fatalf("only alerts inside the window are plotted: %+v", timeline)
	}
	if _, _, _, week, bucket := summarizeAlerts(nil, now, 168); bucket != 24 || len(week) != 7 {
		t.Fatalf("a week is shown by the day: %d %d", bucket, len(week))
	}
}

func TestPrivateSourcesAreGroupedAsLocalNetwork(t *testing.T) {
	for value, want := range map[string]string{"192.168.80.4": "LOCAL", "10.0.0.0/8": "LOCAL", "127.0.0.1": "LOCAL", "fe80::1": "LOCAL", "203.0.113.9": "??"} {
		if got := alertCountry(docker.Alert{Value: value}); got != want {
			t.Errorf("%s: got %s, want %s", value, got, want)
		}
	}
	if got := alertCountry(docker.Alert{Value: "192.168.1.1", Country: "ma"}); got != "MA" {
		t.Fatalf("a recorded country is kept: %s", got)
	}
}
