package certificates

import (
	"testing"

	"github.com/traefik-console/internal/models"
)

func domain(name, main, method, source string) models.UIResource[models.DomainConfig] {
	cfg := models.DomainConfig{Name: name, Main: main, Method: method}
	if method == "resolver" {
		cfg.Resolver = source
	} else {
		cfg.Certificate = source
	}
	return models.UIResource[models.DomainConfig]{Name: name, Config: cfg}
}

func router(name, domainName, resolver string, hosts ...string) models.UIResource[models.RouterConfig] {
	tls := &models.RouterTLS{Enabled: true, CertResolver: resolver}
	for _, h := range hosts {
		tls.Domains = append(tls.Domains, models.TLSDomain{Main: h})
	}
	return models.UIResource[models.RouterConfig]{Name: name, Config: models.RouterConfig{DomainName: domainName, Domains: hosts, Rule: "Host(`" + hosts[0] + "`)", TLS: tls}}
}

func names[T any](items []T, name func(T) string) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, name(i))
	}
	return out
}

func TestACMEUsageLinksSubdomainCertificateToZoneAndRouter(t *testing.T) {
	domains := []models.UIResource[models.DomainConfig]{
		domain("domain-aniqhomelab-ovh", "aniqhomelab.ovh", "resolver", "claudflare"),
		domain("domain-fouadaniq-com", "fouadaniq.com", "resolver", "claudflare"),
		domain("lookalike", "homelab.ovh", "resolver", "claudflare"),
		domain("other-resolver", "aniqhomelab.ovh", "resolver", "other"),
	}
	routers := []models.UIResource[models.RouterConfig]{
		router("portainer", "domain-aniqhomelab-ovh", "claudflare", "portainer.aniqhomelab.ovh"),
		router("other", "domain-aniqhomelab-ovh", "claudflare", "other.aniqhomelab.ovh"),
		router("wrong-resolver", "", "other", "portainer.aniqhomelab.ovh"),
		{Name: "api@internal", Config: models.RouterConfig{Rule: "PathPrefix(`/api`)"}},
	}
	u := ACMEUsage([]string{"portainer.aniqhomelab.ovh"}, "claudflare", domains, routers)
	if got := names(u.Domains, func(d models.DomainRef) string { return d.Name }); len(got) != 1 || got[0] != "domain-aniqhomelab-ovh" {
		t.Fatal("domains", got)
	}
	if got := names(u.Routers, func(r models.RouterRef) string { return r.Name }); len(got) != 1 || got[0] != "portainer" {
		t.Fatal("routers", got)
	}
	if r := u.Routers[0]; r.Rule != "Host(`portainer.aniqhomelab.ovh`)" || !r.TLS {
		t.Fatal("router summary", r)
	}
}

func TestUploadedUsageAndResolversInUse(t *testing.T) {
	domains := []models.UIResource[models.DomainConfig]{domain("shop", "shop.example", "files", "shop-cert"), domain("blog", "blog.example", "resolver", "le")}
	routers := []models.UIResource[models.RouterConfig]{router("store", "shop", "", "www.shop.example"), router("news", "blog", "le", "news.blog.example")}
	u := UploadedUsage("shop-cert", domains, routers)
	if len(u.Domains) != 1 || u.Domains[0].Source != "shop-cert" || len(u.Routers) != 1 || u.Routers[0].Name != "store" {
		t.Fatal(u)
	}
	if none := UploadedUsage("unused", domains, routers); none.Domains == nil || len(none.Domains)+len(none.Routers) != 0 {
		t.Fatal("unused certificate must report empty, non-nil usage", none)
	}
	used := ResolversInUse(domains, routers)
	if !used["le"] || len(used) != 1 {
		t.Fatal(used)
	}
}

func TestCoversWildcardsOneLabelDeep(t *testing.T) {
	for host, want := range map[string]bool{"app.example.com": true, "a.b.example.com": false, "example.com": false, "APP.example.com.": true} {
		if Covers([]string{"*.example.com"}, host) != want {
			t.Fatal(host)
		}
	}
}

func TestACMEUsageEdgeCases(t *testing.T) {
	zone := domain("zone", "example.com", "resolver", "le")
	zone.Config.SANs = []string{"example.net"}
	external := router("ext", "", "le", "app.example.com")
	external.ReadOnly, external.Origin = true, "external"
	noTLS := models.UIResource[models.RouterConfig]{Name: "plain", Config: models.RouterConfig{Domains: []string{"app.example.com"}}}
	sanRouter := models.UIResource[models.RouterConfig]{Name: "san", Config: models.RouterConfig{TLS: &models.RouterTLS{CertResolver: "le", Domains: []models.TLSDomain{{Main: "other.example.org", SANs: []string{"www.example.com"}}}}}}
	routers := []models.UIResource[models.RouterConfig]{noTLS, external, sanRouter}
	domains := []models.UIResource[models.DomainConfig]{zone}

	t.Run("wildcard certificate covers routers one label deep", func(t *testing.T) {
		u := ACMEUsage([]string{"*.example.com"}, "le", domains, routers)
		got := names(u.Routers, func(r models.RouterRef) string { return r.Name })
		if len(got) != 2 || got[0] != "ext" || got[1] != "san" {
			t.Fatal("routers", got)
		}
		if len(u.Domains) != 1 || u.Domains[0].Name != "zone" {
			t.Fatal("wildcard of the zone must link the zone domain", u.Domains)
		}
	})
	t.Run("external routers are listed read-only", func(t *testing.T) {
		u := ACMEUsage([]string{"app.example.com"}, "le", domains, routers)
		if len(u.Routers) != 1 || !u.Routers[0].ReadOnly || !u.Routers[0].TLS {
			t.Fatal(u.Routers)
		}
	})
	t.Run("router without TLS never uses a certificate", func(t *testing.T) {
		u := ACMEUsage([]string{"app.example.com"}, "le", nil, []models.UIResource[models.RouterConfig]{noTLS})
		if len(u.Routers) != 0 {
			t.Fatal(u.Routers)
		}
	})
	t.Run("a SAN of the domain is a zone too", func(t *testing.T) {
		u := ACMEUsage([]string{"api.example.net"}, "le", domains, nil)
		if len(u.Domains) != 1 {
			t.Fatal(u.Domains)
		}
	})
	t.Run("router's domain is linked even if its own method differs", func(t *testing.T) {
		filesZone := domain("files-zone", "example.org", "files", "uploaded")
		r := router("via", "files-zone", "le", "app.example.org")
		u := ACMEUsage([]string{"app.example.org"}, "le", []models.UIResource[models.DomainConfig]{filesZone}, []models.UIResource[models.RouterConfig]{r})
		if len(u.Domains) != 1 || u.Domains[0].Source != "uploaded" {
			t.Fatal(u.Domains)
		}
	})
	t.Run("results are sorted and never nil", func(t *testing.T) {
		u := ACMEUsage([]string{"nothing.invalid"}, "le", domains, routers)
		if u.Domains == nil || u.Routers == nil {
			t.Fatal("empty usage must serialize as [] not null")
		}
		b := router("b", "", "le", "x.example.com")
		a := router("a", "", "le", "x.example.com")
		u = ACMEUsage([]string{"x.example.com"}, "le", nil, []models.UIResource[models.RouterConfig]{b, a})
		if u.Routers[0].Name != "a" {
			t.Fatal("not sorted", u.Routers)
		}
	})
}

func TestResolversInUseIgnoresUnrelatedReferences(t *testing.T) {
	used := ResolversInUse(
		[]models.UIResource[models.DomainConfig]{domain("f", "f.example", "files", "le")}, // a certificate named like a resolver
		[]models.UIResource[models.RouterConfig]{{Name: "plain", Config: models.RouterConfig{}}},
	)
	if len(used) != 0 {
		t.Fatal(used)
	}
}
