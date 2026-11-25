package files

import (
	"errors"
	"os"
	"strings"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
)

func (m *Repository) ListExternalDynamicFiles() ([]ExternalFile, error) {
	entries, err := os.ReadDir(m.dynamicDir)
	if err != nil {
		return nil, err
	}

	var result []ExternalFile
	for _, entry := range entries {
		if len(result) >= 128 {
			return nil, errors.New("external configuration file count exceeds limit")
		}
		name := entry.Name()
		if entry.IsDir() || name == ManagedDynamicFilename || strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, ExternalFile{
			Filename: name,
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return result, nil
}

func (m *Repository) ReadExternalDynamicFile(filename string) ([]byte, error) {
	target, err := filesystem.SafeResolvePath(m.dynamicDir, filename)
	if err != nil {
		return nil, ErrPathTraversalEscape
	}

	data, err := filesystem.ReadBounded(target, yaml.DefaultMaxYAMLSize)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrSourceFileNotFound
	}
	return data, err
}
