package crowdsec

import (
	"context"

	"github.com/traefik-console/internal/models"
)

type secretPreparer interface {
	PrepareSecret(context.Context, string, string, string, string) (*models.SecretRow, error)
	DecryptSecret(context.Context, string) (string, error)
}
