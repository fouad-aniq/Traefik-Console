package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/core/domains"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

type listedDomains struct {
	domains.DomainManager
	items []models.UIResource[models.DomainConfig]
	err   error
}

func (f listedDomains) ListDomains(context.Context) ([]models.UIResource[models.DomainConfig], error) {
	return f.items, f.err
}

type listedRouters struct {
	routers.RouterManager
	items []models.UIResource[models.RouterConfig]
	err   error
}

func (f listedRouters) ListRouters(context.Context) ([]models.UIResource[models.RouterConfig], error) {
	return f.items, f.err
}

type listedCertificates struct {
	certificates.CertificateManager
	items []models.UIResource[models.CertificateConfig]
}

func (f listedCertificates) ListCertificates(context.Context) ([]models.UIResource[models.CertificateConfig], error) {
	return f.items, nil
}

// The user's homelab: one zone domain on the claudflare resolver, a portainer router under it,
// a domain on an uploaded certificate with its own router, and an internal router without TLS.
func usageDomains() []models.UIResource[models.DomainConfig] {
	return []models.UIResource[models.DomainConfig]{
		{Name: "domain-aniqhomelab-ovh", Config: models.DomainConfig{Name: "domain-aniqhomelab-ovh", Main: "aniqhomelab.ovh", Method: "resolver", Resolver: "claudflare"}},
		{Name: "shop", Config: models.DomainConfig{Name: "shop", Main: "shop.example", Method: "files", Certificate: "shop-cert"}},
	}
}

func usageRouters() []models.UIResource[models.RouterConfig] {
	return []models.UIResource[models.RouterConfig]{
		{Name: "portainer", Config: models.RouterConfig{DomainName: "domain-aniqhomelab-ovh", Domains: []string{"portainer.aniqhomelab.ovh"}, EntryPoints: []string{"websecure"}, Rule: "Host(`portainer.aniqhomelab.ovh`)", Service: "portainer", TLS: &models.RouterTLS{Enabled: true, CertResolver: "claudflare", Domains: []models.TLSDomain{{Main: "portainer.aniqhomelab.ovh"}}}}},
		{Name: "store", Config: models.RouterConfig{DomainName: "shop", Domains: []string{"www.shop.example"}, Rule: "Host(`www.shop.example`)", Service: "store", TLS: &models.RouterTLS{Enabled: true}}},
		{Name: "api@internal", Origin: "external", ReadOnly: true, Config: models.RouterConfig{Rule: "PathPrefix(`/api`)", Service: "api@internal"}},
	}
}

func serve(t *testing.T, d listedDomains, r listedRouters, path string) *httptest.ResponseRecorder {
	t.Helper()
	deps := routeDependencies(t, &routeCalls{})
	deps.Domains, deps.Routers = d, r
	deps.Certificates = listedCertificates{items: []models.UIResource[models.CertificateConfig]{
		{Name: "shop-cert", Config: models.CertificateConfig{Name: "shop-cert", Purpose: "server"}},
		{Name: "spare", Config: models.CertificateConfig{Name: "spare", Purpose: "server"}},
	}}
	deps.ReadStaticConfig = func() ([]byte, error) {
		return []byte("certificatesResolvers:\n  claudflare:\n    acme:\n      storage: /etc/traefik/acme/cf.json\n  unused:\n    acme:\n      storage: /etc/traefik/acme/unused.json\n"), nil
	}
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost"+path, nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w
}

func TestACMEInventoryReportsWhichResolversAreInUse(t *testing.T) {
	w := serve(t, listedDomains{items: usageDomains()}, listedRouters{items: usageRouters()}, "/api/v1/acme/certificates")
	var inv status.ACMEInventory
	if e := json.Unmarshal(w.Body.Bytes(), &inv); e != nil {
		t.Fatal(e)
	}
	inUse := map[string]bool{}
	for _, o := range inv.Observations {
		inUse[o.Resolver] = o.InUse
	}
	if len(inUse) != 2 || !inUse["claudflare"] || inUse["unused"] {
		t.Fatal("in_use wrong", inv.Observations)
	}
}

func TestACMEInventoryKeepsEveryWarningWhenConsumersAreUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		d listedDomains
		r listedRouters
	}{
		"domains fail": {listedDomains{err: errors.New("db down")}, listedRouters{items: usageRouters()}},
		"routers fail": {listedDomains{items: usageDomains()}, listedRouters{err: status.ErrObservationUnavailable}},
	} {
		t.Run(name, func(t *testing.T) {
			var inv status.ACMEInventory
			_ = json.Unmarshal(serve(t, tc.d, tc.r, "/api/v1/acme/certificates").Body.Bytes(), &inv)
			for _, o := range inv.Observations {
				if !o.InUse {
					t.Fatal("a warning could be hidden while usage is unknown", o)
				}
			}
		})
	}
}

func TestAnnotateACMEInventoryAddsUsageToEachCertificate(t *testing.T) {
	inv := status.ACMEInventory{Certificates: []status.CertificateInfo{
		{ResolverName: "claudflare", MainDomain: "portainer.aniqhomelab.ovh"},
		{ResolverName: "claudflare", MainDomain: "example.aniqhomelab.ovh"},
	}}
	annotateACMEInventory(&inv, usageDomains(), usageRouters(), true)
	portainer, example := inv.Certificates[0].UsedBy, inv.Certificates[1].UsedBy
	if portainer == nil || len(portainer.Domains) != 1 || portainer.Domains[0].Name != "domain-aniqhomelab-ovh" || len(portainer.Routers) != 1 || portainer.Routers[0].Name != "portainer" {
		t.Fatal("portainer certificate usage", portainer)
	}
	// No router requests example.aniqhomelab.ovh, but it is a name inside the zone the domain owns.
	if example == nil || len(example.Domains) != 1 || len(example.Routers) != 0 {
		t.Fatal("example certificate usage", example)
	}

	unknown := status.ACMEInventory{Certificates: []status.CertificateInfo{{ResolverName: "claudflare", MainDomain: "portainer.aniqhomelab.ovh"}}}
	annotateACMEInventory(&unknown, nil, nil, false)
	if unknown.Certificates[0].UsedBy != nil {
		t.Fatal("unknown usage must be absent, not empty")
	}
}

func TestUploadedCertificatesCarryUsage(t *testing.T) {
	w := serve(t, listedDomains{items: usageDomains()}, listedRouters{items: usageRouters()}, "/api/v1/certificates")
	var items []models.UIResource[models.CertificateConfig]
	if e := json.Unmarshal(w.Body.Bytes(), &items); e != nil {
		t.Fatal(e)
	}
	byName := map[string]*models.CertificateUsage{}
	for _, c := range items {
		byName[c.Name] = c.Config.UsedBy
	}
	shop := byName["shop-cert"]
	if shop == nil || len(shop.Domains) != 1 || shop.Domains[0].Source != "shop-cert" || len(shop.Routers) != 1 || shop.Routers[0].Name != "store" {
		t.Fatal("shop-cert usage", shop)
	}
	// Unused is reported as empty lists (known, nothing uses it), not as absent.
	if spare := byName["spare"]; spare == nil || len(spare.Domains)+len(spare.Routers) != 0 {
		t.Fatal("spare usage", spare)
	}
	if !containsJSON(w.Body.Bytes(), `"used_by":{"domains":[],"routers":[]}`) {
		t.Fatal("empty usage must serialize as empty arrays", w.Body.String())
	}
}

func TestUploadedCertificatesWithoutUsageWhenDomainsFail(t *testing.T) {
	w := serve(t, listedDomains{err: errors.New("db down")}, listedRouters{items: usageRouters()}, "/api/v1/certificates")
	if containsJSON(w.Body.Bytes(), `"used_by"`) {
		t.Fatal("usage reported although the domains could not be read", w.Body.String())
	}
}

func containsJSON(body []byte, fragment string) bool {
	return json.Valid(body) && strings.Contains(string(body), fragment)
}

func TestDomainsListCarriesUsageAndCertificateState(t *testing.T) {
	w := serve(t, listedDomains{items: usageDomains()}, listedRouters{items: usageRouters()}, "/api/v1/domains")
	var items []models.UIResource[models.DomainConfig]
	if e := json.Unmarshal(w.Body.Bytes(), &items); e != nil {
		t.Fatal(e)
	}
	byName := map[string]models.DomainConfig{}
	for _, d := range items {
		byName[d.Name] = d.Config
	}
	zone := byName["domain-aniqhomelab-ovh"]
	if zone.Usage == nil || len(zone.Usage.Routers) != 1 || zone.Usage.Routers[0].Name != "portainer" || zone.Usage.Routers[0].Rule == "" {
		t.Fatal("zone usage", zone.Usage)
	}
	// No ACME storage is mapped in this test: the state must say unknown, not "no certificate".
	if zone.CertificateState != models.CertStateUnavailable {
		t.Fatal("zone state", zone.CertificateState)
	}
	shop := byName["shop"]
	if shop.Usage == nil || len(shop.Usage.Certificates) != 1 || shop.Usage.Certificates[0].ID != "shop-cert" {
		t.Fatal("shop usage", shop.Usage)
	}
}

func TestDomainsListStillServedWhenRoutersCannotBeRead(t *testing.T) {
	w := serve(t, listedDomains{items: usageDomains()}, listedRouters{err: status.ErrObservationUnavailable}, "/api/v1/domains")
	if containsJSON(w.Body.Bytes(), `"usage"`) || !containsJSON(w.Body.Bytes(), `"certificate_state"`) {
		t.Fatal("usage must be absent, the certificate state still computed", w.Body.String())
	}
}

type listedMiddlewares struct {
	middlewares.MiddlewareManager
	mws    []models.UIResource[models.MiddlewareConfig]
	chains []models.UIResource[models.ChainConfig]
}

func (f listedMiddlewares) ListMiddlewares(context.Context) ([]models.UIResource[models.MiddlewareConfig], error) {
	return f.mws, nil
}
func (f listedMiddlewares) ListChains(context.Context) ([]models.UIResource[models.ChainConfig], error) {
	return f.chains, nil
}

func serveMiddlewares(t *testing.T, routersErr error, path string) []byte {
	t.Helper()
	deps := routeDependencies(t, &routeCalls{})
	deps.Routers = listedRouters{err: routersErr, items: []models.UIResource[models.RouterConfig]{
		{Name: "portainer", Config: models.RouterConfig{Rule: "Host(`p.example`)", Middlewares: []string{"basic-chain@file", "crowdsec"}}},
	}}
	deps.Middlewares = listedMiddlewares{
		mws:    []models.UIResource[models.MiddlewareConfig]{{Name: "crowdsec", Config: models.MiddlewareConfig{Type: "plugin", Spec: map[string]any{}}}, {Name: "auth", Config: models.MiddlewareConfig{Type: "basicAuth", Spec: map[string]any{}}}, {Name: "spare", Config: models.MiddlewareConfig{Type: "headers", Spec: map[string]any{}}}},
		chains: []models.UIResource[models.ChainConfig]{{Name: "basic-chain", Config: models.ChainConfig{Middlewares: []string{"auth", "crowdsec"}}}},
	}
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost"+path, nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

func TestMiddlewaresAndChainsCarryTheirUsage(t *testing.T) {
	var mws []models.UIResource[models.MiddlewareConfig]
	if e := json.Unmarshal(serveMiddlewares(t, nil, "/api/v1/middlewares"), &mws); e != nil {
		t.Fatal(e)
	}
	by := map[string]*models.MiddlewareUsage{}
	for _, m := range mws {
		by[m.Name] = m.Config.UsedBy
	}
	if u := by["crowdsec"]; u == nil || len(u.Routers) != 1 || len(u.Chains) != 1 {
		t.Fatal("crowdsec is used by the portainer router and the basic-chain chain", u)
	}
	if u := by["auth"]; u == nil || len(u.Routers) != 0 || len(u.Chains) != 1 || u.Chains[0].Members[0] != "auth" {
		t.Fatal("auth is used by the chain only", u)
	}
	if u := by["spare"]; u == nil || len(u.Routers)+len(u.Chains) != 0 {
		t.Fatal("spare is known unused", u)
	}
	var chains []models.UIResource[models.ChainConfig]
	if e := json.Unmarshal(serveMiddlewares(t, nil, "/api/v1/chains"), &chains); e != nil {
		t.Fatal(e)
	}
	if u := chains[0].Config.UsedBy; u == nil || len(u.Routers) != 1 || u.Routers[0].Name != "portainer" {
		t.Fatal("basic-chain is used through basic-chain@file", u)
	}
}

func TestMiddlewaresListedWithoutUsageWhenRoutersCannotBeRead(t *testing.T) {
	for _, path := range []string{"/api/v1/middlewares", "/api/v1/chains"} {
		if body := serveMiddlewares(t, status.ErrObservationUnavailable, path); containsJSON(body, `"used_by"`) {
			t.Fatal(path, "reported usage it could not read", string(body))
		}
	}
}
