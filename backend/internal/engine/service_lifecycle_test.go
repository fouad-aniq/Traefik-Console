package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/traefik-console/internal/models"
)

// The same scenarios for http, tcp and udp, run through the real pipeline: journal, files, verification, commit.
type protocolCase struct {
	protocol string
	name     string
	v1, v2   models.ServiceConfig
	yaml1    string
	yaml2    string
}

func protocolCases() []protocolCase {
	httpV1 := models.ServiceConfig{Protocol: "http", Servers: []models.ServerEndpoint{{URL: "http://a:80"}}}
	httpV2 := models.ServiceConfig{Protocol: "http", Servers: []models.ServerEndpoint{{URL: "http://b:81"}, {URL: "https://c:82/x"}}, InsecureSkipVerify: true}
	tcpV2 := addresses("tcp", "db:5432", "db2:5432")
	tcpV2.TCP = &models.TCPServiceOptions{ProxyProtocol: 1}
	return []protocolCase{
		{"http", "web", httpV1, httpV2,
			"loadBalancer:\n  servers:\n    - url: http://a:80\n",
			"loadBalancer:\n  servers:\n    - url: http://b:81\n    - url: https://c:82/x\n  serversTransport: web-insecure-transport\n"},
		{"tcp", "db", addresses("tcp", "db:5432"), tcpV2,
			"loadBalancer:\n  servers:\n    - address: db:5432\n",
			"loadBalancer:\n  servers:\n    - address: db:5432\n    - address: db2:5432\n  serversTransport: db-tcp-transport\n"},
		{"udp", "dns", addresses("udp", "dns:53"), addresses("udp", "dns2:53", "dns3:53"),
			"loadBalancer:\n  servers:\n    - address: dns:53\n",
			"loadBalancer:\n  servers:\n    - address: dns2:53\n    - address: dns3:53\n"},
	}
}

func (c protocolCase) ref() models.ItemRef {
	return models.ItemRef{Kind: models.KindService, Name: c.name}
}

func (c protocolCase) row(t *testing.T, cfg models.ServiceConfig) models.ConfigItemRow {
	return serviceCfgRow(t, c.name, cfg)
}

func (f *fixture) has(t *testing.T, c protocolCase) bool {
	t.Helper()
	return f.section(t, c.protocol, "services")[c.name] != nil
}

func TestEveryProtocolIsCreatedEditedAndDeletedInItsOwnSection(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)

			f.apply(t, upsert(c.row(t, c.v1)))
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml1)
			for _, other := range []string{"http", "tcp", "udp"} {
				if other != c.protocol && f.section(t, other, "services") != nil {
					t.Fatalf("a %s service leaked into the %s section:\n%s", c.protocol, other, f.dynamic(t))
				}
			}
			if f.item(t, models.KindService, c.name).Version != 1 {
				t.Fatal("a created service starts at version 1")
			}

			f.apply(t, upsert(c.row(t, c.v2)))
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml2)
			if f.item(t, models.KindService, c.name).Version != 2 {
				t.Fatal("an edit bumps the version")
			}

			f.apply(t, deleteRef(models.KindService, c.name))
			if f.has(t, c) || f.item(t, models.KindService, c.name) != nil {
				t.Fatalf("not deleted:\n%s", f.dynamic(t))
			}

			// The name is free again.
			f.apply(t, upsert(c.row(t, c.v1)))
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml1)
			if f.item(t, models.KindService, c.name).Version != 1 {
				t.Fatal("a recreated service starts at version 1")
			}

			if f.docker.restarts != 0 {
				t.Fatal("a service change is dynamic: Traefik must not be restarted")
			}
		})
	}
}

func TestEveryServiceWriteIsOneVerifiedOperation(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			f.apply(t, upsert(c.row(t, c.v1)))
			f.apply(t, upsert(c.row(t, c.v2)))
			f.apply(t, deleteRef(models.KindService, c.name))
			ops, e := f.store.ListOperations(context.Background(), 10, 0)
			if e != nil || len(ops) != 3 || f.observer.verified != 3 {
				t.Fatalf("%d operations, %d verifications, %v", len(ops), f.observer.verified, e)
			}
			for _, op := range ops {
				if op.Status != models.OpStatusSucceeded {
					t.Fatalf("%+v", op)
				}
			}
		})
	}
}

func TestRepeatingAServiceWriteIsNotAnOperation(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			f.apply(t, upsert(c.row(t, c.v1)))
			r := f.apply(t, upsert(c.row(t, c.v1)))
			ops, _ := f.store.ListOperations(context.Background(), 10, 0)
			if !r.NoChange || len(ops) != 1 || f.item(t, models.KindService, c.name).Version != 1 || f.observer.verified != 1 {
				t.Fatal("an identical write was journaled, verified or bumped the version")
			}
		})
	}
}

func TestServiceVersionPreconditions(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			bg := context.Background()
			unchanged := func(step string) {
				t.Helper()
				if f.has(t, c) != (f.item(t, models.KindService, c.name) != nil) {
					t.Fatalf("%s: the file and the database disagree", step)
				}
			}

			// Create: must not exist.
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 0, true), upsert(c.row(t, c.v1))); e != nil {
				t.Fatal(e)
			}
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 0, true), upsert(c.row(t, c.v2))); !errors.Is(e, ErrVersionConflict) {
				t.Fatalf("create over an existing service: %v", e)
			}
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml1)

			// Edit: needs the version that was read.
			for _, stale := range []int64{7, 2} {
				if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), stale, false), upsert(c.row(t, c.v2))); !errors.Is(e, ErrVersionConflict) {
					t.Fatalf("edit at version %d: %v", stale, e)
				}
			}
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml1)
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 1, false), upsert(c.row(t, c.v2))); e != nil {
				t.Fatal(e)
			}
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml2)

			// The version moved on: the one just used is stale for the delete.
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 1, false), deleteRef(models.KindService, c.name)); !errors.Is(e, ErrVersionConflict) {
				t.Fatalf("delete at a stale version: %v", e)
			}
			if !f.has(t, c) {
				t.Fatal("a conflicting delete removed the service")
			}
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 2, false), deleteRef(models.KindService, c.name)); e != nil {
				t.Fatal(e)
			}
			unchanged("after the delete")

			// Gone: an edit or a delete of what was read is a conflict, not a silent create.
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 2, false), upsert(c.row(t, c.v2))); !errors.Is(e, ErrVersionConflict) {
				t.Fatalf("edit of a deleted service: %v", e)
			}
			if _, e := f.ops.Apply(WithExpectedVersion(bg, c.ref(), 2, false), deleteRef(models.KindService, c.name)); !errors.Is(e, ErrVersionConflict) {
				t.Fatalf("delete of a deleted service: %v", e)
			}
			if f.has(t, c) || f.item(t, models.KindService, c.name) != nil {
				t.Fatal("a conflicting write recreated the service")
			}
		})
	}
}

func TestARefusedPreconditionDoesNotTouchTraefik(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			f.apply(t, upsert(c.row(t, c.v1)))
			verified := f.observer.verified
			if _, e := f.ops.Apply(WithExpectedVersion(context.Background(), c.ref(), 9, false), upsert(c.row(t, c.v2))); !errors.Is(e, ErrVersionConflict) {
				t.Fatal(e)
			}
			if f.observer.verified != verified {
				t.Fatal("a conflicting write reached Traefik")
			}
			ops, _ := f.store.ListOperations(context.Background(), 10, 0)
			if len(ops) != 1 {
				t.Fatalf("a conflicting write was journaled: %d operations", len(ops))
			}
		})
	}
}

// A change Traefik rejects, or cannot confirm, must leave the files and the database as they were.
func TestAServiceChangeThatCannotBeVerifiedIsRolledBack(t *testing.T) {
	for _, c := range protocolCases() {
		for _, how := range []string{"rejected", "traefik offline"} {
			for _, op := range []string{"create", "edit", "delete"} {
				t.Run(c.protocol+" "+op+" "+how, func(t *testing.T) {
					f := newFixture(t)
					if op != "create" {
						f.apply(t, upsert(c.row(t, c.v1)))
					}
					filesBefore := f.dynamic(t)
					var rowBefore *models.ConfigItemRow
					if r := f.item(t, models.KindService, c.name); r != nil {
						kept := *r
						rowBefore = &kept
					}

					if how == "rejected" {
						f.observer.rejectOnce = true
					} else {
						f.observer.alwaysFail = true
					}
					mutation := upsert(c.row(t, c.v2))
					switch op {
					case "create":
						mutation = upsert(c.row(t, c.v1))
					case "delete":
						mutation = deleteRef(models.KindService, c.name)
					}
					_, err := f.ops.Apply(context.Background(), mutation)
					if err == nil {
						t.Fatal("a change Traefik did not confirm was reported as done")
					}
					if how == "rejected" && !errors.Is(err, ErrRollbackTriggered) {
						t.Fatalf("want a rollback, got %v", err)
					}

					if f.dynamic(t) != filesBefore {
						t.Fatalf("the dynamic file was not restored:\n%s", f.dynamic(t))
					}
					rowAfter := f.item(t, models.KindService, c.name)
					switch {
					case rowBefore == nil && rowAfter != nil:
						t.Fatal("the database kept a service that was never confirmed")
					case rowBefore != nil && (rowAfter == nil || rowAfter.Version != rowBefore.Version || string(rowAfter.Payload) != string(rowBefore.Payload)):
						t.Fatalf("the database changed: %+v", rowAfter)
					}

					// The engine is not stuck. With Traefik reachable the rollback was confirmed and the next
					// change goes through. When Traefik was offline the rollback could not be confirmed: changes
					// are refused until the operation is recovered, which works once Traefik answers again.
					f.observer.alwaysFail = false
					next := upsert(serviceCfgRow(t, "after-"+c.name, c.v1))
					if how == "traefik offline" {
						if _, e := f.ops.Apply(context.Background(), next); !errors.Is(e, ErrOperationRecoveryRequired) {
							t.Fatalf("an unconfirmed rollback must block further changes, got %v", e)
						}
						var failure *DeployFailure
						if !errors.As(err, &failure) {
							t.Fatalf("no operation to recover in %v", err)
						}
						if _, e := f.ops.Recover(context.Background(), failure.OperationID); e != nil {
							t.Fatalf("recovery failed once Traefik was back: %v", e)
						}
						if f.dynamic(t) != filesBefore {
							t.Fatal("recovery changed the restored file")
						}
					}
					f.apply(t, next)
				})
			}
		}
	}
}

func TestIdempotentReplayOfAServiceWrite(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			ctx := WithRequestKey(context.Background(), "service-key-0123456789")
			first, e := f.ops.Apply(ctx, upsert(c.row(t, c.v1)))
			if e != nil {
				t.Fatal(e)
			}
			verified := f.observer.verified
			again, e := f.ops.Apply(ctx, upsert(c.row(t, c.v1)))
			if e != nil || again.Operation.ID != first.Operation.ID {
				t.Fatalf("a retry created a second operation: %v", e)
			}
			if f.observer.verified != verified || f.item(t, models.KindService, c.name).Version != 1 {
				t.Fatal("a replay applied the change again")
			}
			// The same key must not be reused for another change.
			if _, e := f.ops.Apply(ctx, upsert(c.row(t, c.v2))); e == nil {
				t.Fatal("a key was reused for a different service definition")
			}
			same(t, f.section(t, c.protocol, "services")[c.name], c.yaml1)
		})
	}
}

func TestMalformedIdempotencyKeysAreRefused(t *testing.T) {
	f := newFixture(t)
	c := protocolCases()[0]
	for _, key := range []string{"short", "has spaces in it 0123456789", "semi;colon-0123456789", string(make([]byte, 129))} {
		ctx := WithRequestKey(context.Background(), key)
		if _, e := f.ops.Apply(ctx, upsert(c.row(t, c.v1))); !errors.Is(e, ErrPreconditionRequired) {
			t.Fatalf("%q: %v", key, e)
		}
	}
	if f.has(t, c) {
		t.Fatal("a request with a bad key was applied")
	}
}

func TestAReplayOfARolledBackWriteDoesNotTryAgain(t *testing.T) {
	for _, c := range protocolCases() {
		t.Run(c.protocol, func(t *testing.T) {
			f := newFixture(t)
			ctx := WithRequestKey(context.Background(), "rolled-back-key-0123456789")
			f.observer.rejectOnce = true
			if _, e := f.ops.Apply(ctx, upsert(c.row(t, c.v1))); !errors.Is(e, ErrRollbackTriggered) {
				t.Fatal(e)
			}
			verified := f.observer.verified
			_, e := f.ops.Apply(ctx, upsert(c.row(t, c.v1)))
			var failure *DeployFailure
			if !errors.As(e, &failure) || !failure.RolledBack {
				t.Fatalf("a replay of a failed write must report the same failure: %v", e)
			}
			if f.observer.verified != verified || f.has(t, c) {
				t.Fatal("a replay of a failed write was attempted again")
			}
		})
	}
}

func TestChangingOneServiceLeavesTheOthersAlone(t *testing.T) {
	f := newFixture(t)
	cases := protocolCases()
	for _, c := range cases {
		f.apply(t, upsert(c.row(t, c.v1)))
	}
	hand := "http:\n  services:\n    hand-written:\n      loadBalancer:\n        servers:\n          - url: http://hand:1\n"
	if e := os.WriteFile(filepath.Join(f.root, "dynamic", "hand.yml"), []byte(hand), 0o600); e != nil {
		t.Fatal(e)
	}
	snapshot := func() map[string]any {
		out := map[string]any{}
		for _, c := range cases {
			out[c.protocol] = f.section(t, c.protocol, "services")[c.name]
		}
		return out
	}
	before := snapshot()

	for _, target := range cases {
		f.apply(t, upsert(target.row(t, target.v2)))
		after := snapshot()
		for _, c := range cases {
			if c.protocol == target.protocol {
				continue
			}
			same(t, after[c.protocol], c.yaml1)
		}
		f.apply(t, deleteRef(models.KindService, target.name))
		after = snapshot()
		for _, c := range cases {
			if c.protocol != target.protocol && after[c.protocol] == nil {
				t.Fatalf("deleting %s removed %s", target.name, c.name)
			}
		}
		f.apply(t, upsert(target.row(t, target.v1)))
	}
	for protocol, was := range before {
		if !reflect.DeepEqual(snapshot()[protocol], was) {
			t.Fatalf("%s did not come back as it was", protocol)
		}
	}
	raw, e := os.ReadFile(filepath.Join(f.root, "dynamic", "hand.yml"))
	if e != nil || string(raw) != hand {
		t.Fatalf("the hand-written file changed: %v\n%s", e, raw)
	}
}

func TestTheHTTPInsecureTransportFollowsTheService(t *testing.T) {
	f := newFixture(t)
	web := protocolCases()[0]
	cfg := web.v1
	cfg.InsecureSkipVerify = true
	f.apply(t, upsert(web.row(t, cfg)))
	same(t, f.section(t, "http", "serversTransports")["web-insecure-transport"], "insecureSkipVerify: true\n")
	f.apply(t, upsert(web.row(t, web.v1)))
	if f.section(t, "http", "serversTransports") != nil {
		t.Fatalf("the transport outlived the option:\n%s", f.dynamic(t))
	}
	f.apply(t, upsert(web.row(t, cfg)))
	f.apply(t, deleteRef(models.KindService, "web"))
	if f.section(t, "http", "serversTransports") != nil || f.has(t, web) {
		t.Fatalf("deleting the service left its transport:\n%s", f.dynamic(t))
	}
}

func TestEachTCPOptionChangeUpdatesTheTransport(t *testing.T) {
	f := newFixture(t)
	db := protocolCases()[1]
	steps := []struct {
		opts *models.TCPServiceOptions
		skip bool
		want string // "" = no transport
	}{
		{&models.TCPServiceOptions{ProxyProtocol: 1}, false, "proxyProtocol: {version: 1}\n"},
		{&models.TCPServiceOptions{ProxyProtocol: 2, DialTimeout: "3s"}, false, "proxyProtocol: {version: 2}\ndialTimeout: 3s\n"},
		{&models.TCPServiceOptions{TLS: true, ServerName: "db.example"}, false, "tls: {serverName: db.example}\n"},
		{&models.TCPServiceOptions{TLS: true}, true, "tls: {insecureSkipVerify: true}\n"},
		{&models.TCPServiceOptions{TerminationDelay: "1s"}, false, "terminationDelay: 1s\n"},
		{nil, false, ""},
	}
	for i, s := range steps {
		cfg := addresses("tcp", "db:5432")
		cfg.TCP, cfg.InsecureSkipVerify = s.opts, s.skip
		f.apply(t, upsert(db.row(t, cfg)))
		got := f.section(t, "tcp", "serversTransports")
		if s.want == "" {
			if got != nil {
				t.Fatalf("step %d: transport left behind: %v", i, got)
			}
			continue
		}
		if len(got) != 1 {
			t.Fatalf("step %d: %v", i, got)
		}
		same(t, got["db-tcp-transport"], s.want)
	}
}

func TestAnHTTPRouterCannotUseAUDPServiceEither(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte("entryPoints:\n  web:\n    address: \":80\"\n")); e != nil {
		t.Fatal(e)
	}
	if e := f.ops.SeedPublishedHashes(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.apply(t, upsert(serviceCfgRow(t, "svc", addresses("udp", "dns:53"))))
	if _, err := f.ops.Apply(context.Background(), upsert(routerUsing(t, "shop"))); !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("want a pre-flight failure, got %v", err)
	}
	if f.item(t, models.KindRouter, "shop") != nil {
		t.Fatal("the router was stored")
	}
}

func TestADeleteThatWouldLeaveARouterDanglingIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte("entryPoints:\n  web:\n    address: \":80\"\n")); e != nil {
		t.Fatal(e)
	}
	if e := f.ops.SeedPublishedHashes(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.apply(t, upsert(serviceCfgRow(t, "svc", models.ServiceConfig{Protocol: "http", Servers: []models.ServerEndpoint{{URL: "http://a:80"}}})))
	f.apply(t, upsert(routerUsing(t, "shop")))
	before, verified := f.dynamic(t), f.observer.verified
	if _, err := f.ops.Apply(context.Background(), deleteRef(models.KindService, "svc")); !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("want a pre-flight failure, got %v", err)
	}
	if f.dynamic(t) != before || f.observer.verified != verified || f.item(t, models.KindService, "svc") == nil {
		t.Fatal("a refused delete changed something")
	}
	// Once the router is gone the service can go.
	f.apply(t, deleteRef(models.KindRouter, "shop"))
	f.apply(t, deleteRef(models.KindService, "svc"))
}
