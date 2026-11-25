package plugins

import (
	"context"
	"github.com/traefik-console/internal/models"
)

type staticConfigReader interface{ ReadStaticConfig() ([]byte, error) }

type catalogReader interface {
	Fetch(context.Context) ([]models.CatalogPluginRow, error)
}
