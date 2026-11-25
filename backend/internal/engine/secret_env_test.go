package engine

import (
	"context"
	"encoding/json"
	"testing"

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

func TestCredentialVariablesComeFromContainerOrShippedEnvFile(t *testing.T) {
	payload, err := json.Marshal(models.ResolverInput{Values: map[string]string{"CLOUDFLARE_DNS_API_TOKEN": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	items := []models.ConfigItemRow{{Kind: models.KindResolver, Name: "le", Payload: payload}}
	path := "/etc/traefik/secrets/cloudflare_dns_api_token.secret"
	secrets := []models.SecretRow{{Name: "cloudflare_dns_api_token", EnvVarName: "CLOUDFLARE_DNS_API_TOKEN_FILE", FilePath: path}}
	preset := map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": path}
	disabled := inspectResult{err: docker.ErrCapabilityDisabled}
	for _, tc := range []struct {
		name   string
		d      docker.TraefikContainerInspector
		preset map[string]string
		want   bool
	}{
		{"no Docker access and no env file", disabled, nil, false},
		{"no Docker access, shipped env file declares it", disabled, preset, true},
		{"container has it", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{"CLOUDFLARE_DNS_API_TOKEN_FILE": path}}}, nil, true},
		{"container lacks it, env file does not override the container", inspectResult{status: &docker.ContainerStatus{SecretFileEnv: map[string]string{}}}, preset, false},
	} {
		got := checkContainerSecretEnvVars(context.Background(), items, secrets, tc.d, tc.preset)
		if len(got) != 1 || got[0].EnvVarName != "CLOUDFLARE_DNS_API_TOKEN_FILE" || got[0].AlreadySet != tc.want {
			t.Fatalf("%s: got %+v, want AlreadySet=%v", tc.name, got, tc.want)
		}
	}
}
