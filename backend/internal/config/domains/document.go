// Package domains encodes Console-owned domain preferences, outside Traefik's
// watched directory. Ownership stays in SQLite; active preferences have a
// deterministic, secret-free file representation protected by the engine journal.
package domains

import (
	"encoding/json"

	"github.com/traefik-console/internal/models"
	"gopkg.in/yaml.v3"
)

type settings struct {
	Main        string   `yaml:"main"`
	SANs        []string `yaml:"sans,omitempty"`
	Method      string   `yaml:"method"`
	Resolver    string   `yaml:"resolver,omitempty"`
	Certificate string   `yaml:"certificate,omitempty"`
	Options     string   `yaml:"options,omitempty"`
	// Compatibility field: probe selection only, never router configuration.
	ProbeEntryPoint string `yaml:"probe_entry_point,omitempty"`
}

// Render writes the committed domain rows. Computed health, usage and secret
// material are never part of this document.
func Render(rows []models.ConfigItemRow) ([]byte, error) {
	doc := struct {
		Version int                 `yaml:"version"`
		Domains map[string]settings `yaml:"domains"`
	}{Version: 1, Domains: map[string]settings{}}
	for _, row := range rows {
		if row.Kind != models.KindDomain || len(row.Payload) == 0 {
			continue
		}
		var cfg models.DomainConfig
		if err := json.Unmarshal(row.Payload, &cfg); err != nil {
			return nil, err
		}
		doc.Domains[row.Name] = settings{Main: cfg.Main, SANs: cfg.SANs, Method: cfg.Method, Resolver: cfg.Resolver, Certificate: cfg.Certificate, Options: cfg.Options, ProbeEntryPoint: cfg.EntryPoint}
	}
	return yaml.Marshal(doc)
}
