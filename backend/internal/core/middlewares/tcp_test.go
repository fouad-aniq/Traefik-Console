package middlewares

import (
	"context"
	"errors"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// tcpRuntime adds a TCP middleware declared by Docker next to the HTTP ones.
type tcpRuntime struct{ runtime }

func (r tcpRuntime) ObserveRuntime(c context.Context) (*status.RuntimeSnapshot, error) {
	snap, e := r.runtime.ObserveRuntime(c)
	if e != nil {
		return nil, e
	}
	snap.TCP = status.RuntimeResources{Middlewares: map[string]status.ExternalItem{
		"limit@docker": {Name: "limit", FullName: "limit@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"inFlightConn": map[string]any{"amount": 5}}},
		"cap@file":     {Name: "cap", FullName: "cap@file", Provider: "file", Status: "enabled", Raw: map[string]any{"inFlightConn": map[string]any{"amount": 3}}},
	}}
	return snap, nil
}

func tcp(typ string, spec map[string]any) models.MiddlewareConfig {
	return models.MiddlewareConfig{Protocol: "tcp", Type: typ, Spec: spec}
}

func (f *fixture) tcpRouter(t *testing.T, name string, mws ...string) {
	t.Helper()
	cfg := `{"protocol":"tcp","rule":"HostSNI(` + "`*`" + `)","service":"s","middlewares":[`
	for i, m := range mws {
		if i > 0 {
			cfg += ","
		}
		cfg += `"` + m + `"`
	}
	raw := []byte(cfg + `]}`)
	if _, e := f.store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: raw}}}}); e != nil {
		t.Fatal(e)
	}
}

func TestATCPMiddlewareIsStoredWithItsProtocol(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res := f.upsert(t, "office", tcp("ipAllowList", map[string]any{"sourceRange": []any{"10.0.0.0/8", "192.168.1.7"}}))
	if res.Resource.Config.Protocol != "tcp" || res.Resource.Config.Type != "ipAllowList" {
		t.Fatalf("%+v", res.Resource.Config)
	}
	f.upsert(t, "cap", tcp("inFlightConn", map[string]any{"amount": 20}))
	row, _ := f.store.GetItem(ctx, models.KindMiddleware, "cap")
	if string(row.Payload) != `{"protocol":"tcp","type":"inFlightConn","spec":{"amount":20}}` {
		t.Fatal(string(row.Payload))
	}
}

func TestAnHTTPMiddlewarePayloadIsUnchanged(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "api", models.MiddlewareConfig{Protocol: "http", Type: "addPrefix", Spec: map[string]any{"prefix": "/api"}})
	row, _ := f.store.GetItem(ctx, models.KindMiddleware, "api")
	if string(row.Payload) != `{"type":"addPrefix","spec":{"prefix":"/api"}}` {
		t.Fatal(string(row.Payload))
	}
}

func TestTCPMiddlewareRejections(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	cases := map[string]struct {
		in   models.MiddlewareConfig
		want error
	}{
		"udp protocol":           {models.MiddlewareConfig{Protocol: "udp", Type: "inFlightConn", Spec: map[string]any{"amount": 1}}, ErrMiddlewareInvalidProtocol},
		"unknown protocol":       {models.MiddlewareConfig{Protocol: "grpc", Type: "addPrefix", Spec: map[string]any{"prefix": "/x"}}, ErrMiddlewareInvalidProtocol},
		"an HTTP type over TCP":  {tcp("headers", map[string]any{}), ErrMiddlewareInvalidType},
		"a plugin over TCP":      {models.MiddlewareConfig{Protocol: "tcp", Type: "plugin", PluginName: "x", Spec: map[string]any{}}, ErrMiddlewareInvalidType},
		"a TCP type over HTTP":   {models.MiddlewareConfig{Type: "inFlightConn", Spec: map[string]any{"amount": 1}}, ErrMiddlewareInvalidType},
		"no connection limit":    {tcp("inFlightConn", map[string]any{}), ErrMiddlewareInvalidType},
		"zero connections":       {tcp("inFlightConn", map[string]any{"amount": 0}), ErrMiddlewareInvalidType},
		"negative connections":   {tcp("inFlightConn", map[string]any{"amount": -2}), ErrMiddlewareInvalidType},
		"fractional connections": {tcp("inFlightConn", map[string]any{"amount": 1.5}), ErrMiddlewareInvalidType},
		"text connections":       {tcp("inFlightConn", map[string]any{"amount": "ten"}), ErrMiddlewareInvalidType},
		"no source range":        {tcp("ipAllowList", map[string]any{}), ErrMiddlewareInvalidType},
		"empty source range":     {tcp("ipAllowList", map[string]any{"sourceRange": []any{}}), ErrMiddlewareInvalidType},
		"HTTP-only ip strategy":  {tcp("ipAllowList", map[string]any{"sourceRange": []any{"1.2.3.4"}, "ipStrategy": map[string]any{"depth": 1}}), ErrMiddlewareInvalidType},
		"secrets on a TCP one":   {models.MiddlewareConfig{Protocol: "tcp", Type: "inFlightConn", Spec: map[string]any{"amount": 1}, SecretRefs: map[string]string{"a": "b"}}, ErrMiddlewareInvalidType},
		"bad name":               {models.MiddlewareConfig{Protocol: "tcp", Type: "inFlightConn", Spec: map[string]any{"amount": 1}}, nil},
	}
	for name, c := range cases {
		mwName := "m"
		if name == "bad name" {
			mwName, c.want = "Bad Name", ErrMiddlewareInvalidName
		}
		if _, e := f.m.UpsertMiddleware(ctx, mwName, c.in, models.WriteOptions{}); !errors.Is(e, c.want) {
			t.Errorf("%s: got %v, want %v", name, e, c.want)
		}
	}
	if items, _ := f.store.ListItemsByKind(ctx, models.KindMiddleware); len(items) != 0 {
		t.Fatal("a refused middleware was stored", items)
	}
}

func TestAMiddlewareKeepsItsProtocol(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "cap", tcp("inFlightConn", map[string]any{"amount": 2}))
	if _, e := f.m.UpsertMiddleware(ctx, "cap", prefix("/x"), models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInvalidProtocol) {
		t.Fatalf("tcp to http: %v", e)
	}
	f.upsert(t, "api", prefix("/api"))
	if _, e := f.m.UpsertMiddleware(ctx, "api", tcp("inFlightConn", map[string]any{"amount": 2}), models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInvalidProtocol) {
		t.Fatalf("http to tcp: %v", e)
	}
	// Same protocol is a plain edit.
	if res := f.upsert(t, "cap", tcp("inFlightConn", map[string]any{"amount": 9})); res.Resource.Version != 2 {
		t.Fatal(res.Resource.Version)
	}
}

func TestTheListCarriesTheProtocolAndTraefiksTCPMiddlewares(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.m = NewMiddlewareManager(f.store, nil, tcpRuntime{dockerRuntime()}, f.applier)
	f.upsert(t, "office", tcp("ipAllowList", map[string]any{"sourceRange": []any{"10.0.0.0/8"}}))
	f.upsert(t, "api", prefix("/api"))
	items, e := f.m.ListMiddlewares(ctx)
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]models.UIResource[models.MiddlewareConfig]{}
	for _, it := range items {
		got[it.ID] = it
	}
	if got["middleware:office"].Config.Protocol != "tcp" || got["middleware:api"].Config.Protocol != "http" {
		t.Fatalf("%+v", got)
	}
	ext, ok := got["external:tcp-middleware:limit@docker"]
	if !ok || ext.Config.Protocol != "tcp" || !ext.ReadOnly || ext.Origin != "external" {
		t.Fatalf("Traefik's TCP middleware missing: %+v", items)
	}
	if _, ok := got["external:middleware:auth@docker"]; !ok {
		t.Fatal("HTTP externals lost")
	}
}

func TestDeletingATCPMiddleware(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "cap", tcp("inFlightConn", map[string]any{"amount": 2}))
	f.tcpRouter(t, "pg", "cap@file")
	if _, e := f.m.DeleteMiddleware(ctx, "cap", models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInUse) {
		t.Fatalf("used by a TCP router: %v", e)
	}
	f.tcpRouter(t, "pg")
	// An HTTP router naming a middleware "cap" uses an HTTP one, not this TCP one.
	f.router(t, "web", "cap")
	if _, e := f.m.DeleteMiddleware(ctx, "cap", models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
}

func TestAnHTTPMiddlewareIsNotHeldByATCPRouterOfTheSameName(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "api", prefix("/api"))
	f.tcpRouter(t, "pg", "api")
	if _, e := f.m.DeleteMiddleware(ctx, "api", models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
}

func TestAChainCannotHoldATCPMiddleware(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "cap", tcp("inFlightConn", map[string]any{"amount": 2}))
	f.upsert(t, "api", prefix("/api"))
	for _, member := range []string{"cap", "cap@file"} {
		if _, e := f.m.UpsertChain(ctx, "guard", models.ChainConfig{Middlewares: []string{"api", member}}, models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInvalidType) {
			t.Fatalf("%s: %v", member, e)
		}
	}
	f.chain(t, "guard", "api")
}

func TestUsageOfATCPMiddlewareCountsTCPRoutersOnly(t *testing.T) {
	routers := []models.UIResource[models.RouterConfig]{
		{Name: "pg", Config: models.RouterConfig{Protocol: "tcp", Middlewares: []string{"cap"}}},
		{Name: "web", Config: models.RouterConfig{Middlewares: []string{"cap"}}},
	}
	chains := []models.UIResource[models.ChainConfig]{{Name: "guard", Config: models.ChainConfig{Middlewares: []string{"cap"}}}}
	u := UsageOf("cap", "tcp", routers, chains)
	if len(u.Routers) != 1 || u.Routers[0].Name != "pg" || len(u.Chains) != 0 {
		t.Fatalf("%+v", u)
	}
	h := Usage("cap", routers, chains)
	if len(h.Routers) != 1 || h.Routers[0].Name != "web" || len(h.Chains) != 1 {
		t.Fatalf("%+v", h)
	}
}

func TestDeletingTraefiksOwnTCPMiddlewareIsReadOnly(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.m = NewMiddlewareManager(f.store, nil, tcpRuntime{dockerRuntime()}, f.applier)
	if _, e := f.m.DeleteMiddleware(ctx, "cap", models.WriteOptions{}); e == nil || errors.Is(e, ErrMiddlewareInUse) {
		t.Fatalf("an unmanaged TCP file middleware: %v", e)
	}
}
