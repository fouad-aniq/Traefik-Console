package files

import (
	"path/filepath"
	"strings"

	"github.com/traefik-console/internal/config/filesystem"
)

const ManagedDynamicFilename = "00_console_managed.yml"
const ConsoleDomainsFilename = "console-domains.yml"

type ExternalFile struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	ModTime  string `json:"mod_time"`
}

type Repository struct {
	staticPath string
	dynamicDir string
}

func New(staticPath, dynamicDir string) (*Repository, error) {
	// Console metadata must never be read as Traefik dynamic configuration.
	metadata := filepath.Join(filepath.Dir(staticPath), ConsoleDomainsFilename)
	if filepath.Clean(metadata) == filepath.Clean(staticPath) {
		return nil, filesystem.ErrPathTraversalEscape
	}
	if rel, err := filepath.Rel(dynamicDir, metadata); err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, filesystem.ErrPathTraversalEscape
	}
	if err := filesystem.EnsureDir(filepath.Dir(staticPath)); err != nil {
		return nil, err
	}

	if err := filesystem.EnsureDir(dynamicDir); err != nil {
		return nil, err
	}

	return &Repository{
		staticPath: staticPath,
		dynamicDir: dynamicDir,
	}, nil
}
