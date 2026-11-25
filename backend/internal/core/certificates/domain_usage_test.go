package certificates

import (
	"testing"
	"time"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

func acmeCert(main, status_ string) status.CertificateInfo {
	return status.CertificateInfo{ResolverName: "claudflare", MainDomain: main, Fingerprint: "fp-" + main, Issuer: "Staging", ExpiresAt: time.Date(2027, 1, 11, 0, 0, 0, 0, time.UTC), Status: status_}
}

func inventory(obsState string, certs ...status.CertificateInfo) *status.ACMEInventory {
	return &status.ACMEInventory{Certificates: certs, Observations: []status.ACMEStorageObservation{{Resolver: "claudflare", State: obsState}}}
}

func annotate(d models.UIResource[models.DomainConfig], src DomainSources) models.DomainConfig {
	AnnotateDomain(&d, src)
	return d.Config
}

func TestZoneDomainGetsItsSubdomainCertificatesAndRouters(t *testing.T) {
	zone := domain("domain-aniqhomelab-ovh", "aniqhomelab.ovh", "resolver", "claudflare")
	routers := []models.UIResource[models.RouterConfig]{
		router("portainer", "domain-aniqhomelab-ovh", "claudflare", "portainer.aniqhomelab.ovh"),
		router("elsewhere", "other-domain", "claudflare", "x.other.example"),
	}
	inv := inventory("observed", acmeCert("portainer.aniqhomelab.ovh", "valid"), acmeCert("example.aniqhomelab.ovh", "valid"), acmeCert("unrelated.example", "valid"))
	got := annotate(zone, DomainSources{Routers: routers, ACME: inv, UploadedKnown: true})
	if got.CertificateState != "valid" {
		t.Fatal("state", got.CertificateState)
	}
	if got.Usage == nil || len(got.Usage.Routers) != 1 || got.Usage.Routers[0].Name != "portainer" {
		t.Fatal("routers", got.Usage)
	}
	if len(got.Usage.Certificates) != 2 || got.Usage.Certificates[0].Name != "example.aniqhomelab.ovh" || got.Usage.Certificates[0].ID != "fp-example.aniqhomelab.ovh" || got.Usage.Certificates[0].Source != "acme" {
		t.Fatal("certificates", got.Usage.Certificates)
	}
}

func TestResolverDomainStateIsTheWorstValidity(t *testing.T) {
	zone := domain("z", "example.com", "resolver", "claudflare")
	for _, tc := range []struct {
		statuses []string
		want     string
	}{
		{[]string{"valid", "expiring_soon"}, "expiring"},
		{[]string{"valid", "expired", "expiring_soon"}, "expired"},
		{[]string{"valid"}, "valid"},
	} {
		var certs []status.CertificateInfo
		for i, st := range tc.statuses {
			certs = append(certs, acmeCert(string(rune('a'+i))+".example.com", st))
		}
		if got := annotate(zone, DomainSources{Routers: nil, ACME: inventory("observed", certs...)}).CertificateState; got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.statuses, got, tc.want)
		}
	}
}

func TestResolverDomainWithoutCertificate(t *testing.T) {
	zone := domain("z", "example.com", "resolver", "claudflare")
	for obs, want := range map[string]string{
		"observed":      models.CertStateNotObserved,
		"empty":         models.CertStateNotObserved,
		"uninitialized": models.CertStateNotObserved, // storage exists, nothing issued yet
		"not_mapped":    models.CertStateUnavailable, // cannot conclude anything
		"unreadable":    models.CertStateUnavailable,
	} {
		if got := annotate(zone, DomainSources{ACME: inventory(obs)}).CertificateState; got != want {
			t.Fatalf("%s: got %s want %s", obs, got, want)
		}
	}
	if got := annotate(zone, DomainSources{}).CertificateState; got != models.CertStateUnavailable {
		t.Fatal("unread ACME storage must be unavailable", got)
	}
	// Another resolver's observation says nothing about this one.
	other := &status.ACMEInventory{Observations: []status.ACMEStorageObservation{{Resolver: "le", State: "observed"}}}
	if got := annotate(zone, DomainSources{ACME: other}).CertificateState; got != models.CertStateUnavailable {
		t.Fatal(got)
	}
}

func TestUploadedCertificateDomainStates(t *testing.T) {
	d := domain("shop", "shop.example", "files", "shop-cert")
	d.Config.SANs = []string{"www.shop.example"}
	cert := func(domains ...string) models.UIResource[models.CertificateConfig] {
		return models.UIResource[models.CertificateConfig]{Name: "shop-cert", Config: models.CertificateConfig{Name: "shop-cert", Domains: domains, Status: "expiring", NotAfter: "2027-01-01T00:00:00Z"}}
	}
	for name, tc := range map[string]struct {
		src  DomainSources
		want string
	}{
		"covered":              {DomainSources{UploadedKnown: true, Uploaded: []models.UIResource[models.CertificateConfig]{cert("shop.example", "www.shop.example")}}, "expiring"},
		"wildcard covers SANs": {DomainSources{UploadedKnown: true, Uploaded: []models.UIResource[models.CertificateConfig]{cert("shop.example", "*.shop.example")}}, "expiring"},
		"a name not covered":   {DomainSources{UploadedKnown: true, Uploaded: []models.UIResource[models.CertificateConfig]{cert("shop.example")}}, models.CertStateNotCovered},
		"missing":              {DomainSources{UploadedKnown: true}, models.CertStateMissing},
		"list unreadable":      {DomainSources{UploadedKnown: false}, models.CertStateUnavailable},
	} {
		if got := annotate(d, tc.src).CertificateState; got != tc.want {
			t.Fatalf("%s: got %s want %s", name, got, tc.want)
		}
	}
	got := annotate(d, DomainSources{Routers: []models.UIResource[models.RouterConfig]{}, UploadedKnown: true, Uploaded: []models.UIResource[models.CertificateConfig]{cert("shop.example", "www.shop.example")}})
	if len(got.Usage.Certificates) != 1 || got.Usage.Certificates[0].Source != "uploaded" || got.Usage.Certificates[0].ID != "shop-cert" || !got.Usage.Certificates[0].Covers {
		t.Fatal(got.Usage)
	}
}

func TestDomainUsageUnknownWithoutRoutersButStateStillComputed(t *testing.T) {
	zone := domain("z", "example.com", "resolver", "claudflare")
	got := annotate(zone, DomainSources{ACME: inventory("observed", acmeCert("app.example.com", "valid"))})
	if got.Usage != nil || got.CertificateState != "valid" {
		t.Fatal(got.Usage, got.CertificateState)
	}
	// Known but empty: serialized as empty lists, not absent.
	got = annotate(zone, DomainSources{Routers: []models.UIResource[models.RouterConfig]{}, ACME: inventory("observed")})
	if got.Usage == nil || got.Usage.Routers == nil || got.Usage.Certificates == nil {
		t.Fatal("empty usage must not be nil", got.Usage)
	}
}
