package routers

import (
	"context"
	"github.com/traefik-console/internal/traefik/status"
)

type runtimeObserver interface {
	ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error)
}
