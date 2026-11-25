package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingEmptyAndRestoredConfigurationRemainDistinct(t *testing.T) {
	root := t.TempDir()

	static := filepath.Join(root, "static", "traefik.yml")
	dynamic := filepath.Join(root, "dynamic")
	f, e := New(static, dynamic)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.ReadStaticConfig(); !errors.Is(e, ErrSourceFileNotFound) {
		t.Fatal("missing static must be explicit", e)
	}
	if b, e := f.ReadManagedDynamicConfig(); e != nil || b != nil {
		t.Fatal("missing dynamic must be absent", e)
	}
	external := filepath.Join(dynamic, "operator.yml")
	original := []byte("http: {}\n# operator owned\n")
	if e = os.WriteFile(external, original, 0600); e != nil {
		t.Fatal(e)
	}
	if e = f.WriteManagedDynamicConfig([]byte{}); e != nil {
		t.Fatal(e)
	}
	if b, e := f.ReadManagedDynamicConfig(); e != nil || b == nil || len(b) != 0 {
		t.Fatal("empty file must remain present", e)
	}
	if e = f.WriteStaticConfig([]byte("api: {}\n")); e != nil {
		t.Fatal(e)
	}
	if e = f.RestoreStaticConfig(nil); e != nil {
		t.Fatal(e)
	}
	if _, e = f.ReadStaticConfig(); !errors.Is(e, ErrSourceFileNotFound) {
		t.Fatal(e)
	}
	if e = f.RestoreManagedDynamicConfig(nil); e != nil {
		t.Fatal(e)
	}
	if b, e := f.ReadManagedDynamicConfig(); e != nil || b != nil {
		t.Fatal("restore absent", e)
	}
	b, e := os.ReadFile(external)
	if e != nil || !bytes.Equal(b, original) {
		t.Fatal("operator file changed", e)
	}
	if _, e = f.ReadExternalDynamicFile("../operator.yml"); e == nil {
		t.Fatal("traversal accepted")
	}
}
