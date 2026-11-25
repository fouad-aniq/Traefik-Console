package routing

import (
	"fmt"
	"strings"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

func hasMiddleware(names []string) bool {
	for _, name := range names {
		if name != "" {
			return true
		}
	}
	return false
}

// Project produces the same canonical effective configuration for reads, review
// and YAML. It does not mutate slices/pointers owned by the supplied config.
func Project(input models.RouterConfig, domain *models.DomainConfig, certificate *models.CertificateConfig, static Static) (models.RouterConfig, error) {
	cfg := input
	cfg.DomainName = strings.TrimSpace(cfg.DomainName)
	cfg.Service = strings.TrimSpace(cfg.Service)
	cfg.Subdomain = strings.ToLower(strings.TrimSpace(cfg.Subdomain))
	cfg.CustomRule = strings.TrimSpace(cfg.CustomRule)
	cfg.PathPrefix = strings.TrimSpace(cfg.PathPrefix)
	if cfg.Service == "" || cfg.Priority > 9007199254740991 || cfg.Priority < -9007199254740991 {
		return cfg, ErrInvalidRouter
	}
	if strings.ContainsAny(cfg.PathPrefix, "`\\\r\n\x00") {
		return cfg, ErrInvalidRouter
	}
	protocol := models.RouterProtocol(input)
	switch protocol {
	case models.ServiceHTTP:
		cfg.Protocol = ""
	case models.ServiceTCP, models.ServiceUDP:
		cfg.Protocol = protocol
	default:
		return cfg, fmt.Errorf("%w: protocol must be http, tcp or udp", ErrInvalidRouter)
	}
	if protocol == models.ServiceUDP {
		return projectUDP(cfg, static)
	}
	if protocol == models.ServiceTCP {
		if cfg.PathPrefix != "" {
			return cfg, fmt.Errorf("%w: a TCP router has no path to match", ErrInvalidRouter)
		}
		if cfg.Service == models.NoopService {
			return cfg, fmt.Errorf("%w: a TCP router needs a TCP service", ErrInvalidRouter)
		}
	} else if cfg.TLS != nil && cfg.TLS.Passthrough {
		return cfg, fmt.Errorf("%w: TLS passthrough is for TCP routers", ErrInvalidRouter)
	}
	if cfg.PathPrefix != "" && !strings.HasPrefix(cfg.PathPrefix, "/") {
		cfg.PathPrefix = "/" + cfg.PathPrefix
	}
	cfg.Domains = append([]string(nil), cfg.Domains...)
	cfg.EntryPoints = append([]string(nil), cfg.EntryPoints...)
	cfg.Middlewares = append([]string(nil), cfg.Middlewares...)
	passthrough := protocol == models.ServiceTCP && cfg.TLS != nil && cfg.TLS.Passthrough
	if cfg.DomainName != "" && passthrough {
		return cfg, fmt.Errorf("%w: a managed domain terminates TLS, so it cannot be passed through", ErrInvalidRouter)
	}
	if cfg.DomainName != "" {
		if domain == nil {
			return cfg, fmt.Errorf("%w: domain missing", ErrInvalidRouter)
		}
		if err := ApplyDomainBinding(&cfg, *domain); err != nil {
			return cfg, err
		}
	} else if cfg.TLS != nil {
		tls := *cfg.TLS
		tls.Domains = append([]models.TLSDomain(nil), tls.Domains...)
		tls.CertResolver = strings.TrimSpace(tls.CertResolver)
		tls.Options = strings.TrimSpace(tls.Options)
		if passthrough {
			if tls.CertResolver != "" || tls.Options != "" || len(tls.Domains) > 0 {
				return cfg, fmt.Errorf("%w: a passed-through connection is not terminated, so it has no certificate settings", ErrInvalidRouter)
			}
			tls.Enabled = true
		}
		cfg.TLS = &tls
		if !tls.Enabled {
			cfg.TLS = nil
		}
	}
	for i, host := range cfg.Domains {
		normalized, err := NormalizeDomain(host)
		if err != nil || strings.HasPrefix(normalized, "*.") {
			return cfg, fmt.Errorf("%w: invalid host", ErrInvalidRouter)
		}
		cfg.Domains[i] = normalized
	}
	for i, name := range cfg.Middlewares {
		cfg.Middlewares[i] = strings.TrimSpace(name)
	}
	if protocol == models.ServiceHTTP && cfg.Service == models.NoopService && !hasMiddleware(cfg.Middlewares) {
		return cfg, fmt.Errorf("%w: a router without a backend needs a middleware", ErrInvalidRouter)
	}
	if protocol == models.ServiceTCP {
		cfg.Rule = CompileTCPRule(cfg.Domains, cfg.CustomRule)
	} else {
		cfg.Rule = CompileRouterRule(cfg.Domains, cfg.PathPrefix, cfg.CustomRule)
	}
	if cfg.Rule == "" {
		return cfg, fmt.Errorf("%w: rule required", ErrInvalidRouter)
	}
	if protocol == models.ServiceTCP && cfg.CustomRule == "" && cfg.TLS == nil {
		// Without TLS there is no SNI to read: only HostSNI(`*`) can match a plain TCP connection.
		return cfg, fmt.Errorf("%w: matching a hostname needs TLS; enable TLS, or match every connection", ErrInvalidRouter)
	}
	if len(cfg.EntryPoints) == 0 {
		var err error
		if protocol == models.ServiceTCP {
			cfg.EntryPoints, err = static.selectEntryPointsFor(protocol)
		} else {
			cfg.EntryPoints, err = static.selectEntryPoints(cfg.TLS != nil)
		}
		if err != nil {
			return cfg, fmt.Errorf("%w: %v", ErrInvalidRouter, err)
		}
	}
	for i, name := range cfg.EntryPoints {
		name = strings.TrimSpace(name)
		cfg.EntryPoints[i] = name
		ep, ok := static.EntryPoints[name]
		if !ok || !ep.TCP {
			return cfg, fmt.Errorf("%w: %s router requires a registered TCP entrypoint", ErrInvalidRouter, strings.ToUpper(protocol))
		}
	}
	if cfg.TLS != nil {
		if cfg.TLS.CertResolver != "" {
			challenge, ok := static.Resolvers[cfg.TLS.CertResolver]
			if !ok {
				return cfg, fmt.Errorf("%w: certificate resolver missing", ErrInvalidRouter)
			}
			var names []string
			for i, d := range cfg.TLS.Domains {
				main, sans, err := NormalizeSANs(d.Main, d.SANs)
				if err != nil {
					return cfg, fmt.Errorf("%w: invalid certificate names", ErrInvalidRouter)
				}
				cfg.TLS.Domains[i] = models.TLSDomain{Main: main, SANs: sans}
				names = append(names, main)
				names = append(names, sans...)
			}
			for _, name := range names {
				if strings.HasPrefix(name, "*.") && challenge != "dns01" {
					return cfg, fmt.Errorf("%w: wildcard requires DNS-01", ErrInvalidRouter)
				}
			}
			if len(names) > 0 {
				for _, host := range cfg.Domains {
					if !validation.NamesCover(names, host) {
						return cfg, fmt.Errorf("%w: certificate request does not cover router host", ErrInvalidRouter)
					}
				}
			}
		} else if domain != nil && domain.Method == "files" {
			if certificate == nil || certificate.Purpose != "server" {
				return cfg, fmt.Errorf("%w: server certificate missing", ErrInvalidRouter)
			}
			for _, host := range cfg.Domains {
				if !validation.NamesCover(certificate.Domains, host) {
					return cfg, fmt.Errorf("%w: certificate does not cover router host", ErrInvalidRouter)
				}
			}
		}
	}
	return cfg, nil
}

// projectUDP validates a UDP router: entry points and a service, nothing else. Traefik gives UDP
// routers no rule, TLS, middlewares or priority, so any of those being set is a mistake to report.
func projectUDP(cfg models.RouterConfig, static Static) (models.RouterConfig, error) {
	if cfg.DomainName != "" || cfg.Subdomain != "" || len(cfg.Domains) > 0 || cfg.PathPrefix != "" || cfg.CustomRule != "" ||
		strings.TrimSpace(cfg.Rule) != "" || cfg.TLS != nil || cfg.Priority != 0 || hasMiddleware(cfg.Middlewares) {
		return cfg, fmt.Errorf("%w: a UDP router has only entry points and a service", ErrInvalidRouter)
	}
	if cfg.Service == models.NoopService {
		return cfg, fmt.Errorf("%w: a UDP router needs a UDP service", ErrInvalidRouter)
	}
	cfg.Rule, cfg.Domains, cfg.Middlewares = "", nil, nil
	cfg.EntryPoints = append([]string(nil), cfg.EntryPoints...)
	if len(cfg.EntryPoints) == 0 {
		var err error
		if cfg.EntryPoints, err = static.selectEntryPointsFor(models.ServiceUDP); err != nil {
			return cfg, fmt.Errorf("%w: %v", ErrInvalidRouter, err)
		}
	}
	for i, name := range cfg.EntryPoints {
		name = strings.TrimSpace(name)
		cfg.EntryPoints[i] = name
		if ep, ok := static.EntryPoints[name]; !ok || !ep.UDP {
			return cfg, fmt.Errorf("%w: a UDP router requires a registered UDP entrypoint (an address ending in /udp)", ErrInvalidRouter)
		}
	}
	return cfg, nil
}
