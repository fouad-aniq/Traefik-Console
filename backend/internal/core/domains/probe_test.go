package domains

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traefik-console/internal/models"
)

// tlsTarget is a TLS listener like Traefik's websecure: it serves one certificate and records the SNI.
type tlsTarget struct {
	addr string
	mu   sync.Mutex
	sni  []string
}

func (t *tlsTarget) lastSNI() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sni) == 0 {
		return ""
	}
	return t.sni[len(t.sni)-1]
}

func startTLSTarget(t *testing.T, names ...string) *tlsTarget {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	target := &tlsTarget{}
	cfg := &tls.Config{GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		target.mu.Lock()
		target.sni = append(target.sni, h.ServerName)
		target.mu.Unlock()
		return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
	}}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	target.addr = ln.Addr().String()
	return target
}

func routerRow(t *testing.T, name, domainName string, hosts ...string) *models.ConfigItemRow {
	raw, _ := json.Marshal(models.RouterConfig{DomainName: domainName, Domains: hosts, Service: name})
	return &models.ConfigItemRow{ID: "router:" + name, Kind: models.KindRouter, Name: name, Payload: raw}
}

func probeManager(t *testing.T, target string, rows ...*models.ConfigItemRow) DomainManager {
	store := newStubStore(rows...)
	return NewDomainManager(store, stubFiles{}, map[string]string{"websecure": target}, fakeApplier{store})
}

func zoneRow(t *testing.T, main string) *models.ConfigItemRow {
	return activeRow(t, models.KindDomain, "zone", models.DomainConfig{Name: "zone", Main: main, Method: "resolver", Resolver: "le", EntryPoint: "websecure"})
}

func TestProbeChecksAHostnameARouterServesNotTheZoneApex(t *testing.T) {
	srv := startTLSTarget(t, "portainer.example.com")
	mgr := probeManager(t, srv.addr, zoneRow(t, "example.com"), routerRow(t, "portainer", "zone", "portainer.example.com"))
	res, err := mgr.ProbeDomain(context.Background(), "zone")
	if err != nil {
		t.Fatal(err)
	}
	if srv.lastSNI() != "portainer.example.com" || res.Hostname != "portainer.example.com" {
		t.Fatalf("probed %q (SNI %q), want the routed host", res.Hostname, srv.lastSNI())
	}
	if res.State != "observed" || res.Error != "" || res.Fingerprint == "" {
		t.Fatal("served certificate not observed", res)
	}
	if res.Trust != "untrusted" {
		t.Fatal("a self-signed certificate must read as untrusted", res.Trust)
	}
}

func TestProbePrefersTheMainNameWhenARouterServesIt(t *testing.T) {
	srv := startTLSTarget(t, "example.com", "a.example.com")
	mgr := probeManager(t, srv.addr, zoneRow(t, "example.com"), routerRow(t, "a", "zone", "a.example.com"), routerRow(t, "apex", "zone", "example.com"))
	res, err := mgr.ProbeDomain(context.Background(), "zone")
	if err != nil || res.Hostname != "example.com" || srv.lastSNI() != "example.com" {
		t.Fatal(res, err)
	}
}

func TestProbeWildcardDomainChecksAConcreteRoutedHost(t *testing.T) {
	srv := startTLSTarget(t, "*.example.com")
	mgr := probeManager(t, srv.addr, zoneRow(t, "*.example.com"), routerRow(t, "app", "zone", "*.example.com", "app.example.com"))
	res, err := mgr.ProbeDomain(context.Background(), "zone")
	if err != nil || res.Hostname != "app.example.com" || res.State != "observed" {
		t.Fatal(res, err)
	}
}

func TestProbeMismatchWhenTraefikServesAnotherCertificate(t *testing.T) {
	srv := startTLSTarget(t, "TRAEFIK DEFAULT CERT")
	mgr := probeManager(t, srv.addr, zoneRow(t, "example.com"), routerRow(t, "a", "zone", "a.example.com"))
	if res, err := mgr.ProbeDomain(context.Background(), "zone"); err != nil || res.State != "mismatch" {
		t.Fatal(res, err)
	}
}

func TestProbeNeedsARoutedConcreteHost(t *testing.T) {
	srv := startTLSTarget(t, "example.com")
	for name, rows := range map[string][]*models.ConfigItemRow{
		"no router":          {zoneRow(t, "example.com")},
		"router elsewhere":   {zoneRow(t, "example.com"), routerRow(t, "a", "other", "a.example.com")},
		"only wildcard host": {zoneRow(t, "*.example.com"), routerRow(t, "a", "zone", "*.example.com")},
	} {
		if _, err := probeManager(t, srv.addr, rows...).ProbeDomain(context.Background(), "zone"); !errors.Is(err, ErrDomainProbeInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := probeManager(t, "", zoneRow(t, "example.com"), routerRow(t, "a", "zone", "a.example.com")).ProbeDomain(context.Background(), "zone"); !errors.Is(err, ErrDomainProbeInvalid) {
		t.Fatal("no registered target accepted", err)
	}
}

func TestProbeUnreachableTargetExplainsTheSetting(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	res, err := probeManager(t, addr, zoneRow(t, "example.com"), routerRow(t, "a", "zone", "a.example.com")).ProbeDomain(context.Background(), "zone")
	if err != nil || res.State != "unreachable" || res.Address != addr {
		t.Fatal(res, err)
	}
	if !strings.Contains(res.Error, addr) || !strings.Contains(res.Error, "TRAEFIK_PROBE_TARGETS") || !strings.Contains(res.Error, "websecure") {
		t.Fatal("unhelpful error", res.Error)
	}
}

func TestSavingADomainNeverStoresComputedFields(t *testing.T) {
	mgr := newTestManager(dnsResolverRow(t, "cf-prod", "dns01"))
	in := models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "cf-prod", UsedBy: []string{"x"}, CertificateState: "valid", Usage: &models.DomainUsage{Routers: []models.RouterRef{{Name: "x"}}}}
	if _, err := mgr.SaveDomain(context.Background(), "d", in, models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	raw := string(mgr.(*domainManager).store.(*stubStore).items["domain:d"].Payload)
	for _, field := range []string{"used_by", "usage", "certificate_state"} {
		if strings.Contains(raw, field) {
			t.Fatalf("%s stored: %s", field, raw)
		}
	}
}
