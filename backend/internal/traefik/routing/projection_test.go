package routing

import (
	"errors"
	"fmt"
	"github.com/traefik-console/internal/models"
	"reflect"
	"testing"
)

func registered(t *testing.T, raw string) Static {
	t.Helper()
	s, e := ParseStatic([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestProjectCanonicalHostReferencesAndMiddlewareOrder(t *testing.T) {
	static := registered(t, "entryPoints:\n  secure: {address: ':8443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n")
	domain := &models.DomainConfig{Main: "*.example.com", Method: "resolver", Resolver: "dns"}
	input := models.RouterConfig{DomainName: " site ", Subdomain: " Café ", Service: " app@docker ", Middlewares: []string{" security ", " auth-chain@file ", " security "}, PathPrefix: " api "}
	got, e := Project(input, domain, nil, static)
	if e != nil {
		t.Fatal(e)
	}
	if got.Subdomain != "xn--caf-dma" || got.Domains[0] != "xn--caf-dma.example.com" || got.Service != "app@docker" || got.PathPrefix != "/api" || got.EntryPoints[0] != "secure" {
		t.Fatalf("bad projection %+v", got)
	}
	if !reflect.DeepEqual(got.Middlewares, []string{"security", "auth-chain@file", "security"}) || input.Middlewares[0] != " security " {
		t.Fatal("order/repeats/input ownership changed")
	}
	if got.Rule != "Host(`xn--caf-dma.example.com`) && PathPrefix(`/api`)" {
		t.Fatal(got.Rule)
	}
}
func TestProjectRejectsUncoveredHostsAndResolverGaps(t *testing.T) {
	static := registered(t, "entryPoints:\n  websecure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n  http: {acme: {httpChallenge: {entryPoint: web}}}\n")
	cases := []struct {
		name   string
		cfg    models.RouterConfig
		domain *models.DomainConfig
	}{
		{"manual-uncovered-request", models.RouterConfig{Domains: []string{"app.example.com"}, Service: "app", TLS: &models.RouterTLS{Enabled: true, CertResolver: "dns", Domains: []models.TLSDomain{{Main: "example.com"}}}}, nil},
		{"imported-uncovered-host", models.RouterConfig{DomainName: "d", Subdomain: "app", Service: "app"}, &models.DomainConfig{Main: "example.com", Method: "files", Certificate: "cert"}},
		{"wildcard-http", models.RouterConfig{DomainName: "d", Subdomain: "app", Service: "app"}, &models.DomainConfig{Main: "*.example.com", Method: "resolver", Resolver: "http"}},
		{"manual-absent-resolver", models.RouterConfig{Domains: []string{"example.com"}, Service: "app", TLS: &models.RouterTLS{Enabled: true, CertResolver: "missing"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Project(c.cfg, c.domain, nil, static); e == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}
func TestProjectEntrypointSelection(t *testing.T) {
	cfg := models.RouterConfig{Domains: []string{"example.com"}, Service: "app"}
	multi := registered(t, "entryPoints:\n  admin: {address: ':8080'}\n  public: {address: ':8081'}\n  udp: {address: ':9000/udp'}\n")
	if _, e := Project(cfg, nil, nil, multi); e == nil {
		t.Fatal("ambiguous listener choice silently exposed service")
	}
	cfg.EntryPoints = []string{"udp"}
	if _, e := Project(cfg, nil, nil, multi); e == nil {
		t.Fatal("HTTP route accepted UDP listener")
	}
	cfg.EntryPoints = nil
	defaults := registered(t, "entryPoints:\n  admin: {address: ':8080'}\n  public: {address: ':8081', asDefault: true}\n")
	got, e := Project(cfg, nil, nil, defaults)
	if e != nil || got.EntryPoints[0] != "public" {
		t.Fatal(got, e)
	}
}

func TestResolverDomainsFollowEachRouterWithoutParentSANs(t *testing.T) {
	static := registered(t, "entryPoints:\n  secure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n  http: {acme: {httpChallenge: {entryPoint: web}}}\n  alpn: {acme: {tlsChallenge: {}}}\n")
	for _, resolver := range []string{"dns", "http", "alpn"} {
		t.Run(resolver, func(t *testing.T) {
			domain := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: resolver}
			for _, sub := range []string{"", "portainer", "another", " Café "} {
				cfg, e := Project(models.RouterConfig{DomainName: "site", Subdomain: sub, Service: "app"}, domain, nil, static)
				if e != nil {
					t.Fatal(e)
				}
				if len(cfg.TLS.Domains) != 1 || cfg.TLS.Domains[0].Main != cfg.Domains[0] || len(cfg.TLS.Domains[0].SANs) != 0 {
					t.Fatal("router host not used for certificate request", cfg)
				}
			}
			if len(domain.SANs) != 0 || domain.Main != "example.com" {
				t.Fatal("parent preference mutated")
			}
		})
	}
}
func TestExplicitCertificateBundlesArePreservedAlongsideHostRequests(t *testing.T) {
	static := registered(t, "entryPoints:\n  secure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n")
	cases := []struct {
		name     string
		domain   models.DomainConfig
		sub      string
		requests int
	}{
		{"covered-wildcard", models.DomainConfig{Main: "*.example.com"}, "app", 1},
		{"wildcard-apex", models.DomainConfig{Main: "*.example.com"}, "", 2},
		{"explicit-SAN", models.DomainConfig{Main: "example.com", SANs: []string{"alias.example.com"}}, "future", 2},
		{"covered-SAN", models.DomainConfig{Main: "example.com", SANs: []string{"alias.example.com"}}, "alias", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.domain.Method = "resolver"
			c.domain.Resolver = "dns"
			cfg, e := Project(models.RouterConfig{DomainName: "site", Subdomain: c.sub, Service: "app"}, &c.domain, nil, static)
			if e != nil || len(cfg.TLS.Domains) != c.requests {
				t.Fatal(cfg, e)
			}
			if cfg.TLS.Domains[0].Main != c.domain.Main || !reflect.DeepEqual(cfg.TLS.Domains[0].SANs, c.domain.SANs) {
				t.Fatal("explicit bundle changed")
			}
			if c.requests == 2 && cfg.TLS.Domains[1].Main != cfg.Domains[0] {
				t.Fatal("missing independent router certificate")
			}
		})
	}
}
func TestImportedCertificateCoverageRemainsRequired(t *testing.T) {
	static := registered(t, "entryPoints:\n  secure: {address: ':443'}\n")
	domain := &models.DomainConfig{Main: "example.com", Method: "files", Certificate: "uploaded"}
	cert := &models.CertificateConfig{Purpose: "server", Domains: []string{"example.com"}}
	input := models.RouterConfig{DomainName: "site", Subdomain: "portainer", Service: "app"}
	if _, e := Project(input, domain, cert, static); e == nil {
		t.Fatal("imported certificate was treated like an issuer")
	}
	cert.Domains = append(cert.Domains, "portainer.example.com")
	cfg, e := Project(input, domain, cert, static)
	if e != nil || len(cfg.TLS.Domains) != 0 || cfg.TLS.CertResolver != "" {
		t.Fatal(cfg, e)
	}
}
func TestFullExplicitSANBundleDoesNotBlockNewRouter(t *testing.T) {
	static := registered(t, "entryPoints:\n  secure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n")
	domain := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "dns"}
	for i := 0; i < 50; i++ {
		domain.SANs = append(domain.SANs, fmt.Sprintf("old%d.example.com", i))
	}
	cfg, e := Project(models.RouterConfig{DomainName: "site", Subdomain: "future", Service: "app"}, domain, nil, static)
	if e != nil || len(cfg.TLS.Domains) != 2 || len(cfg.TLS.Domains[0].SANs) != 50 || cfg.TLS.Domains[1].Main != "future.example.com" {
		t.Fatal(cfg, e)
	}
}

func TestProjectionRefusesNoBackendWithoutAMiddleware(t *testing.T) {
	static := Static{EntryPoints: map[string]EntryPoint{"web": {TCP: true}}, Resolvers: map[string]string{}}
	base := models.RouterConfig{Domains: []string{"a.example"}, EntryPoints: []string{"web"}, Service: models.NoopService}
	if _, err := Project(base, nil, nil, static); !errors.Is(err, ErrInvalidRouter) {
		t.Fatalf("error = %v, want ErrInvalidRouter", err)
	}
	base.Middlewares = []string{"to-https"}
	got, err := Project(base, nil, nil, static)
	if err != nil || got.Service != models.NoopService {
		t.Fatalf("%v %+v", err, got)
	}
}
