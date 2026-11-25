package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// runtime is Traefik's observed services. diagnose records the name the manager asks about.
type runtime struct {
	services    map[string]status.ExternalItem
	httpRouters map[string]status.ExternalItem
	tcp, udp    status.RuntimeResources
	err         error
	diagnose    *string
}

func (r runtime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &status.RuntimeSnapshot{HTTP: status.RuntimeResources{Services: r.services, Routers: r.httpRouters}, TCP: r.tcp, UDP: r.udp}, nil
}

func (r runtime) DiagnoseNamedService(_ context.Context, protocol string, name string) (status.ServiceDiagnostic, error) {
	if r.diagnose != nil {
		*r.diagnose = protocol + ":" + name
	}
	return status.ServiceDiagnostic{Protocol: protocol, Name: name, Observed: true}, nil
}

func dockerRuntime() runtime {
	return runtime{services: map[string]status.ExternalItem{
		"api@internal": {Name: "api", FullName: "api@internal", Provider: "internal", Status: "enabled", Raw: map[string]any{"status": "enabled"}},
		"whoami@docker": {Name: "whoami", FullName: "whoami@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"loadBalancer": map[string]any{
			"servers":        []any{map[string]any{"url": "http://172.18.0.3:80"}, map[string]any{"url": "http://172.18.0.4:80"}},
			"passHostHeader": true,
			"healthCheck":    map[string]any{"path": "/health", "interval": "10s"},
		}}},
		"legacy@file": {Name: "legacy", FullName: "legacy@file", Provider: "file", Status: "error", ErrorMsg: "no servers", Raw: map[string]any{}},
	}}
}

type fixture struct {
	store   db.Store
	applier *enginetest.DirectApplier
	m       ServiceManager
}

func newFixture(t *testing.T, rt runtime) *fixture {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	database, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	s := db.NewStore(database, make([]byte, 32))
	a := &enginetest.DirectApplier{Store: s}
	return &fixture{store: s, applier: a, m: NewServiceManager(s, rt, a)}
}

var ctx = context.Background()

func servers(urls ...string) models.ServiceConfig {
	cfg := models.ServiceConfig{}
	for _, u := range urls {
		cfg.Servers = append(cfg.Servers, models.ServerEndpoint{URL: u})
	}
	return cfg
}

func (f *fixture) upsert(t *testing.T, name string, cfg models.ServiceConfig) *models.WriteResult[models.ServiceConfig] {
	t.Helper()
	res, e := f.m.UpsertService(ctx, name, cfg, models.WriteOptions{})
	if e != nil {
		t.Fatal(e)
	}
	return res
}

func (f *fixture) router(t *testing.T, name, service string) {
	t.Helper()
	raw := []byte(`{"rule":"Host(` + "`" + name + `.example` + "`" + `)","service":"` + service + `"}`)
	if _, e := f.store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: raw}}}}); e != nil {
		t.Fatal(e)
	}
}

/* ---------------------------------------------------------------- create / edit */

func TestCreateServiceIsOneDynamicUpsert(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res := f.upsert(t, "app", servers("http://10.0.0.1:8080", "h2c://grpc:50051"))
	if len(f.applier.Applied) != 1 {
		t.Fatal("one create must be one operation")
	}
	up := f.applier.Applied[0].Upserts
	if len(up) != 1 || up[0].Kind != models.KindService || up[0].Scope != models.ScopeDynamic || up[0].ID != "service:app" {
		t.Fatal(up)
	}
	if res.Resource == nil || res.Resource.Name != "app" || res.Resource.Origin != "managed" || len(res.Resource.Config.Servers) != 2 || res.Resource.Version != 1 {
		t.Fatalf("%+v", res.Resource)
	}
}

func TestEditBumpsTheVersionAndReplacesTheDefinition(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a:1", "http://b:2"))
	res := f.upsert(t, "app", servers("https://c:3/base"))
	if res.Resource.Version != 2 || len(res.Resource.Config.Servers) != 1 || res.Resource.Config.Servers[0].URL != "https://c:3/base" {
		t.Fatalf("%+v", res.Resource)
	}
}

func TestEveryServerSchemeTraefikAcceptsIsAccepted(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	for i, u := range []string{"http://a", "https://a:8443/p", "h2c://a:50051", "http://[::1]:8080", "http://10.0.0.1:65535"} {
		if _, e := f.m.UpsertService(ctx, "svc"+string(rune('a'+i)), servers(u), models.WriteOptions{}); e != nil {
			t.Fatalf("%s refused: %v", u, e)
		}
	}
}

func TestInvalidServicesAreRefusedBeforeTheEngine(t *testing.T) {
	cases := []struct {
		name string
		svc  string
		cfg  models.ServiceConfig
		want error
	}{
		{"uppercase name", "App", servers("http://a"), ErrServiceInvalidName},
		{"qualified name", "app@docker", servers("http://a"), ErrServiceInvalidName},
		{"no servers", "app", servers(), ErrServiceNoServers},
		{"unknown protocol", "app", models.ServiceConfig{Protocol: "sctp", Servers: servers("http://a").Servers}, ErrServiceInvalidProtocol},
		{"http server given an address", "app", models.ServiceConfig{Servers: []models.ServerEndpoint{{Address: "a:1"}}}, ErrServiceInvalidURL},
		{"tcp options on http", "app", models.ServiceConfig{Servers: servers("http://a").Servers, TCP: &models.TCPServiceOptions{TLS: true}}, ErrServiceInvalidOption},
		{"port out of range", "app", servers("http://a:70000"), ErrServiceInvalidURL},
		{"tcp scheme", "app", servers("tcp://a:1"), ErrServiceInvalidURL},
		{"ws scheme", "app", servers("ws://a"), ErrServiceInvalidURL},
		{"no host", "app", servers("http://"), ErrServiceInvalidURL},
		{"credentials", "app", servers("http://u:p@a"), ErrServiceInvalidURL},
		{"query", "app", servers("http://a/?x=1"), ErrServiceInvalidURL},
		{"fragment", "app", servers("http://a/#x"), ErrServiceInvalidURL},
		{"second server bad", "app", servers("http://a", "nope"), ErrServiceInvalidURL},
		{"health path relative", "app", models.ServiceConfig{Servers: servers("http://a").Servers, HealthCheck: &models.ServiceHealth{Path: "health"}}, ErrServiceInvalidURL},
		{"health interval bad", "app", models.ServiceConfig{Servers: servers("http://a").Servers, HealthCheck: &models.ServiceHealth{Path: "/", Interval: "soon"}}, ErrServiceInvalidURL},
		{"health timeout too long", "app", models.ServiceConfig{Servers: servers("http://a").Servers, HealthCheck: &models.ServiceHealth{Path: "/", Timeout: "2h"}}, ErrServiceInvalidURL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			if _, e := f.m.UpsertService(ctx, c.svc, c.cfg, models.WriteOptions{}); !errors.Is(e, c.want) {
				t.Fatalf("got %v, want %v", e, c.want)
			}
			if len(f.applier.Applied) != 0 {
				t.Fatal("an invalid service reached the engine")
			}
		})
	}
}

func TestHealthCheckPathDefaultsToRoot(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	cfg := servers("http://a")
	cfg.HealthCheck = &models.ServiceHealth{Interval: "10s"}
	res := f.upsert(t, "app", cfg)
	if res.Resource.Config.HealthCheck == nil || res.Resource.Config.HealthCheck.Path != "/" {
		t.Fatalf("%+v", res.Resource.Config.HealthCheck)
	}
}

func TestUsageSentByAClientIsNeverStored(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	cfg := servers("http://a")
	cfg.UsedBy = &models.ServiceUsage{Routers: []models.RouterRef{{Name: "forged"}}}
	f.upsert(t, "app", cfg)
	row, _ := f.store.GetItem(ctx, models.KindService, "app")
	if strings.Contains(string(row.Payload), "used_by") || strings.Contains(string(row.Payload), "forged") {
		t.Fatalf("usage stored: %s", row.Payload)
	}
}

func TestServiceDryRunAppliesNothing(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res, e := f.m.UpsertService(ctx, "app", servers("http://a"), models.WriteOptions{DryRun: true})
	if e != nil || res.Preview == nil {
		t.Fatalf("%+v %v", res, e)
	}
	if len(f.applier.Applied) != 0 {
		t.Fatal("dry run applied")
	}
	if row, _ := f.store.GetItem(ctx, models.KindService, "app"); row != nil {
		t.Fatal("dry run stored")
	}
}

func TestServiceEngineFailureIsReturnedAndNothingIsStored(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.applier.Err = errors.New("traefik rejected it")
	if _, e := f.m.UpsertService(ctx, "app", servers("http://a"), models.WriteOptions{}); e == nil {
		t.Fatal("failure swallowed")
	}
	if row, _ := f.store.GetItem(ctx, models.KindService, "app"); row != nil {
		t.Fatal("failed write stored")
	}
}

/* ---------------------------------------------------------------- read */

func TestListMergesManagedAndExternalServices(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a"))
	all, e := f.m.ListServices(ctx)
	if e != nil {
		t.Fatal(e)
	}
	byName := map[string]models.UIResource[models.ServiceConfig]{}
	for _, s := range all {
		if _, dup := byName[s.Name]; dup {
			t.Fatalf("duplicate %s", s.Name)
		}
		byName[s.Name] = s
	}
	if len(all) != 4 {
		t.Fatalf("want app + 3 external, got %d", len(all))
	}
	if a := byName["app"]; a.Origin != "managed" || a.ReadOnly {
		t.Fatalf("%+v", a)
	}
	w := byName["whoami@docker"]
	if w.Origin != "external" || !w.ReadOnly || w.Provider != "docker" || len(w.Config.Servers) != 2 {
		t.Fatalf("%+v", w)
	}
	if w.Config.PassHostHeader == nil || !*w.Config.PassHostHeader || w.Config.HealthCheck == nil || w.Config.HealthCheck.Path != "/health" || w.Config.HealthCheck.Interval != "10s" {
		t.Fatalf("external definition not read: %+v", w.Config)
	}
	if l := byName["legacy@file"]; l.LiveHealth != "error" || l.LiveErrorMsg != "no servers" {
		t.Fatalf("%+v", l)
	}
}

func TestAManagedServiceIsNotListedTwiceWhenTraefikReportsIt(t *testing.T) {
	rt := dockerRuntime()
	rt.services["app@file"] = status.ExternalItem{Name: "app", FullName: "app@file", Provider: "file", Status: "enabled"}
	f := newFixture(t, rt)
	f.upsert(t, "app", servers("http://a"))
	all, _ := f.m.ListServices(ctx)
	n := 0
	for _, s := range all {
		if strings.TrimSuffix(s.Name, "@file") == "app" {
			n++
			if s.LiveHealth != "loaded" {
				t.Fatalf("runtime health not merged: %+v", s)
			}
		}
	}
	if n != 1 {
		t.Fatalf("listed %d times", n)
	}
}

func TestGetServiceByBareAndQualifiedName(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a"))
	for _, n := range []string{"app", "app@file", "whoami@docker", "api@internal"} {
		if got, e := f.m.GetService(ctx, n, ""); e != nil || got == nil {
			t.Fatalf("%s: %v", n, e)
		}
	}
	if _, e := f.m.GetService(ctx, "ghost", ""); !errors.Is(e, files.ErrSourceFileNotFound) {
		t.Fatalf("ghost: %v", e)
	}
}

func TestServiceListFailsWhenTraefikIsUnreachable(t *testing.T) {
	f := newFixture(t, runtime{err: status.ErrObservationUnavailable})
	if _, e := f.m.ListServices(ctx); !errors.Is(e, status.ErrObservationUnavailable) {
		t.Fatalf("%v", e)
	}
}

func TestDiagnosticsAskForTheRuntimeName(t *testing.T) {
	var asked string
	rt := dockerRuntime()
	rt.diagnose = &asked
	f := newFixture(t, rt)
	for in, want := range map[string]string{"app": "http:app", "app@file": "http:app", "api@internal": "http:api@internal", "whoami@docker": "http:whoami@docker"} {
		if _, e := f.m.DiagnoseService(ctx, in, ""); e != nil || asked != want {
			t.Fatalf("%s: asked %q, %v", in, asked, e)
		}
	}
}

/* ---------------------------------------------------------------- delete */

func TestDeleteAnUnusedService(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a"))
	if _, e := f.m.DeleteService(ctx, "app", models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
	if row, _ := f.store.GetItem(ctx, models.KindService, "app"); row != nil {
		t.Fatal("not deleted")
	}
}

func TestDeleteIsBlockedWhileARouterUsesTheService(t *testing.T) {
	for _, ref := range []string{"app", "app@file"} {
		t.Run(ref, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, "app", servers("http://a"))
			f.router(t, "shop", ref)
			before := len(f.applier.Applied)
			if _, e := f.m.DeleteService(ctx, "app", models.WriteOptions{}); !errors.Is(e, ErrServiceInUse) || !strings.Contains(e.Error(), "shop") {
				t.Fatalf("%v", e)
			}
			if len(f.applier.Applied) != before {
				t.Fatal("blocked delete reached the engine")
			}
		})
	}
}

func TestAnotherProvidersServiceOfTheSameNameDoesNotBlockDelete(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a"))
	f.router(t, "shop", "app@docker")
	if _, e := f.m.DeleteService(ctx, "app", models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
}

func TestServiceDeleteDryRunRemovesNothing(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "app", servers("http://a"))
	if res, e := f.m.DeleteService(ctx, "app", models.WriteOptions{DryRun: true}); e != nil || res.Preview == nil {
		t.Fatalf("%+v %v", res, e)
	}
	if row, _ := f.store.GetItem(ctx, models.KindService, "app"); row == nil {
		t.Fatal("dry run deleted")
	}
}

func TestDeleteOfAServiceTheConsoleDoesNotManage(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	for name, want := range map[string]error{
		"whoami@docker": files.ErrCannotModifyReadOnly, // another provider
		"api@internal":  files.ErrCannotModifyReadOnly,
		"legacy":        files.ErrCannotModifyReadOnly, // hand-written file service
		"ghost":         files.ErrSourceFileNotFound,
	} {
		if _, e := f.m.DeleteService(ctx, name, models.WriteOptions{}); !errors.Is(e, want) {
			t.Fatalf("%s: got %v, want %v", name, e, want)
		}
	}
	if len(f.applier.Applied) != 0 {
		t.Fatal("reached the engine")
	}
}

/* ---------------------------------------------------------------- usage */

func resource(name, origin, provider string) models.UIResource[models.ServiceConfig] {
	return models.UIResource[models.ServiceConfig]{Name: name, Origin: origin, Provider: provider}
}

func route(name, provider, service string) models.UIResource[models.RouterConfig] {
	return models.UIResource[models.RouterConfig]{Name: name, Provider: provider, ReadOnly: provider != "file", Config: models.RouterConfig{Service: service, Rule: "Host(`x`)", EntryPoints: []string{"web"}}}
}

func names(u *models.ServiceUsage) []string {
	out := []string{}
	for _, r := range u.Routers {
		out = append(out, r.Name)
	}
	return out
}

func TestUsageResolvesReferencesTheWayTraefikDoes(t *testing.T) {
	routers := []models.UIResource[models.RouterConfig]{
		route("b-shop", "file", "app"),       // bare → app@file
		route("a-admin", "file", "app@file"), // explicit
		route("c-docker", "docker", "app"),   // bare from docker → app@docker, not ours
		route("d-hand@file", "file", "app"),  // unmanaged file router → app@file
		route("e-dash@internal", "internal", "api@internal"),
		route("f-whoami@docker", "docker", "whoami"), // bare from docker → whoami@docker
		route("g-empty", "file", ""),
	}
	cases := []struct {
		svc  models.UIResource[models.ServiceConfig]
		want []string
	}{
		{resource("app", "managed", "file"), []string{"a-admin", "b-shop", "d-hand@file"}},
		{resource("api@internal", "external", "internal"), []string{"e-dash@internal"}},
		{resource("whoami@docker", "external", "docker"), []string{"f-whoami@docker"}},
		{resource("app@docker", "external", "docker"), []string{"c-docker"}},
		{resource("unused", "managed", "file"), []string{}},
	}
	for _, c := range cases {
		got := names(Usage(c.svc, routers))
		b, _ := json.Marshal(got)
		w, _ := json.Marshal(c.want)
		if string(b) != string(w) {
			t.Fatalf("%s: got %s, want %s", c.svc.Name, b, w)
		}
	}
}

func TestUsageCarriesOnlyWhatTheDialogShows(t *testing.T) {
	u := Usage(resource("app", "managed", "file"), []models.UIResource[models.RouterConfig]{route("shop", "file", "app")})
	b, _ := json.Marshal(u)
	if string(b) != `{"routers":[{"name":"shop","rule":"Host(`+"`x`"+`)","entry_points":["web"],"service":"app","tls":false,"read_only":false}]}` {
		t.Fatal(string(b))
	}
}

/* ---------------------------------------------------------------- tcp / udp */

func tcpService(list ...string) models.ServiceConfig {
	cfg := models.ServiceConfig{Protocol: "tcp"}
	for _, a := range list {
		cfg.Servers = append(cfg.Servers, models.ServerEndpoint{Address: a})
	}
	return cfg
}

func udpService(list ...string) models.ServiceConfig {
	cfg := tcpService(list...)
	cfg.Protocol = "udp"
	return cfg
}

func transportRuntime() runtime {
	rt := dockerRuntime()
	rt.tcp = status.RuntimeResources{
		Services: map[string]status.ExternalItem{
			"pg@docker": {Name: "pg", FullName: "pg@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"loadBalancer": map[string]any{
				"servers":     []any{map[string]any{"address": "172.18.0.9:5432", "tls": true}},
				"healthCheck": map[string]any{"interval": "30s", "port": float64(8008)},
			}}},
		},
		Routers: map[string]status.ExternalItem{
			"pg@docker":  {Name: "pg", FullName: "pg@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": "db@file", "entryPoints": []any{"postgres"}}},
			"own@docker": {Name: "own", FullName: "own@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": "pg"}},
		},
	}
	rt.udp = status.RuntimeResources{
		Services: map[string]status.ExternalItem{"dns@docker": {Name: "dns", FullName: "dns@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"loadBalancer": map[string]any{"servers": []any{map[string]any{"address": "172.18.0.53:53"}}}}}},
		Routers:  map[string]status.ExternalItem{"dns@docker": {Name: "dns", FullName: "dns@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"service": "resolver@file", "entryPoints": []any{"dns"}}}},
	}
	return rt
}

func TestCreateTCPAndUDPServices(t *testing.T) {
	f := newFixture(t, transportRuntime())
	tcp := tcpService("10.0.0.5:5432", "[fd00::5]:5432", " db.internal:5432 ")
	tcp.InsecureSkipVerify = true
	tcp.TCP = &models.TCPServiceOptions{TLS: true, ServerName: "db.example", ProxyProtocol: 2, DialTimeout: "5s", TerminationDelay: "100ms", HealthCheck: &models.TCPHealthCheck{Interval: "10s", Timeout: "2s", Send: "PING", Expect: "PONG", Port: 9000}}
	res := f.upsert(t, "db", tcp)
	got := res.Resource.Config
	if got.Protocol != "tcp" || len(got.Servers) != 3 || got.Servers[2].Address != "db.internal:5432" || got.Servers[0].URL != "" || got.TCP == nil || got.TCP.ProxyProtocol != 2 || !got.InsecureSkipVerify {
		t.Fatalf("%+v", got)
	}
	res = f.upsert(t, "dns", udpService("10.0.0.53:53"))
	if res.Resource.Config.Protocol != "udp" || res.Resource.Config.Servers[0].Address != "10.0.0.53:53" {
		t.Fatalf("%+v", res.Resource.Config)
	}
}

func TestAnHTTPServiceIsStoredWithItsProtocol(t *testing.T) {
	f := newFixture(t, transportRuntime())
	if p := f.upsert(t, "web", servers("http://a")).Resource.Config.Protocol; p != "http" {
		t.Fatal(p)
	}
}

func TestEmptyTCPOptionsAreNotStored(t *testing.T) {
	f := newFixture(t, transportRuntime())
	cfg := tcpService("db:5432")
	cfg.TCP = &models.TCPServiceOptions{}
	if got := f.upsert(t, "db", cfg).Resource.Config.TCP; got != nil {
		t.Fatalf("%+v", got)
	}
}

func TestInvalidTCPAndUDPServicesAreRefused(t *testing.T) {
	with := func(cfg models.ServiceConfig, edit func(*models.ServiceConfig)) models.ServiceConfig {
		edit(&cfg)
		return cfg
	}
	opts := func(o models.TCPServiceOptions) models.ServiceConfig {
		c := tcpService("db:5432")
		c.TCP = &o
		return c
	}
	cases := map[string]struct {
		cfg  models.ServiceConfig
		want error
	}{
		"tcp without port":        {tcpService("db"), ErrServiceInvalidAddress},
		"tcp port zero":           {tcpService("db:0"), ErrServiceInvalidAddress},
		"tcp port too big":        {tcpService("db:65536"), ErrServiceInvalidAddress},
		"tcp port with letters":   {tcpService("db:54a"), ErrServiceInvalidAddress},
		"tcp url instead":         {tcpService("tcp://db:5432"), ErrServiceInvalidAddress},
		"tcp with path":           {tcpService("db:5432/x"), ErrServiceInvalidAddress},
		"tcp ipv6 unbracketed":    {tcpService("fd00::5:5432"), ErrServiceInvalidAddress},
		"tcp duplicate":           {tcpService("db:5432", "db:5432"), ErrServiceInvalidAddress},
		"tcp given a url":         {with(tcpService(), func(c *models.ServiceConfig) { c.Servers = []models.ServerEndpoint{{URL: "http://a"}} }), ErrServiceInvalidAddress},
		"tcp pass host header":    {with(tcpService("db:5432"), func(c *models.ServiceConfig) { v := true; c.PassHostHeader = &v }), ErrServiceInvalidOption},
		"tcp http health check":   {with(tcpService("db:5432"), func(c *models.ServiceConfig) { c.HealthCheck = &models.ServiceHealth{Path: "/"} }), ErrServiceInvalidOption},
		"skip verify without tls": {with(tcpService("db:5432"), func(c *models.ServiceConfig) { c.InsecureSkipVerify = true }), ErrServiceInvalidOption},
		"sni without tls":         {opts(models.TCPServiceOptions{ServerName: "db.example"}), ErrServiceInvalidOption},
		"bad sni":                 {opts(models.TCPServiceOptions{TLS: true, ServerName: "db example"}), ErrServiceInvalidOption},
		"proxy protocol 3":        {opts(models.TCPServiceOptions{ProxyProtocol: 3}), ErrServiceInvalidOption},
		"bad dial timeout":        {opts(models.TCPServiceOptions{DialTimeout: "soon"}), ErrServiceInvalidOption},
		"negative delay":          {opts(models.TCPServiceOptions{TerminationDelay: "-1s"}), ErrServiceInvalidOption},
		"health interval > 1h":    {opts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Interval: "2h"}}), ErrServiceInvalidOption},
		"health port":             {opts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Port: 70000}}), ErrServiceInvalidOption},
		"expect without send":     {opts(models.TCPServiceOptions{HealthCheck: &models.TCPHealthCheck{Expect: "OK"}}), ErrServiceInvalidOption},
		"udp tcp options":         {with(udpService("dns:53"), func(c *models.ServiceConfig) { c.TCP = &models.TCPServiceOptions{TLS: true} }), ErrServiceInvalidOption},
		"udp skip verify":         {with(udpService("dns:53"), func(c *models.ServiceConfig) { c.InsecureSkipVerify = true }), ErrServiceInvalidOption},
		"udp without port":        {udpService("dns"), ErrServiceInvalidAddress},
		"udp no servers":          {udpService(), ErrServiceNoServers},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, transportRuntime())
			if _, e := f.m.UpsertService(ctx, "svc", c.cfg, models.WriteOptions{}); !errors.Is(e, c.want) {
				t.Fatalf("got %v, want %v", e, c.want)
			}
			if len(f.applier.Applied) != 0 {
				t.Fatal("an invalid service reached the engine")
			}
		})
	}
}

func TestAServiceNeverChangesProtocol(t *testing.T) {
	f := newFixture(t, transportRuntime())
	f.upsert(t, "web", servers("http://a"))
	f.upsert(t, "db", tcpService("db:5432"))
	for name, cfg := range map[string]models.ServiceConfig{"web": tcpService("a:1"), "db": udpService("db:5432")} {
		if _, e := f.m.UpsertService(ctx, name, cfg, models.WriteOptions{}); !errors.Is(e, ErrServiceInvalidProtocol) {
			t.Fatalf("%s: %v", name, e)
		}
	}
	// A service stored before protocols existed is an HTTP service.
	if _, e := f.store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{ID: "service:old", Kind: models.KindService, Name: "old", Scope: models.ScopeDynamic, Payload: []byte(`{"servers":[{"url":"http://a"}]}`)}}}}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.m.UpsertService(ctx, "old", tcpService("a:1"), models.WriteOptions{}); !errors.Is(e, ErrServiceInvalidProtocol) {
		t.Fatal(e)
	}
	if got, _ := f.m.GetService(ctx, "old", "http"); got == nil || got.Config.Protocol != "http" {
		t.Fatal("legacy service not listed as http")
	}
}

func TestListIncludesTCPAndUDPServicesWithTheirProtocol(t *testing.T) {
	f := newFixture(t, transportRuntime())
	f.upsert(t, "db", tcpService("db:5432"))
	f.upsert(t, "resolver", udpService("dns:53"))
	all, e := f.m.ListServices(ctx)
	if e != nil {
		t.Fatal(e)
	}
	by := map[string]models.UIResource[models.ServiceConfig]{}
	for _, s := range all {
		by[s.Config.Protocol+":"+s.Name] = s
	}
	if s := by["tcp:db"]; s.Origin != "managed" || s.Config.Servers[0].Address != "db:5432" {
		t.Fatalf("%+v", s)
	}
	if s := by["udp:resolver"]; s.Origin != "managed" {
		t.Fatalf("%+v", s)
	}
	pg := by["tcp:pg@docker"]
	if pg.Origin != "external" || !pg.ReadOnly || pg.Config.Servers[0].Address != "172.18.0.9:5432" || pg.Config.TCP == nil || !pg.Config.TCP.TLS || pg.Config.TCP.HealthCheck.Port != 8008 {
		t.Fatalf("external tcp definition not read: %+v %+v", pg, pg.Config.TCP)
	}
	if s := by["udp:dns@docker"]; s.Origin != "external" || s.Config.Servers[0].Address != "172.18.0.53:53" {
		t.Fatalf("%+v", s)
	}
	if _, http := by["http:db"]; http {
		t.Fatal("tcp service listed as http")
	}
}

func TestGetServiceNarrowsByProtocol(t *testing.T) {
	rt := transportRuntime()
	rt.services["pg@docker"] = status.ExternalItem{Name: "pg", FullName: "pg@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{}}
	f := newFixture(t, rt)
	if got, e := f.m.GetService(ctx, "pg@docker", "tcp"); e != nil || got.Config.Protocol != "tcp" {
		t.Fatalf("%+v %v", got, e)
	}
	if got, e := f.m.GetService(ctx, "pg@docker", ""); e != nil || got.Config.Protocol != "http" {
		t.Fatalf("http first when unspecified: %+v %v", got, e)
	}
	if _, e := f.m.GetService(ctx, "dns@docker", "tcp"); !errors.Is(e, files.ErrSourceFileNotFound) {
		t.Fatal(e)
	}
}

func TestDiagnosticsUseTheRequestedProtocol(t *testing.T) {
	var asked string
	rt := transportRuntime()
	rt.diagnose = &asked
	f := newFixture(t, rt)
	if _, e := f.m.DiagnoseService(ctx, "db", "tcp"); e != nil || asked != "tcp:db" {
		t.Fatal(asked, e)
	}
	if _, e := f.m.DiagnoseService(ctx, "dns@docker", "udp"); e != nil || asked != "udp:dns@docker" {
		t.Fatal(asked, e)
	}
	if _, e := f.m.DiagnoseService(ctx, "db", "quic"); !errors.Is(e, ErrServiceInvalidProtocol) {
		t.Fatal(e)
	}
}

func TestRuntimeRoutersAreReadPerProtocol(t *testing.T) {
	f := newFixture(t, transportRuntime())
	tcp, e := f.m.RuntimeRouters(ctx, "tcp")
	if e != nil || len(tcp) != 2 || tcp[1].Name != "pg@docker" || tcp[1].Config.Service != "db@file" || tcp[1].Config.EntryPoints[0] != "postgres" || !tcp[1].ReadOnly {
		t.Fatalf("%+v %v", tcp, e)
	}
	udp, e := f.m.RuntimeRouters(ctx, "udp")
	if e != nil || len(udp) != 1 || udp[0].Provider != "docker" {
		t.Fatalf("%+v %v", udp, e)
	}
	if _, e := f.m.RuntimeRouters(ctx, "http"); !errors.Is(e, ErrServiceInvalidProtocol) {
		t.Fatal(e)
	}
}

func TestDeleteIsBlockedByARouterTraefikReports(t *testing.T) {
	f := newFixture(t, transportRuntime())
	f.upsert(t, "db", tcpService("db:5432"))
	f.upsert(t, "resolver", udpService("dns:53"))
	f.upsert(t, "pg", tcpService("pg:5432")) // own@docker says "pg" but means pg@docker
	for name, router := range map[string]string{"db": "pg@docker", "resolver": "dns@docker"} {
		if _, e := f.m.DeleteService(ctx, name, models.WriteOptions{}); !errors.Is(e, ErrServiceInUse) || !strings.Contains(e.Error(), router) {
			t.Fatalf("%s: %v", name, e)
		}
	}
	if _, e := f.m.DeleteService(ctx, "pg", models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
}

func TestADockerHTTPRouterAlsoBlocksDelete(t *testing.T) {
	rt := dockerRuntime()
	rt.httpRouters = map[string]status.ExternalItem{"site@docker": {Name: "site", FullName: "site@docker", Provider: "docker", Raw: map[string]any{"service": "app@file"}}}
	f := newFixture(t, rt)
	f.upsert(t, "app", servers("http://a"))
	if _, e := f.m.DeleteService(ctx, "app", models.WriteOptions{}); !errors.Is(e, ErrServiceInUse) || !strings.Contains(e.Error(), "site@docker") {
		t.Fatal(e)
	}
}

func TestDeleteNeedsTraefikToCheckItsRouters(t *testing.T) {
	f := newFixture(t, transportRuntime())
	f.upsert(t, "db", tcpService("db:5432"))
	f.m = NewServiceManager(f.store, runtime{err: status.ErrObservationUnavailable}, f.applier)
	if _, e := f.m.DeleteService(ctx, "db", models.WriteOptions{}); !errors.Is(e, status.ErrObservationUnavailable) {
		t.Fatal(e)
	}
	if row, _ := f.store.GetItem(ctx, models.KindService, "db"); row == nil {
		t.Fatal("deleted without checking")
	}
}

func TestAHandWrittenTCPServiceIsReadOnly(t *testing.T) {
	rt := transportRuntime()
	rt.tcp.Services["legacy-tcp@file"] = status.ExternalItem{Name: "legacy-tcp", FullName: "legacy-tcp@file", Provider: "file"}
	f := newFixture(t, rt)
	if _, e := f.m.DeleteService(ctx, "legacy-tcp", models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
		t.Fatal(e)
	}
}
