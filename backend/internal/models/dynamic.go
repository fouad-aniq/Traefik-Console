package models

import "strings"

// RouterConfig (`kind = 'router'`)
type RouterConfig struct {
	// Protocol is http (the default when empty), tcp or udp. It cannot change once created.
	// TCP routers match with HostSNI rules (and may pass TLS through); UDP routers have only
	// entry points and a service. Neither takes middlewares from the console.
	Protocol    string     `json:"protocol,omitempty" yaml:"-"`
	EntryPoints []string   `json:"entry_points" yaml:"entryPoints"` // e.g., ["websecure"]
	DomainName  string     `json:"domain_name,omitempty" yaml:"-"`  // Selected managed Domain item (e.g. "main-domain")
	Subdomain   string     `json:"subdomain,omitempty" yaml:"-"`    // Optional prefix (e.g. "app" -> "app.example.com")
	Domains     []string   `json:"domains" yaml:"-"`                // UI helper: ["app.aniqhomelab.ovh"]
	PathPrefix  string     `json:"path_prefix,omitempty" yaml:"-"`  // UI helper: "/api"
	CustomRule  string     `json:"custom_rule,omitempty" yaml:"-"`  // Optional override for advanced rules
	Rule        string     `json:"rule" yaml:"rule"`                // Computed: Host(`app.aniqhomelab.ovh`)
	Service     string     `json:"service" yaml:"service"`          // e.g., "my-app-svc"
	Middlewares []string   `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
	Priority    int        `json:"priority,omitempty" yaml:"priority,omitempty"`
	TLS         *RouterTLS `json:"tls,omitempty" yaml:"tls,omitempty"`

	// ExtraDomains are more managed hostnames for the same router (domain mode only): the router
	// answers on every one of them, and each follows its domain when that domain changes.
	ExtraDomains []RouterHost `json:"extra_domains,omitempty" yaml:"-"`
}

// RouterHost is one more hostname of a domain-bound router: an optional subdomain of a managed domain.
type RouterHost struct {
	DomainName string `json:"domain_name"`
	Subdomain  string `json:"subdomain,omitempty"`
}

// DomainNames lists the managed domains a router uses: its main domain first, then its extra
// hostnames' domains, each once.
func (c RouterConfig) DomainNames() []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	add(c.DomainName)
	for _, h := range c.ExtraDomains {
		add(h.DomainName)
	}
	return out
}

// UsesDomain reports whether the router has a hostname on the managed domain.
func (c RouterConfig) UsesDomain(name string) bool {
	for _, n := range c.DomainNames() {
		if n == name {
			return true
		}
	}
	return false
}

// NoopService is Traefik's built-in service for a router that has no backend: the
// middleware attached to the router answers the request (a redirect, for example).
const NoopService = "noop@internal"

// RouterProtocol is the protocol of a stored or submitted router (http when unset).
func RouterProtocol(c RouterConfig) string {
	if c.Protocol == "" {
		return ServiceHTTP
	}
	return c.Protocol
}

type RouterTLS struct {
	Enabled bool `json:"enabled" yaml:"-"`
	// Passthrough (TCP routers only) forwards the TLS connection untouched; Traefik reads only the SNI.
	Passthrough  bool        `json:"passthrough,omitempty" yaml:"passthrough,omitempty"`
	CertResolver string      `json:"cert_resolver,omitempty" yaml:"certResolver,omitempty"`
	Options      string      `json:"options,omitempty" yaml:"options,omitempty"`
	Domains      []TLSDomain `json:"domains,omitempty" yaml:"domains,omitempty"` // For wildcard certs (*.domain.com)
}

type TLSDomain struct {
	Main string   `json:"main" yaml:"main"`
	SANs []string `json:"sans,omitempty" yaml:"sans,omitempty"`
}

// Service protocols. Traefik keeps HTTP, TCP and UDP services in separate sections.
const (
	ServiceHTTP = "http"
	ServiceTCP  = "tcp"
	ServiceUDP  = "udp"
)

// ServiceConfig (`kind = 'service'`)
type ServiceConfig struct {
	// Protocol is http (the default when empty), tcp or udp. It cannot change once created.
	Protocol           string           `json:"protocol,omitempty" yaml:"-"`
	PassHostHeader     *bool            `json:"pass_host_header,omitempty" yaml:"passHostHeader,omitempty"` // http only
	Servers            []ServerEndpoint `json:"servers" yaml:"servers"`
	HealthCheck        *ServiceHealth   `json:"health_check,omitempty" yaml:"healthCheck,omitempty"` // http only
	InsecureSkipVerify bool             `json:"insecure_skip_verify,omitempty" yaml:"-"`             // http, or tcp with TLS: creates a serversTransport
	// TCP holds the TCP-only options (TLS to backends, PROXY protocol, timeouts, health check).
	TCP *TCPServiceOptions `json:"tcp,omitempty" yaml:"-"`
	// UsedBy is computed at read time by the API (never stored); absent when usage is unknown.
	UsedBy *ServiceUsage `json:"used_by,omitempty" yaml:"-"`
}

// ServerEndpoint is one backend: a URL for HTTP, a host:port address for TCP and UDP.
type ServerEndpoint struct {
	URL     string `json:"url,omitempty" yaml:"url,omitempty"`         // e.g., "http://192.168.1.50:8080"
	Address string `json:"address,omitempty" yaml:"address,omitempty"` // e.g., "192.168.1.50:5432"
}

// TCPServiceOptions are a TCP service's options. Everything but the health check is published
// through a TCP serversTransport the console creates for the service.
type TCPServiceOptions struct {
	// TLS connects to the backends over TLS.
	TLS bool `json:"tls,omitempty"`
	// ServerName is the SNI sent to TLS backends (default: the backend host).
	ServerName string `json:"server_name,omitempty"`
	// ProxyProtocol sends a PROXY protocol header (version 1 or 2) to the backends; 0 is off.
	ProxyProtocol int `json:"proxy_protocol,omitempty"`
	// DialTimeout bounds connecting to a backend (Traefik default 30s).
	DialTimeout string `json:"dial_timeout,omitempty"`
	// TerminationDelay is how long a half-closed connection is kept (Traefik default 100ms).
	TerminationDelay string          `json:"termination_delay,omitempty"`
	HealthCheck      *TCPHealthCheck `json:"health_check,omitempty"`
}

// TCPHealthCheck opens a connection to each backend; Send/Expect optionally check a reply.
type TCPHealthCheck struct {
	Interval string `json:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	Port     int    `json:"port,omitempty"`
	Send     string `json:"send,omitempty"`
	Expect   string `json:"expect,omitempty"`
}

// ServiceProtocol is the protocol of a stored or submitted service (http when unset).
func ServiceProtocol(c ServiceConfig) string {
	if c.Protocol == "" {
		return ServiceHTTP
	}
	return c.Protocol
}

type ServiceHealth struct {
	Path     string `json:"path" yaml:"path"`
	Interval string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

// MiddlewareConfig (`kind = 'middleware'`)
// Uses a clean Type + Spec map so any Traefik middleware or Plugin instance works without 30 structs
type MiddlewareConfig struct {
	// Protocol is http (the default, left out of stored payloads) or tcp. TCP middlewares live in
	// their own Traefik section and only TCP routers can use them.
	Protocol   string            `json:"protocol,omitempty" yaml:"-"`
	Type       string            `json:"type"`                  // e.g., "headers", "rateLimit", "forwardAuth", "plugin"
	PluginName string            `json:"plugin_name,omitempty"` // Only set if Type == "plugin" (e.g., "crowdsec-bouncer")
	Spec       map[string]any    `json:"spec"`                  // The exact Traefik YAML keys under that middleware type
	SecretRefs map[string]string `json:"secret_refs,omitempty"` // If referencing a .htpasswd or CrowdSec key file

	// UsedBy is computed at read time by the API (never stored); absent when usage is unknown.
	UsedBy *MiddlewareUsage `json:"used_by,omitempty" yaml:"-"`
}

// MiddlewareProtocol is the protocol of a stored or submitted middleware (http when unset).
func MiddlewareProtocol(c MiddlewareConfig) string {
	if c.Protocol == "" {
		return ServiceHTTP
	}
	return c.Protocol
}

// ChainConfig (`kind = 'chain'`)
type ChainConfig struct {
	Middlewares []string `json:"middlewares" yaml:"middlewares"` // Ordered list of middleware names

	// UsedBy is computed at read time by the API (never stored); absent when usage is unknown.
	UsedBy *MiddlewareUsage `json:"used_by,omitempty" yaml:"-"`
}

// DomainConfig represents a managed domain entry and its TLS binding
type DomainConfig struct {
	Name        string   `json:"name"`
	Main        string   `json:"main"`
	SANs        []string `json:"sans,omitempty"`
	Method      string   `json:"method"`                // "resolver" or "files"
	Resolver    string   `json:"resolver,omitempty"`    // ACME resolver name
	Certificate string   `json:"certificate,omitempty"` // Imported certificate name
	UsedBy      []string `json:"used_by,omitempty"`     // Computed at read time: routers referencing this domain
	EntryPoint  string   `json:"entry_point,omitempty"` // Compatibility probe selector only; routing uses RouterConfig.EntryPoints.
	State       string   `json:"state"`
	Options     string   `json:"options,omitempty"` // TLS options name

	// Usage and CertificateState are computed at read time by the API (never stored).
	// Usage is absent when the routers could not be read.
	Usage            *DomainUsage `json:"usage,omitempty"`
	CertificateState string       `json:"certificate_state,omitempty"` // a validity or one of the CertState* values
}

// CertificateConfig represents an imported X.509 certificate stored in 0600 files
type CertificateConfig struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`    // "imported"
	CertFile    string   `json:"cert_file"` // Path to .crt file on disk
	KeyFile     string   `json:"key_file"`  // Path to 0600 .key secret file on disk
	Fingerprint string   `json:"fingerprint"`
	Domains     []string `json:"domains"`
	Issuer      string   `json:"issuer"`
	NotBefore   string   `json:"not_before"`
	NotAfter    string   `json:"not_after"`
	Algorithm   string   `json:"algorithm"`
	Status      string   `json:"status"`  // "valid", "expiring", "expired"
	Purpose     string   `json:"purpose"` // "server" or "ca"

	// UsedBy is computed at read time by the API (never stored); absent when usage is unknown.
	UsedBy *CertificateUsage `json:"used_by,omitempty"`
}

// DomainProbeResult captures a live, read-only TLS handshake inspection for a domain.
type DomainProbeResult struct {
	Issuer      string `json:"issuer,omitempty"`
	Names       string `json:"names,omitempty"`
	NotBefore   string `json:"not_before,omitempty"`
	NotAfter    string `json:"not_after,omitempty"`
	Algorithm   string `json:"algorithm,omitempty"`
	Validity    string `json:"validity,omitempty"`
	State       string `json:"state"` // "unreachable", "observed", "matches", "mismatch"
	Trust       string `json:"trust"` // "unknown", "trusted", "untrusted"
	Fingerprint string `json:"fingerprint,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Hostname    string `json:"hostname"`
	Address     string `json:"address"`
	ObservedAt  string `json:"observed_at"`
	Error       string `json:"error,omitempty"`
}
