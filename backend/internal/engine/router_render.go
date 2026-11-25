package engine

import (
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/models"
)

// renderRouter writes a managed router into its protocol's section. TCP and UDP routers are
// written field by field: a UDP router has no rule, and only values that were set are written
// because Traefik omits defaults from its runtime view (convergence is checked by subset).
func renderRouter(doc *yamlv3.Node, name string, cfg models.RouterConfig) error {
	switch models.RouterProtocol(cfg) {
	case models.ServiceTCP:
		out := map[string]any{"entryPoints": cfg.EntryPoints, "rule": cfg.Rule, "service": cfg.Service}
		if len(cfg.Middlewares) > 0 {
			out["middlewares"] = cfg.Middlewares
		}
		if cfg.Priority != 0 {
			out["priority"] = cfg.Priority
		}
		if cfg.TLS != nil {
			out["tls"] = tcpRouterTLS(cfg.TLS)
		}
		return yaml.SetNestedValue(doc, out, "tcp", "routers", name)
	case models.ServiceUDP:
		return yaml.SetNestedValue(doc, map[string]any{"entryPoints": cfg.EntryPoints, "service": cfg.Service}, "udp", "routers", name)
	default:
		return yaml.SetNestedValue(doc, cfg, "http", "routers", name)
	}
}

func tcpRouterTLS(t *models.RouterTLS) map[string]any {
	out := map[string]any{}
	if t.Passthrough {
		out["passthrough"] = true
	}
	if t.CertResolver != "" {
		out["certResolver"] = t.CertResolver
	}
	if t.Options != "" {
		out["options"] = t.Options
	}
	if len(t.Domains) > 0 {
		domains := make([]map[string]any, 0, len(t.Domains))
		for _, d := range t.Domains {
			entry := map[string]any{"main": d.Main}
			if len(d.SANs) > 0 {
				entry["sans"] = d.SANs
			}
			domains = append(domains, entry)
		}
		out["domains"] = domains
	}
	return out
}

// routerSection is the owned-key prefix of a stored router: protocol:routers:name.
func routerOwnedKey(name string, cfg models.RouterConfig) string {
	return models.RouterProtocol(cfg) + ":routers:" + name
}
