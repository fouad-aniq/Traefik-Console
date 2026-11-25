package models

type PropagationConfig struct {
	DelayBeforeChecks string `json:"delayBeforeChecks,omitempty" yaml:"delayBeforeChecks,omitempty"`
	DisableChecks     bool   `json:"disableChecks,omitempty" yaml:"disableChecks,omitempty"`
	DisableANSChecks  bool   `json:"disableANSChecks,omitempty" yaml:"disableANSChecks,omitempty"`
	RequireAllRNS     bool   `json:"requireAllRNS,omitempty" yaml:"requireAllRNS,omitempty"`
}

// ResolverInput is the request/stored model for creating or editing a resolver
type ResolverInput struct {
	Name                string             `json:"name"`
	Type                string             `json:"type"`                  // "acme", "tailscale", "custom"
	Environment         string             `json:"environment,omitempty"` // "production", "staging", "custom"
	CAURL               string             `json:"ca_url,omitempty"`
	Email               string             `json:"email,omitempty"`
	KeyType             string             `json:"key_type,omitempty"`    // "EC256", "EC384", "RSA2048", "RSA4096"
	Challenge           string             `json:"challenge,omitempty"`   // "dns01", "http01", "tlsalpn01"
	EntryPoint          string             `json:"entry_point,omitempty"` // For http01
	Provider            string             `json:"provider,omitempty"`    // e.g., "cloudflare", "ovh"
	Alternative         int                `json:"alternative"`           // Index into provider.Alternatives
	Values              map[string]string  `json:"values,omitempty"`      // EnvVar -> raw value (on POST) or masked preview (on GET)
	Nameservers         []string           `json:"nameservers,omitempty"` // e.g., ["1.1.1.1:53"]
	Delay               string             `json:"delay,omitempty"`       // e.g., "10s"
	Propagation         *PropagationConfig `json:"propagation,omitempty"`
	Storage             string             `json:"storage,omitempty"`
	CustomYAML          string             `json:"custom_yaml,omitempty"`
	ExternalCredentials bool               `json:"external_credentials,omitempty"`
	Warnings            []string           `json:"warnings,omitempty"`

	// RenderedNode is the pre-validated YAML map ("acme": {...} or "tailscale": {})
	// that engine/generator.go injects directly into certificatesResolvers.<name>
	RenderedNode map[string]any `json:"rendered_node"`
}

// StaticPluginConfig represents a plugin registered in traefik.yml (`kind = 'plugin'`)
type StaticPluginConfig struct {
	ModuleName string `json:"module_name" yaml:"moduleName"` // e.g., "github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin"
	Version    string `json:"version" yaml:"version"`        // e.g., "v1.3.3"
}
