package domains

import (
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/traefik/status"
)

type domainManager struct {
	store        db.ItemReader
	files        staticConfigReader
	acmeObserver *status.ACMEObserver

	probeTargets map[string]string
	applier      engine.Applier
}

func NewDomainManager(store db.ItemReader, filesMgr staticConfigReader, probeTargets map[string]string, applier engine.Applier, observers ...*status.ACMEObserver) DomainManager {
	var observer *status.ACMEObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	return &domainManager{acmeObserver: observer,
		store: store,
		files: filesMgr,

		probeTargets: probeTargets,
		applier:      applier,
	}
}

type staticConfigReader interface{ ReadStaticConfig() ([]byte, error) }
