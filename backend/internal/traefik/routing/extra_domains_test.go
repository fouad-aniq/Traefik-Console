package routing

import (
	"errors"
	"reflect"
	"testing"

	"github.com/traefik-console/internal/models"
)

func twoResolvers(t *testing.T) Static {
	return registered(t, "entryPoints:\n  secure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n  web: {acme: {httpChallenge: {entryPoint: secure}}}\n")
}

func TestExtraDomainsAddHostsAndCertificateRequests(t *testing.T) {
	static := twoResolvers(t)
	oldDomain := &models.DomainConfig{Main: "example.com", SANs: []string{"*.example.com"}, Method: "resolver", Resolver: "dns"}
	newDomain := models.DomainConfig{Main: "example.net", Method: "resolver", Resolver: "dns"}
	input := models.RouterConfig{DomainName: "old", Subdomain: "git", Service: "gitea", PathPrefix: "/api",
		ExtraDomains: []models.RouterHost{{DomainName: " new ", Subdomain: " GIT "}}}
	got, e := ProjectWith(input, oldDomain, nil, []Binding{{Domain: newDomain}}, static)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got.Domains, []string{"git.example.com", "git.example.net"}) {
		t.Fatalf("hosts %v", got.Domains)
	}
	if got.Rule != "(Host(`git.example.com`) || Host(`git.example.net`)) && PathPrefix(`/api`)" {
		t.Fatalf("rule %s", got.Rule)
	}
	if got.ExtraDomains[0] != (models.RouterHost{DomainName: "new", Subdomain: "git"}) {
		t.Fatalf("extra hostname not normalized %+v", got.ExtraDomains)
	}
	if got.TLS == nil || got.TLS.CertResolver != "dns" {
		t.Fatalf("tls %+v", got.TLS)
	}
	want := []models.TLSDomain{{Main: "example.com", SANs: []string{"*.example.com"}}, {Main: "git.example.net"}}
	if !reflect.DeepEqual(got.TLS.Domains, want) {
		t.Fatalf("certificate requests %+v", got.TLS.Domains)
	}
	if input.ExtraDomains[0].DomainName != " new " {
		t.Fatal("projection mutated the input")
	}
}

func TestExtraDomainsMustShareTheResolver(t *testing.T) {
	static := twoResolvers(t)
	main := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "dns"}
	other := models.DomainConfig{Main: "example.net", Method: "resolver", Resolver: "web"}
	input := models.RouterConfig{DomainName: "a", Service: "app", ExtraDomains: []models.RouterHost{{DomainName: "b"}}}
	if _, e := ProjectWith(input, main, nil, []Binding{{Domain: other}}, static); !errors.Is(e, ErrInvalidRouter) {
		t.Fatal("two certificate resolvers on one router were accepted", e)
	}
}

func TestExtraDomainsMixUploadedCertificatesAndAResolver(t *testing.T) {
	static := twoResolvers(t)
	uploaded := &models.DomainConfig{Main: "intranet.example.com", Method: "files", Certificate: "corp"}
	cert := &models.CertificateConfig{Purpose: "server", Domains: []string{"*.intranet.example.com"}}
	public := models.DomainConfig{Main: "example.net", Method: "resolver", Resolver: "web"}
	input := models.RouterConfig{DomainName: "intranet", Subdomain: "wiki", Service: "wiki", ExtraDomains: []models.RouterHost{{DomainName: "public", Subdomain: "wiki"}}}
	got, e := ProjectWith(input, uploaded, cert, []Binding{{Domain: public}}, static)
	if e != nil {
		t.Fatal(e)
	}
	// The resolver only requests the public host; the uploaded certificate serves the other one.
	if got.TLS.CertResolver != "web" || !reflect.DeepEqual(got.TLS.Domains, []models.TLSDomain{{Main: "wiki.example.net"}}) {
		t.Fatalf("tls %+v", got.TLS)
	}
	// An extra hostname on an uploaded certificate must be covered by it.
	badCert := &models.CertificateConfig{Purpose: "server", Domains: []string{"other.example.org"}}
	input = models.RouterConfig{DomainName: "public", Subdomain: "wiki", Service: "wiki", ExtraDomains: []models.RouterHost{{DomainName: "intranet", Subdomain: "wiki"}}}
	if _, e := ProjectWith(input, &public, nil, []Binding{{Domain: *uploaded, Certificate: badCert}}, static); !errors.Is(e, ErrInvalidRouter) {
		t.Fatal("uncovered extra hostname accepted", e)
	}
	if _, e := ProjectWith(input, &public, nil, []Binding{{Domain: *uploaded, Certificate: cert}}, static); e != nil {
		t.Fatal(e)
	}
}

func TestExtraDomainsRejections(t *testing.T) {
	static := twoResolvers(t)
	main := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "dns"}
	same := models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "dns"}
	cases := map[string]struct {
		cfg    models.RouterConfig
		domain *models.DomainConfig
		extras []Binding
	}{
		"same hostname twice":   {models.RouterConfig{DomainName: "a", Subdomain: "x", Service: "s", ExtraDomains: []models.RouterHost{{DomainName: "a", Subdomain: "x"}}}, main, []Binding{{Domain: same}}},
		"without a main domain": {models.RouterConfig{Domains: []string{"x.example.com"}, Service: "s", ExtraDomains: []models.RouterHost{{DomainName: "a"}}}, nil, []Binding{{Domain: same}}},
		"empty domain":          {models.RouterConfig{DomainName: "a", Service: "s", ExtraDomains: []models.RouterHost{{DomainName: " "}}}, main, []Binding{{Domain: same}}},
		"unresolved domain":     {models.RouterConfig{DomainName: "a", Service: "s", ExtraDomains: []models.RouterHost{{DomainName: "b"}}}, main, nil},
		"different TLS options": {models.RouterConfig{DomainName: "a", Service: "s", ExtraDomains: []models.RouterHost{{DomainName: "b", Subdomain: "y"}}}, main, []Binding{{Domain: models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "dns", Options: "modern"}}}},
	}
	for name, c := range cases {
		if _, e := ProjectWith(c.cfg, c.domain, nil, c.extras, static); !errors.Is(e, ErrInvalidRouter) {
			t.Errorf("%s: accepted (%v)", name, e)
		}
	}
	udp := Static{EntryPoints: map[string]EntryPoint{"dns": {UDP: true}}, Resolvers: map[string]string{}}
	if _, e := Project(models.RouterConfig{Protocol: models.ServiceUDP, Service: "dns", ExtraDomains: []models.RouterHost{{DomainName: "a"}}}, nil, nil, udp); !errors.Is(e, ErrInvalidRouter) {
		t.Fatal("UDP router with hostnames accepted", e)
	}
}

func TestViewResolvesExtraDomainsAndFollowsDomainChanges(t *testing.T) {
	rows := []models.ConfigItemRow{
		{Kind: models.KindDomain, Name: "old", Payload: []byte(`{"main":"example.com","method":"resolver","resolver":"dns"}`)},
		{Kind: models.KindDomain, Name: "new", Payload: []byte(`{"main":"example.net","method":"resolver","resolver":"dns"}`)},
	}
	raw := []byte("entryPoints:\n  secure: {address: ':443'}\ncertificatesResolvers:\n  dns: {acme: {dnsChallenge: {provider: cloudflare}}}\n")
	view, e := NewView(rows, raw)
	if e != nil {
		t.Fatal(e)
	}
	cfg := models.RouterConfig{DomainName: "old", Subdomain: "app", Service: "app", ExtraDomains: []models.RouterHost{{DomainName: "new", Subdomain: "app"}}}
	got, e := view.Project(cfg)
	if e != nil || got.Rule != "(Host(`app.example.com`) || Host(`app.example.net`))" {
		t.Fatal(got.Rule, e)
	}
	// Renaming the extra domain moves its hostname too, without touching the router.
	rows[1].Payload = []byte(`{"main":"example.org","method":"resolver","resolver":"dns"}`)
	if view, e = NewView(rows, raw); e != nil {
		t.Fatal(e)
	}
	if got, e = view.Project(cfg); e != nil || got.Domains[1] != "app.example.org" {
		t.Fatal(got.Domains, e)
	}
	cfg.ExtraDomains[0].DomainName = "gone"
	if _, e = view.Project(cfg); !errors.Is(e, ErrDomainMissing) {
		t.Fatal("missing extra domain not reported", e)
	}
	if !cfg.UsesDomain("gone") || !cfg.UsesDomain("old") || cfg.UsesDomain("new") {
		t.Fatal("UsesDomain", cfg.DomainNames())
	}
}
