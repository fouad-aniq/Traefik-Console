package status

import (
	"context"
	"errors"
	"testing"
)

const diagBody = `{
 "services":{
  "app@file":{"status":"enabled","serverStatus":{"http://a:1":"UP","http://b:2":"DOWN"}},
  "api@internal":{"status":"enabled"},
  "whoami@docker":{"status":"enabled","serverStatus":{"http://172.18.0.3:80":"UP"}}
 },
 "routers":{
  "shop@file":{"status":"enabled","service":"app","entryPoints":["web"]},
  "admin@file":{"status":"enabled","service":"app@file"},
  "other@docker":{"status":"enabled","service":"app"},
  "dashboard@internal":{"status":"enabled","service":"api@internal","entryPoints":["traefik"]},
  "whoami-web@docker":{"status":"enabled","service":"whoami"}
 }
}`

func routerNames(d ServiceDiagnostic) []string {
	out := []string{}
	for _, r := range d.Routers {
		out = append(out, r.Name)
	}
	return out
}

func TestDiagnosticsOfAManagedService(t *testing.T) {
	d, err := observationFixture(t, diagBody).DiagnoseNamedService(context.Background(), "http", "app")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "app" || !d.Observed || d.RuntimeStatus != "enabled" || d.HealthStatus != "mixed" || d.ServerStatus["UP"] != 1 || d.ServerStatus["DOWN"] != 1 {
		t.Fatalf("%+v", d)
	}
	// "other@docker" says "app" but means app@docker: Traefik resolves bare names per provider.
	if got := routerNames(d); len(got) != 2 || got[0] != "admin" || got[1] != "shop" {
		t.Fatalf("routers %v", got)
	}
}

// Regression: external services such as api@internal returned ERR_INTERNAL.
func TestDiagnosticsOfExternalServices(t *testing.T) {
	c := observationFixture(t, diagBody)
	d, err := c.DiagnoseNamedService(context.Background(), "http", "api@internal")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "api@internal" || d.RuntimeStatus != "enabled" || d.HealthStatus != "unknown" {
		t.Fatalf("%+v", d)
	}
	if got := routerNames(d); len(got) != 1 || got[0] != "dashboard@internal" || d.Routers[0].EntryPoints[0] != "traefik" {
		t.Fatalf("routers %v", got)
	}
	d, err = c.DiagnoseNamedService(context.Background(), "http", "whoami@docker")
	if err != nil || d.HealthStatus != "healthy" {
		t.Fatalf("%+v %v", d, err)
	}
	if got := routerNames(d); len(got) != 1 || got[0] != "whoami-web@docker" {
		t.Fatalf("routers %v", got)
	}
}

func TestDiagnosticsErrorsAreTyped(t *testing.T) {
	c := observationFixture(t, diagBody)
	for _, name := range []string{"", "Bad Name", "a@", "@docker", "x@Docker", "../etc@file"} {
		if _, err := c.DiagnoseNamedService(context.Background(), "http", name); !errors.Is(err, ErrInvalidServiceName) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	for _, name := range []string{"ghost", "ghost@docker"} {
		if _, err := c.DiagnoseNamedService(context.Background(), "http", name); !errors.Is(err, ErrServiceNotObserved) {
			t.Fatalf("%q: %v", name, err)
		}
	}
}
