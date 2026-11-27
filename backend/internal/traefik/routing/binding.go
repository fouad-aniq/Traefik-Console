package routing

import (
	"fmt"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
	"strings"
)

// CompileRouterRule builds the Traefik rule expression: a custom rule wins when
// provided, otherwise Host(...) parts from the domains with an optional PathPrefix.
func CompileRouterRule(domains []string, pathPrefix, customRule string) string {
	if strings.TrimSpace(customRule) != "" {
		return strings.TrimSpace(customRule)
	}

	var hostParts []string
	for _, d := range domains {
		clean := strings.TrimSpace(strings.ToLower(d))
		if clean != "" {
			hostParts = append(hostParts, fmt.Sprintf("Host(`%s`)", clean))
		}
	}
	if len(hostParts) == 0 {
		return ""
	}

	hostExpr := hostParts[0]
	if len(hostParts) > 1 {
		hostExpr = "(" + strings.Join(hostParts, " || ") + ")"
	}

	cleanPrefix := strings.TrimSpace(pathPrefix)
	if cleanPrefix != "" {
		if !strings.HasPrefix(cleanPrefix, "/") {
			cleanPrefix = "/" + cleanPrefix
		}
		return fmt.Sprintf("%s && PathPrefix(`%s`)", hostExpr, cleanPrefix)
	}

	return hostExpr
}

// ApplyDomainBinding derives a router's host domains and TLS block from the
// managed domain item it references. Subdomain "app" + Main "*.example.com"
// targets "app.example.com". Ordinary resolver bindings request the actual
// router host; explicitly configured wildcard/SAN bundles remain independent.
func ApplyDomainBinding(cfg *models.RouterConfig, dom models.DomainConfig) error {
	main, sans, target, sub, err := hostOn(dom, cfg.Subdomain)
	if err != nil {
		return err
	}
	cfg.Subdomain = sub
	cfg.Domains = []string{target}
	cfg.TLS = &models.RouterTLS{Enabled: true, Options: strings.TrimSpace(dom.Options)}
	if dom.Method == "resolver" {
		cfg.TLS.CertResolver = strings.TrimSpace(dom.Resolver)
		cfg.TLS.Domains = resolverDomains(target, main, sans)
	}
	return nil
}

// hostOn is the hostname a subdomain names inside a managed domain ("app" + "example.com" or
// "*.example.com" -> "app.example.com"; no subdomain -> the domain itself), with the domain's
// normalized names and the canonical subdomain to store.
func hostOn(dom models.DomainConfig, subdomain string) (main string, sans []string, target, sub string, err error) {
	main, sans, err = NormalizeSANs(dom.Main, dom.SANs)
	if err != nil {
		return "", nil, "", "", fmt.Errorf("%w: invalid domain", ErrInvalidRouter)
	}
	base := strings.TrimPrefix(main, "*.")
	target = base
	sub = strings.ToLower(strings.TrimSpace(subdomain))
	if sub != "" {
		target = sub + "." + base
	}
	target, err = NormalizeDomain(target)
	if err != nil {
		return "", nil, "", "", fmt.Errorf("%w: invalid subdomain", ErrInvalidRouter)
	}
	if sub != "" {
		sub = strings.TrimSuffix(target, "."+base)
	}
	return main, sans, target, sub, nil
}

// Binding is a managed domain used by a router, with the uploaded certificate it selects when its
// certificates come from files.
type Binding struct {
	Domain      models.DomainConfig
	Certificate *models.CertificateConfig
}

// applyExtraBindings adds the router's extra hostnames after its main domain was applied: one more
// Host() per hostname, and their certificate requests on the same TLS block. Traefik gives a router a
// single certificate resolver, so every hostname whose certificates come from a resolver must use the
// same one; hostnames with uploaded certificates can be mixed in freely (Traefik serves a matching
// certificate from its store). It returns the hostnames whose certificates come from the resolver.
func applyExtraBindings(cfg *models.RouterConfig, primary models.DomainConfig, extras []Binding) ([]string, error) {
	var resolverHosts []string
	if primary.Method == "resolver" {
		resolverHosts = append(resolverHosts, cfg.Domains...)
	}
	seen := map[string]bool{}
	for _, h := range cfg.Domains {
		seen[h] = true
	}
	for i := range cfg.ExtraDomains {
		host, b := &cfg.ExtraDomains[i], extras[i]
		main, sans, target, sub, err := hostOn(b.Domain, host.Subdomain)
		if err != nil {
			return nil, err
		}
		host.Subdomain = sub
		if seen[target] {
			return nil, fmt.Errorf("%w: hostname %s is listed twice", ErrInvalidRouter, target)
		}
		seen[target] = true
		cfg.Domains = append(cfg.Domains, target)
		if strings.TrimSpace(b.Domain.Options) != cfg.TLS.Options {
			return nil, fmt.Errorf("%w: every hostname of a router must use the same TLS options", ErrInvalidRouter)
		}
		switch b.Domain.Method {
		case "resolver":
			resolver := strings.TrimSpace(b.Domain.Resolver)
			if cfg.TLS.CertResolver == "" {
				cfg.TLS.CertResolver = resolver
			} else if cfg.TLS.CertResolver != resolver {
				return nil, fmt.Errorf("%w: a router has one certificate resolver; %s uses %s and %s uses %s", ErrInvalidRouter, cfg.Domains[0], cfg.TLS.CertResolver, target, resolver)
			}
			for _, request := range resolverDomains(target, main, sans) {
				if !hasRequest(cfg.TLS.Domains, request) {
					cfg.TLS.Domains = append(cfg.TLS.Domains, request)
				}
			}
			resolverHosts = append(resolverHosts, target)
		case "files":
			if b.Certificate == nil || b.Certificate.Purpose != "server" {
				return nil, fmt.Errorf("%w: server certificate missing", ErrInvalidRouter)
			}
			if !validation.NamesCover(b.Certificate.Domains, target) {
				return nil, fmt.Errorf("%w: certificate does not cover router host", ErrInvalidRouter)
			}
		default:
			return nil, fmt.Errorf("%w: invalid domain", ErrInvalidRouter)
		}
	}
	return resolverHosts, nil
}

func hasRequest(requests []models.TLSDomain, r models.TLSDomain) bool {
	for _, have := range requests {
		if have.Main == r.Main && strings.Join(have.SANs, ",") == strings.Join(r.SANs, ",") {
			return true
		}
	}
	return false
}

func resolverDomains(host, main string, sans []string) []models.TLSDomain {
	if len(sans) == 0 && !strings.HasPrefix(main, "*.") {
		return []models.TLSDomain{{Main: host}}
	}
	// Preserve explicitly requested bundles, including wildcards, without making
	// their names an allowlist for future routers. A separate leaf request avoids
	// expanding the bundle or exceeding the existing per-certificate SAN limit.
	requests := []models.TLSDomain{{Main: main, SANs: append([]string(nil), sans...)}}
	if !validation.NamesCover(append([]string{main}, sans...), host) {
		requests = append(requests, models.TLSDomain{Main: host})
	}
	return requests
}

// CompileTCPRule builds a TCP router's rule: a custom rule wins when provided, otherwise
// HostSNI(...) parts from the hostnames. A TCP router has no path to match.
func CompileTCPRule(domains []string, customRule string) string {
	if strings.TrimSpace(customRule) != "" {
		return strings.TrimSpace(customRule)
	}
	var parts []string
	for _, d := range domains {
		if clean := strings.TrimSpace(strings.ToLower(d)); clean != "" {
			parts = append(parts, fmt.Sprintf("HostSNI(`%s`)", clean))
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
}
