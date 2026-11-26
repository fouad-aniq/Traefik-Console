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

// TightenPrivateFolders removes group and other write access from Console's private state folders
// (the database and key folders) when the Console user owns them: a folder created by hand with
// the usual umask (0775) is then accepted instead of refused. Folders owned by someone else are
// left alone for the state checks to report. It returns the folders it changed.
func TightenPrivateFolders(dirs ...string) []string {
	var changed []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode().Perm()&0o022 == 0 {
			continue
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != os.Getuid() {
			continue
		}
		if os.Chmod(dir, st.Mode().Perm()&^0o077) == nil {
			changed = append(changed, dir)
		}
	}
	return changed
}

// PrivateFolderProblem reports a private state folder that is writable by group or others and
// that Console cannot correct (it does not own it), or "" when all are fine. Checked before any
// state file is written, so a refused start leaves nothing behind.
func PrivateFolderProblem(dirs ...string) string {
	for _, dir := range dirs {
		st, err := os.Stat(dir)
		if err == nil && st.Mode().Perm()&0o022 != 0 {
			return fmt.Sprintf("%s must not be writable by group or others: run chmod 700 on that folder on the host", dir)
		}
	}
	return ""
}
