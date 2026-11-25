package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsoleDomainFileIsPrivateOutsideWatchedDirectory(t *testing.T) {
	root := t.TempDir()
	f, err := New(filepath.Join(root, "traefik.yml"), filepath.Join(root, "dynamic"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.WriteConsoleDomainsConfig([]byte("version: 1\ndomains: {}\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, ConsoleDomainsFilename))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("domain file permissions", err)
	}
	if _, err = New(filepath.Join(root, "traefik.yml"), root); err == nil {
		t.Fatal("metadata accepted inside watched directory")
	}
	if _, err = New(filepath.Join(root, ConsoleDomainsFilename), filepath.Join(root, "dynamic")); err == nil {
		t.Fatal("metadata collides with static file")
	}
	if err = f.RestoreConsoleDomainsConfig(nil); err != nil {
		t.Fatal(err)
	}
	if b, err := f.ReadConsoleDomainsConfig(); err != nil || b != nil {
		t.Fatal("missing file not restored", err)
	}
	outside := filepath.Join(root, "operator.yml")
	if err = os.WriteFile(outside, []byte("operator-owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, ConsoleDomainsFilename)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.ReadConsoleDomainsConfig(); err == nil {
		t.Fatal("symlink metadata was followed")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "operator-owned" {
		t.Fatal("outside data changed")
	}
}
