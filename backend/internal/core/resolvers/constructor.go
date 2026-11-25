package resolvers

import (
	"fmt"

	"github.com/traefik-console/internal/core/resolvers/providers"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
)

type resolverManager struct {
	store          resourceStore
	files          staticConfigReader
	secrets        secretPreparer
	registry       *providers.Registry
	acmeStorageDir string

	applier engine.Applier
}

func NewResolverManager(store resourceStore, filesMgr staticConfigReader, secretsMgr secretPreparer, applier engine.Applier, runtimeDirs ...string) (ResolverManager, error) {
	reg, err := providers.LoadRegistry()
	if err != nil {
		return nil, fmt.Errorf("failed to load DNS providers registry: %w", err)
	}
	runtimeDir := "/etc/traefik/acme"
	if len(runtimeDirs) > 0 && runtimeDirs[0] != "" {
		runtimeDir = runtimeDirs[0]
	}
	return &resolverManager{
		store:          store,
		files:          filesMgr,
		secrets:        secretsMgr,
		registry:       reg,
		acmeStorageDir: runtimeDir,

		applier: applier,
	}, nil
}

type resourceStore interface {
	db.ItemReader
	db.SecretReader
}

type staticConfigReader interface{ ReadStaticConfig() ([]byte, error) }
