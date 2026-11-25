package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/files"
)

func TestPreflightPreservesFreshConfigurationAndRejectsCollision(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"dynamic", "secrets", "keys", "data"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	opts := &config.Options{TraefikStaticPath: filepath.Join(root, "traefik.yml"), TraefikDynamicDir: filepath.Join(root, "dynamic"), SecretsDir: filepath.Join(root, "secrets"), MasterKeyFile: filepath.Join(root, "keys", "master.key"), DBPath: filepath.Join(root, "data", "console.db"), Auth: config.AuthOptions{DBPath: filepath.Join(root, "data", "auth.db")}}
	t.Setenv("TRAEFIK_DYNAMIC_DIR", "/etc/traefik/dynamic")
	if err := os.WriteFile(opts.TraefikStaticPath, []byte("providers: {file: {directory: /etc/traefik/dynamic, watch: true}}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkLayout(opts); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{opts.DBPath, opts.Auth.DBPath, opts.MasterKeyFile, filepath.Join(root, files.ConsoleDomainsFilename)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected preflight side effect %s", path)
		}
	}
	collision := filepath.Join(opts.TraefikDynamicDir, files.ManagedDynamicFilename)
	if err := os.WriteFile(collision, []byte("operator content"), 0600); err != nil {
		t.Fatal(err)
	}
	if checkLayout(opts) == nil {
		t.Fatal("accepted existing reserved filename")
	}
	b, err := os.ReadFile(collision)
	if err != nil || string(b) != "operator content" {
		t.Fatal("modified existing file")
	}
}
