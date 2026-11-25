package files

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
)

func (m *Repository) ReadManagedDynamicConfig() ([]byte, error) {
	target, err := filesystem.SafeResolvePath(m.dynamicDir, ManagedDynamicFilename)
	if err != nil {
		return nil, ErrPathTraversalEscape
	}

	data, err := filesystem.ReadBounded(target, yaml.DefaultMaxYAMLSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func (m *Repository) WriteManagedDynamicConfig(content []byte) error {
	target, err := filesystem.SafeResolvePath(m.dynamicDir, ManagedDynamicFilename)
	if err != nil {
		return ErrPathTraversalEscape
	}
	return filesystem.WriteFileAtomic(target, content, 0600)
}

func (m *Repository) RestoreManagedDynamicConfig(b []byte) error {
	if b == nil {
		return filesystem.RemoveRegular(filepath.Join(m.dynamicDir, ManagedDynamicFilename))
	}
	return m.WriteManagedDynamicConfig(b)
}
