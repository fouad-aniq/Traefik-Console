package engine

import (
	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/models"
)

// Transport names the console creates next to a service. They are owned with the service.
func httpTransportName(service string) string { return service + "-insecure-transport" }
func tcpTransportName(service string) string  { return service + "-tcp-transport" }

// serviceTransports lists the serversTransports a stored service may own, by protocol.
func serviceTransports(name string, cfg models.ServiceConfig) (protocol string, transports []string) {
	switch models.ServiceProtocol(cfg) {
	case models.ServiceTCP:
		return models.ServiceTCP, []string{tcpTransportName(name)}
	case models.ServiceUDP:
		return models.ServiceUDP, nil
	default:
		return models.ServiceHTTP, []string{httpTransportName(name)}
	}
}

// renderService writes a managed service into its protocol's section. Only values that were set
// are written: Traefik omits defaults from its runtime view, and convergence is checked by subset.
func renderService(doc *yamlv3.Node, name string, cfg models.ServiceConfig) error {
	switch models.ServiceProtocol(cfg) {
	case models.ServiceTCP:
		return renderTCPService(doc, name, cfg)
	case models.ServiceUDP:
		servers := make([]map[string]any, 0, len(cfg.Servers))
		for _, s := range cfg.Servers {
			servers = append(servers, map[string]any{"address": s.Address})
		}
		return yaml.SetNestedValue(doc, map[string]any{"loadBalancer": map[string]any{"servers": servers}}, "udp", "services", name)
	default:
		return renderHTTPService(doc, name, cfg)
	}
}

func renderHTTPService(doc *yamlv3.Node, name string, cfg models.ServiceConfig) error {
	servers := make([]map[string]any, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		servers = append(servers, map[string]any{"url": s.URL})
	}
	lb := map[string]any{"servers": servers}
	if cfg.PassHostHeader != nil {
		lb["passHostHeader"] = *cfg.PassHostHeader
	}
	if cfg.HealthCheck != nil {
		lb["healthCheck"] = cfg.HealthCheck
	}
	if cfg.InsecureSkipVerify {
		transport := httpTransportName(name)
		if err := yaml.SetNestedValue(doc, map[string]bool{"insecureSkipVerify": true}, "http", "serversTransports", transport); err != nil {
			return err
		}
		lb["serversTransport"] = transport
	}
	return yaml.SetNestedValue(doc, map[string]any{"loadBalancer": lb}, "http", "services", name)
}

func renderTCPService(doc *yamlv3.Node, name string, cfg models.ServiceConfig) error {
	opts := models.TCPServiceOptions{}
	if cfg.TCP != nil {
		opts = *cfg.TCP
	}
	servers := make([]map[string]any, 0, len(cfg.Servers))
	for _, s := range cfg.Servers {
		server := map[string]any{"address": s.Address}
		if opts.TLS {
			server["tls"] = true
		}
		servers = append(servers, server)
	}
	lb := map[string]any{"servers": servers}
	if h := opts.HealthCheck; h != nil {
		check := map[string]any{}
		for k, v := range map[string]string{"interval": h.Interval, "timeout": h.Timeout, "send": h.Send, "expect": h.Expect} {
			if v != "" {
				check[k] = v
			}
		}
		if h.Port > 0 {
			check["port"] = h.Port
		}
		lb["healthCheck"] = check
	}

	transport := map[string]any{}
	if opts.ProxyProtocol > 0 {
		transport["proxyProtocol"] = map[string]any{"version": opts.ProxyProtocol}
	}
	if opts.DialTimeout != "" {
		transport["dialTimeout"] = opts.DialTimeout
	}
	if opts.TerminationDelay != "" {
		transport["terminationDelay"] = opts.TerminationDelay
	}
	if opts.TLS && (opts.ServerName != "" || cfg.InsecureSkipVerify) {
		tls := map[string]any{}
		if opts.ServerName != "" {
			tls["serverName"] = opts.ServerName
		}
		if cfg.InsecureSkipVerify {
			tls["insecureSkipVerify"] = true
		}
		transport["tls"] = tls
	}
	if len(transport) > 0 {
		if err := yaml.SetNestedValue(doc, transport, "tcp", "serversTransports", tcpTransportName(name)); err != nil {
			return err
		}
		lb["serversTransport"] = tcpTransportName(name)
	}
	return yaml.SetNestedValue(doc, map[string]any{"loadBalancer": lb}, "tcp", "services", name)
}
