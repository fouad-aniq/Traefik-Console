package main

import (
	"context"
	"os"
	"strings"
	"testing"

	api "github.com/traefik-console/internal/api/handlers"
	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
)

type compositionOperations struct{ engine.Operations }

func TestCompositionRequiresOperationEngineBeforeConstruction(t *testing.T) {
	if _, err := newAPIDependencies(nil, nil, nil, nil, nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "operation engine") {
		t.Fatal("missing operation engine accepted", err)
	}
}
func TestCompositionRetainsProtectedNetworkValidation(t *testing.T) {
	opts := &config.Options{ProtectedCIDRs: []string{"invalid-network"}}
	if _, err := newAPIDependencies(opts, nil, nil, nil, nil, nil, compositionOperations{}, nil); err == nil || !strings.Contains(err.Error(), "crowdsec") {
		t.Fatal("protected-network error lost", err)
	}
}
func TestHTTPCompositionBuildsUsableResourceDependencies(t *testing.T) {
	opts := startupOptions(t)
	state, err := openState(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	repository, err := files.New(opts.TraefikStaticPath, opts.TraefikDynamicDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.WriteStaticConfig([]byte("entryPoints:\n  web: {}\n")); err != nil {
		t.Fatal(err)
	}
	vault, err := secrets.New(opts.SecretsDir, opts.RuntimeSecretsDir, state.key, state.store)
	if err != nil {
		t.Fatal(err)
	}
	observation := status.NewObservationClient(opts.TraefikAPIURL)
	defer observation.Close()
	runtime, err := docker.New(docker.Registration{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client := catalog.NewCatalogClient()
	defer client.Close()
	deps, err := newAPIDependencies(opts, state.store, repository, vault, observation, runtime, compositionOperations{}, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.NewServer(deps, middleware.Policy{}); err != nil {
		t.Fatal("incomplete API wiring", err)
	}
	guided, _ := deps.Resolvers.ListProviders()
	if len(guided) != 8 {
		t.Fatal("provider scope changed")
	}
	ctx := context.Background()
	if _, err = deps.Resolvers.ListResolvers(ctx); err != nil {
		t.Fatal("resolver static capability not wired", err)
	}
	if _, err = deps.Domains.ListDomains(ctx); err != nil {
		t.Fatal("domain persistence not wired", err)
	}
	if _, err = deps.Certificates.ListCertificates(ctx); err != nil {
		t.Fatal("certificate persistence not wired", err)
	}
	if _, err = deps.Plugins.ListInstalledPlugins(ctx); err != nil {
		t.Fatal("plugin composition not wired", err)
	}
	cfg, err := deps.CrowdSec.GetConfig(ctx)
	if err != nil || cfg.ActiveEnabled {
		t.Fatal("unconfigured CrowdSec falsely enabled", err)
	}
	if deps.ACMEObserver == nil {
		t.Fatal("registered observation missing")
	}
}
func TestCLIRollbackWithoutHistoryPreservesKeyAndReleasesLease(t *testing.T) {
	opts := startupOptions(t)
	state, err := openState(opts)
	if err != nil {
		t.Fatal(err)
	}
	state.Close()
	before, err := os.ReadFile(opts.MasterKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = runCLISnapshot(nil, true); err == nil || !strings.Contains(err.Error(), "no snapshot found") {
		t.Fatal("unexpected CLI recovery failure", err)
	}
	assertLeaseReleased(t, opts.DBPath)
	after, err := os.ReadFile(opts.MasterKeyFile)
	if err != nil || string(before) != string(after) {
		t.Fatal("CLI replaced original master key", err)
	}
}
