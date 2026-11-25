package crowdsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/traefik-console/internal/models"
)

// savedStore returns one stored integration row.
type savedStore struct {
	externalStore
	payload []byte
}

func (s savedStore) GetItem(_ context.Context, kind models.ItemKind, name string) (*models.ConfigItemRow, error) {
	if kind == models.KindCrowdSec && name == "integration" && s.payload != nil {
		return &models.ConfigItemRow{Kind: kind, Name: name, Payload: s.payload, Version: 3}, nil
	}
	return nil, nil
}

func (savedStore) ListItemsByKind(context.Context, models.ItemKind) ([]models.ConfigItemRow, error) {
	return nil, nil
}

func savedPayload(t *testing.T, applier *externalApplier) CrowdSecConfig {
	t.Helper()
	for _, row := range applier.mutation.Upserts {
		if row.Kind == models.KindCrowdSec {
			var cfg CrowdSecConfig
			if err := json.Unmarshal(row.Payload, &cfg); err != nil {
				t.Fatal(err)
			}
			return cfg
		}
	}
	t.Fatal("no integration row was written")
	return CrowdSecConfig{}
}

func lapiServer(t *testing.T, key string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, `{"status":"up"}`)
			return
		}
		if r.Header.Get("X-Api-Key") != key {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestConnectionSettingsAreSavedWithoutTouchingProtection(t *testing.T) {
	server := lapiServer(t, "ui-key")
	applier := &externalApplier{}
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: applier, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	_, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{
		LAPIURL: server.URL + "/", LAPIKey: "ui-key", MetricsURL: "http://crowdsec:6060", MetricsToken: "metrics-secret",
		AccessLogSource: "/var/log/traefik/access.log", ProtectedCIDRs: []string{"10.0.0.0/8"}, AppSecURL: "http://crowdsec:7422",
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := savedPayload(t, applier)
	if cfg.Enabled || cfg.LAPIURL != server.URL || cfg.MetricsURL != "http://crowdsec:6060/metrics" || cfg.AccessLogSource != "/var/log/traefik/access.log" || cfg.AppSecURL != "http://crowdsec:7422" {
		t.Fatalf("connection settings were not saved: %+v", cfg)
	}
	if len(cfg.ProtectedCIDRs) != 1 || cfg.ProtectedCIDRs[0] != "10.0.0.0/8" || !cfg.MetricsTokenSet || !cfg.KeepEnrollments {
		t.Fatalf("unexpected saved connection: %+v", cfg)
	}
	for _, row := range applier.mutation.Upserts {
		if row.Kind != models.KindCrowdSec {
			t.Fatalf("a connection save changed Traefik configuration: %+v", row)
		}
		if strings.Contains(string(row.Payload), "secret") || strings.Contains(string(row.Payload), "ui-key") {
			t.Fatal("a secret was written into the configuration row")
		}
	}
	names := map[string]bool{}
	for _, secret := range applier.mutation.Secrets {
		names[secret.Name] = true
	}
	if !names[crowdsecSecretName] || !names[crowdsecMetricsTokenSecret] {
		t.Fatalf("connection secrets were not stored encrypted: %+v", names)
	}
}

func TestEnvironmentLocksConnectionSettings(t *testing.T) {
	server := lapiServer(t, "environment-key")
	applier := &externalApplier{}
	_, protected, _ := net.ParseCIDR("192.168.0.0/16")
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: applier, defaultLAPIURL: server.URL, protected: []*net.IPNet{protected},
		settings: Settings{APIKey: "environment-key", MetricsURL: "http://env:6060/metrics", MetricsToken: "env-token", AccessLogSource: "/env.log", AppSecURL: "http://env:7422", ProtectedCIDRsSet: true, ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	_, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{
		LAPIURL: server.URL, LAPIKey: "browser-key", MetricsURL: "http://ui:6060", MetricsToken: "ui-token", AccessLogSource: "/ui.log", AppSecURL: "http://ui:7422", ProtectedCIDRs: []string{"10.0.0.0/8"},
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(applier.mutation.Secrets) != 0 {
		t.Fatalf("an environment secret was replaced: %+v", applier.mutation.Secrets)
	}
	cfg := savedPayload(t, applier)
	if cfg.MetricsURL != "" || cfg.AccessLogSource != "" || cfg.ProtectedCIDRs != nil || cfg.AppSecURL != "http://env:7422" {
		t.Fatalf("locked settings took UI values: %+v", cfg)
	}
	// A different LAPI address than the registered one is refused.
	if _, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{LAPIURL: "http://other:8080"}, models.WriteOptions{DryRun: true}); !errors.Is(err, ErrCrowdSecUnreachable) {
		t.Fatalf("registered LAPI address was replaced: %v", err)
	}
	read, err := manager.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if read.MetricsURL != "http://env:6060/metrics" || read.AccessLogSource != "/env.log" || !read.MetricsTokenSet || read.MetricsToken != "" || len(read.ProtectedCIDRs) != 1 || read.ProtectedCIDRs[0] != "192.168.0.0/16" {
		t.Fatalf("environment values are not reported: %+v", read)
	}
}

func TestProtectionSaveKeepsConnectionSettings(t *testing.T) {
	server := lapiServer(t, "saved-key")
	payload, _ := json.Marshal(CrowdSecConfig{IntegrationMode: "external", LAPIURL: server.URL, MetricsURL: "http://crowdsec:6060/metrics", AccessLogSource: "/a.log", ProtectedCIDRs: []string{"10.0.0.0/8"}, MetricsTokenSet: true})
	applier := &externalApplier{}
	manager := &crowdsecManager{store: savedStore{payload: payload}, secrets: &externalSecrets{values: map[string]string{crowdsecSecretName: "saved-key"}}, applier: applier, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	if _, err := manager.ConfigureBouncer(context.Background(), CrowdSecConfig{IntegrationMode: "external", LAPIURL: server.URL}, models.WriteOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	cfg := savedPayload(t, applier)
	if cfg.MetricsURL != "http://crowdsec:6060/metrics" || cfg.AccessLogSource != "/a.log" || len(cfg.ProtectedCIDRs) != 1 || !cfg.MetricsTokenSet {
		t.Fatalf("a protection save dropped connection settings: %+v", cfg)
	}
}

func TestConnectionRejectsInvalidSettings(t *testing.T) {
	server := lapiServer(t, "k")
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: &externalApplier{}, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	for name, in := range map[string]ConnectionSettings{
		"metrics path":   {LAPIURL: server.URL, MetricsURL: "http://crowdsec:6060/other"},
		"open network":   {LAPIURL: server.URL, ProtectedCIDRs: []string{"0.0.0.0/0"}},
		"log injection":  {LAPIURL: server.URL, AccessLogSource: "a\"b"},
		"lapi with path": {LAPIURL: server.URL + "/v1"},
		"wrong key":      {LAPIURL: server.URL, LAPIKey: "bad"},
	} {
		if _, err := manager.ConfigureConnection(context.Background(), in, models.WriteOptions{DryRun: true}); err == nil {
			t.Errorf("%s: invalid connection was accepted", name)
		}
	}
	if _, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL, GenerateKey: true}, models.WriteOptions{DryRun: true}); !errors.Is(err, ErrCrowdSecUnavailableCapability) {
		t.Fatalf("key generation without a registered container: %v", err)
	}
}

func TestConnectionTestReportsEachPart(t *testing.T) {
	server := lapiServer(t, "good")
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	result, err := manager.TestConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL})
	if err != nil || result.LAPI != "reachable" || result.LAPIURL != server.URL || result.Machine != "not_configured" || result.CanGenerateKey {
		t.Fatalf("health probe: %+v %v", result, err)
	}
	if result, err = manager.TestConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL, LAPIKey: "good"}); err != nil || result.LAPI != "ok" {
		t.Fatalf("valid key: %+v %v", result, err)
	}
	if result, err = manager.TestConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL, LAPIKey: "bad"}); err != nil || result.LAPI != "invalid_key" {
		t.Fatalf("rejected key: %+v %v", result, err)
	}
	if result, err = manager.TestConnection(context.Background(), ConnectionSettings{LAPIURL: "http://127.0.0.1:1"}); err != nil || result.LAPI != "unreachable" {
		t.Fatalf("closed port: %+v %v", result, err)
	}
}

func TestSavedProtectedNetworksGuardManualBans(t *testing.T) {
	payload, _ := json.Marshal(CrowdSecConfig{ProtectedCIDRs: []string{"203.0.113.0/24"}})
	manager := &crowdsecManager{store: savedStore{payload: payload}, secrets: &externalSecrets{}, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	if err := manager.AddDecision(context.Background(), "203.0.113.7", "1h", ""); !errors.Is(err, ErrCrowdSecInvalidIP) {
		t.Fatalf("a saved protected network could be banned: %v", err)
	}
}

func TestOneFormEnablesManagedProtectionWithoutRouters(t *testing.T) {
	server := lapiServer(t, "ui-key")
	applier := &externalApplier{}
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: applier, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	_, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{
		LAPIURL: server.URL, LAPIKey: "ui-key", Protection: &ProtectionSettings{Mode: "managed", BouncerMode: "live", PluginVersion: "v1.8.2", UpdateMaxFailure: -1},
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[models.ItemKind]int{}
	for _, row := range applier.mutation.Upserts {
		kinds[row.Kind]++
	}
	if kinds[models.KindPlugin] != 1 || kinds[models.KindMiddleware] != 1 || kinds[models.KindRouter] != 0 {
		t.Fatalf("unexpected protection changes: %+v", kinds)
	}
	cfg := savedPayload(t, applier)
	if !cfg.Enabled || cfg.Mode != "live" || cfg.PluginVersion != "v1.8.2" || cfg.UpdateMaxFailure != -1 {
		t.Fatalf("protection settings were not applied: %+v", cfg)
	}
	if _, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL, Protection: &ProtectionSettings{Mode: "sometimes"}}, models.WriteOptions{DryRun: true}); err == nil {
		t.Fatal("an unknown protection mode was accepted")
	}
}

func TestMonitoringCredentialsNeedTheContainer(t *testing.T) {
	server := lapiServer(t, "k")
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: &externalApplier{}, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	if _, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{LAPIURL: server.URL, GenerateMachine: true}, models.WriteOptions{DryRun: true}); !errors.Is(err, ErrCrowdSecUnavailableCapability) {
		t.Fatalf("monitoring credentials without a reachable container: %v", err)
	}
}

func TestGeneratedSecretKeepsTheProposedValue(t *testing.T) {
	proposed := strings.Repeat("ab", 32)
	if got, err := generatedSecret(proposed); err != nil || got != proposed {
		t.Fatalf("a proposed credential was replaced: %q %v", got, err)
	}
	for _, weak := range []string{"", "short", strings.Repeat("AB", 32), strings.Repeat("zz", 32)} {
		if got, err := generatedSecret(weak); err != nil || got == weak || !generatedSecretPattern.MatchString(got) {
			t.Fatalf("an unusable proposal %q was kept: %q %v", weak, got, err)
		}
	}
}

func TestEnabledProtectionCanCreateMonitoringAccess(t *testing.T) {
	server := lapiServer(t, "ui-key")
	applier := &externalApplier{}
	manager := &crowdsecManager{store: savedStore{}, secrets: &externalSecrets{}, applier: applier, settings: Settings{ReadTimeout: 20 * time.Second, AlertLimit: 500}}
	password := strings.Repeat("cd", 32)
	_, err := manager.ConfigureConnection(context.Background(), ConnectionSettings{
		LAPIURL: server.URL, LAPIKey: "ui-key", GenerateMachine: true, MachinePassword: password, Protection: &ProtectionSettings{Mode: "managed"},
	}, models.WriteOptions{DryRun: true})
	if err != nil {
		t.Fatalf("creating monitoring access with protection on was refused: %v", err)
	}
	cfg := savedPayload(t, applier)
	if !cfg.AutoEnrollMachine || cfg.MachineID != "" {
		t.Fatalf("the machine is not left for the operation to register: %+v", cfg)
	}
	staged := false
	for _, secret := range applier.mutation.Secrets {
		staged = staged || secret.Name == crowdsecMachineSecret
	}
	if !staged {
		t.Fatal("the proposed machine password was not staged")
	}
}
