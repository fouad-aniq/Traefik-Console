package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkingFoldersAreCreatedWhenMissing(t *testing.T) {
	root := t.TempDir()
	dynamic, secrets := filepath.Join(root, "dynamic"), filepath.Join(root, "secrets")
	if !Creatable(dynamic) || !Creatable(secrets) {
		t.Fatal("missing folders under a writable parent are creatable")
	}
	created, err := EnsureWorkingFolders(dynamic, secrets)
	if err != nil || len(created) != 2 {
		t.Fatalf("created=%v err=%v", created, err)
	}
	for _, d := range []string{dynamic, secrets} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Fatalf("%s not created", d)
		}
	}
	if created, err = EnsureWorkingFolders(dynamic, secrets); err != nil || len(created) != 0 {
		t.Fatalf("existing folders must be left alone: %v %v", created, err)
	}
}

func TestWorkingFoldersLeaveOddLayoutsToTheCheck(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "dynamic")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "secrets")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	noParent := filepath.Join(root, "missing", "dynamic")
	if Creatable(link) || Creatable(file) || Creatable(noParent) {
		t.Fatal("symlinks, files and missing parents are not creatable")
	}
	if created, err := EnsureWorkingFolders(noParent, file); err != nil || len(created) != 0 {
		t.Fatalf("nothing must be created: %v %v", created, err)
	}
}
