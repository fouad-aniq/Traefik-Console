package resolvers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/core/resolvers/providers"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

func TestAllEightProvidersExportEveryRequiredFieldWithoutPersisting(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	database, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	key := make([]byte, 32)
	s := db.NewStore(database, key)

	sm, e := secrets.New(filepath.Join(root, "secrets"), filepath.Join(root, "secrets"), key, s)
	if e != nil {
		t.Fatal(e)
	}
	reg, e := providers.LoadRegistry()
	if e != nil {
		t.Fatal(e)
	}
	if len(reg.Providers()) != 8 {
		t.Fatal("guided provider scope changed")
	}
	m := &resolverManager{store: s, secrets: sm, registry: reg, acmeStorageDir: "/etc/traefik/acme"}
	for _, p := range reg.Providers() {
		for alt, a := range p.Alternatives {
			t.Run(p.Code+"/"+a.Name, func(t *testing.T) {
				values := map[string]string{}
				for _, f := range a.Required {
					values[f.Env] = "synthetic-provider-value"
				}
				cfg := models.ResolverInput{Name: "audit", Type: "acme", Environment: "staging", Email: "audit@example.invalid", Challenge: "dns01", Provider: p.Code, Alternative: alt, Values: values}
				for _, f := range p.Options {
					if f.Type == "duration" {
						cfg.Values[f.Env] = "120s"
						break
					}
				}
				pending := []models.SecretRow{}
				if e = m.validateAndBuild(context.Background(), &cfg, nil, &pending); e != nil {
					t.Fatal(e)
				}
				envs := map[string]bool{}
				for _, secret := range pending {
					envs[secret.EnvVarName] = true
					if cfg.Values[strings.TrimSuffix(secret.EnvVarName, "_FILE")] == "120s" {
						block, _ := aes.NewCipher(key)
						gcm, _ := cipher.NewGCM(block)
						raw := secret.EncryptedValue
						plain, e := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
						if e != nil || string(plain) != "120" {
							t.Fatal("lego expects integer seconds", e)
						}
					}
				}
				for _, f := range a.Required {
					if !envs[f.Env+"_FILE"] {
						t.Fatal("required provider field is not exported", f.Env)
					}
					if f.Secret && cfg.Values[f.Env] != "••••••••" {
						t.Fatal("raw credential in resolver model")
					}
				}
				stored, _ := s.ListSecrets(context.Background())
				if len(stored) != 0 {
					t.Fatal("validation persisted secrets outside engine")
				}
				raw, _ := json.Marshal(cfg)
				if strings.Contains(string(raw), `"rendered_node":{"acme":{"secret"`) {
					t.Fatal("unexpected secret rendering")
				}
			})
		}
	}
}
func TestCustomResolverValidation(t *testing.T) {
	reg, _ := providers.LoadRegistry()
	m := &resolverManager{registry: reg, acmeStorageDir: "/etc/traefik/acme"}
	for _, tc := range []struct {
		yaml       string
		ack, valid bool
	}{{"acme:\n  tlsChallenge: {}\n", false, true}, {"acme:\n  unknown: true\n  tlsChallenge: {}\n", false, false}, {"acme:\n  dnsChallenge:\n    provider: exec\n", true, false}, {"acme:\n  dnsChallenge:\n    provider: cloudflare\n", false, false}, {"acme:\n  dnsChallenge:\n    provider: cloudflare\n", true, true}, {"acme:\n  eab:\n    hmacEncoded: synthetic\n  tlsChallenge: {}\n", true, false}} {
		cfg := models.ResolverInput{Name: "audit", CustomYAML: tc.yaml, ExternalCredentials: tc.ack}
		e := m.buildCustomYAMLResolver(&cfg)
		if (e == nil) != tc.valid {
			t.Fatal(tc.yaml, e)
		}
	}
}
