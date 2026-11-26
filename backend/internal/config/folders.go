package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Folder is a folder Console creates at start when it is missing.
type Folder struct {
	Path string
	Perm os.FileMode
}

// StartFolders lists what a first start needs inside the mounted folders: Traefik's watched folder
// and the secrets folder (in the Traefik config folder), and the private state folders.
func StartFolders(opts *Options) []Folder {
	return []Folder{
		{opts.TraefikDynamicDir, 0o755},
		{opts.SecretsDir, 0o750},
		{filepath.Dir(opts.DBPath), 0o700},
		{filepath.Dir(opts.Auth.DBPath), 0o700},
		{filepath.Dir(opts.MasterKeyFile), 0o700},
	}
}

// Creatable reports whether dir does not exist yet but can be created: its parent is an existing,
// writable directory. Such a folder is created at start (EnsureFolders), so the layout check
// accepts it as missing.
func Creatable(dir string) bool {
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return false
	}
	parent := filepath.Dir(dir)
	st, err := os.Stat(parent)
	return err == nil && st.IsDir() && syscall.Access(parent, 3) == nil
}

// MissingHint explains why a missing folder cannot be created, or returns "" when it exists or
// can be created.
func MissingHint(dir string) string {
	if _, err := os.Lstat(dir); !os.IsNotExist(err) || Creatable(dir) {
		return ""
	}
	parent := filepath.Dir(dir)
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return fmt.Sprintf("%s is missing and so is its parent %s: check the volume mounts", dir, parent)
	}
	return fmt.Sprintf("%s is missing and Console (uid %d) cannot create it: %s is not writable. On the host, give the mounted folder to that user (chown -R %d:%d <host folder>)", dir, os.Getuid(), parent, os.Getuid(), os.Getgid())
}

// EnsureFolders creates the missing folders whose parent exists, so a first start needs no manual
// mkdir. Anything else (a symlink, a file in the way, a missing or read-only parent) is left for
// the layout check to report. It returns the folders it created.
func EnsureFolders(folders ...Folder) ([]string, error) {
	var created []string
	for _, f := range folders {
		if !Creatable(f.Path) {
			continue
		}
		if err := os.Mkdir(f.Path, f.Perm); err != nil && !os.IsExist(err) {
			return created, fmt.Errorf("create %s: %w", f.Path, err)
		}
		created = append(created, f.Path)
	}
	return created, nil
}

// EnsureWorkingFolders creates Traefik's watched folder and the secrets folder when missing.
func EnsureWorkingFolders(dynamicDir, secretsDir string) ([]string, error) {
	return EnsureFolders(Folder{dynamicDir, 0o755}, Folder{secretsDir, 0o750})
}
