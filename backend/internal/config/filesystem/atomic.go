package filesystem

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	return f.Sync()
}

// WriteFileAtomic Staging is a private sibling of the target directory, never a transient watched
// YAML file. O_EXCL prevents following/reusing predictable files. Rename must stay
// on the same filesystem; EXDEV fails instead of degrading to a copy.
func WriteFileAtomic(path string, b []byte, perm os.FileMode) error {
	return writeStaged(path, b, perm, filepath.Dir(filepath.Dir(path)), filepath.Base(filepath.Dir(path)))
}

// WriteStaticFileAtomic stages within the non-watched static directory, which
// can itself be the root of a bind mount.
func WriteStaticFileAtomic(path string, b []byte, perm os.FileMode) error {
	return writeStaged(path, b, perm, filepath.Dir(path), ".")
}

func writeStaged(path string, b []byte, perm os.FileMode, parent, base string) error {
	dir := filepath.Dir(path)
	st, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrPathTraversalEscape
	}

	root, e := os.OpenRoot(parent)
	if e != nil {
		return e
	}
	defer func(root *os.Root) {
		_ = root.Close()
	}(root)

	stage := ".console-staging-" + filepath.Base(dir)
	if e = root.Mkdir(stage, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	st, e = root.Lstat(stage)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 || st.Mode()&os.ModeSymlink != 0 {
		return ErrPathTraversalEscape
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}

	tmp := filepath.Join(stage, hex.EncodeToString(nonce))
	f, e := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("%w: staging: %w", ErrAtomicWriteFailed, e)
	}
	defer func(root *os.Root, name string) {
		_ = root.Remove(name)
	}(root, tmp)

	if _, e = f.Write(b); e == nil {
		e = f.Chmod(perm)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return fmt.Errorf("%w: staging flush: %w", ErrAtomicWriteFailed, e)
	}
	if e = syncDirectory(filepath.Join(parent, stage)); e != nil {
		return e
	}
	if e = root.Rename(tmp, filepath.Join(base, filepath.Base(path))); e != nil {
		return fmt.Errorf("%w: rename: %w", ErrAtomicWriteFailed, e)
	}
	if e = syncDirectory(dir); e != nil {
		return e
	}

	return syncDirectory(filepath.Join(parent, stage))
}
