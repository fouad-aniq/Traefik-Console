package routers

import (
	"context"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/presentation"
	"github.com/traefik-console/internal/traefik/routing"
)

type routerView struct {
	rows []models.ConfigItemRow
	view routing.View
}

func (m *routerManager) loadView(ctx context.Context) (routerView, error) {
	rows, err := m.store.ListAllItems(ctx)
	if err != nil {
		return routerView{}, err
	}
	raw, err := m.files.ReadStaticConfig()
	if err != nil {
		return routerView{}, err
	}
	view, err := routing.NewView(rows, raw)
	if err != nil {
		return routerView{}, err
	}
	return routerView{rows: rows, view: view}, nil
}

func (v routerView) project(cfg models.RouterConfig) (models.RouterConfig, error) {
	return v.view.Project(cfg)
}

func (v routerView) resource(row models.ConfigItemRow) (*models.UIResource[models.RouterConfig], error) {
	cfg, err := presentation.DecodeUIConfig[models.RouterConfig](row)
	if err != nil {
		return nil, err
	}
	cfg, projectionErr := v.project(cfg)
	cfg.Protocol = models.RouterProtocol(cfg)
	health := "unknown"
	message := ""
	if projectionErr != nil {
		health = "error"
		message = "Router configuration needs validation"
	}
	return &models.UIResource[models.RouterConfig]{ID: row.ID, Name: row.Name, Origin: "managed", Provider: "file", Status: models.StatusActive, ReadOnly: false, Config: cfg, Version: row.Version, LiveHealth: health, LiveErrorMsg: message, UpdatedAt: presentation.ParseTimestamp(row.UpdatedAt)}, nil
}
