package consoleroute

import (
	"context"
	"errors"
	"testing"

	"github.com/traefik-console/internal/models"
)

type fakeSettings map[string]string

func (f fakeSettings) GetSetting(_ context.Context, k string) (string, error) { return f[k], nil }
func (f fakeSettings) SetSetting(_ context.Context, k, v string) error        { f[k] = v; return nil }

var cfg = Config{Host: "console.example.com", Upstream: "http://console:8081"}

type fakeTraefik struct {
	entryPoints []models.UIResource[models.EntryPointConfig]
	routers     []models.UIResource[models.RouterConfig]
	services    []models.UIResource[models.ServiceConfig]
	listErr     error
	writeErr    error
	svcWrites   map[string]models.ServiceConfig
	rtWrites    map[string]models.RouterConfig
}

func (f *fakeTraefik) ListRouters(context.Context) ([]models.UIResource[models.RouterConfig], error) {
	return f.routers, f.listErr
}
func (f *fakeTraefik) ListEntryPoints(context.Context) ([]models.UIResource[models.EntryPointConfig], error) {
	return f.entryPoints, f.listErr
}
func (f *fakeTraefik) ListServices(context.Context) ([]models.UIResource[models.ServiceConfig], error) {
	return f.services, f.listErr
}
func (f *fakeTraefik) UpsertService(_ context.Context, name string, in models.ServiceConfig, _ models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.svcWrites[name] = in
	f.services = append(f.services, models.UIResource[models.ServiceConfig]{Name: name, Origin: "managed", Config: in})
	return &models.WriteResult[models.ServiceConfig]{}, nil
}
func (f *fakeTraefik) UpsertRouter(_ context.Context, name string, in models.RouterConfig, _ models.WriteOptions) (*models.WriteResult[models.RouterConfig], error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	f.rtWrites[name] = in
	return &models.WriteResult[models.RouterConfig]{}, nil
}

func shipped() *fakeTraefik {
	return &fakeTraefik{
		routers: []models.UIResource[models.RouterConfig]{{Name: "console-bootstrap@file", Origin: "external",
			Config: models.RouterConfig{EntryPoints: []string{"websecure"}, Rule: "Host(`console.example.com`)", Service: "console-bootstrap", Priority: 1, TLS: &models.RouterTLS{Enabled: true}}}},
		services: []models.UIResource[models.ServiceConfig]{{Name: "console-bootstrap@file", Origin: "external",
			Config: models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://console:8081"}}}}},
		svcWrites: map[string]models.ServiceConfig{}, rtWrites: map[string]models.RouterConfig{},
	}
}

func TestSeedsTheManagedServiceAndRouteOnce(t *testing.T) {
	settings, tr := fakeSettings{}, shipped()
	done, err := Seed(context.Background(), cfg, settings, tr, tr, tr)
	if !done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	svc := tr.svcWrites[Name]
	if len(svc.Servers) != 1 || svc.Servers[0].URL != "http://console:8081" {
		t.Fatalf("service: %+v", svc)
	}
	rt := tr.rtWrites[Name]
	if rt.Service != Name || len(rt.Domains) != 1 || rt.Domains[0] != "console.example.com" || rt.CustomRule != "" ||
		len(rt.EntryPoints) != 1 || rt.EntryPoints[0] != "websecure" || rt.TLS == nil || !rt.TLS.Enabled ||
		rt.Priority != len("Host(`console.example.com`)")+1 {
		t.Fatalf("route: %+v", rt)
	}
	if settings[SettingKey] != "seeded" {
		t.Fatal("seeding not remembered")
	}
	// A later start does nothing, even if the operator deleted the route meanwhile.
	tr.svcWrites, tr.rtWrites = map[string]models.ServiceConfig{}, map[string]models.RouterConfig{}
	if done, _ := Seed(context.Background(), cfg, settings, tr, tr, tr); !done || len(tr.rtWrites)+len(tr.svcWrites) != 0 {
		t.Fatal("seeded twice")
	}
}

func ownTraefik() *fakeTraefik {
	return &fakeTraefik{
		entryPoints: []models.UIResource[models.EntryPointConfig]{
			{Name: "traefik", Config: models.EntryPointConfig{Name: "traefik", Protocol: "tcp", Port: 8080}},
			{Name: "web", Config: models.EntryPointConfig{Name: "web", Protocol: "tcp", Port: 80, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{}}}},
			{Name: "web-secure", Config: models.EntryPointConfig{Name: "web-secure", Protocol: "tcp", Port: 443, HTTP: &models.EntryPointHTTP{TLS: &models.EntryPointTLS{}}}},
		},
		routers:   []models.UIResource[models.RouterConfig]{{Name: "plex@file", Origin: "external", Config: models.RouterConfig{Rule: "Host(`plex.example.com`)"}}},
		svcWrites: map[string]models.ServiceConfig{}, rtWrites: map[string]models.RouterConfig{},
	}
}

// Without a bootstrap route (the operator's own Traefik), the Console builds its route from its
// settings on the HTTPS entrypoint found in Traefik's configuration.
func TestCreatesTheRouteFromSettingsWithoutABootstrapRoute(t *testing.T) {
	settings, tr := fakeSettings{}, ownTraefik()
	done, err := Seed(context.Background(), cfg, settings, tr, tr, tr)
	if !done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if svc := tr.svcWrites[Name]; len(svc.Servers) != 1 || svc.Servers[0].URL != "http://console:8081" {
		t.Fatalf("service: %+v", svc)
	}
	rt := tr.rtWrites[Name]
	if rt.Service != Name || len(rt.Domains) != 1 || rt.Domains[0] != "console.example.com" || len(rt.EntryPoints) != 1 || rt.EntryPoints[0] != "web-secure" || rt.TLS == nil || !rt.TLS.Enabled || rt.Priority != 0 {
		t.Fatalf("route: %+v", rt)
	}
	if settings[SettingKey] != "seeded" {
		t.Fatal("not remembered")
	}
}

func TestLeavesAnotherRouteForTheConsoleAddressAlone(t *testing.T) {
	settings, tr := fakeSettings{}, ownTraefik()
	tr.routers = append(tr.routers, models.UIResource[models.RouterConfig]{Name: "console@docker", Origin: "external", Config: models.RouterConfig{Rule: "Host(`console.example.com`)"}})
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); !done || err != nil || len(tr.rtWrites)+len(tr.svcWrites) != 0 {
		t.Fatalf("an existing route for the address must be kept: done=%v err=%v", done, err)
	}
}

func TestNothingToBuildFrom(t *testing.T) {
	settings, tr := fakeSettings{}, ownTraefik()
	if done, err := Seed(context.Background(), Config{}, settings, tr, tr, tr); !done || err != nil || len(tr.rtWrites) != 0 || settings[SettingKey] != "" {
		t.Fatal("no CONSOLE_HOST: nothing created, nothing remembered")
	}
	tr.entryPoints = tr.entryPoints[:2] // no HTTPS entrypoint
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); !done || err != nil || len(tr.rtWrites) != 0 || settings[SettingKey] != "" {
		t.Fatal("no HTTPS entrypoint: nothing created, nothing remembered")
	}
}

func TestHostPortIsDropped(t *testing.T) {
	if hostOnly(" console.example.com:8443 ") != "console.example.com" || hostOnly("console.example.com") != "console.example.com" {
		t.Fatal("hostOnly")
	}
}

func TestKeepsAnExistingConsoleRoute(t *testing.T) {
	settings, tr := fakeSettings{}, shipped()
	tr.routers = append(tr.routers, models.UIResource[models.RouterConfig]{Name: "console", Origin: "managed"})
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); !done || err != nil || len(tr.rtWrites)+len(tr.svcWrites) != 0 {
		t.Fatalf("an existing route must be kept: done=%v err=%v", done, err)
	}
	if settings[SettingKey] == "" {
		t.Fatal("must be remembered")
	}
}

func TestRetriesWhileTraefikOrTheEngineIsNotReady(t *testing.T) {
	settings, tr := fakeSettings{}, shipped()
	tr.listErr = errors.New("traefik unavailable")
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); done || err == nil {
		t.Fatal("an unreachable Traefik must be retried")
	}
	tr.listErr, tr.services = nil, nil
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); done || !errors.Is(err, ErrNotReady) {
		t.Fatal("a missing bootstrap service must be retried", err)
	}
	tr = shipped()
	tr.writeErr = errors.New("operation running")
	if done, _ := Seed(context.Background(), cfg, settings, tr, tr, tr); done || settings[SettingKey] != "" {
		t.Fatal("a failed write must be retried and not remembered")
	}
	// The service was created but the route failed: the next attempt reuses the service.
	tr = shipped()
	tr.services = append(tr.services, models.UIResource[models.ServiceConfig]{Name: Name, Origin: "managed"})
	if done, err := Seed(context.Background(), cfg, settings, tr, tr, tr); !done || err != nil || len(tr.svcWrites) != 0 || tr.rtWrites[Name].Service != Name {
		t.Fatalf("must reuse the service and create the route: done=%v err=%v", done, err)
	}
}

func TestCustomRuleAndExplicitPriorityAreKept(t *testing.T) {
	rt := routeFrom(models.RouterConfig{Rule: "Host(`a.example.com`) || Host(`b.example.com`)", Priority: 500})
	if rt.CustomRule == "" || len(rt.Domains) != 0 || rt.Priority != 501 || rt.TLS != nil {
		t.Fatalf("%+v", rt)
	}
}
