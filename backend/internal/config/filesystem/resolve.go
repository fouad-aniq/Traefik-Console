package filesystem

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ResolveWithin resolves target (absolute or relative) to a concrete path
// that is guaranteed to stay inside root after lexical cleaning AND symlink
// resolution. It is the safe way to turn an operator-supplied path into a
// file handle: '..' segments, escapes outside root, and symlinks pointing
// outside root are all rejected with ErrPathTraversalEscape.
func ResolveWithin(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	if containsDotDotSegment(target) {
		return "", ErrPathTraversalEscape
	}
	if !withinRoot(absRoot, absTarget) {
		return "", ErrPathTraversalEscape
	}

	resolved, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return "", err
	}
	if !withinRoot(absRoot, resolved) {
		return "", ErrPathTraversalEscape
	}

	return resolved, nil
}

// OpenWithin opens target for reading after confining it to root via
// ResolveWithin, so callers can never be tricked into reading a file
// outside the directory they declared.
func OpenWithin(root, target string) (*os.File, error) {
	safePath, err := ResolveWithin(root, target)
	if err != nil {
		return nil, err
	}

	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func(rootDir *os.Root) {
		_ = rootDir.Close()
	}(rootDir)

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(absRoot, safePath)
	if err != nil {
		return nil, err
	}

	return rootDir.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func containsDotDotSegment(p string) bool {
	return slices.Contains(strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }), "..")
}
