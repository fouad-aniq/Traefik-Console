package db

import (
	"context"
	"time"

	"github.com/traefik-console/internal/models"
)

// ItemReader Read capabilities keep resource managers from mutating persistence directly.
// Mutations belong to the engine's operation pipeline.
type ItemReader interface {
	ListItemsByKind(context.Context, models.ItemKind) ([]models.ConfigItemRow, error)
	ListAllItems(context.Context) ([]models.ConfigItemRow, error)
	GetItem(context.Context, models.ItemKind, string) (*models.ConfigItemRow, error)
}

type SecretReader interface {
	GetSecret(context.Context, string) (*models.SecretRow, error)
	ListSecrets(context.Context) ([]models.SecretRow, error)
}

type CatalogStore interface {
	ReadPluginCatalogSnapshot(context.Context) (*models.PluginCatalogSnapshot, error)
	ClaimPluginCatalogRefresh(context.Context, time.Time, time.Duration) (bool, error)
	ReadPluginCatalogPage(context.Context, string, string, int, int, bool) (*models.CatalogPage, error)
	ReplacePluginCatalog(context.Context, []models.CatalogPluginRow) error
	SearchPluginCatalog(context.Context, string) ([]models.CatalogPluginRow, error)
}

// HistoryReader is the persistence surface HTTP handlers use for snapshots and the operations log.
type HistoryReader interface {
	GetSnapshotMeta(context.Context, int64) (*models.SnapshotRow, error)
	ListSnapshots(ctx context.Context, limit int, cursor int64) ([]models.SnapshotRow, error)
	GetOperation(context.Context, int64) (*models.OperationRow, error)
	ListOperations(ctx context.Context, limit int, cursor int64) ([]models.OperationRow, error)
}

// GenerationReader permits observation/rendering but no persistence mutation.
type GenerationReader interface {
	ItemReader
	SecretReader
}
