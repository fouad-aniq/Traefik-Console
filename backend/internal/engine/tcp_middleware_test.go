package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

func tcpMiddleware(t *testing.T, name, typ string, spec map[string]any) models.ConfigItemRow {
	return middlewareRow(t, name, models.MiddlewareConfig{Protocol: "tcp", Type: typ, Spec: spec})
}

func tcpRouterUsing(t *testing.T, name string, mws ...string) models.ConfigItemRow {
	return routerCfgRow(t, name, models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}, Middlewares: mws})
}

func pg(t *testing.T) models.ConfigItemRow {
	return serviceCfgRow(t, "pg", addresses("tcp", "db:5432"))
}

func TestATCPMiddlewareIsWrittenToTheTCPSection(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		tcpMiddleware(t, "office", "ipAllowList", map[string]any{"sourceRange": []any{"10.0.0.0/8"}}),
		tcpMiddleware(t, "cap", "inFlightConn", map[string]any{"amount": 10}),
	))
	same(t, f.section(t, "tcp", "middlewares")["office"], "ipAllowList: {sourceRange: [10.0.0.0/8]}\n")
	same(t, f.section(t, "tcp", "middlewares")["cap"], "inFlightConn: {amount: 10}\n")
	if f.section(t, "http", "middlewares") != nil {
		t.Fatal("a TCP middleware leaked into the HTTP section", f.dynamic(t))
	}
}

func TestATCPRouterUsesItsTCPMiddlewaresInOrder(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		pg(t),
		tcpMiddleware(t, "office", "ipAllowList", map[string]any{"sourceRange": []any{"10.0.0.0/8"}}),
		tcpMiddleware(t, "cap", "inFlightConn", map[string]any{"amount": 10}),
		tcpRouterUsing(t, "pg", "office", "cap"),
	))
	same(t, f.section(t, "tcp", "routers")["pg"], "entryPoints: [db]\nrule: HostSNI(`*`)\nservice: pg\nmiddlewares: [office, cap]\n")
}

// HTTP and TCP middlewares are separate pools in Traefik: a reference across them is refused
// before anything reaches Traefik.
func TestARouterCannotUseAMiddlewareOfAnotherProtocol(t *testing.T) {
	cases := map[string][]models.ConfigItemRow{
		"tcp router, http middleware": {pg(t), addPrefix(t, "api", "/api"), tcpRouterUsing(t, "r", "api")},
		"http router, tcp middleware": {
			serviceCfgRow(t, "svc", models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://a"}}}),
			tcpMiddleware(t, "office", "ipAllowList", map[string]any{"sourceRange": []any{"10.0.0.0/8"}}),
			routerUsing(t, "r", "office"),
		},
		"tcp router, missing middleware": {pg(t), tcpRouterUsing(t, "r", "ghost")},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			f := protocolFixture(t)
			f.apply(t, upsert(rows[:len(rows)-1]...))
			verified := f.observer.verified
			if _, err := f.ops.Apply(context.Background(), upsert(rows[len(rows)-1])); !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want a pre-flight failure, got %v", err)
			}
			if f.observer.verified != verified {
				t.Fatal("refused change sent to Traefik")
			}
		})
	}
}

func TestATCPMiddlewareWithFieldsTraefikDoesNotKnowIsRefused(t *testing.T) {
	for name, row := range map[string]models.ConfigItemRow{
		"unknown field":      tcpMiddleware(t, "m", "inFlightConn", map[string]any{"amount": 1, "burst": 2}),
		"zero connections":   tcpMiddleware(t, "m", "inFlightConn", map[string]any{"amount": 0}),
		"no source range":    tcpMiddleware(t, "m", "ipAllowList", map[string]any{"sourceRange": []any{}}),
		"an HTTP-only field": tcpMiddleware(t, "m", "ipAllowList", map[string]any{"sourceRange": []any{"1.2.3.4"}, "ipStrategy": map[string]any{"depth": 1}}),
	} {
		t.Run(name, func(t *testing.T) {
			f := protocolFixture(t)
			if _, err := f.ops.Apply(context.Background(), upsert(row)); !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want a pre-flight failure, got %v", err)
			}
		})
	}
}

func TestATCPMiddlewareStillUsedCannotBeDeleted(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(pg(t), tcpMiddleware(t, "cap", "inFlightConn", map[string]any{"amount": 10}), tcpRouterUsing(t, "pg", "cap")))
	if _, err := f.ops.Apply(context.Background(), deleteRef(models.KindMiddleware, "cap")); err == nil {
		t.Fatal("a middleware still used by a TCP router was removed")
	}
	// Once the router lets go of it, it goes.
	f.apply(t, upsert(tcpRouterUsing(t, "pg")))
	f.apply(t, deleteRef(models.KindMiddleware, "cap"))
	if f.section(t, "tcp", "middlewares") != nil {
		t.Fatal(f.dynamic(t))
	}
}

func TestAHandWrittenTCPMiddlewareIsNotTakenOver(t *testing.T) {
	f := protocolFixture(t)
	if e := os.WriteFile(filepath.Join(f.root, "dynamic", "hand.yml"), []byte("tcp:\n  middlewares:\n    cap:\n      inFlightConn: {amount: 3}\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	_, err := f.ops.Apply(context.Background(), upsert(addPrefix(t, "cap", "/x")))
	if !errors.Is(err, files.ErrCannotModifyReadOnly) {
		t.Fatalf("want read-only, got %v", err)
	}
}

func TestATCPMiddlewareRejectedByTraefikIsRolledBack(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(tcpMiddleware(t, "cap", "inFlightConn", map[string]any{"amount": 10})))
	before := f.dynamic(t)
	f.observer.rejectOnce = true
	if _, err := f.ops.Apply(context.Background(), upsert(tcpMiddleware(t, "cap", "inFlightConn", map[string]any{"amount": 20}))); !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal(err)
	}
	if f.dynamic(t) != before {
		t.Fatal("rollback did not restore the dynamic file")
	}
}
