package routers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// fakeApplier commits straight to the stub store: manager tests exercise validation and
// projection, not the publish pipeline (that has its own engine tests).
type fakeApplier struct{ st *stubStore }

func (f fakeApplier) Apply(_ context.Context, m models.Mutation) (*models.OperationResult, error) {
	for _, ref := range m.Deletes {
		delete(f.st.items, string(ref.Kind)+":"+ref.Name)
		f.st.deleted = append(f.st.deleted, string(ref.Kind)+":"+ref.Name)
	}
	for i := range m.Upserts {
		row := m.Upserts[i]
		key := string(row.Kind) + ":" + row.Name
		row.Version = 1
		if old, ok := f.st.items[key]; ok {
			row.Version = old.Version + 1
		}
		f.st.items[key] = &row
	}
	return &models.OperationResult{Operation: &models.OperationRow{Status: models.OpStatusSucceeded}}, nil
}

func (f fakeApplier) Preview(context.Context, models.Mutation) (*models.OperationPreview, error) {
	return &models.OperationPreview{}, nil
}

// The stubs embed the dependency interfaces and override only the methods the
// router manager actually calls during upsert/list.
type stubStore struct {
	db.Store
	items   map[string]*models.ConfigItemRow
	deleted []string
}

func newStubStore(rows ...*models.ConfigItemRow) *stubStore {
	s := &stubStore{items: map[string]*models.ConfigItemRow{}}
	for _, r := range rows {
		s.items[string(r.Kind)+":"+r.Name] = r
	}
	return s
}

func (s *stubStore) GetItem(_ context.Context, kind models.ItemKind, name string) (*models.ConfigItemRow, error) {
	if row, ok := s.items[string(kind)+":"+name]; ok {
		return row, nil
	}
	return nil, nil
}

func (s *stubStore) ListItemsByKind(_ context.Context, kind models.ItemKind) ([]models.ConfigItemRow, error) {
	var rows []models.ConfigItemRow
	for _, r := range s.items {
		if r.Kind == kind {
			rows = append(rows, *r)
		}
	}
	return rows, nil
}

type stubFiles struct{}

func (stubFiles) ReadStaticConfig() ([]byte, error) {
	return []byte("entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\ncertificatesResolvers:\n  cf-prod: {acme: {dnsChallenge: {provider: cloudflare}}}\n"), nil
}
func (s *stubStore) ListAllItems(ctx context.Context) ([]models.ConfigItemRow, error) {
	var rows []models.ConfigItemRow
	for _, row := range s.items {
		rows = append(rows, *row)
	}
	return rows, nil
}

func activeRow(t *testing.T, kind models.ItemKind, name string, cfg any) *models.ConfigItemRow {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return &models.ConfigItemRow{
		ID:      string(kind) + ":" + name,
		Kind:    kind,
		Name:    name,
		Payload: raw,
	}
}

func newTestManager(t *testing.T, rows ...*models.ConfigItemRow) RouterManager {
	t.Helper()
	inspector := emptyRuntime{}
	store := newStubStore(rows...)
	mgr := NewRouterManager(store, stubFiles{}, inspector, fakeApplier{store})
	return mgr
}

func TestUpsertRouterDerivesFromResolverDomain(t *testing.T) {
	dom := activeRow(t, models.KindDomain, "main-domain", models.DomainConfig{
		Name:     "main-domain",
		Main:     "*.example.com",
		SANs:     []string{"example.com"},
		Method:   "resolver",
		Resolver: "cf-prod",
		Options:  "mintls12",
	})
	mgr := newTestManager(t, dom)

	item, err := mgr.UpsertRouter(context.Background(), "grafana", models.RouterConfig{
		DomainName: "main-domain",
		Subdomain:  "App",
		Service:    "grafana-svc",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("UpsertRouter() error = %v", err)
	}
	cfg := item.Resource.Config
	if len(cfg.Domains) != 1 || cfg.Domains[0] != "app.example.com" {
		t.Errorf("Domains = %v, want [app.example.com]", cfg.Domains)
	}
	if cfg.Rule != "Host(`app.example.com`)" {
		t.Errorf("Rule = %q, want Host(`app.example.com`)", cfg.Rule)
	}
	if cfg.TLS == nil || !cfg.TLS.Enabled {
		t.Fatal("TLS must be enabled")
	}
	if cfg.TLS.CertResolver != "cf-prod" || cfg.TLS.Options != "mintls12" {
		t.Errorf("TLS = %+v, want certResolver cf-prod / options mintls12", cfg.TLS)
	}
	if len(cfg.TLS.Domains) != 1 || cfg.TLS.Domains[0].Main != "*.example.com" {
		t.Errorf("TLS.Domains = %+v, want main *.example.com", cfg.TLS.Domains)
	}
	if len(cfg.EntryPoints) != 1 || cfg.EntryPoints[0] != "websecure" {
		t.Errorf("EntryPoints = %v, want [websecure] (TLS-enabled default)", cfg.EntryPoints)
	}
}

func TestUpsertRouterDerivesFromFilesDomain(t *testing.T) {
	dom := activeRow(t, models.KindDomain, "cert-domain", models.DomainConfig{
		Name:        "cert-domain",
		Main:        "example.com",
		Method:      "files",
		Certificate: "my-cert",
	})
	mgr := newTestManager(t, dom, activeRow(t, models.KindTLS, "my-cert", models.CertificateConfig{Purpose: "server", Domains: []string{"example.com"}}))

	item, err := mgr.UpsertRouter(context.Background(), "r1", models.RouterConfig{
		DomainName: "cert-domain",
		Service:    "svc",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("UpsertRouter() error = %v", err)
	}
	if item.Resource.Config.TLS == nil || !item.Resource.Config.TLS.Enabled || item.Resource.Config.TLS.CertResolver != "" {
		t.Errorf("TLS = %+v, want enabled without certResolver for files method", item.Resource.Config.TLS)
	}
}

func TestUpsertRouterRejectsMissingDomain(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.UpsertRouter(context.Background(), "r1", models.RouterConfig{
		DomainName: "ghost",
		Service:    "svc",
	}, models.WriteOptions{})
	if !errors.Is(err, ErrRouterDomainNotFound) {
		t.Fatalf("error = %v, want ErrRouterDomainNotFound", err)
	}
}

func TestUpsertRouterCustomRuleWinsButTLSStillDerived(t *testing.T) {
	dom := activeRow(t, models.KindDomain, "main-domain", models.DomainConfig{
		Main: "example.com", Method: "resolver", Resolver: "cf-prod",
	})
	mgr := newTestManager(t, dom)

	item, err := mgr.UpsertRouter(context.Background(), "r1", models.RouterConfig{
		DomainName: "main-domain",
		CustomRule: "PathPrefix(`/x`)",
		Service:    "svc",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("UpsertRouter() error = %v", err)
	}
	if item.Resource.Config.Rule != "PathPrefix(`/x`)" {
		t.Errorf("Rule = %q, want the custom rule", item.Resource.Config.Rule)
	}
	if item.Resource.Config.TLS == nil || item.Resource.Config.TLS.CertResolver != "cf-prod" {
		t.Errorf("TLS = %+v, want still derived from the domain", item.Resource.Config.TLS)
	}
}

func TestUpsertRouterManualDomainsStillWork(t *testing.T) {
	mgr := newTestManager(t)
	item, err := mgr.UpsertRouter(context.Background(), "manual", models.RouterConfig{
		Domains: []string{"a.example"},
		Service: "svc",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("UpsertRouter() error = %v", err)
	}
	if item.Resource.Config.Rule != "Host(`a.example`)" {
		t.Errorf("Rule = %q, want Host(`a.example`)", item.Resource.Config.Rule)
	}
	if item.Resource.Config.TLS != nil {
		t.Errorf("TLS = %+v, want nil for manual router without TLS", item.Resource.Config.TLS)
	}
}

type emptyRuntime struct{}

func (emptyRuntime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	return &status.RuntimeSnapshot{}, nil
}

func TestARouterWithoutABackendNeedsAMiddlewareThatAnswers(t *testing.T) {
	mgr := newTestManager(t)
	base := models.RouterConfig{Domains: []string{"old.example"}, EntryPoints: []string{"web"}, Service: models.NoopService}

	for name, middlewares := range map[string][]string{"none": nil, "empty": {}, "blank names": {"", "  "}} {
		in := base
		in.Middlewares = middlewares
		if _, err := mgr.UpsertRouter(context.Background(), "redirect", in, models.WriteOptions{}); !errors.Is(err, ErrRouterNoBackend) {
			t.Errorf("%s: error = %v, want ErrRouterNoBackend", name, err)
		}
	}

	in := base
	in.Middlewares = []string{"to-https"}
	got, err := mgr.UpsertRouter(context.Background(), "redirect", in, models.WriteOptions{})
	if err != nil {
		t.Fatalf("a redirect-only router was refused: %v", err)
	}
	if cfg := got.Resource.Config; cfg.Service != models.NoopService || len(cfg.Middlewares) != 1 || cfg.Rule != "Host(`old.example`)" {
		t.Fatalf("stored = %+v", cfg)
	}
}

func TestARouterStillNeedsAServiceOrTheNoBackendChoice(t *testing.T) {
	mgr := newTestManager(t)
	for _, service := range []string{"", "   "} {
		_, err := mgr.UpsertRouter(context.Background(), "r1", models.RouterConfig{Domains: []string{"a.example"}, Service: service, Middlewares: []string{"to-https"}}, models.WriteOptions{})
		if !errors.Is(err, ErrRouterNoService) {
			t.Errorf("service %q: error = %v, want ErrRouterNoService", service, err)
		}
	}
}
