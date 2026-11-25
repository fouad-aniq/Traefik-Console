package files

import (
	"errors"
	"os"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
)

func (m *Repository) ReadStaticConfig() ([]byte, error) {
	data, err := filesystem.ReadBounded(m.staticPath, yaml.DefaultMaxYAMLSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrSourceFileNotFound
	}
	return data, err
}

func (m *Repository) WriteStaticConfig(content []byte) error {
	return filesystem.WriteStaticFileAtomic(m.staticPath, content, 0600)
}

func (m *Repository) RestoreStaticConfig(b []byte) error {
	if b == nil {
		return filesystem.RemoveRegular(m.staticPath)
	}
	return m.WriteStaticConfig(b)
}
