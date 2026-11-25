package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyReusesOriginalAndRefusesUnsafeReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "master.key")

	first, e := LoadMasterKey(path, false)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) != 64 {
		t.Fatal("key encoding changed", e)
	}
	second, e := LoadMasterKey(path, false)
	if e != nil || !bytes.Equal(first, second) {
		t.Fatal("key rotated on reload", e)
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadMasterKey(path, false); e == nil {
		t.Fatal("unsafe key accepted")
	}
	now, _ := os.ReadFile(path)
	if !bytes.Equal(now, raw) {
		t.Fatal("key replaced after validation failure")
	}
}
