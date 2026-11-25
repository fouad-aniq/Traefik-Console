package files

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
)

func (m *Repository) ReadConsoleDomainsConfig() ([]byte, error) {
	b, err := filesystem.ReadBounded(filepath.Join(filepath.Dir(m.staticPath), ConsoleDomainsFilename), yaml.DefaultMaxYAMLSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

func (m *Repository) WriteConsoleDomainsConfig(b []byte) error {
	if len(b) > yaml.DefaultMaxYAMLSize {
		return ErrResourceConflict
	}
	return filesystem.WriteStaticFileAtomic(filepath.Join(filepath.Dir(m.staticPath), ConsoleDomainsFilename), b, 0600)
}

func (m *Repository) RestoreConsoleDomainsConfig(b []byte) error {
	if b == nil {
		return filesystem.RemoveRegular(filepath.Join(filepath.Dir(m.staticPath), ConsoleDomainsFilename))
	}
	return m.WriteConsoleDomainsConfig(b)
}
