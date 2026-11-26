package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestStateFoldersAndAReadOnlyParent(t *testing.T) {
	root := t.TempDir()
	opts := &Options{TraefikDynamicDir: filepath.Join(root, "config", "dynamic"), SecretsDir: filepath.Join(root, "config", "secrets"),
		DBPath: filepath.Join(root, "state", "data", "console.db"), MasterKeyFile: filepath.Join(root, "state", "keys", "master.key")}
	opts.Auth.DBPath = filepath.Join(root, "state", "data", "auth.db")
	for _, d := range []string{"config", "state"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	created, err := EnsureFolders(StartFolders(opts)...)
	if err != nil || len(created) != 4 { // data/ appears twice: the database and the auth database
		t.Fatalf("created=%v err=%v", created, err)
	}
	if st, _ := os.Stat(filepath.Join(root, "state", "keys")); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatal("state folders must be private")
	}
	if os.Getuid() == 0 {
		t.Skip("root writes everywhere")
	}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	if hint := MissingHint(filepath.Join(locked, "data")); hint == "" || !strings.Contains(hint, "chown") {
		t.Fatalf("a read-only parent must explain the fix: %q", hint)
	}
}

func TestPrivateFoldersAreTightenedWhenOwned(t *testing.T) {
	root := t.TempDir()
	data, keys := filepath.Join(root, "data"), filepath.Join(root, "keys")
	for _, d := range []string{data, keys} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o775); err != nil { // as made by hand with a 002 umask
			t.Fatal(err)
		}
	}
	if PrivateFolderProblem(data, keys) == "" {
		t.Fatal("group-writable folders must be reported before tightening")
	}
	changed := TightenPrivateFolders(data, keys, data)
	if len(changed) != 2 {
		t.Fatalf("changed=%v", changed)
	}
	for _, d := range []string{data, keys} {
		if st, _ := os.Stat(d); st.Mode().Perm() != 0o700 {
			t.Fatalf("%s is %v", d, st.Mode().Perm())
		}
	}
	if PrivateFolderProblem(data, keys) != "" || len(TightenPrivateFolders(data, keys)) != 0 {
		t.Fatal("tightened folders are fine and left alone")
	}
}
