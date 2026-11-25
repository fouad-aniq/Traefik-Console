package routers

import (
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
)

type routerManager struct {
	store db.ItemReader
	files staticReader

	inspector runtimeObserver
	applier   engine.Applier
}

func NewRouterManager(store db.ItemReader, files staticReader, inspector runtimeObserver, applier engine.Applier) RouterManager {
	return &routerManager{
		store: store, files: files,

		inspector: inspector,
		applier:   applier,
	}
}

type staticReader interface{ ReadStaticConfig() ([]byte, error) }
