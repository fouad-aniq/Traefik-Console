package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleCreatesAndCompletesTheDNSEnvFile(t *testing.T) {
	f := &DNSEnvFile{Path: filepath.Join(t.TempDir(), DNSProvidersEnvFile), SecretsRoot: "/etc/traefik/secrets"}
	if f.Load() != nil {
		t.Fatal("a missing file must load as nil")
	}
	token := "/etc/traefik/secrets/cloudflare_dns_api_token.secret"
	changed, err := f.Ensure(map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": token})
	if err != nil || !changed {
		t.Fatalf("create: changed=%v err=%v", changed, err)
	}
	if got := f.Load(); len(got) != 1 || got["CLOUDFLARE_DNS_API_TOKEN_FILE"] != token {
		t.Fatalf("after create: %v", got)
	}
	if changed, err = f.Ensure(map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": token}); err != nil || changed {
		t.Fatalf("same lines again must not rewrite: changed=%v err=%v", changed, err)
	}
	// Lines the user or an older release wrote are kept; a wrong path is corrected in place.
	raw, _ := os.ReadFile(f.Path)
	edited := string(raw) + "# my note\nOVH_ENDPOINT_FILE=/etc/traefik/secrets/old.secret\n"
	if err = os.WriteFile(f.Path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = f.Ensure(map[string]string{
		"OVH_ENDPOINT_FILE":   "/etc/traefik/secrets/ovh_endpoint.secret",
		"CLOUDFLARE_TTL_FILE": "/etc/traefik/secrets/cloudflare_ttl.secret",
	})
	if err != nil || !changed {
		t.Fatalf("update: changed=%v err=%v", changed, err)
	}
	raw, _ = os.ReadFile(f.Path)
	text := string(raw)
	for _, want := range []string{"# my note\n", "CLOUDFLARE_DNS_API_TOKEN_FILE=" + token + "\n", "OVH_ENDPOINT_FILE=/etc/traefik/secrets/ovh_endpoint.secret\n", "CLOUDFLARE_TTL_FILE=/etc/traefik/secrets/cloudflare_ttl.secret\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old.secret") || strings.Count(text, "OVH_ENDPOINT_FILE=") != 1 {
		t.Fatalf("the old path must be replaced, not duplicated:\n%s", text)
	}
}

func TestDNSEnvFileRefusesUnsafeLines(t *testing.T) {
	f := &DNSEnvFile{Path: filepath.Join(t.TempDir(), DNSProvidersEnvFile), SecretsRoot: "/etc/traefik/secrets"}
	for key, value := range map[string]string{
		"PLAIN_VALUE":    "/etc/traefik/secrets/a.secret",
		"ESCAPE_FILE":    "/etc/traefik/secrets/../../etc/passwd",
		"ELSEWHERE_FILE": "/run/secrets/token",
		"INJECT_FILE":    "/etc/traefik/secrets/a.secret\nPATH=/tmp",
		"SHELL_FILE":     "/etc/traefik/secrets/$(id).secret",
		"lowercase_FILE": "/etc/traefik/secrets/a.secret",
	} {
		if _, err := f.Ensure(map[string]string{key: value}); err == nil {
			t.Fatalf("%s=%q must be refused", key, value)
		}
	}
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Fatal("a refused line must not create the file")
	}
	content := "# comment\nCLOUDFLARE_DNS_API_TOKEN_FILE=\"/etc/traefik/secrets/t.secret\"\nPLAIN=x\nESCAPE_FILE=/etc/traefik/secrets/../x\n"
	if err := os.WriteFile(f.Path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := f.Load(); len(got) != 1 || got["CLOUDFLARE_DNS_API_TOKEN_FILE"] != "/etc/traefik/secrets/t.secret" {
		t.Fatalf("load must keep only safe NAME_FILE lines: %v", got)
	}
}
