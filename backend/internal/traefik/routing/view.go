package routing

import (
	"encoding/json"
	"errors"
	"github.com/traefik-console/internal/models"
	"strings"
)

var ErrDomainMissing = errors.New("selected domain was not found")

// View is an immutable projection context over one resource set (committed rows,
// or the candidate rows of an operation that has not been published yet).
type View struct {
	static       Static
	domains      map[string]models.DomainConfig
	certificates map[string]models.CertificateConfig
}

func NewView(rows []models.ConfigItemRow, rawStatic []byte) (View, error) {
	static, err := ParseStatic(rawStatic)
	if err != nil {
		return View{}, err
	}
	view := View{static: static, domains: map[string]models.DomainConfig{}, certificates: map[string]models.CertificateConfig{}}
	for _, row := range rows {
		payload := row.Payload
		if len(payload) == 0 {
			continue
		}
		switch row.Kind {
		case models.KindDomain:
			var cfg models.DomainConfig
			if err = json.Unmarshal(payload, &cfg); err != nil {
				return View{}, err
			}
			view.domains[row.Name] = cfg
		case models.KindTLS:
			var cfg models.CertificateConfig
			if err = json.Unmarshal(payload, &cfg); err != nil {
				return View{}, err
			}
			view.certificates[row.Name] = cfg
		case models.KindResolver:
			var cfg models.ResolverInput
			if err = json.Unmarshal(payload, &cfg); err != nil {
				return View{}, err
			}
			if cfg.Challenge != "" {
				view.static.Resolvers[row.Name] = cfg.Challenge
			}
		}
	}
	return view, nil
}
func (v View) Project(cfg models.RouterConfig) (models.RouterConfig, error) {
	cfg.DomainName = strings.TrimSpace(cfg.DomainName)
	var domain *models.DomainConfig
	var cert *models.CertificateConfig
	if cfg.DomainName != "" {
		d, ok := v.domains[cfg.DomainName]
		if !ok {
			return cfg, ErrDomainMissing
		}
		domain = &d
		if d.Method == "files" {
			if c, ok := v.certificates[d.Certificate]; ok {
				cert = &c
			}
		}
	}
	var extras []Binding
	for _, host := range cfg.ExtraDomains {
		d, ok := v.domains[strings.TrimSpace(host.DomainName)]
		if !ok {
			return cfg, ErrDomainMissing
		}
		b := Binding{Domain: d}
		if d.Method == "files" {
			if c, ok := v.certificates[d.Certificate]; ok {
				b.Certificate = &c
			}
		}
		extras = append(extras, b)
	}
	return ProjectWith(cfg, domain, cert, extras, v.static)
}
