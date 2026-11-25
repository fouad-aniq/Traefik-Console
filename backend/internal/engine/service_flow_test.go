package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

func serviceCfgRow(t *testing.T, name string, cfg models.ServiceConfig) models.ConfigItemRow {
	t.Helper()
	raw, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return models.ConfigItemRow{ID: "service:" + name, Kind: models.KindService, Name: name, Scope: models.ScopeDynamic, Payload: raw}
}

func addresses(protocol string, list ...string) models.ServiceConfig {
	cfg := models.ServiceConfig{Protocol: protocol}
	for _, a := range list {
		cfg.Servers = append(cfg.Servers, models.ServerEndpoint{Address: a})
	}
	return cfg
}

// section reads one protocol/category of the managed dynamic file as plain data.
func (f *fixture) section(t *testing.T, protocol, category string) map[string]any {
	t.Helper()
	var doc map[string]any
	if e := yamlv3.Unmarshal([]byte(f.dynamic(t)), &doc); e != nil {
		t.Fatal(e)
	}
	p, _ := doc[protocol].(map[string]any)
	c, _ := p[category].(map[string]any)
	return c
}

func same(t *testing.T, got any, wantYAML string) {
	t.Helper()
	var want any
	if e := yamlv3.Unmarshal([]byte(wantYAML), &want); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}

func TestPlainTCPServiceWritesOnlyItsServers(t *testing.T) {
	f := newFixture(t)
	r := f.apply(t, upsert(serviceCfgRow(t, "db", addresses("tcp", "10.0.0.5:5432", "10.0.0.6:5432"))))
	same(t, f.section(t, "tcp", "services")["db"], "loadBalancer:\n  servers:\n    - address: 10.0.0.5:5432\n    - address: 10.0.0.6:5432\n")
	// No defaults and no transport: Traefik leaves them out of its runtime view.
	if f.section(t, "tcp", "serversTransports") != nil || f.section(t, "http", "services") != nil {
		t.Fatal(f.dynamic(t))
	}
	if r.RestartRequired() || f.docker.restarts != 0 || f.observer.verified != 1 {
		t.Fatal("a TCP service is a dynamic change verified without a restart")
	}
}

func TestTCPOptionsArePublishedThroughTheServiceTransport(t *testing.T) {
	f := newFixture(t)
	cfg := addresses("tcp", "db.internal:5432")
	cfg.InsecureSkipVerify = true
	cfg.TCP = &models.TCPServiceOptions{TLS: true, ServerName: "db.example", ProxyProtocol: 2, DialTimeout: "5s", TerminationDelay: "200ms",
		HealthCheck: &models.TCPHealthCheck{Interval: "10s", Timeout: "3s", Send: "PING", Expect: "PONG", Port: 8080}}
	f.apply(t, upsert(serviceCfgRow(t, "db", cfg)))
	same(t, f.section(t, "tcp", "services")["db"], `
loadBalancer:
  servers:
    - address: db.internal:5432
      tls: true
  healthCheck: {interval: 10s, timeout: 3s, send: PING, expect: PONG, port: 8080}
  serversTransport: db-tcp-transport
`)
	same(t, f.section(t, "tcp", "serversTransports")["db-tcp-transport"], `
proxyProtocol: {version: 2}
dialTimeout: 5s
terminationDelay: 200ms
tls: {serverName: db.example, insecureSkipVerify: true}
`)
}

func TestUDPServiceIsWrittenToTheUDPSection(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceCfgRow(t, "dns", addresses("udp", "10.0.0.53:53"))))
	same(t, f.section(t, "udp", "services")["dns"], "loadBalancer:\n  servers:\n    - address: 10.0.0.53:53\n")
}

func TestHTTPServiceRenderingIsUnchanged(t *testing.T) {
	f := newFixture(t)
	pass := false
	f.apply(t, upsert(serviceCfgRow(t, "web", models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "https://app:8443"}}, PassHostHeader: &pass, HealthCheck: &models.ServiceHealth{Path: "/up", Interval: "10s"}, InsecureSkipVerify: true})))
	same(t, f.section(t, "http", "services")["web"], `
loadBalancer:
  servers: [{url: "https://app:8443"}]
  passHostHeader: false
  healthCheck: {path: /up, interval: 10s}
  serversTransport: web-insecure-transport
`)
	same(t, f.section(t, "http", "serversTransports")["web-insecure-transport"], "insecureSkipVerify: true\n")
}

func TestRemovingTCPOptionsAndDeletingRemoveTheTransport(t *testing.T) {
	f := newFixture(t)
	cfg := addresses("tcp", "db:5432")
	cfg.TCP = &models.TCPServiceOptions{ProxyProtocol: 1}
	f.apply(t, upsert(serviceCfgRow(t, "db", cfg), serviceCfgRow(t, "cache", addresses("tcp", "redis:6379"))))
	if f.section(t, "tcp", "serversTransports")["db-tcp-transport"] == nil {
		t.Fatal(f.dynamic(t))
	}
	f.apply(t, upsert(serviceCfgRow(t, "db", addresses("tcp", "db:5432"))))
	if f.section(t, "tcp", "serversTransports") != nil {
		t.Fatal("transport left behind", f.dynamic(t))
	}
	f.apply(t, deleteRef(models.KindService, "db"))
	if f.section(t, "tcp", "services")["db"] != nil || f.section(t, "tcp", "services")["cache"] == nil {
		t.Fatal(f.dynamic(t))
	}
}

func TestTCPServiceRejectedByTraefikIsRolledBack(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceCfgRow(t, "db", addresses("tcp", "db:5432"))))
	before := f.dynamic(t)
	f.observer.rejectOnce = true
	if _, err := f.ops.Apply(context.Background(), upsert(serviceCfgRow(t, "db", addresses("tcp", "db:6543")))); !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal(err)
	}
	if f.dynamic(t) != before {
		t.Fatal("rollback did not restore the dynamic file")
	}
}

// An HTTP router cannot send traffic to a TCP or UDP service: Traefik resolves it in http only.
func TestAnHTTPRouterCannotUseATCPService(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte("entryPoints:\n  web:\n    address: \":80\"\n")); e != nil {
		t.Fatal(e)
	}
	if e := f.ops.SeedPublishedHashes(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.apply(t, upsert(serviceCfgRow(t, "svc", addresses("tcp", "db:5432"))))
	verified := f.observer.verified
	if _, err := f.ops.Apply(context.Background(), upsert(routerUsing(t, "shop"))); !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("want a pre-flight failure, got %v", err)
	}
	if f.observer.verified != verified {
		t.Fatal("refused change sent to Traefik")
	}
}

func TestAHandWrittenServiceOfAnyProtocolIsNotTakenOver(t *testing.T) {
	for name, external := range map[string]string{
		"tcp service":   "tcp:\n  services:\n    db:\n      loadBalancer:\n        servers:\n          - address: x:1\n",
		"udp service":   "udp:\n  services:\n    db:\n      loadBalancer:\n        servers:\n          - address: x:1\n",
		"http service":  "http:\n  services:\n    db:\n      loadBalancer:\n        servers:\n          - url: http://x\n",
		"its transport": "tcp:\n  serversTransports:\n    db-tcp-transport:\n      dialTimeout: 1s\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if e := os.WriteFile(filepath.Join(f.root, "dynamic", "hand.yml"), []byte(external), 0o600); e != nil {
				t.Fatal(e)
			}
			if _, err := f.ops.Apply(context.Background(), upsert(serviceCfgRow(t, "db", addresses("tcp", "db:5432")))); !errors.Is(err, files.ErrCannotModifyReadOnly) {
				t.Fatalf("want read-only, got %v", err)
			}
		})
	}
}
