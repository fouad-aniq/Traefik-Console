package certificates

import (
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
)

type certificateManager struct {
	store db.ItemReader

	secrets secretPreparer
	applier engine.Applier
}

func NewCertificateManager(store db.ItemReader, secretsMgr secretPreparer, applier engine.Applier) CertificateManager {
	return &certificateManager{
		store: store,

		secrets: secretsMgr,
		applier: applier,
	}
}
