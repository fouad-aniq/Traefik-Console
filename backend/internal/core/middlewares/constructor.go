package middlewares

import (
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
)

type middlewareManager struct {
	store db.ItemReader

	secrets   secretPreparer
	inspector runtimeObserver
	applier   engine.Applier
}

func NewMiddlewareManager(store db.ItemReader, secretsMgr secretPreparer, inspector runtimeObserver, applier engine.Applier) MiddlewareManager {
	return &middlewareManager{
		store: store,

		secrets:   secretsMgr,
		inspector: inspector,
		applier:   applier,
	}
}
