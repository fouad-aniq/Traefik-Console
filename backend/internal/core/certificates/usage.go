package certificates

import (
	"sort"
	"strings"

	"github.com/traefik-console/internal/models"
)

type (
	domainList = []models.UIResource[models.DomainConfig]
	routerList = []models.UIResource[models.RouterConfig]
)

func normalizeHost(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// Covers reports whether one of the certificate names serves host: an exact match or a
// one-label wildcard (*.example.com covers app.example.com, not a.b.example.com).
func Covers(names []string, host string) bool {
	h := normalizeHost(host)
	for _, raw := range names {
		n := normalizeHost(raw)
		if n == h {
			return true
		}
		if suffix, ok := strings.CutPrefix(n, "*"); ok && strings.HasPrefix(suffix, ".") && strings.HasSuffix(h, suffix) {
			if label := strings.TrimSuffix(h, suffix); label != "" && !strings.Contains(label, ".") {
				return true
			}
		}
	}
	return false
}

func routerHosts(r models.RouterConfig) []string {
	var out []string
	if r.TLS != nil {
		for _, d := range r.TLS.Domains {
			out = append(out, d.Main)
			out = append(out, d.SANs...)
		}
	}
	return append(out, r.Domains...)
}

func domainRef(d models.UIResource[models.DomainConfig]) models.DomainRef {
	source := d.Config.Certificate
	if d.Config.Method == "resolver" {
		source = d.Config.Resolver
	}
	return models.DomainRef{Name: d.Name, Main: d.Config.Main, SANs: d.Config.SANs, Source: source, ReadOnly: d.ReadOnly}
}

func routerRef(r models.UIResource[models.RouterConfig]) models.RouterRef {
	return models.RouterRefOf(r)
}

func newUsage() *models.CertificateUsage {
	return &models.CertificateUsage{Domains: []models.DomainRef{}, Routers: []models.RouterRef{}}
}

// ACMEUsage lists what uses an automatic certificate. Routers match when they use the same resolver
// and request a host the certificate covers. A domain is a zone (example.com) whose routes request
// hosts inside it, so it matches when one of those routers belongs to it, or when it uses the same
// resolver and the certificate names the zone itself or a host under it.
func ACMEUsage(names []string, resolver string, domains domainList, routers routerList) *models.CertificateUsage {
	out := newUsage()
	viaRouters := map[string]bool{}
	for _, r := range routers {
		if r.Config.TLS == nil || r.Config.TLS.CertResolver != resolver {
			continue
		}
		for _, h := range routerHosts(r.Config) {
			if Covers(names, h) {
				out.Routers = append(out.Routers, routerRef(r))
				if r.Config.DomainName != "" {
					viaRouters[r.Config.DomainName] = true
				}
				break
			}
		}
	}
	for _, d := range domains {
		if viaRouters[d.Name] || (d.Config.Method == "resolver" && d.Config.Resolver == resolver && namesInZone(names, d.Config)) {
			out.Domains = append(out.Domains, domainRef(d))
		}
	}
	sortUsage(out)
	return out
}

func namesInZone(names []string, d models.DomainConfig) bool {
	for _, zone := range append([]string{d.Main}, d.SANs...) {
		z := normalizeHost(zone)
		if z == "" {
			continue
		}
		for _, raw := range names {
			n := normalizeHost(raw)
			if n == z || n == "*."+z || strings.HasSuffix(n, "."+z) {
				return true
			}
		}
	}
	return false
}

// UploadedUsage lists the domains that select an uploaded certificate (method "files") and the
// routers that belong to those domains.
func UploadedUsage(name string, domains domainList, routers routerList) *models.CertificateUsage {
	out := newUsage()
	selected := map[string]bool{}
	for _, d := range domains {
		if d.Config.Method == "files" && d.Config.Certificate == name {
			selected[d.Name] = true
			out.Domains = append(out.Domains, domainRef(d))
		}
	}
	for _, r := range routers {
		if r.Config.DomainName != "" && selected[r.Config.DomainName] {
			out.Routers = append(out.Routers, routerRef(r))
		}
	}
	sortUsage(out)
	return out
}

// ResolversInUse is the set of ACME resolvers that a router or a domain requests certificates from.
func ResolversInUse(domains domainList, routers routerList) map[string]bool {
	used := map[string]bool{}
	for _, r := range routers {
		if r.Config.TLS != nil && r.Config.TLS.CertResolver != "" {
			used[r.Config.TLS.CertResolver] = true
		}
	}
	for _, d := range domains {
		if d.Config.Method == "resolver" && d.Config.Resolver != "" {
			used[d.Config.Resolver] = true
		}
	}
	return used
}

func sortUsage(u *models.CertificateUsage) {
	sort.Slice(u.Domains, func(i, j int) bool { return u.Domains[i].Name < u.Domains[j].Name })
	sort.Slice(u.Routers, func(i, j int) bool { return u.Routers[i].Name < u.Routers[j].Name })
}
