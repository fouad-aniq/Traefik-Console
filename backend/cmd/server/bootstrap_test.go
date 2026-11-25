package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/secrets"
)

func startupOptions(t *testing.T) *config.Options {
	t.Helper()
	for _, name := range []string{"TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE", "ALLOW_TRAEFIK_RESTART", "BROKER_SOCKET"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	for name, value := range map[string]string{"DB_PATH": filepath.Join(root, "state", "console.db"), "MASTER_KEY_FILE": filepath.Join(root, "keys", "master.key"), "STATIC_FILE": filepath.Join(root, "config", "traefik.yml"), "DYNAMIC_DIR": filepath.Join(root, "dynamic"), "SECRETS_DIR": filepath.Join(root, "secrets"), "DOCKER_HOST": "", "CROWDSEC_PROTECTED_CIDRS": "127.0.0.0/8,::1/128", "MAX_JOURNAL_BYTES": "1048576", "PORT": "8081", "BIND_ADDRESS": "127.0.0.1", "TRAEFIK_CONVERGENCE_TIMEOUT_SEC": "15"} {
		t.Setenv(name, value)
	}
	opts, _, e := loadOptions()
	if e != nil {
		t.Fatal(e)
	}
	return opts
}
func assertLeaseReleased(t *testing.T, path string) {
	t.Helper()
	f, e := filesystem.AcquireLease(path + ".lock")
	if e != nil {
		t.Fatal("state lease leaked", e)
	}
	f.Close()
}
func TestStateLeasePrecedesKeyCreationAndOriginalKeyIsReusable(t *testing.T) {
	opts := startupOptions(t)
	first, e := openState(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	original, _ := os.ReadFile(opts.MasterKeyFile)
	if e = os.Remove(opts.MasterKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, e = openState(opts); e == nil {
		t.Fatal("second writer accepted")
	}
	if _, e = os.Lstat(opts.MasterKeyFile); !os.IsNotExist(e) {
		t.Fatal("second writer generated a replacement key")
	}
	if e = os.WriteFile(opts.MasterKeyFile, original, 0400); e != nil {
		t.Fatal(e)
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := openState(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if !bytes.Equal(first.key, next.key) {
		t.Fatal("original key not reused")
	}
}
func TestExistingStateWithoutKeyFailsAndReleasesLease(t *testing.T) {
	opts := startupOptions(t)
	if e := filesystem.EnsureDir(filepath.Dir(opts.DBPath)); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(opts.DBPath, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openState(opts); !errors.Is(e, secrets.ErrMasterKeyRequired) {
		t.Fatal("missing original key accepted", e)
	}
	if _, e := os.Lstat(opts.MasterKeyFile); !os.IsNotExist(e) {
		t.Fatal("replacement key created")
	}
	assertLeaseReleased(t, opts.DBPath)
}
func TestDatabaseFailureReleasesLeaseWithoutChangingOriginalKey(t *testing.T) {
	opts := startupOptions(t)
	for _, p := range []string{filepath.Dir(opts.DBPath), filepath.Dir(opts.MasterKeyFile)} {
		if e := filesystem.EnsureDir(p); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := secrets.LoadMasterKey(opts.MasterKeyFile, false); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(opts.MasterKeyFile)
	if e := os.WriteFile(opts.DBPath, []byte("invalid database"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openState(opts); e == nil {
		t.Fatal("corrupt database accepted")
	}
	assertLeaseReleased(t, opts.DBPath)
	now, _ := os.ReadFile(opts.MasterKeyFile)
	if !bytes.Equal(original, now) {
		t.Fatal("key changed after DB failure")
	}
}
func TestLaterInitializationFailureClosesState(t *testing.T) {
	opts := startupOptions(t)
	if e := os.WriteFile(opts.TraefikDynamicDir, []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runServer(); e == nil {
		t.Fatal("invalid configuration directory accepted")
	}
	assertLeaseReleased(t, opts.DBPath)
}
func TestInvalidSettingsFailBeforeKeyGeneration(t *testing.T) {
	opts := startupOptions(t)
	for _, tc := range []struct{ name, value string }{{"MAX_JOURNAL_BYTES", "invalid"}, {"DOCKER_HOST", "unix://unexpected-host/path"}, {"CROWDSEC_PROTECTED_CIDRS", "invalid"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if e := runServer(); e == nil {
				t.Fatal("invalid settings accepted")
			}
			if _, e := os.Lstat(opts.MasterKeyFile); !os.IsNotExist(e) {
				t.Fatal("invalid settings created key")
			}
		})
	}
}
