package services

import (
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
)

type serviceManager struct {
	store db.ItemReader

	inspector runtimeObserver
	applier   engine.Applier
}

func NewServiceManager(store db.ItemReader, inspector runtimeObserver, applier engine.Applier) ServiceManager {
	return &serviceManager{
		store: store,

		inspector: inspector,
		applier:   applier,
	}
}
