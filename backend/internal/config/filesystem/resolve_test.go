package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithinAcceptsPathsInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "dynamic.yml"), []byte("http:\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Absolute target inside root.
	p, err := ResolveWithin(root, filepath.Join(root, "dynamic.yml"))
	if err != nil {
		t.Fatalf("ResolveWithin(absolute) error = %v", err)
	}
	if filepath.Base(p) != "dynamic.yml" {
		t.Errorf("ResolveWithin(absolute) = %q, want the dynamic.yml path", p)
	}

	// Relative targets resolve against the process working directory, so
	// step into root first — mirroring how the CLI uses root ".".
	t.Chdir(root)
	p, err = ResolveWithin(root, "dynamic.yml")
	if err != nil {
		t.Fatalf("ResolveWithin(relative) error = %v", err)
	}
	if filepath.Base(p) != "dynamic.yml" {
		t.Errorf("ResolveWithin(relative) = %q, want the dynamic.yml path", p)
	}
}

func TestResolveWithinRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // sibling of root

	cases := []struct {
		name   string
		target string
	}{
		{"dot-dot segment", "../secret.yml"},
		{"absolute escape", filepath.Join(outside, "secret.yml")},
		{"deep dot-dot escape", filepath.Join(root, "sub", "..", "..", "secret.yml")},
	}
	for _, tc := range cases {
		if _, err := ResolveWithin(root, tc.target); !errors.Is(err, ErrPathTraversalEscape) {
			t.Errorf("%s: error = %v, want ErrPathTraversalEscape", tc.name, err)
		}
	}
}

func TestResolveWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	outsideFile := filepath.Join(outside, "secret.yml")
	if err := os.WriteFile(outsideFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// Lexically the link lives inside root; it points outside.
	if err := os.Symlink(outsideFile, filepath.Join(root, "link.yml")); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveWithin(root, filepath.Join(root, "link.yml")); !errors.Is(err, ErrPathTraversalEscape) {
		t.Errorf("symlink escape: error = %v, want ErrPathTraversalEscape", err)
	}
}

func TestResolveWithinMissingFile(t *testing.T) {
	if _, err := ResolveWithin(t.TempDir(), "missing.yml"); err == nil {
		t.Error("missing file must return the stat error, not nil")
	}
}

func TestOpenWithin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "dynamic.yml"), []byte("http:\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	f, err := OpenWithin(".", "dynamic.yml")
	if err != nil {
		t.Fatalf("OpenWithin() error = %v", err)
	}
	f.Close()

	if _, err := OpenWithin(root, "../secret.yml"); !errors.Is(err, ErrPathTraversalEscape) {
		t.Errorf("OpenWithin(traversal) error = %v, want ErrPathTraversalEscape", err)
	}
}
