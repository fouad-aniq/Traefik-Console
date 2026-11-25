package engine

import (
	"context"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

// Generator renders candidate static/dynamic YAML from a resource set and
// validates the result. It is an interface so the operation engine and tests can
// substitute fakes without Docker, Traefik, or disk.
type Generator interface {
	Render(items, previous []models.ConfigItemRow, rawStatic, rawDynamic []byte) (*GeneratedBundle, error)
	Validate(ctx context.Context, in ValidationInput) (*Validation, error)
}

// generator is the real renderer. Render is pure; Validate only reads Traefik.
type generator struct {
	inspector runtimeObserver
	validator *validation.ConfigValidator
	runtime   docker.TraefikContainerInspector
	// dnsEnv is Traefik's DNS provider env file (nil: not configured). Without Docker access it
	// answers the credential check, since the shipped Compose files load it into Traefik.
	dnsEnv *config.DNSEnvFile
}

// NewGenerator builds the renderer. dnsEnv (optional) is Traefik's DNS provider env file.
func NewGenerator(
	inspector runtimeObserver,
	validator *validation.ConfigValidator,
	runtimeClient docker.TraefikContainerInspector,
	dnsEnv ...*config.DNSEnvFile,
) Generator {
	g := &generator{inspector: inspector, validator: validator, runtime: runtimeClient}
	if len(dnsEnv) > 0 {
		g.dnsEnv = dnsEnv[0]
	}
	return g
}

type runtimeObserver interface {
	ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error)
}
