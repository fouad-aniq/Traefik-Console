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
	main, sans, err := NormalizeSANs(dom.Main, dom.SANs)
	if err != nil {
		return fmt.Errorf("%w: invalid domain", ErrInvalidRouter)
	}
	base := strings.TrimPrefix(main, "*.")
	target := base
	sub := strings.ToLower(strings.TrimSpace(cfg.Subdomain))
	if sub != "" {
		target = sub + "." + base
	}
	target, err = NormalizeDomain(target)
	if err != nil {
		return fmt.Errorf("%w: invalid subdomain", ErrInvalidRouter)
	}
	cfg.Subdomain = ""
	if sub != "" {
		cfg.Subdomain = strings.TrimSuffix(target, "."+base)
	}
	cfg.Domains = []string{target}
	cfg.TLS = &models.RouterTLS{Enabled: true, Options: strings.TrimSpace(dom.Options)}
	if dom.Method == "resolver" {
		cfg.TLS.CertResolver = strings.TrimSpace(dom.Resolver)
		cfg.TLS.Domains = resolverDomains(target, main, sans)
	}
	return nil
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
