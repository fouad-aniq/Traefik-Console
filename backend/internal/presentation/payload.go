package presentation

import (
	"encoding/json"

	"github.com/traefik-console/internal/models"
)

// DecodeUIConfig decodes the committed payload of a resource.
// It deliberately leaves resource-specific health, ownership and redaction to callers.
func DecodeUIConfig[T any](row models.ConfigItemRow) (config T, err error) {
	if len(row.Payload) > 0 {
		err = json.Unmarshal(row.Payload, &config)
	}
	return config, err
}

// ManagedResource projects one committed row without any live-runtime overlay. Write
// endpoints use it to answer after a successful commit: a write that already succeeded
// must not fail because Traefik's API is unreachable.
func ManagedResource[T any](row models.ConfigItemRow, provider string) (*models.UIResource[T], error) {
	cfg, err := DecodeUIConfig[T](row)
	if err != nil {
		return nil, err
	}
	return &models.UIResource[T]{ID: row.ID, UpdatedAt: ParseTimestamp(row.UpdatedAt), Name: row.Name, Origin: "managed", Provider: provider, Status: models.StatusActive, LiveHealth: "unknown", Config: cfg, Version: row.Version}, nil
}
