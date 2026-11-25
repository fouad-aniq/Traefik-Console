package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

const protocolStatic = "entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\n  db: {address: ':5432'}\n  dns: {address: ':53/udp'}\ncertificatesResolvers:\n  le: {acme: {tlsChallenge: {}}}\n"

func protocolFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte(protocolStatic)); e != nil {
		t.Fatal(e)
	}
	if e := f.ops.SeedPublishedHashes(context.Background()); e != nil {
		t.Fatal(e)
	}
	return f
}

func routerCfgRow(t *testing.T, name string, cfg models.RouterConfig) models.ConfigItemRow {
	t.Helper()
	raw, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return models.ConfigItemRow{ID: "router:" + name, Kind: models.KindRouter, Name: name, Scope: models.ScopeDynamic, Payload: raw}
}

func TestATCPRouterIsWrittenToTheTCPSection(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "pg", addresses("tcp", "db:5432")),
		routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}}),
	))
	same(t, f.section(t, "tcp", "routers")["pg"], "entryPoints: [db]\nrule: HostSNI(`*`)\nservice: pg\n")
	if f.section(t, "http", "routers") != nil || f.section(t, "udp", "routers") != nil {
		t.Fatal("a TCP router leaked into another protocol", f.dynamic(t))
	}
}

func TestATCPRouterThatPassesTLSThroughSaysSo(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "vault", addresses("tcp", "vault:8200")),
		routerCfgRow(t, "vault", models.RouterConfig{Protocol: "tcp", Domains: []string{"vault.example.com"}, Service: "vault", EntryPoints: []string{"websecure"}, Priority: 5, TLS: &models.RouterTLS{Enabled: true, Passthrough: true}}),
	))
	same(t, f.section(t, "tcp", "routers")["vault"], "entryPoints: [websecure]\nrule: HostSNI(`vault.example.com`)\nservice: vault\npriority: 5\ntls: {passthrough: true}\n")
}

func TestATCPRouterThatTerminatesTLSWritesItsCertificateSettings(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "mqtt", addresses("tcp", "mqtt:1883")),
		routerCfgRow(t, "mqtt", models.RouterConfig{Protocol: "tcp", Domains: []string{"mqtt.example.com"}, Service: "mqtt", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true, CertResolver: "le", Options: "modern"}}),
	))
	same(t, f.section(t, "tcp", "routers")["mqtt"], "entryPoints: [websecure]\nrule: HostSNI(`mqtt.example.com`)\nservice: mqtt\ntls: {certResolver: le, options: modern}\n")
}

func TestAUDPRouterHasOnlyEntryPointsAndAService(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "dns", addresses("udp", "10.0.0.53:53")),
		routerCfgRow(t, "dns", models.RouterConfig{Protocol: "udp", Service: "dns", EntryPoints: []string{"dns"}}),
	))
	same(t, f.section(t, "udp", "routers")["dns"], "entryPoints: [dns]\nservice: dns\n")
}

func TestAnHTTPRouterIsStillWrittenWhereItWas(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "web", models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://app:80"}}}),
		routerCfgRow(t, "site", models.RouterConfig{Domains: []string{"a.example.com"}, Service: "web", EntryPoints: []string{"web"}, Middlewares: nil}),
	))
	same(t, f.section(t, "http", "routers")["site"], "entryPoints: [web]\nrule: Host(`a.example.com`)\nservice: web\n")
	if f.section(t, "tcp", "routers") != nil || f.section(t, "udp", "routers") != nil {
		t.Fatal(f.dynamic(t))
	}
}

func TestEachProtocolHasItsOwnRoutersAtOnce(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "web", models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://app:80"}}}),
		serviceCfgRow(t, "pg", addresses("tcp", "db:5432")),
		serviceCfgRow(t, "dns", addresses("udp", "10.0.0.53:53")),
		routerCfgRow(t, "site", models.RouterConfig{Domains: []string{"a.example.com"}, Service: "web", EntryPoints: []string{"web"}}),
		routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}}),
		routerCfgRow(t, "dns", models.RouterConfig{Protocol: "udp", Service: "dns", EntryPoints: []string{"dns"}}),
	))
	for protocol, name := range map[string]string{"http": "site", "tcp": "pg", "udp": "dns"} {
		if f.section(t, protocol, "routers")[name] == nil {
			t.Fatalf("%s router %s missing:\n%s", protocol, name, f.dynamic(t))
		}
	}
}

func TestDeletingARouterRemovesItFromItsOwnSectionOnly(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(
		serviceCfgRow(t, "pg", addresses("tcp", "db:5432")),
		serviceCfgRow(t, "dns", addresses("udp", "10.0.0.53:53")),
		routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}}),
		routerCfgRow(t, "dns", models.RouterConfig{Protocol: "udp", Service: "dns", EntryPoints: []string{"dns"}}),
	))
	f.apply(t, deleteRef(models.KindRouter, "pg"))
	if f.section(t, "tcp", "routers") != nil || f.section(t, "udp", "routers")["dns"] == nil {
		t.Fatal(f.dynamic(t))
	}
	f.apply(t, deleteRef(models.KindRouter, "dns"))
	if f.section(t, "udp", "routers") != nil {
		t.Fatal(f.dynamic(t))
	}
}

// Traefik resolves a router's service in the router's own protocol, so a mismatch is refused
// before anything reaches Traefik.
func TestARouterCannotUseAServiceOfAnotherProtocol(t *testing.T) {
	cases := map[string]struct {
		service models.ServiceConfig
		router  models.RouterConfig
	}{
		"tcp router, http service": {models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://a"}}}, models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "svc", EntryPoints: []string{"db"}}},
		"tcp router, udp service":  {addresses("udp", "a:53"), models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "svc", EntryPoints: []string{"db"}}},
		"udp router, tcp service":  {addresses("tcp", "a:53"), models.RouterConfig{Protocol: "udp", Service: "svc", EntryPoints: []string{"dns"}}},
		"udp router, http service": {models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://a"}}}, models.RouterConfig{Protocol: "udp", Service: "svc", EntryPoints: []string{"dns"}}},
		"http router, udp service": {addresses("udp", "a:53"), models.RouterConfig{CustomRule: "Host(`a.example.com`)", Service: "svc", EntryPoints: []string{"web"}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := protocolFixture(t)
			f.apply(t, upsert(serviceCfgRow(t, "svc", c.service)))
			verified := f.observer.verified
			if _, err := f.ops.Apply(context.Background(), upsert(routerCfgRow(t, "r", c.router))); !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want a pre-flight failure, got %v", err)
			}
			if f.observer.verified != verified {
				t.Fatal("refused change sent to Traefik")
			}
		})
	}
}

func TestARouterWithNoServiceAtAllIsRefusedInEveryProtocol(t *testing.T) {
	for protocol, cfg := range map[string]models.RouterConfig{
		"tcp": {Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "ghost", EntryPoints: []string{"db"}},
		"udp": {Protocol: "udp", Service: "ghost", EntryPoints: []string{"dns"}},
	} {
		t.Run(protocol, func(t *testing.T) {
			f := protocolFixture(t)
			if _, err := f.ops.Apply(context.Background(), upsert(routerCfgRow(t, "r", cfg))); !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want a pre-flight failure, got %v", err)
			}
		})
	}
}

func TestATCPRouterRejectedByTraefikIsRolledBack(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(serviceCfgRow(t, "pg", addresses("tcp", "db:5432")), routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}})))
	before := f.dynamic(t)
	f.observer.rejectOnce = true
	changed := routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`other`)", Service: "pg", EntryPoints: []string{"db"}})
	if _, err := f.ops.Apply(context.Background(), upsert(changed)); !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal(err)
	}
	if f.dynamic(t) != before {
		t.Fatal("rollback did not restore the dynamic file")
	}
}

func TestAHandWrittenRouterOfAnyProtocolIsNotTakenOver(t *testing.T) {
	for name, external := range map[string]string{
		"tcp router":  "tcp:\n  routers:\n    r:\n      rule: HostSNI(`*`)\n      service: x\n",
		"udp router":  "udp:\n  routers:\n    r:\n      service: x\n",
		"http router": "http:\n  routers:\n    r:\n      rule: Host(`a.example.com`)\n      service: x\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := protocolFixture(t)
			if e := os.WriteFile(filepath.Join(f.root, "dynamic", "hand.yml"), []byte(external), 0o600); e != nil {
				t.Fatal(e)
			}
			f.apply(t, upsert(serviceCfgRow(t, "pg", addresses("tcp", "db:5432"))))
			_, err := f.ops.Apply(context.Background(), upsert(routerCfgRow(t, "r", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}})))
			if !errors.Is(err, files.ErrCannotModifyReadOnly) {
				t.Fatalf("want read-only, got %v", err)
			}
		})
	}
}

func TestADeletedTCPServiceCannotLeaveARouterBehind(t *testing.T) {
	f := protocolFixture(t)
	f.apply(t, upsert(serviceCfgRow(t, "pg", addresses("tcp", "db:5432")), routerCfgRow(t, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}})))
	if _, err := f.ops.Apply(context.Background(), deleteRef(models.KindService, "pg")); err == nil {
		t.Fatal("a service still used by a TCP router was removed")
	}
}
