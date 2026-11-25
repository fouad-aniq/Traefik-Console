package engine

import (
	"context"

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
	// presetEnv is the *_FILE environment declared in Traefik's shipped env file; it answers the
	// credential check when the Traefik container cannot be inspected.
	presetEnv map[string]string
}

// NewGenerator builds the renderer. presetEnv (optional) lists the *_FILE variables Traefik loads
// from its env file, used when Docker access is not configured.
func NewGenerator(
	inspector runtimeObserver,
	validator *validation.ConfigValidator,
	runtimeClient docker.TraefikContainerInspector,
	presetEnv ...map[string]string,
) Generator {
	g := &generator{inspector: inspector, validator: validator, runtime: runtimeClient}
	if len(presetEnv) > 0 {
		g.presetEnv = presetEnv[0]
	}
	return g
}

type runtimeObserver interface {
	ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error)
}
