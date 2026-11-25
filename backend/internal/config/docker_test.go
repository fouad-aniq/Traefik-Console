package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetDockerEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DOCKER_HOST", "TRAEFIK_CONTAINER", "CROWDSEC_CONTAINER", "TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE", "ALLOW_TRAEFIK_RESTART", "BROKER_SOCKET", "BROKER_LISTEN_SOCKET", "BROKER_DOCKER_HOST", "BROKER_TRAEFIK_ID", "BROKER_CROWDSEC_ID", "BROKER_COMPOSE_PROJECT", "BROKER_TRAEFIK_SERVICE", "BROKER_CROWDSEC_SERVICE", "BROKER_ALLOW_RESTART", "BROKER_TRAEFIK_SECRETS_DIR"} {
		t.Setenv(name, "")
	}
}
func TestLegacyBrokerSettingsAreNotSilentlyIgnored(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	t.Setenv("BROKER_SOCKET", "unix:///private/old.sock")
	if _, e := Load(); e == nil || !strings.Contains(e.Error(), "legacy custom broker") {
		t.Fatal("legacy broker configuration silently accepted")
	}
}

func TestObsoleteACMEPathHasExplicitMigration(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	t.Setenv("TRAEFIK_ACME_PATH", "/operator/old-acme.json")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "acme folder") || strings.Contains(err.Error(), "/operator/") {
		t.Fatal("obsolete setting silently accepted or path leaked", err)
	}
}
func TestRenamedFolderSettingsPointToNewNames(t *testing.T) {
	for _, pair := range [][2]string{{"TRAEFIK_STATIC_PATH", "STATIC_FILE"}, {"TRAEFIK_RUNTIME_DYNAMIC_DIR", "TRAEFIK_DYNAMIC_DIR"}, {"ACME_OBSERVATION_DIR", "acme folder"}, {"ACME_DIR", "acme folder"}, {"TRAEFIK_ACME_DIR", "acme folder"}} {
		resetDockerEnv(t)
		t.Setenv("PORT", "8081")
		t.Setenv("BIND_ADDRESS", "127.0.0.1")
		for _, name := range []string{"TRAEFIK_STATIC_PATH", "TRAEFIK_RUNTIME_DYNAMIC_DIR", "ACME_OBSERVATION_DIR", "ACME_DIR", "TRAEFIK_ACME_DIR"} {
			t.Setenv(name, "")
		}
		t.Setenv(pair[0], "/operator/old")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), pair[1]) || strings.Contains(err.Error(), "/operator/") {
			t.Fatalf("%s accepted or not pointed to %s: %v", pair[0], pair[1], err)
		}
	}
}

func TestACMEFolderIsFoundNextToTheDynamicFolder(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	root := t.TempDir()
	t.Setenv("DYNAMIC_DIR", filepath.Join(root, "dynamic"))
	opts, err := Load()
	if err != nil || opts.ACMEObservationDir != "" || opts.RuntimeACMEDir != TraefikACMEDir {
		t.Fatalf("missing acme folder must be unmapped: %v %+v", err, opts)
	}
	if err := os.Mkdir(filepath.Join(root, "acme"), 0700); err != nil {
		t.Fatal(err)
	}
	opts, err = Load()
	if err != nil || opts.ACMEObservationDir != filepath.Join(root, "acme") {
		t.Fatalf("acme folder next to the dynamic folder not found: %v %+v", err, opts)
	}
}
func TestDockerSettingsAreOperatorValues(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	t.Setenv("TRAEFIK_SECRETS_DIR", "/etc/traefik/secrets")
	t.Setenv("MAX_JOURNAL_BYTES", "")
	t.Setenv("CROWDSEC_PROTECTED_CIDRS", "")
	t.Setenv("DOCKER_HOST", "tcp://socket-proxy:2375")
	t.Setenv("ALLOW_TRAEFIK_RESTART", "true")
	opts, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if opts.Docker.Host != "tcp://socket-proxy:2375" || !opts.Docker.AllowRestart || opts.Docker.TraefikContainer != "traefik" || opts.Docker.CrowdSecContainer != "" {
		t.Fatal("Docker settings lost or default container name missing", opts.Docker)
	}
	t.Setenv("TRAEFIK_CONTAINER", "edge-proxy")
	t.Setenv("CROWDSEC_CONTAINER", "crowdsec")
	if opts, e = Load(); e != nil || opts.Docker.TraefikContainer != "edge-proxy" || opts.Docker.CrowdSecContainer != "crowdsec" {
		t.Fatal("container names not taken from the environment", e)
	}
	for _, old := range []string{"TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE"} {
		t.Setenv(old, "registered")
		if _, e = Load(); e == nil || !strings.Contains(e.Error(), "TRAEFIK_CONTAINER") {
			t.Fatal("obsolete Docker setting silently accepted", old, e)
		}
		t.Setenv(old, "")
	}
	t.Setenv("ALLOW_TRAEFIK_RESTART", "yes")
	if _, e = Load(); e == nil {
		t.Fatal("ambiguous restart opt-in accepted")
	}
}

func TestAutoSnapshotBeforeStaticCanBeEnabled(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	for value, want := range map[string]bool{"": false, "false": false, "no": false, "true": true, "yes": true, "1": true} {
		t.Setenv("AUTO_SNAPSHOT_BEFORE_STATIC", value)
		opts, err := Load()
		if err != nil || opts.AutoSnapshotBeforeStatic != want {
			t.Fatalf("AUTO_SNAPSHOT_BEFORE_STATIC=%q: got %v, want %v (%v)", value, opts != nil && opts.AutoSnapshotBeforeStatic, want, err)
		}
	}
	t.Setenv("AUTO_SNAPSHOT_BEFORE_STATIC", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("invalid AUTO_SNAPSHOT_BEFORE_STATIC accepted")
	}
}

func TestDNSEnvFileSitsNextToTheDynamicFolder(t *testing.T) {
	resetDockerEnv(t)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	root := t.TempDir()
	t.Setenv("DYNAMIC_DIR", filepath.Join(root, "dynamic"))
	opts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if opts.DNSEnv == nil || opts.DNSEnv.Path != filepath.Join(root, DNSProvidersEnvFile) || opts.DNSEnv.SecretsRoot != "/etc/traefik/secrets" {
		t.Fatalf("unexpected DNS env file: %+v", opts.DNSEnv)
	}
}
