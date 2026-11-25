package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// One scenario per protocol, written once and run for http, tcp and udp.
type lifecycle struct {
	protocol string
	name     string
	create   models.ServiceConfig
	update   models.ServiceConfig
}

func lifecycles() []lifecycle {
	no := false
	httpCreate := servers("http://a:1", "https://b:2/x")
	httpCreate.HealthCheck = &models.ServiceHealth{Path: "/up", Interval: "10s"}
	httpCreate.InsecureSkipVerify = true
	httpUpdate := servers("http://c:3")
	httpUpdate.PassHostHeader = &no

	tcpCreate := tcpService("db:5432")
	tcpCreate.InsecureSkipVerify = true
	tcpCreate.TCP = &models.TCPServiceOptions{TLS: true, ServerName: "db.example", ProxyProtocol: 2, HealthCheck: &models.TCPHealthCheck{Interval: "10s"}}
	tcpUpdate := tcpService("db:5433", "db2:5432")

	return []lifecycle{
		{"http", "web", httpCreate, httpUpdate},
		{"tcp", "db", tcpCreate, tcpUpdate},
		{"udp", "dns", udpService("dns:53"), udpService("dns2:53", "dns3:53")},
	}
}

func addressesOf(cfg models.ServiceConfig) []string {
	out := []string{}
	for _, s := range cfg.Servers {
		if s.URL != "" {
			out = append(out, s.URL)
		} else {
			out = append(out, s.Address)
		}
	}
	return out
}

func stored(t *testing.T, f *fixture, name string) *models.ConfigItemRow {
	t.Helper()
	row, e := f.store.GetItem(ctx, models.KindService, name)
	if e != nil {
		t.Fatal(e)
	}
	return row
}

func listedNames(t *testing.T, f *fixture, protocol string) []string {
	t.Helper()
	all, e := f.m.ListServices(ctx)
	if e != nil {
		t.Fatal(e)
	}
	out := []string{}
	for _, s := range all {
		if s.Origin == "managed" && models.ServiceProtocol(s.Config) == protocol {
			out = append(out, s.Name)
		}
	}
	return out
}

func TestEveryProtocolCanBeCreatedEditedAndDeleted(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())

			created := f.upsert(t, c.name, c.create)
			if created.Resource.Version != 1 || created.Resource.Origin != "managed" || created.Resource.Config.Protocol != c.protocol || created.Operation == nil {
				t.Fatalf("%+v", created.Resource)
			}
			if got := strings.Join(addressesOf(created.Resource.Config), ","); got != strings.Join(addressesOf(c.create), ",") {
				t.Fatalf("created %q", got)
			}
			if names := listedNames(t, f, c.protocol); len(names) != 1 || names[0] != c.name {
				t.Fatalf("listed %v", names)
			}
			one, e := f.m.GetService(ctx, c.name, c.protocol)
			if e != nil || one.Name != c.name || one.Config.Protocol != c.protocol {
				t.Fatalf("%+v %v", one, e)
			}

			edited := f.upsert(t, c.name, c.update)
			if edited.Resource.Version != 2 || edited.Resource.Config.Protocol != c.protocol {
				t.Fatalf("%+v", edited.Resource)
			}
			if got := strings.Join(addressesOf(edited.Resource.Config), ","); got != strings.Join(addressesOf(c.update), ",") {
				t.Fatalf("edited %q", got)
			}
			if names := listedNames(t, f, c.protocol); len(names) != 1 {
				t.Fatalf("an edit must not add a second entry: %v", names)
			}

			if _, e := f.m.DeleteService(ctx, c.name, models.WriteOptions{}); e != nil {
				t.Fatal(e)
			}
			if stored(t, f, c.name) != nil {
				t.Fatal("not deleted")
			}
			last := f.applier.Applied[len(f.applier.Applied)-1]
			if len(last.Deletes) != 1 || last.Deletes[0].Kind != models.KindService || last.Deletes[0].Name != c.name || len(last.Upserts) != 0 {
				t.Fatalf("%+v", last)
			}
			if names := listedNames(t, f, c.protocol); len(names) != 0 {
				t.Fatalf("still listed: %v", names)
			}
			// A second delete finds nothing, and the name can be used again.
			if _, e := f.m.DeleteService(ctx, c.name, models.WriteOptions{}); !errors.Is(e, files.ErrSourceFileNotFound) {
				t.Fatalf("second delete: %v", e)
			}
			if again := f.upsert(t, c.name, c.create); again.Resource.Version != 1 {
				t.Fatalf("recreated at version %d", again.Resource.Version)
			}
		})
	}
}

func TestEachWriteIsOneOperationOfTheRightShape(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, c.name, c.create)
			f.upsert(t, c.name, c.update)
			_, _ = f.m.DeleteService(ctx, c.name, models.WriteOptions{})
			if len(f.applier.Applied) != 3 {
				t.Fatalf("%d operations for 3 writes", len(f.applier.Applied))
			}
			for i, m := range f.applier.Applied[:2] {
				if len(m.Upserts) != 1 || len(m.Deletes) != 0 || m.Upserts[0].ID != "service:"+c.name || m.Upserts[0].Scope != models.ScopeDynamic || m.Upserts[0].Kind != models.KindService {
					t.Fatalf("write %d: %+v", i, m)
				}
			}
		})
	}
}

func TestAnEditReplacesTheWholeDefinition(t *testing.T) {
	for _, c := range lifecycles()[:2] {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, c.name, c.create)
			if !strings.Contains(string(stored(t, f, c.name).Payload), `"servers"`) {
				t.Fatal("nothing stored")
			}
			f.upsert(t, c.name, c.update)
			payload := string(stored(t, f, c.name).Payload)
			for _, gone := range []string{"insecure_skip_verify", `"tcp":`, "health_check", "server_name", "proxy_protocol"} {
				if strings.Contains(payload, gone) {
					t.Fatalf("%s survived the edit: %s", gone, payload)
				}
			}
		})
	}
}

func TestServicesAreStoredNormalized(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	web := f.upsert(t, "web", servers("  HTTP://app:80  ")).Resource.Config
	db := f.upsert(t, "db", tcpService("  db:5432\n")).Resource.Config
	dns := f.upsert(t, "dns", udpService(" dns:53 ")).Resource.Config
	if web.Servers[0].URL != "http://app:80" || db.Servers[0].Address != "db:5432" || dns.Servers[0].Address != "dns:53" {
		t.Fatalf("%+v %+v %+v", web, db, dns)
	}
	// The name is trimmed as well.
	if res := f.upsert(t, "  spaced  ", servers("http://a")); res.Resource.Name != "spaced" || stored(t, f, "spaced") == nil {
		t.Fatalf("%+v", res.Resource)
	}
}

func TestAFailedWriteLeavesTheServiceAsItWas(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, c.name, c.create)
			before := stored(t, f, c.name)
			applied := len(f.applier.Applied)

			f.applier.Err = errors.New("traefik rejected it")
			if _, e := f.m.UpsertService(ctx, c.name, c.update, models.WriteOptions{}); e == nil {
				t.Fatal("a failed edit was reported as done")
			}
			if _, e := f.m.DeleteService(ctx, c.name, models.WriteOptions{}); e == nil {
				t.Fatal("a failed delete was reported as done")
			}
			after := stored(t, f, c.name)
			if after == nil || after.Version != before.Version || string(after.Payload) != string(before.Payload) {
				t.Fatalf("changed by a failed write: %+v", after)
			}
			if len(f.applier.Applied) != applied {
				t.Fatal("a failed write was recorded")
			}

			// The same writes succeed once the engine works again.
			f.applier.Err = nil
			f.upsert(t, c.name, c.update)
			if _, e := f.m.DeleteService(ctx, c.name, models.WriteOptions{}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestAFailedCreateStoresNothing(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.applier.Err = errors.New("boom")
			if _, e := f.m.UpsertService(ctx, c.name, c.create, models.WriteOptions{}); e == nil {
				t.Fatal("swallowed")
			}
			if stored(t, f, c.name) != nil {
				t.Fatal("stored")
			}
			if names := listedNames(t, f, c.protocol); len(names) != 0 {
				t.Fatal(names)
			}
		})
	}
}

func TestDryRunsChangeNothingForEveryProtocol(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			dry := models.WriteOptions{DryRun: true}

			if res, e := f.m.UpsertService(ctx, c.name, c.create, dry); e != nil || res.Preview == nil || res.Operation != nil {
				t.Fatalf("%+v %v", res, e)
			}
			if stored(t, f, c.name) != nil || len(f.applier.Applied) != 0 {
				t.Fatal("a create preview stored something")
			}

			f.upsert(t, c.name, c.create)
			before := stored(t, f, c.name)
			applied := len(f.applier.Applied)
			if res, e := f.m.UpsertService(ctx, c.name, c.update, dry); e != nil || res.Preview == nil {
				t.Fatalf("%+v %v", res, e)
			}
			if res, e := f.m.DeleteService(ctx, c.name, dry); e != nil || res.Preview == nil {
				t.Fatalf("%+v %v", res, e)
			}
			after := stored(t, f, c.name)
			if after == nil || after.Version != before.Version || string(after.Payload) != string(before.Payload) || len(f.applier.Applied) != applied {
				t.Fatal("a preview changed the service")
			}
		})
	}
}

func TestADryRunStillRefusesWhatIsInvalid(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	dry := models.WriteOptions{DryRun: true}
	for protocol, bad := range map[string]models.ServiceConfig{"http": servers("nope"), "tcp": tcpService("db"), "udp": udpService("dns")} {
		if _, e := f.m.UpsertService(ctx, "x", bad, dry); e == nil {
			t.Fatalf("%s: an invalid preview was accepted", protocol)
		}
	}
	f.upsert(t, "used", servers("http://a"))
	f.router(t, "shop", "used")
	if _, e := f.m.DeleteService(ctx, "used", dry); !errors.Is(e, ErrServiceInUse) {
		t.Fatalf("a delete preview ignored the router: %v", e)
	}
}

func TestServiceNameRules(t *testing.T) {
	long63 := "a" + strings.Repeat("b", 62)
	valid := []string{"a", "a1", "my-app", "my_app", "x-1_y", long63, "web2"}
	invalid := []string{"", "   ", "1app", "-app", "_app", "App", "mY", "my.app", "my app", "app@file", "app@docker", "a/b", "é", "ab\ncd", strings.Repeat("a", 64), "app!"}

	for _, c := range lifecycles() {
		for _, name := range valid {
			t.Run(c.protocol+" accepts "+name, func(t *testing.T) {
				f := newFixture(t, dockerRuntime())
				if _, e := f.m.UpsertService(ctx, name, c.create, models.WriteOptions{}); e != nil {
					t.Fatal(e)
				}
			})
		}
		for _, name := range invalid {
			t.Run(c.protocol+" refuses "+name, func(t *testing.T) {
				f := newFixture(t, dockerRuntime())
				if _, e := f.m.UpsertService(ctx, name, c.create, models.WriteOptions{}); !errors.Is(e, ErrServiceInvalidName) {
					t.Fatalf("got %v", e)
				}
				if len(f.applier.Applied) != 0 {
					t.Fatal("an invalid name reached the engine")
				}
			})
		}
	}
}

func TestAServiceNeverChangesProtocolInAnyDirection(t *testing.T) {
	all := lifecycles()
	for _, from := range all {
		for _, to := range all {
			if from.protocol == to.protocol {
				continue
			}
			t.Run(from.protocol+" to "+to.protocol, func(t *testing.T) {
				f := newFixture(t, dockerRuntime())
				f.upsert(t, "svc", from.create)
				before := stored(t, f, "svc")
				applied := len(f.applier.Applied)
				if _, e := f.m.UpsertService(ctx, "svc", to.create, models.WriteOptions{}); !errors.Is(e, ErrServiceInvalidProtocol) {
					t.Fatalf("got %v", e)
				}
				if _, e := f.m.UpsertService(ctx, "svc", to.create, models.WriteOptions{DryRun: true}); !errors.Is(e, ErrServiceInvalidProtocol) {
					t.Fatalf("preview: got %v", e)
				}
				after := stored(t, f, "svc")
				if string(after.Payload) != string(before.Payload) || after.Version != before.Version || len(f.applier.Applied) != applied {
					t.Fatal("a refused change altered the service")
				}
				// Staying in the same protocol is fine.
				f.upsert(t, "svc", from.update)
			})
		}
	}
}

func TestCreatingAndEditingDoNotNeedTraefikToBeReachable(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.m = NewServiceManager(f.store, runtime{err: status.ErrObservationUnavailable}, f.applier)
			f.upsert(t, c.name, c.create)
			f.upsert(t, c.name, c.update)
		})
	}
}

func TestInvalidInputNeverReachesTheEngineOrTheStore(t *testing.T) {
	bad := map[string]models.ServiceConfig{
		"http, no servers": servers(),
		"http, bad url":    servers("ftp://a"),
		"tcp, no servers":  tcpService(),
		"tcp, bad address": tcpService("db"),
		"udp, no servers":  udpService(),
		"udp, bad address": udpService("dns:99999"),
		"tcp options on udp": func() models.ServiceConfig {
			c := udpService("dns:53")
			c.TCP = &models.TCPServiceOptions{ProxyProtocol: 1}
			return c
		}(),
	}
	for name, cfg := range bad {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			if _, e := f.m.UpsertService(ctx, "x", cfg, models.WriteOptions{}); e == nil {
				t.Fatal("accepted")
			}
			if len(f.applier.Applied) != 0 || stored(t, f, "x") != nil {
				t.Fatal("an invalid service got through")
			}
		})
	}
}

func TestAnInvalidEditKeepsTheStoredService(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, c.name, c.create)
			before := stored(t, f, c.name)
			bad := c.update
			bad.Servers = nil
			if _, e := f.m.UpsertService(ctx, c.name, bad, models.WriteOptions{}); !errors.Is(e, ErrServiceNoServers) {
				t.Fatalf("got %v", e)
			}
			after := stored(t, f, c.name)
			if string(after.Payload) != string(before.Payload) || after.Version != before.Version {
				t.Fatal("changed")
			}
		})
	}
}

/* ---------------------------------------------------------------- delete guard */

func runtimeRouter(provider, name, service string) status.ExternalItem {
	return status.ExternalItem{Name: name, FullName: name + "@" + provider, Provider: provider, Status: "enabled", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": service}}
}

func TestTheDeleteGuardLooksAtTheRoutersOfTheSameProtocolOnly(t *testing.T) {
	type router struct{ protocol, provider, name, service, status string }
	cases := map[string]struct {
		service  lifecycle
		routers  []router
		blocked  bool
		blamedBy string
	}{
		"a tcp router of another provider, by qualified name": {lifecycles()[1], []router{{"tcp", "docker", "pg", "db@file", ""}}, true, "pg@docker"},
		"a tcp router of the file provider, by bare name":     {lifecycles()[1], []router{{"tcp", "file", "pg", "db", ""}}, true, "pg@file"},
		"a tcp router that is itself in error":                {lifecycles()[1], []router{{"tcp", "docker", "pg", "db@file", "error"}}, true, "pg@docker"},
		"a tcp router that is disabled":                       {lifecycles()[1], []router{{"tcp", "docker", "pg", "db@file", "disabled"}}, true, "pg@docker"},
		"a tcp router among others":                           {lifecycles()[1], []router{{"tcp", "docker", "a", "other@file", ""}, {"tcp", "docker", "pg", "db@file", ""}}, true, "pg@docker"},
		"a udp router of another provider":                    {lifecycles()[2], []router{{"udp", "docker", "dnsr", "dns@file", ""}}, true, "dnsr@docker"},
		"a udp router, bare name from the file provider":      {lifecycles()[2], []router{{"udp", "file", "dnsr", "dns", ""}}, true, "dnsr@file"},
		"a docker http router":                                {lifecycles()[0], []router{{"http", "docker", "site", "web@file", ""}}, true, "site@docker"},
		"a bare docker router name means docker's service":    {lifecycles()[1], []router{{"tcp", "docker", "pg", "db", ""}}, false, ""},
		"a tcp router of another provider's service":          {lifecycles()[1], []router{{"tcp", "docker", "pg", "db@docker", ""}}, false, ""},
		"a udp router with the same service name":             {lifecycles()[1], []router{{"udp", "docker", "dnsr", "db@file", ""}}, false, ""},
		"a tcp router with the same service name":             {lifecycles()[2], []router{{"tcp", "docker", "pg", "dns@file", ""}}, false, ""},
		"a tcp router naming the http service":                {lifecycles()[0], []router{{"tcp", "docker", "pg", "web@file", ""}}, false, ""},
		"a router with no service":                            {lifecycles()[1], []router{{"tcp", "docker", "pg", "", ""}}, false, ""},
		"no routers":                                          {lifecycles()[1], nil, false, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rt := dockerRuntime()
			for _, r := range c.routers {
				item := runtimeRouter(r.provider, r.name, r.service)
				if r.status != "" {
					item.Status = r.status
				}
				switch r.protocol {
				case "tcp":
					if rt.tcp.Routers == nil {
						rt.tcp.Routers = map[string]status.ExternalItem{}
					}
					rt.tcp.Routers[item.FullName] = item
				case "udp":
					if rt.udp.Routers == nil {
						rt.udp.Routers = map[string]status.ExternalItem{}
					}
					rt.udp.Routers[item.FullName] = item
				default:
					if rt.httpRouters == nil {
						rt.httpRouters = map[string]status.ExternalItem{}
					}
					rt.httpRouters[item.FullName] = item
				}
			}
			f := newFixture(t, rt)
			f.upsert(t, c.service.name, c.service.create)
			applied := len(f.applier.Applied)
			_, e := f.m.DeleteService(ctx, c.service.name, models.WriteOptions{})
			if c.blocked {
				if !errors.Is(e, ErrServiceInUse) || !strings.Contains(e.Error(), c.blamedBy) {
					t.Fatalf("got %v, want a block by %s", e, c.blamedBy)
				}
				if stored(t, f, c.service.name) == nil || len(f.applier.Applied) != applied {
					t.Fatal("a blocked delete changed something")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if stored(t, f, c.service.name) != nil {
				t.Fatal("not deleted")
			}
		})
	}
}

func TestAManagedHTTPRouterOnlyBlocksHTTPServices(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "web", servers("http://a"))
	f.upsert(t, "db", tcpService("db:5432"))
	f.router(t, "shop", "web")
	f.router(t, "odd", "db") // an HTTP router can never use a TCP service
	if _, e := f.m.DeleteService(ctx, "web", models.WriteOptions{}); !errors.Is(e, ErrServiceInUse) {
		t.Fatal(e)
	}
	if _, e := f.m.DeleteService(ctx, "db", models.WriteOptions{}); e != nil {
		t.Fatalf("an http router blocked a tcp service: %v", e)
	}
}

func TestDeleteNeedsTraefikToCheckRoutersForEveryProtocol(t *testing.T) {
	for _, c := range lifecycles() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, c.name, c.create)
			f.m = NewServiceManager(f.store, runtime{err: status.ErrObservationUnavailable}, f.applier)
			applied := len(f.applier.Applied)
			if _, e := f.m.DeleteService(ctx, c.name, models.WriteOptions{}); !errors.Is(e, status.ErrObservationUnavailable) {
				t.Fatalf("got %v", e)
			}
			if stored(t, f, c.name) == nil || len(f.applier.Applied) != applied {
				t.Fatal("deleted without checking")
			}
		})
	}
}

func TestAServiceTheConsoleDoesNotManageCannotBeChangedOrDeleted(t *testing.T) {
	rt := transportRuntime()
	rt.tcp.Services["hand@file"] = status.ExternalItem{Name: "hand", FullName: "hand@file", Provider: "file", Status: "enabled"}
	rt.udp.Services["hand-udp@file"] = status.ExternalItem{Name: "hand-udp", FullName: "hand-udp@file", Provider: "file", Status: "enabled"}
	f := newFixture(t, rt)
	for _, name := range []string{"hand", "hand-udp", "legacy", "whoami@docker", "pg@docker", "dns@docker", "api@internal"} {
		if _, e := f.m.DeleteService(ctx, name, models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
			t.Fatalf("%s: got %v", name, e)
		}
	}
	for _, name := range []string{"nothing", "ghost-udp"} {
		if _, e := f.m.DeleteService(ctx, name, models.WriteOptions{}); !errors.Is(e, files.ErrSourceFileNotFound) {
			t.Fatalf("%s: got %v", name, e)
		}
	}
	if len(f.applier.Applied) != 0 {
		t.Fatal("reached the engine")
	}
}

/* ---------------------------------------------------------------- health */

func TestAManagedServicesHealthComesFromItsProtocolsRuntime(t *testing.T) {
	rt := dockerRuntime()
	rt.tcp = status.RuntimeResources{Services: map[string]status.ExternalItem{
		"db@file":  {Name: "db", FullName: "db@file", Provider: "file", Status: "enabled"},
		"bad@file": {Name: "bad", FullName: "bad@file", Provider: "file", Status: "error", ErrorMsg: "no servers"},
	}}
	rt.udp = status.RuntimeResources{Services: map[string]status.ExternalItem{"dns@file": {Name: "dns", FullName: "dns@file", Provider: "file", Status: "enabled"}}}
	rt.services["web@file"] = status.ExternalItem{Name: "web", FullName: "web@file", Provider: "file", Status: "enabled"}
	f := newFixture(t, rt)
	f.upsert(t, "web", servers("http://a"))
	f.upsert(t, "db", tcpService("db:5432"))
	f.upsert(t, "bad", tcpService("bad:1"))
	f.upsert(t, "dns", udpService("dns:53"))
	f.upsert(t, "unseen", udpService("x:1"))
	// Each protocol is read from its own runtime section: "other" has no tcp counterpart in Traefik.
	f.upsert(t, "other", tcpService("o:1"))
	all, e := f.m.ListServices(ctx)
	if e != nil {
		t.Fatal(e)
	}
	health := map[string]string{}
	for _, s := range all {
		if s.Origin == "managed" {
			health[s.Name] = s.LiveHealth
		}
	}
	want := map[string]string{"web": "loaded", "db": "loaded", "bad": "error", "dns": "loaded", "unseen": "unknown", "other": "unknown"}
	for name, h := range want {
		if health[name] != h {
			t.Errorf("%s: %q, want %q", name, health[name], h)
		}
	}
}
