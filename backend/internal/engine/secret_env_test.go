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

func TestCredentialVariablesComeFromTraefikOrTheEnvFile(t *testing.T) {
	payload, err := json.Marshal(models.ResolverInput{Values: map[string]string{"CLOUDFLARE_DNS_API_TOKEN": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	items := []models.ConfigItemRow{{Kind: models.KindResolver, Name: "le", Payload: payload}}
	path := "/etc/traefik/secrets/cloudflare_dns_api_token.secret"
	secrets := []models.SecretRow{{Name: "cloudflare_dns_api_token", EnvVarName: "CLOUDFLARE_DNS_API_TOKEN_FILE", FilePath: path}}
	envFile := &config.DNSEnvFile{Path: filepath.Join(t.TempDir(), config.DNSProvidersEnvFile), SecretsRoot: "/etc/traefik/secrets"}
	if err := os.WriteFile(envFile.Path, []byte("CLOUDFLARE_DNS_API_TOKEN_FILE="+path+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missingFile := &config.DNSEnvFile{Path: filepath.Join(t.TempDir(), "missing.env"), SecretsRoot: "/etc/traefik/secrets"}
	disabled := inspectResult{err: docker.ErrCapabilityDisabled}
	for _, tc := range []struct {
		name string
		d    docker.TraefikContainerInspector
		file *config.DNSEnvFile
		set  bool
	}{
		{"no Docker access, no env file configured", disabled, nil, false},
		{"no Docker access, env file missing", disabled, missingFile, false},
		{"no Docker access, the env file declares it", disabled, envFile, true},
		{"Traefik has it (env_file loaded)", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": path}}}, envFile, true},
		{"Traefik lacks it: the file alone does not count when Docker can see Traefik", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{}}}, envFile, false},
		{"Traefik has another path", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": "/etc/traefik/secrets/other.secret"}}}, envFile, false},
	} {
		got := checkContainerSecretEnvVars(context.Background(), items, secrets, tc.d, tc.file)
		if len(got) != 1 || got[0].EnvVarName != "CLOUDFLARE_DNS_API_TOKEN_FILE" || got[0].AlreadySet != tc.set {
			t.Fatalf("%s: got %+v, want AlreadySet=%v", tc.name, got, tc.set)
		}
	}
}
