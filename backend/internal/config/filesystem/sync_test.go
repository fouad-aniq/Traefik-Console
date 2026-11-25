package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriterUsesPrivateSiblingAndIgnoresPredictableTemp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "watched")

	if e := EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(dir, "managed.yml")
	trap := fmt.Sprintf("%s.tmp.%d", target, os.Getpid())
	if e := os.WriteFile(trap, []byte("untouched"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := WriteFileAtomic(target, []byte("http: {}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(trap)
	if string(b) != "untouched" {
		t.Fatal("predictable temp was overwritten")
	}
	st, _ := os.Stat(target)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	entries, e := os.ReadDir(filepath.Join(root, ".console-staging-watched"))
	if e != nil || len(entries) != 0 {
		t.Fatal("staging not cleaned", e)
	}
	if _, e := ReadBounded(target, 2); e == nil {
		t.Fatal("oversize read allowed")
	}
}
func TestRootedReadAndResolveRefuseSymlinksAndTraversal(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "private")
	os.WriteFile(out, []byte("synthetic"), 0600)
	os.Symlink(out, filepath.Join(root, "link"))

	if _, e := SafeResolvePath(root, "../private"); e == nil {
		t.Fatal("traversal normalized silently")
	}
	if _, e := ReadBounded(filepath.Join(root, "link"), 100); e == nil {
		t.Fatal("symlink followed")
	}
}
func TestLeaseExcludesSecondManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.lock")
	first, e := AcquireLease(path)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := AcquireLease(path); e == nil {
		second.Close()
		t.Fatal("second lease acquired")
	}
	first.Close()
	second, e := AcquireLease(path)
	if e != nil {
		t.Fatal(e)
	}
	second.Close()
}
