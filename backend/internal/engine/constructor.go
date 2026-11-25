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
}

func NewGenerator(
	inspector runtimeObserver,
	validator *validation.ConfigValidator,
	runtimeClient docker.TraefikContainerInspector,
) Generator {
	return &generator{inspector: inspector, validator: validator, runtime: runtimeClient}
}

type runtimeObserver interface {
	ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error)
}
