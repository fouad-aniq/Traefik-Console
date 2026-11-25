package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

type listedServices struct {
	services.ServiceManager
	items []models.UIResource[models.ServiceConfig]
	tcp   []models.UIResource[models.RouterConfig]
	err   error
}

func (f listedServices) ListServices(context.Context) ([]models.UIResource[models.ServiceConfig], error) {
	return f.items, nil
}

func (f listedServices) RuntimeRouters(_ context.Context, protocol string) ([]models.UIResource[models.RouterConfig], error) {
	if f.err != nil {
		return nil, f.err
	}
	if protocol == models.ServiceTCP {
		return f.tcp, nil
	}
	return []models.UIResource[models.RouterConfig]{}, nil
}

func serveServices(t *testing.T, routersErr error) []models.UIResource[models.ServiceConfig] {
	t.Helper()
	deps := routeDependencies(t, &routeCalls{})
	deps.Routers = listedRouters{err: routersErr, items: []models.UIResource[models.RouterConfig]{
		{Name: "portainer", Provider: "file", Config: models.RouterConfig{Rule: "Host(`p.example`)", EntryPoints: []string{"websecure"}, Service: "portainer", TLS: &models.RouterTLS{Enabled: true}, Middlewares: []string{"auth"}}},
		{Name: "dashboard@internal", Provider: "internal", ReadOnly: true, Config: models.RouterConfig{Rule: "PathPrefix(`/dashboard`)", Service: "dashboard@internal"}},
	}}
	deps.Services = listedServices{err: routersErr, items: []models.UIResource[models.ServiceConfig]{
		{Name: "portainer", Origin: "managed", Provider: "file", Config: models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://p:9000"}}}},
		{Name: "dashboard@internal", Origin: "external", Provider: "internal", ReadOnly: true},
		{Name: "spare", Origin: "managed", Provider: "file"},
		// A TCP service of the same name as an HTTP router's service is not used by that router.
		{Name: "db", Origin: "managed", Provider: "file", Config: models.ServiceConfig{Protocol: "tcp", Servers: []models.ServerEndpoint{{Address: "db:5432"}}}},
		{Name: "dns", Origin: "managed", Provider: "file", Config: models.ServiceConfig{Protocol: "udp", Servers: []models.ServerEndpoint{{Address: "dns:53"}}}},
	}, tcp: []models.UIResource[models.RouterConfig]{
		{Name: "postgres@docker", Provider: "docker", ReadOnly: true, Config: models.RouterConfig{Rule: "HostSNI(`*`)", Service: "db@file", EntryPoints: []string{"pg"}}},
	}}
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/services", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out []models.UIResource[models.ServiceConfig]
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestServicesCarryTheRoutersThatUseThem(t *testing.T) {
	by := map[string]*models.ServiceUsage{}
	for _, s := range serveServices(t, nil) {
		by[s.Name] = s.Config.UsedBy
	}
	if u := by["portainer"]; u == nil || len(u.Routers) != 1 || u.Routers[0].Name != "portainer" || !u.Routers[0].TLS || u.Routers[0].EntryPoints[0] != "websecure" {
		t.Fatal("portainer is used by its router", u)
	}
	if u := by["dashboard@internal"]; u == nil || len(u.Routers) != 1 || !u.Routers[0].ReadOnly {
		t.Fatal("external services get their usage too", u)
	}
	if u := by["spare"]; u == nil || len(u.Routers) != 0 {
		t.Fatal("spare is known unused", u)
	}
	if u := by["db"]; u == nil || len(u.Routers) != 1 || u.Routers[0].Name != "postgres@docker" {
		t.Fatal("db is used by the TCP router Traefik reports", u)
	}
	if u := by["dns"]; u == nil || len(u.Routers) != 0 {
		t.Fatal("dns is known unused", u)
	}
}

func TestServicesListedWithoutUsageWhenRoutersCannotBeRead(t *testing.T) {
	for _, s := range serveServices(t, status.ErrObservationUnavailable) {
		if s.Config.UsedBy != nil {
			t.Fatal("reported usage it could not read", s.Name)
		}
	}
}
