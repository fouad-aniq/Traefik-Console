package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidatesWithoutCreatingFilesOrLoadingKeys(t *testing.T) {
	for _, name := range []string{"TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE", "ALLOW_TRAEFIK_RESTART", "BROKER_SOCKET"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	db := filepath.Join(root, "new-state", "console.db")
	key := filepath.Join(root, "new-keys", "master.key")
	t.Setenv("DB_PATH", db)
	t.Setenv("MASTER_KEY_FILE", key)
	t.Setenv("PORT", "8081")
	t.Setenv("BIND_ADDRESS", "127.0.0.1")
	t.Setenv("MAX_JOURNAL_BYTES", "1048576")
	t.Setenv("CROWDSEC_PROTECTED_CIDRS", "127.0.0.0/8,::1/128")
	t.Setenv("TRAEFIK_CONVERGENCE_TIMEOUT_SEC", "15")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("CONSOLE_HOST", " console.example.invalid:8081 ")
	t.Setenv("CONSOLE_ORIGIN", "https://console.example.invalid/")
	opts, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if opts.DBPath != db || opts.MasterKeyFile != key || opts.JournalLimitBytes != 1048576 {
		t.Fatal("settings changed")
	}
	if opts.ConsoleHost != "console.example.invalid:8081" || opts.ConsoleOrigin != "https://console.example.invalid" {
		t.Fatal("HTTP policy was not captured at startup")
	}
	for _, p := range []string{filepath.Dir(db), filepath.Dir(key)} {
		if _, e = os.Lstat(p); !os.IsNotExist(e) {
			t.Fatal("settings load caused a persistent effect", e)
		}
	}
	t.Setenv("MAX_JOURNAL_BYTES", "invalid")
	if _, e = Load(); e == nil {
		t.Fatal("invalid quota accepted")
	}
	if _, e = os.Lstat(key); !os.IsNotExist(e) {
		t.Fatal("invalid settings generated a key")
	}
}
func TestLegacyDefaultsAndTimeoutFallbackRemain(t *testing.T) {
	for _, name := range []string{"PORT", "BIND_ADDRESS", "DB_PATH", "MASTER_KEY_FILE", "DOCKER_HOST", "MAX_JOURNAL_BYTES", "TRAEFIK_SECRETS_DIR", "TRAEFIK_ACME_DIR", "CROWDSEC_PROTECTED_CIDRS", "TRAEFIK_CONVERGENCE_TIMEOUT_SEC", "TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE", "ALLOW_TRAEFIK_RESTART", "BROKER_SOCKET"} {
		t.Setenv(name, "")
	}
	opts, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	expected, _ := filepath.Abs("./data/console.db")
	key, _ := filepath.Abs("./keys/master.key")
	if opts.DBPath != expected || opts.MasterKeyFile != key || opts.Port != "8081" || opts.BindAddress != "127.0.0.1" || opts.JournalLimitBytes != 512*1024*1024 || opts.RuntimeSecretsDir != "/etc/traefik/secrets" {
		t.Fatal("defaults changed")
	}
	if opts.CrowdSec.LAPIURL != "" || opts.CrowdSec.ReadTimeout.Seconds() != 20 || opts.CrowdSec.AlertLimit != 500 {
		t.Fatal("CrowdSec must be explicitly registered with bounded read defaults")
	}
	t.Setenv("TRAEFIK_CONVERGENCE_TIMEOUT_SEC", "nonsense")
	if opts, e = Load(); e != nil || opts.VerifyConvergenceTo.Seconds() != 15 {
		t.Fatal("legacy fallback changed", e)
	}
}

func TestCrowdSecSettingsAreExplicitAndBounded(t *testing.T) {
	t.Setenv("CROWDSEC_LAPI_URL", " http://crowdsec:8080/ ")
	t.Setenv("CROWDSEC_READ_TIMEOUT", "1000")
	t.Setenv("CROWDSEC_ALERT_LIMIT", "10000")
	opts, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if opts.CrowdSec.LAPIURL != "http://crowdsec:8080" || opts.CrowdSec.ReadTimeout.Seconds() != 120 || opts.CrowdSec.AlertLimit != 1000 {
		t.Fatalf("unexpected bounded settings: %+v", opts.CrowdSec)
	}
	t.Setenv("CROWDSEC_READ_TIMEOUT", "0")
	t.Setenv("CROWDSEC_ALERT_LIMIT", "0")
	t.Setenv("CROWDSEC_CLIENT_CERT", "/run/crowdsec-client.crt")
	t.Setenv("CROWDSEC_CLIENT_KEY", "/run/crowdsec-client.key")
	opts, err = Load()
	if err != nil || opts.CrowdSec.ReadTimeout.Seconds() != 20 || opts.CrowdSec.AlertLimit != 500 {
		t.Fatalf("zero must use bounded defaults, not unlimited reads: %+v %v", opts.CrowdSec, err)
	}
	if opts.CrowdSec.ClientCertFile != "/run/crowdsec-client.crt" || opts.CrowdSec.ClientKeyFile != "/run/crowdsec-client.key" || !opts.CrowdSec.ReadTimeoutSet || !opts.CrowdSec.AlertLimitSet {
		t.Fatalf("shared mTLS or environment source settings were lost: %+v", opts.CrowdSec)
	}
}
