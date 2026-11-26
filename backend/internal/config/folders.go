package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Creatable reports whether dir does not exist yet but can be created: its parent is an existing,
// writable directory. Such a folder is created at start (EnsureWorkingFolders), so the layout
// check accepts it as missing.
func Creatable(dir string) bool {
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return false
	}
	parent := filepath.Dir(dir)
	st, err := os.Stat(parent)
	return err == nil && st.IsDir() && syscall.Access(parent, 3) == nil
}

// EnsureWorkingFolders creates Traefik's watched folder and the secrets folder inside the mounted
// config folder when they are missing, so a first start needs no manual mkdir. Only a missing
// folder whose parent exists is created; anything else (a symlink, a file in the way, a missing
// parent) is left for the layout check to report. It returns the folders it created.
func EnsureWorkingFolders(dynamicDir, secretsDir string) ([]string, error) {
	var created []string
	for _, f := range []struct {
		dir  string
		perm os.FileMode
	}{{dynamicDir, 0o755}, {secretsDir, 0o750}} {
		if !Creatable(f.dir) {
			continue
		}
		if err := os.Mkdir(f.dir, f.perm); err != nil && !os.IsExist(err) {
			return created, fmt.Errorf("create %s: %w", f.dir, err)
		}
		created = append(created, f.dir)
	}
	return created, nil
}
