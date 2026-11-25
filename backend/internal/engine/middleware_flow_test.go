package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

func middlewareRow(t *testing.T, name string, cfg models.MiddlewareConfig) models.ConfigItemRow {
	t.Helper()
	raw, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return models.ConfigItemRow{ID: "middleware:" + name, Kind: models.KindMiddleware, Name: name, Scope: models.ScopeDynamic, Payload: raw}
}

func chainRow(t *testing.T, name string, members ...string) models.ConfigItemRow {
	t.Helper()
	raw, _ := json.Marshal(models.ChainConfig{Middlewares: members})
	return models.ConfigItemRow{ID: "chain:" + name, Kind: models.KindChain, Name: name, Scope: models.ScopeDynamic, Payload: raw}
}

func routerUsing(t *testing.T, name string, mws ...string) models.ConfigItemRow {
	t.Helper()
	raw, _ := json.Marshal(models.RouterConfig{CustomRule: "Host(`" + name + ".example`)", Service: "svc", EntryPoints: []string{"web"}, Middlewares: mws})
	return models.ConfigItemRow{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: raw}
}

func addPrefix(t *testing.T, name, p string) models.ConfigItemRow {
	return middlewareRow(t, name, models.MiddlewareConfig{Type: "addPrefix", Spec: map[string]any{"prefix": p}})
}

func deleteRef(kind models.ItemKind, name string) models.Mutation {
	return models.Mutation{Summary: "test", Deletes: []models.ItemRef{{Kind: kind, Name: name}}}
}

func TestMiddlewareIsPublishedToTheDynamicFileWithoutARestart(t *testing.T) {
	f := newFixture(t)
	r := f.apply(t, upsert(addPrefix(t, "api", "/api")))
	dyn := f.dynamic(t)
	if !strings.Contains(dyn, "middlewares:") || !strings.Contains(dyn, "api:") || !strings.Contains(dyn, "addPrefix:") || !strings.Contains(dyn, "prefix: /api") {
		t.Fatal("middleware not rendered", dyn)
	}
	if r.Operation.Status != models.OpStatusSucceeded || r.RestartRequired() || f.docker.restarts != 0 || f.observer.verified != 1 {
		t.Fatalf("dynamic change must verify without restarting: status=%s restarts=%d verified=%d", r.Operation.Status, f.docker.restarts, f.observer.verified)
	}
}

func TestChainIsPublishedWithItsMembersInOrder(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(addPrefix(t, "a", "/a"), addPrefix(t, "b", "/b"), chainRow(t, "web", "b", "a")))
	dyn := f.dynamic(t)
	i, j := strings.Index(dyn, "- b"), strings.Index(dyn, "- a")
	if !strings.Contains(dyn, "chain:") || i < 0 || j < 0 || i > j {
		t.Fatal("chain not rendered in order", dyn)
	}
}

func TestEditingAMiddlewareRewritesOnlyItsEntry(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(addPrefix(t, "a", "/a"), addPrefix(t, "b", "/b")))
	f.apply(t, upsert(addPrefix(t, "a", "/changed")))
	dyn := f.dynamic(t)
	if !strings.Contains(dyn, "/changed") || strings.Contains(dyn, "prefix: /a\n") || !strings.Contains(dyn, "prefix: /b") {
		t.Fatal(dyn)
	}
}

func TestDeletingAMiddlewareRemovesItFromTheDynamicFile(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(addPrefix(t, "a", "/a"), addPrefix(t, "keep", "/k")))
	f.apply(t, deleteRef(models.KindMiddleware, "a"))
	dyn := f.dynamic(t)
	if strings.Contains(dyn, "prefix: /a") || !strings.Contains(dyn, "prefix: /k") || f.item(t, models.KindMiddleware, "a") != nil {
		t.Fatal(dyn)
	}
}

func TestMiddlewareRejectedByTraefikIsRolledBack(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(addPrefix(t, "a", "/a")))
	before := f.dynamic(t)
	f.observer.rejectOnce = true
	_, err := f.ops.Apply(context.Background(), upsert(addPrefix(t, "a", "/broken")))
	if !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal(err)
	}
	if f.dynamic(t) != before {
		t.Fatal("rollback did not restore the dynamic file")
	}
	var cfg models.MiddlewareConfig
	_ = json.Unmarshal(f.item(t, models.KindMiddleware, "a").Payload, &cfg)
	if cfg.Spec["prefix"] != "/a" {
		t.Fatal("the stored middleware changed after a rollback", cfg.Spec)
	}
}

func TestChainRejectedByTraefikIsRolledBack(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(addPrefix(t, "a", "/a")))
	before := f.dynamic(t)
	f.observer.rejectOnce = true
	if _, err := f.ops.Apply(context.Background(), upsert(chainRow(t, "web", "a"))); !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal(err)
	}
	if f.dynamic(t) != before || f.item(t, models.KindChain, "web") != nil {
		t.Fatal("failed chain left behind")
	}
}

// The engine is the last safety net: whatever a client sends, it never publishes a configuration
// in which Traefik would find a dangling reference or a loop.
func TestEngineRefusesBrokenMiddlewareReferences(t *testing.T) {
	for name, tc := range map[string]struct {
		setup []models.ConfigItemRow
		apply models.Mutation
	}{
		"chain member that does not exist":    {nil, upsert(chainRow(t, "web", "ghost"))},
		"chain loop":                          {[]models.ConfigItemRow{addPrefix(t, "a", "/a"), chainRow(t, "one", "a")}, upsert(chainRow(t, "two", "one"), chainRow(t, "one", "two"))},
		"middleware and chain share a name":   {[]models.ConfigItemRow{addPrefix(t, "same", "/a")}, upsert(chainRow(t, "same", "same"))},
		"deleting a middleware a router uses": {[]models.ConfigItemRow{addPrefix(t, "a", "/a"), routerUsing(t, "shop", "a@file"), serviceRow("svc", "http://svc.invalid")}, deleteRef(models.KindMiddleware, "a")},
		"deleting a chain a router uses":      {[]models.ConfigItemRow{addPrefix(t, "a", "/a"), chainRow(t, "web", "a"), routerUsing(t, "shop", "web"), serviceRow("svc", "http://svc.invalid")}, deleteRef(models.KindChain, "web")},
		"deleting a middleware a chain uses":  {[]models.ConfigItemRow{addPrefix(t, "a", "/a"), chainRow(t, "web", "a")}, deleteRef(models.KindMiddleware, "a")},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			// Routers are checked against traefik.yml's entry points.
			if e := f.files.WriteStaticConfig([]byte("entryPoints:\n  web:\n    address: \":80\"\n")); e != nil {
				t.Fatal(e)
			}
			if e := f.ops.SeedPublishedHashes(context.Background()); e != nil {
				t.Fatal(e)
			}
			if tc.setup != nil {
				f.apply(t, upsert(tc.setup...))
			}
			before := f.dynamic(t)
			verified := f.observer.verified
			_, err := f.ops.Apply(context.Background(), tc.apply)
			if !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want a pre-flight validation failure, got %v\n%s", err, f.dynamic(t))
			}
			if f.observer.verified != verified {
				t.Fatal("a refused change was sent to Traefik")
			}
			if f.dynamic(t) != before {
				t.Fatal("a refused change touched the dynamic file")
			}
		})
	}
}

func TestStoredSecretsAreKeptWhenAnEditSendsTheRedactedMarker(t *testing.T) {
	f := newFixture(t)
	auth := func(usersFile, realm string) models.ConfigItemRow {
		return middlewareRow(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{"usersFile": usersFile, "realm": realm}})
	}
	secret, e := f.secrets.PrepareSecret(context.Background(), "basicauth_auth", "basic_auth", "", "admin:$2a$10$abcdefghijklmnopqrstuuJ0Zq0m6p2y3m0b8m5yqk2xj3xg0xg0e\n")
	if e != nil {
		t.Fatal(e)
	}
	created := upsert(auth(secret.FilePath, "old"))
	created.Secrets = []models.SecretRow{*secret}
	f.apply(t, created)
	// An edit that keeps the users sends back what the API showed: the marker, never the path.
	f.apply(t, upsert(auth("[redacted]", "new")))
	var cfg models.MiddlewareConfig
	_ = json.Unmarshal(f.item(t, models.KindMiddleware, "auth").Payload, &cfg)
	if cfg.Spec["usersFile"] != secret.FilePath || cfg.Spec["realm"] != "new" {
		t.Fatal("marker not resolved to the stored value", cfg.Spec)
	}
	if dyn := f.dynamic(t); strings.Contains(dyn, "[redacted]") || !strings.Contains(dyn, secret.FilePath) {
		t.Fatal("published file has the marker instead of the stored path", dyn)
	}
}

func TestARedactedMarkerWithNothingStoredIsRefused(t *testing.T) {
	f := newFixture(t)
	_, err := f.ops.Apply(context.Background(), upsert(middlewareRow(t, "auth", models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{"usersFile": "[redacted]"}})))
	if !errors.Is(err, ErrValidationFailed) || f.item(t, models.KindMiddleware, "auth") != nil {
		t.Fatal("a marker became a setting", err)
	}
}
