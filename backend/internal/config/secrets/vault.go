// Package secrets prepares encrypted credentials and publishes/reconciles
// registered runtime files only when called by the transaction engine.
package secrets

import (
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/db"
)

type Vault struct {
	runtimeDir, secretsDir string
	masterKey              []byte
	store                  db.SecretReader
}

// New binds the manager directory and its Traefik-visible mount explicitly.
func New(dir, runtimeDir string, key []byte, store db.SecretReader) (*Vault, error) {
	if e := filesystem.EnsureDir(dir); e != nil {
		return nil, e
	}
	return &Vault{secretsDir: dir, runtimeDir: runtimeDir, masterKey: append([]byte(nil), key...), store: store}, nil
}
