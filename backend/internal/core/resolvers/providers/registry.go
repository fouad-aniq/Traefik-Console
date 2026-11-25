package providers

import (
	_ "embed"
	"encoding/json"
	"sort"
)

//go:embed providers.json
var providerJSON []byte

type Field struct {
	Env          string   `json:"env_var"`
	Aliases      []string `json:"aliases,omitempty"`
	Type         string   `json:"type"` // "string", "secret", "duration", "integer", "boolean"
	Required     bool     `json:"required"`
	Secret       bool     `json:"is_secret"`
	File         bool     `json:"file_support"`
	DefaultValue string   `json:"default_value,omitempty"`
	Bounds       string   `json:"bounds,omitempty"`
	Description  string   `json:"description"`
}

type Alternative struct {
	Name        string  `json:"alternative_name"`
	Description string  `json:"description"`
	Required    []Field `json:"required_fields"`
	Optional    []Field `json:"optional_fields"`
}

type Provider struct {
	Code         string        `json:"provider_code"`
	Name         string        `json:"display_name"`
	DocURL       string        `json:"doc_url"`
	Supported    bool          `json:"form_supported"`
	FileSupport  bool          `json:"file_support_all"`
	Status       string        `json:"verification_status"`
	AuditNotes   string        `json:"source_audit_notes"`
	Alternatives []Alternative `json:"auth_alternatives"`
	Options      []Field       `json:"global_options"`
}

type Registry struct {
	providers       map[string]Provider
	customProviders map[string]bool
}

func LoadRegistry() (*Registry, error) {
	var raw struct {
		Providers           map[string]Provider `json:"providers"`
		CustomProviderCodes []string            `json:"custom_provider_codes"`
	}
	if err := json.Unmarshal(providerJSON, &raw); err != nil {
		return nil, err
	}
	codes := make(map[string]bool, len(raw.CustomProviderCodes))
	for _, code := range raw.CustomProviderCodes {
		// Exclude manual and exec for security as in the original build.go
		codes[code] = code != "exec" && code != "manual"
	}
	return &Registry{
		providers:       raw.Providers,
		customProviders: codes,
	}, nil
}

// EnvVars lists every variable the supported providers read (all alternatives and options),
// sorted and without duplicates.
func (r *Registry) EnvVars() []string {
	seen := map[string]bool{}
	add := func(fields []Field) {
		for _, f := range fields {
			if f.Env != "" {
				seen[f.Env] = true
			}
		}
	}
	for _, p := range r.providers {
		for _, alt := range p.Alternatives {
			add(alt.Required)
			add(alt.Optional)
		}
		add(p.Options)
	}
	out := make([]string, 0, len(seen))
	for env := range seen {
		out = append(out, env)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Providers() []Provider {
	list := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func (r *Registry) CustomProviderCodes() []string {
	codes := make([]string, 0, len(r.customProviders))
	for c, allowed := range r.customProviders {
		if allowed {
			codes = append(codes, c)
		}
	}
	sort.Strings(codes)
	return codes
}

func (r *Registry) Provider(code string) (Provider, bool) {
	p, ok := r.providers[code]
	return p, ok
}

func (r *Registry) IsCustomProvider(code string) bool {
	return r.customProviders[code]
}
