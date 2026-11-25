package routers

import (
	"context"
	"errors"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"testing"
)

type unavailableObserver struct{ calls int }

func (o *unavailableObserver) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	o.calls++
	return nil, errors.New("synthetic unavailable runtime")
}
func TestRouterSaveDoesNotReadRuntimeAfterPersistence(t *testing.T) {
	store := newStubStore()
	observer := &unavailableObserver{}
	manager := NewRouterManager(store, stubFiles{}, observer, fakeApplier{store})
	got, e := manager.UpsertRouter(context.Background(), "r", models.RouterConfig{Domains: []string{"example.com"}, Service: " app ", Middlewares: []string{" security "}}, models.WriteOptions{})
	if e != nil || got.Resource.Config.Service != "app" || observer.calls != 0 {
		t.Fatal("successful save requires runtime read", e)
	}
	if store.items["router:r"] == nil {
		t.Fatal("router missing")
	}
}

func TestRouterReadProjectsTheCommittedDomain(t *testing.T) {
	domain := activeRow(t, models.KindDomain, "site", models.DomainConfig{Name: "site", Main: "old.example.com", Method: "resolver", Resolver: "cf-prod"})
	router := activeRow(t, models.KindRouter, "r", models.RouterConfig{DomainName: "site", Domains: []string{"stale.example.com"}, Rule: "Host(`stale.example.com`)", Service: "app", EntryPoints: []string{"websecure"}})
	router.Version = 3
	store := newStubStore(domain, router)
	manager := NewRouterManager(store, stubFiles{}, emptyRuntime{}, fakeApplier{store})
	got, e := manager.GetRouter(context.Background(), "r")
	if e != nil || got.Config.Domains[0] != "old.example.com" || got.Version != 3 {
		t.Fatalf("projection wrong: %+v %v", got, e)
	}
	domain.Payload = []byte(`{"name":"site","main":"new.example.com","method":"resolver","resolver":"cf-prod"}`)
	got, e = manager.GetRouter(context.Background(), "r")
	if e != nil || got.Config.Domains[0] != "new.example.com" {
		t.Fatal("read does not follow the committed domain", e)
	}
}
