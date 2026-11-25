package domains

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

// fakeApplier commits straight to the stub store; the publish pipeline has its own engine tests.
type fakeApplier struct{ st *stubStore }

func (f fakeApplier) Apply(_ context.Context, m models.Mutation) (*models.OperationResult, error) {
	for _, ref := range m.Deletes {
		delete(f.st.items, string(ref.Kind)+":"+ref.Name)
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

type stubStore struct {
	db.Store
	items map[string]*models.ConfigItemRow
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

func (stubFiles) ReadStaticConfig() ([]byte, error) { return nil, nil }

func newTestManager(rows ...*models.ConfigItemRow) DomainManager {
	// Empty static fixture; managed resolver/domain definitions come from the test store.
	store := newStubStore(rows...)
	mgr := NewDomainManager(store, stubFiles{}, nil, fakeApplier{store})
	return mgr
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

func dnsResolverRow(t *testing.T, name, challenge string) *models.ConfigItemRow {
	return activeRow(t, models.KindResolver, name, models.ResolverInput{
		Name: name, Type: "acme", Challenge: challenge,
	})
}

func TestSaveDomainAcceptsWildcardWithDNS01Resolver(t *testing.T) {
	mgr := newTestManager(dnsResolverRow(t, "cf-prod", "dns01"))
	_, err := mgr.SaveDomain(context.Background(), "main-domain", models.DomainConfig{
		Main: "*.example.com", Method: "resolver", Resolver: "cf-prod",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("SaveDomain() error = %v", err)
	}
}

func TestSaveDomainRejectsWildcardWithNonDNS01Resolver(t *testing.T) {
	mgr := newTestManager(dnsResolverRow(t, "le-http", "http01"))
	_, err := mgr.SaveDomain(context.Background(), "bad-domain", models.DomainConfig{
		Main: "*.example.com", Method: "resolver", Resolver: "le-http",
	}, models.WriteOptions{})
	if !errors.Is(err, ErrDomainWildcardNeedsDNS01) {
		t.Fatalf("error = %v, want ErrDomainWildcardNeedsDNS01", err)
	}
}

func TestSaveDomainAcceptsConcreteHostWithAnyChallenge(t *testing.T) {
	mgr := newTestManager(dnsResolverRow(t, "le-http", "http01"))
	_, err := mgr.SaveDomain(context.Background(), "site", models.DomainConfig{
		Main: "example.com", Method: "resolver", Resolver: "le-http",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("SaveDomain() error = %v", err)
	}
}

func TestSaveDomainRejectsUnknownResolver(t *testing.T) {
	mgr := newTestManager()
	_, err := mgr.SaveDomain(context.Background(), "d", models.DomainConfig{
		Main: "example.com", Method: "resolver", Resolver: "ghost",
	}, models.WriteOptions{})
	if !errors.Is(err, ErrDomainResolverNotFound) {
		t.Fatalf("error = %v, want ErrDomainResolverNotFound", err)
	}
}

func TestSaveDomainRejectsUnknownCertificate(t *testing.T) {
	mgr := newTestManager()
	_, err := mgr.SaveDomain(context.Background(), "d", models.DomainConfig{
		Main: "example.com", Method: "files", Certificate: "ghost-cert",
	}, models.WriteOptions{})
	if !errors.Is(err, ErrDomainCertNotFound) {
		t.Fatalf("error = %v, want ErrDomainCertNotFound", err)
	}
}

func TestSaveDomainAcceptsExistingCertificate(t *testing.T) {
	cert := activeRow(t, models.KindTLS, "my-cert", models.CertificateConfig{Name: "my-cert", Purpose: "server", Domains: []string{"example.com"}})
	mgr := newTestManager(cert)
	_, err := mgr.SaveDomain(context.Background(), "d", models.DomainConfig{
		Main: "example.com", Method: "files", Certificate: "my-cert",
	}, models.WriteOptions{})
	if err != nil {
		t.Fatalf("SaveDomain() error = %v", err)
	}
}

func upsertDomain(t *testing.T, mgr DomainManager) {
	t.Helper()
	if _, err := mgr.SaveDomain(context.Background(), "main-domain", models.DomainConfig{
		Main: "*.example.com", Method: "resolver", Resolver: "cf-prod",
	}, models.WriteOptions{}); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
}

func TestDeleteDomainBlockedWhenRouterUsesDomain(t *testing.T) {
	routerRaw, _ := json.Marshal(models.RouterConfig{DomainName: "main-domain", Service: "svc"})
	store := &stubStore{items: map[string]*models.ConfigItemRow{
		"domain:main-domain": activeRow(t, models.KindDomain, "main-domain", models.DomainConfig{Name: "main-domain", Main: "*.example.com", Method: "resolver", Resolver: "cf-prod"}),
		"router:site":        {ID: "router:site", Kind: models.KindRouter, Name: "site", Payload: routerRaw},
	}}
	mgr := NewDomainManager(store, stubFiles{}, nil, fakeApplier{store})

	_, err := mgr.DeleteDomain(context.Background(), "main-domain", models.WriteOptions{})
	if !errors.Is(err, ErrDomainInUse) {
		t.Fatalf("error = %v, want ErrDomainInUse", err)
	}
}

func TestDeleteDomainAllowedWhenUnused(t *testing.T) {
	mgr := newTestManager(dnsResolverRow(t, "cf-prod", "dns01"))
	upsertDomain(t, mgr)
	if _, err := mgr.DeleteDomain(context.Background(), "main-domain", models.WriteOptions{}); err != nil {
		t.Fatalf("DeleteDomain() error = %v", err)
	}
}

func TestListDomainsComputesUsedBy(t *testing.T) {
	routerRaw, _ := json.Marshal(models.RouterConfig{DomainName: "main-domain", Service: "svc"})
	store := &stubStore{items: map[string]*models.ConfigItemRow{
		"domain:main-domain": activeRow(t, models.KindDomain, "main-domain", models.DomainConfig{Name: "main-domain", Main: "example.com"}),
		"router:site":        {ID: "router:site", Kind: models.KindRouter, Name: "site", Payload: routerRaw},
	}}
	mgr := NewDomainManager(store, stubFiles{}, nil, fakeApplier{store})

	items, err := mgr.ListDomains(context.Background())
	if err != nil {
		t.Fatalf("ListDomains() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d domains, want 1", len(items))
	}
	if len(items[0].Config.UsedBy) != 1 || items[0].Config.UsedBy[0] != "site" {
		t.Errorf("UsedBy = %v, want [site]", items[0].Config.UsedBy)
	}
}
