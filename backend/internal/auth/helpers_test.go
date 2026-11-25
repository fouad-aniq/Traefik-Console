package auth

import (
	"os"
	"testing"
)

func chmod(path string, mode os.FileMode) error { return os.Chmod(path, mode) }

// privateDir returns a 0700 temporary directory. t.TempDir follows the umask,
// which is group-writable (0775) on hosts with umask 002 (Ubuntu defaults),
// and the store rightly refuses such a parent.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}
