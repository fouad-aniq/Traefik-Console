package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
)

type inspectResult struct {
	status *docker.ContainerStatus
	err    error
}

func (r inspectResult) InspectTraefikContainer(context.Context) (*docker.ContainerStatus, error) {
	return r.status, r.err
}

func TestCredentialVariablesComeFromContainerOrConsoleEnvFile(t *testing.T) {
	payload, err := json.Marshal(models.ResolverInput{Values: map[string]string{"CLOUDFLARE_DNS_API_TOKEN": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	items := []models.ConfigItemRow{{Kind: models.KindResolver, Name: "le", Payload: payload}}
	path := "/etc/traefik/secrets/cloudflare_dns_api_token.secret"
	secrets := []models.SecretRow{{Name: "cloudflare_dns_api_token", EnvVarName: "CLOUDFLARE_DNS_API_TOKEN_FILE", FilePath: path}}
	envFile := &config.DNSEnvFile{Path: filepath.Join(t.TempDir(), config.DNSProvidersEnvFile), SecretsRoot: "/etc/traefik/secrets"}
	disabled := inspectResult{err: docker.ErrCapabilityDisabled}
	has := map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": path}
	for _, tc := range []struct {
		name         string
		d            docker.TraefikContainerInspector
		file         *config.DNSEnvFile
		set, viaFile bool
	}{
		{"no Docker access and no env file", disabled, nil, false, false},
		{"no Docker access, Console keeps the env file", disabled, envFile, true, true},
		{"container has it", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: has}}, envFile, true, false},
		{"Traefik loads the env file", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{}, LoadsDNSEnvFile: true}}, envFile, true, true},
		{"Traefik does not load the env file", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{}}}, envFile, false, false},
	} {
		got := checkContainerSecretEnvVars(context.Background(), items, secrets, tc.d, tc.file)
		if len(got) != 1 || got[0].EnvVarName != "CLOUDFLARE_DNS_API_TOKEN_FILE" || got[0].AlreadySet != tc.set || got[0].ViaEnvFile != tc.viaFile {
			t.Fatalf("%s: got %+v, want set=%v viaFile=%v", tc.name, got, tc.set, tc.viaFile)
		}
	}

	g := &generator{dnsEnv: envFile}
	envs := checkContainerSecretEnvVars(context.Background(), items, secrets, disabled, envFile)
	changed, err := g.EnsureDNSEnv(envs)
	if err != nil || !changed {
		t.Fatalf("EnsureDNSEnv: changed=%v err=%v", changed, err)
	}
	if envFile.Load()["CLOUDFLARE_DNS_API_TOKEN_FILE"] != path {
		raw, _ := os.ReadFile(envFile.Path)
		t.Fatalf("variable not written:\n%s", raw)
	}
	if changed, _ = g.EnsureDNSEnv(envs); changed {
		t.Fatal("a second Apply must not change the file again")
	}
}
