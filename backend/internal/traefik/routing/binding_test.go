package routing

import (
	"github.com/traefik-console/internal/models"
	"testing"
)

func TestApplyDomainBindingWildcardBaseWithSubdomain(t *testing.T) {
	cfg := &models.RouterConfig{DomainName: "main-domain", Subdomain: "App"}
	dom := models.DomainConfig{
		Name:     "main-domain",
		Main:     "*.example.com",
		SANs:     []string{"example.com"},
		Method:   "resolver",
		Resolver: "cloudflare-prod",
		Options:  "mintls12",
	}
	ApplyDomainBinding(cfg, dom)

	if len(cfg.Domains) != 1 || cfg.Domains[0] != "app.example.com" {
		t.Errorf("Domains = %v, want [app.example.com]", cfg.Domains)
	}
	if cfg.TLS == nil || !cfg.TLS.Enabled {
		t.Fatal("TLS must be enabled")
	}
	if cfg.TLS.CertResolver != "cloudflare-prod" {
		t.Errorf("CertResolver = %q, want cloudflare-prod", cfg.TLS.CertResolver)
	}
	if cfg.TLS.Options != "mintls12" {
		t.Errorf("Options = %q, want mintls12", cfg.TLS.Options)
	}
	if len(cfg.TLS.Domains) != 1 || cfg.TLS.Domains[0].Main != "*.example.com" {
		t.Errorf("TLS.Domains = %+v, want main *.example.com", cfg.TLS.Domains)
	}
	if len(cfg.TLS.Domains[0].SANs) != 1 || cfg.TLS.Domains[0].SANs[0] != "example.com" {
		t.Errorf("TLS.Domains SANs = %v, want [example.com]", cfg.TLS.Domains[0].SANs)
	}
}

func TestApplyDomainBindingConcreteHostNoSubdomain(t *testing.T) {
	cfg := &models.RouterConfig{DomainName: "d"}
	ApplyDomainBinding(cfg, models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "r"})

	if len(cfg.Domains) != 1 || cfg.Domains[0] != "example.com" {
		t.Errorf("Domains = %v, want [example.com]", cfg.Domains)
	}
}

func TestApplyDomainBindingFilesMethodHasNoCertResolver(t *testing.T) {
	cfg := &models.RouterConfig{DomainName: "d"}
	ApplyDomainBinding(cfg, models.DomainConfig{Main: "example.com", Method: "files", Certificate: "my-cert"})

	if cfg.TLS == nil || !cfg.TLS.Enabled {
		t.Fatal("TLS must still be enabled for imported certificates")
	}
	if cfg.TLS.CertResolver != "" {
		t.Errorf("CertResolver = %q, want empty for files method", cfg.TLS.CertResolver)
	}
	if len(cfg.TLS.Domains) != 0 {
		t.Errorf("TLS.Domains = %+v, want none for files method", cfg.TLS.Domains)
	}
}

func TestCompileRouterRule(t *testing.T) {
	cases := []struct {
		name       string
		domains    []string
		pathPrefix string
		customRule string
		want       string
	}{
		{"single host", []string{"a.example"}, "", "", "Host(`a.example`)"},
		{"multi host", []string{"a.example", "B.example"}, "", "", "(Host(`a.example`) || Host(`b.example`))"},
		{"path prefix added", []string{"a.example"}, "api", "", "Host(`a.example`) && PathPrefix(`/api`)"},
		{"custom rule wins", []string{"a.example"}, "api", "PathPrefix(`/x`)", "PathPrefix(`/x`)"},
		{"empty", nil, "", "", ""},
	}
	for _, tc := range cases {
		if got := CompileRouterRule(tc.domains, tc.pathPrefix, tc.customRule); got != tc.want {
			t.Errorf("%s: CompileRouterRule() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
