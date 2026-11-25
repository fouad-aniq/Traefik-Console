package middlewares

import (
	"context"
	"github.com/traefik-console/internal/traefik/status"

	"github.com/traefik-console/internal/models"
)

type secretPreparer interface {
	PrepareSecret(context.Context, string, string, string, string) (*models.SecretRow, error)
	DecryptSecret(context.Context, string) (string, error)
}

type runtimeObserver interface {
	ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error)
}
