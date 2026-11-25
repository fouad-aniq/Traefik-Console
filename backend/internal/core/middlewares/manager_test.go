package middlewares

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// runtime is Traefik's observed middlewares: a docker middleware and a docker chain the console
// does not manage. err simulates Traefik being unreachable.
type runtime struct {
	middlewares map[string]status.ExternalItem
	err         error
}

func (r runtime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &status.RuntimeSnapshot{HTTP: status.RuntimeResources{Middlewares: r.middlewares}}, nil
}

func dockerRuntime() runtime {
	return runtime{middlewares: map[string]status.ExternalItem{
		"auth@docker":  {Name: "auth", FullName: "auth@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"basicAuth": map[string]any{"users": []any{"x"}}, "status": "enabled"}},
		"guard@docker": {Name: "guard", FullName: "guard@docker", Provider: "docker", Status: "enabled", Raw: map[string]any{"chain": map[string]any{"middlewares": []any{"auth@docker"}}}},
		"strip@file":   {Name: "strip", FullName: "strip@file", Provider: "file", Status: "enabled", Raw: map[string]any{"stripPrefix": map[string]any{}}},
		"broken@file":  {Name: "broken", FullName: "broken@file", Provider: "file", Status: "error", ErrorMsg: "bad regex", Raw: map[string]any{}},
	}}
}

type fixture struct {
	store   db.Store
	applier *enginetest.DirectApplier
	m       MiddlewareManager
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
	key := make([]byte, 32)
	s := db.NewStore(database, key)
	sm, e := secrets.New(filepath.Join(root, "secrets"), filepath.Join(root, "secrets"), key, s)
	if e != nil {
		t.Fatal(e)
	}
	a := &enginetest.DirectApplier{Store: s}
	return &fixture{store: s, applier: a, m: NewMiddlewareManager(s, sm, rt, a)}
}

var ctx = context.Background()

func (f *fixture) upsert(t *testing.T, name string, cfg models.MiddlewareConfig) *models.WriteResult[models.MiddlewareConfig] {
	t.Helper()
	res, e := f.m.UpsertMiddleware(ctx, name, cfg, models.WriteOptions{})
	if e != nil {
		t.Fatal(e)
	}
	return res
}

func (f *fixture) chain(t *testing.T, name string, members ...string) {
	t.Helper()
	if _, e := f.m.UpsertChain(ctx, name, models.ChainConfig{Middlewares: members}, models.WriteOptions{}); e != nil {
		t.Fatal(e)
	}
}

func (f *fixture) router(t *testing.T, name string, mws ...string) {
	t.Helper()
	raw := []byte(`{"rule":"Host(` + "`" + name + `.example` + "`" + `)","service":"s","middlewares":["` + strings.Join(mws, `","`) + `"]}`)
	if _, e := f.store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: raw}}}}); e != nil {
		t.Fatal(e)
	}
}

func prefix(p string) models.MiddlewareConfig {
	return models.MiddlewareConfig{Type: "addPrefix", Spec: map[string]any{"prefix": p}}
}

/* ---------------------------------------------------------------- create / edit */

func TestCreateMiddlewareIsOneDynamicUpsertReturningTheManagedResource(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res := f.upsert(t, "api-prefix", prefix("/api"))
	if len(f.applier.Applied) != 1 {
		t.Fatal("one create must be one operation")
	}
	up := f.applier.Applied[0].Upserts
	if len(up) != 1 || up[0].Kind != models.KindMiddleware || up[0].Scope != models.ScopeDynamic || up[0].ID != "middleware:api-prefix" {
		t.Fatal(up)
	}
	if res.Resource == nil || res.Resource.Origin != "managed" || res.Resource.ReadOnly || res.Resource.Config.Spec["prefix"] != "/api" {
		t.Fatal(res.Resource)
	}
}

func TestEditMiddlewareReplacesTheSpecAndBumpsTheVersion(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	first := f.upsert(t, "p", prefix("/a"))
	second := f.upsert(t, "p", prefix("/b"))
	if second.Resource.Version != first.Resource.Version+1 || second.Resource.Config.Spec["prefix"] != "/b" {
		t.Fatal(first.Resource.Version, second.Resource)
	}
}

func TestCreateMiddlewareRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		name string
		cfg  models.MiddlewareConfig
		want error
	}{
		"upper-case name":          {"Bad", prefix("/a"), ErrMiddlewareInvalidName},
		"name with @provider":      {"auth@docker", prefix("/a"), ErrMiddlewareInvalidName},
		"empty type":               {"x", models.MiddlewareConfig{}, ErrMiddlewareInvalidType},
		"unknown type":             {"x", models.MiddlewareConfig{Type: "teleport"}, ErrMiddlewareInvalidType},
		"plugin without name":      {"x", models.MiddlewareConfig{Type: "plugin"}, ErrMiddlewareInvalidType},
		"missing required field":   {"x", models.MiddlewareConfig{Type: "addPrefix", Spec: map[string]any{}}, ErrMiddlewareInvalidType},
		"basic auth without users": {"x", models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{"realm": "r"}}, ErrMiddlewareInvalidType},
		"stored marker on create":  {"x", models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{"users_htpasswd": "[redacted]"}}, ErrMiddlewareInvalidType},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			if _, e := f.m.UpsertMiddleware(ctx, tc.name, tc.cfg, models.WriteOptions{}); !errors.Is(e, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, e)
			}
			if len(f.applier.Applied) != 0 {
				t.Fatal("a rejected middleware reached the engine")
			}
		})
	}
}

func TestPluginMiddlewareKeepsItsOpaqueSettings(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res := f.upsert(t, "bouncer", models.MiddlewareConfig{Type: "plugin", PluginName: "crowdsec", Spec: map[string]any{"enabled": true, "logLevel": "INFO"}})
	if res.Resource.Config.PluginName != "crowdsec" || res.Resource.Config.Spec["logLevel"] != "INFO" {
		t.Fatal(res.Resource.Config)
	}
}

func TestDryRunAppliesNothing(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res, e := f.m.UpsertMiddleware(ctx, "p", prefix("/a"), models.WriteOptions{DryRun: true})
	if e != nil || res.Preview == nil || len(f.applier.Applied) != 0 {
		t.Fatal(res, e)
	}
	if row, _ := f.store.GetItem(ctx, models.KindMiddleware, "p"); row != nil {
		t.Fatal("dry run stored the middleware")
	}
}

func TestEngineFailureIsReturnedAndNothingIsStored(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.applier.Err = engine.ErrRollbackTriggered
	if _, e := f.m.UpsertMiddleware(ctx, "p", prefix("/a"), models.WriteOptions{}); !errors.Is(e, engine.ErrRollbackTriggered) {
		t.Fatal(e)
	}
	if row, _ := f.store.GetItem(ctx, models.KindMiddleware, "p"); row != nil {
		t.Fatal("failed write left a middleware behind")
	}
}

/* ---------------------------------------------------------------- basic auth secrets */

func TestBasicAuthEditKeepsStoredUsersWithTheMarker(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: credentialsSpec([2]string{"admin", "pw"})})
	stored, _ := f.store.GetItem(ctx, models.KindMiddleware, "auth")
	res := f.upsert(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{"users_htpasswd": "[redacted]", "realm": "edited"}})
	if len(f.applier.Applied[1].Secrets) != 0 {
		t.Fatal("keeping the users must not write a new secret")
	}
	after, _ := f.store.GetItem(ctx, models.KindMiddleware, "auth")
	if !strings.Contains(string(after.Payload), "usersFile") || !strings.Contains(string(after.Payload), "edited") || strings.Contains(string(after.Payload), "users_htpasswd") {
		t.Fatal("stored users not kept", string(stored.Payload), string(after.Payload))
	}
	if res.Resource == nil {
		t.Fatal("no resource returned")
	}
}

func TestBasicAuthReplacingUsersWritesANewSecret(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: credentialsSpec([2]string{"admin", "pw"})})
	f.upsert(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: credentialsSpec([2]string{"other", "pw2"})})
	if len(f.applier.Applied[1].Secrets) != 1 || f.applier.Applied[1].Secrets[0].Name != "basicauth_auth" {
		t.Fatal("replacing users must replace the users file", f.applier.Applied[1].Secrets)
	}
}

/* ---------------------------------------------------------------- list / get */

func TestListMergesManagedAndExternalWithoutChainsOrDuplicates(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "strip", prefix("/s")) // also observed as strip@file: listed once, as managed
	all, e := f.m.ListMiddlewares(ctx)
	if e != nil {
		t.Fatal(e)
	}
	by := map[string]models.UIResource[models.MiddlewareConfig]{}
	for _, m := range all {
		by[m.Name] = m
	}
	if len(all) != 3 || by["strip"].Origin != "managed" || by["strip"].LiveHealth != "loaded" {
		t.Fatal("managed entry missing or duplicated", all)
	}
	if ext := by["auth@docker"]; !ext.ReadOnly || ext.Origin != "external" || ext.Provider != "docker" || ext.Config.Type != "external" {
		t.Fatal("external middleware", ext)
	}
	if _, isChain := by["guard@docker"]; isChain {
		t.Fatal("an external chain was listed as a middleware")
	}
	if br := by["broken@file"]; br.LiveHealth != "error" || br.LiveErrorMsg != "bad regex" {
		t.Fatal("runtime error not reported", br)
	}
}

func TestListChainsMergesManagedAndExternal(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "a", prefix("/a"))
	f.chain(t, "web", "a", "auth@docker")
	all, e := f.m.ListChains(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(all) != 2 {
		t.Fatal(all)
	}
	for _, c := range all {
		switch c.Name {
		case "web":
			if c.ReadOnly || strings.Join(c.Config.Middlewares, ",") != "a,auth@docker" {
				t.Fatal(c)
			}
		case "guard@docker":
			if !c.ReadOnly || c.Config.Middlewares[0] != "auth@docker" {
				t.Fatal(c)
			}
		default:
			t.Fatal("unexpected chain", c.Name)
		}
	}
}

func TestGetFindsBareAndFileQualifiedNames(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	f.upsert(t, "a", prefix("/a"))
	for _, name := range []string{"a", "auth@docker"} {
		if _, e := f.m.GetMiddleware(ctx, name); e != nil {
			t.Fatal(name, e)
		}
	}
	if _, e := f.m.GetMiddleware(ctx, "nope"); !errors.Is(e, files.ErrSourceFileNotFound) {
		t.Fatal(e)
	}
}

func TestListsFailWhenTraefikIsUnreachable(t *testing.T) {
	f := newFixture(t, runtime{err: status.ErrObservationUnavailable})
	if _, e := f.m.ListMiddlewares(ctx); !errors.Is(e, status.ErrObservationUnavailable) {
		t.Fatal(e)
	}
	if _, e := f.m.ListChains(ctx); !errors.Is(e, status.ErrObservationUnavailable) {
		t.Fatal(e)
	}
}

/* ---------------------------------------------------------------- chains */

func TestChainRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		name    string
		members []string
		want    error
	}{
		"invalid name":   {"Web", []string{"a"}, ErrMiddlewareInvalidName},
		"no members":     {"web", nil, ErrChainEmpty},
		"self reference": {"web", []string{"a", "web"}, ErrChainSelfReference},
		"self via @file": {"web", []string{"web@file"}, ErrChainSelfReference},
		"tcp middleware": {"web", []string{"a@tcp"}, ErrMiddlewareInvalidType},
		"udp middleware": {"web", []string{"a@udp"}, ErrMiddlewareInvalidType},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			if _, e := f.m.UpsertChain(ctx, tc.name, models.ChainConfig{Middlewares: tc.members}, models.WriteOptions{}); !errors.Is(e, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, e)
			}
			if len(f.applier.Applied) != 0 {
				t.Fatal("a rejected chain reached the engine")
			}
		})
	}
}

func TestChainKeepsMemberOrderAndNeverStoresUsage(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	res, e := f.m.UpsertChain(ctx, "web", models.ChainConfig{Middlewares: []string{"c", "a", "b@docker"}, UsedBy: &models.MiddlewareUsage{Routers: []models.RouterRef{{Name: "x"}}}}, models.WriteOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(res.Resource.Config.Middlewares, ",") != "c,a,b@docker" {
		t.Fatal("order changed", res.Resource.Config.Middlewares)
	}
	row, _ := f.store.GetItem(ctx, models.KindChain, "web")
	if strings.Contains(string(row.Payload), "used_by") {
		t.Fatal("usage stored", string(row.Payload))
	}
}

func TestMiddlewareNeverStoresUsage(t *testing.T) {
	f := newFixture(t, dockerRuntime())
	cfg := prefix("/a")
	cfg.UsedBy = &models.MiddlewareUsage{Routers: []models.RouterRef{{Name: "x"}}}
	f.upsert(t, "p", cfg)
	row, _ := f.store.GetItem(ctx, models.KindMiddleware, "p")
	if strings.Contains(string(row.Payload), "used_by") {
		t.Fatal("usage stored", string(row.Payload))
	}
}

/* ---------------------------------------------------------------- delete */

func TestDeleteMiddleware(t *testing.T) {
	t.Run("unused managed middleware is deleted", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.upsert(t, "p", prefix("/a"))
		if _, e := f.m.DeleteMiddleware(ctx, "p", models.WriteOptions{}); e != nil {
			t.Fatal(e)
		}
		if row, _ := f.store.GetItem(ctx, models.KindMiddleware, "p"); row != nil {
			t.Fatal("still stored")
		}
	})
	for name, setup := range map[string]func(*testing.T, *fixture){
		"used by a router":           func(t *testing.T, f *fixture) { f.router(t, "shop", "p") },
		"used by a router via @file": func(t *testing.T, f *fixture) { f.router(t, "shop", "p@file") },
		"used by a chain":            func(t *testing.T, f *fixture) { f.chain(t, "web", "x", "p") },
	} {
		t.Run("blocked: "+name, func(t *testing.T) {
			f := newFixture(t, dockerRuntime())
			f.upsert(t, "p", prefix("/a"))
			setup(t, f)
			before := len(f.applier.Applied)
			if _, e := f.m.DeleteMiddleware(ctx, "p", models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInUse) {
				t.Fatal(e)
			}
			if len(f.applier.Applied) != before {
				t.Fatal("a blocked delete reached the engine")
			}
		})
	}
	t.Run("a reference to another provider's middleware of the same name does not block", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.upsert(t, "auth", prefix("/a"))
		f.router(t, "shop", "auth@docker")
		if _, e := f.m.DeleteMiddleware(ctx, "auth", models.WriteOptions{}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("dry run deletes nothing", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.upsert(t, "p", prefix("/a"))
		if res, e := f.m.DeleteMiddleware(ctx, "p", models.WriteOptions{DryRun: true}); e != nil || res.Preview == nil {
			t.Fatal(e)
		}
		if row, _ := f.store.GetItem(ctx, models.KindMiddleware, "p"); row == nil {
			t.Fatal("dry run deleted")
		}
	})
	t.Run("an external middleware cannot be deleted", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		if _, e := f.m.DeleteMiddleware(ctx, "auth@docker", models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
			t.Fatal(e)
		}
		if len(f.applier.Applied) != 0 {
			t.Fatal("reached the engine")
		}
	})
	t.Run("a middleware that does not exist is not found", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		if _, e := f.m.DeleteMiddleware(ctx, "ghost", models.WriteOptions{}); !errors.Is(e, files.ErrSourceFileNotFound) {
			t.Fatal(e)
		}
		if len(f.applier.Applied) != 0 {
			t.Fatal("reached the engine")
		}
	})
}

func TestDeleteChain(t *testing.T) {
	t.Run("unused chain is deleted", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.chain(t, "web", "a")
		if _, e := f.m.DeleteChain(ctx, "web", models.WriteOptions{}); e != nil {
			t.Fatal(e)
		}
		if row, _ := f.store.GetItem(ctx, models.KindChain, "web"); row != nil {
			t.Fatal("still stored")
		}
	})
	t.Run("blocked while a router uses it", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.chain(t, "web", "a")
		f.router(t, "shop", "web@file")
		if _, e := f.m.DeleteChain(ctx, "web", models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInUse) || !strings.Contains(e.Error(), "shop") {
			t.Fatal(e)
		}
	})
	t.Run("blocked while another chain nests it", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		f.chain(t, "inner", "a")
		f.chain(t, "outer", "inner")
		if _, e := f.m.DeleteChain(ctx, "inner", models.WriteOptions{}); !errors.Is(e, ErrMiddlewareInUse) || !strings.Contains(e.Error(), "outer") {
			t.Fatal(e)
		}
	})
	t.Run("an external chain cannot be deleted", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		if _, e := f.m.DeleteChain(ctx, "guard@docker", models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
			t.Fatal(e)
		}
	})
	t.Run("a chain that does not exist is not found", func(t *testing.T) {
		f := newFixture(t, dockerRuntime())
		if _, e := f.m.DeleteChain(ctx, "ghost", models.WriteOptions{}); !errors.Is(e, files.ErrSourceFileNotFound) {
			t.Fatal(e)
		}
	})
}

func TestDeleteOfAnUnmanagedFileMiddlewareIsReadOnly(t *testing.T) {
	f := newFixture(t, dockerRuntime()) // strip@file is observed but not managed by the console
	if _, e := f.m.DeleteMiddleware(ctx, "strip", models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
		t.Fatal(e)
	}
}
