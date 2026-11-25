package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ReadBounded opens a regular file through its parent directory descriptor.
// Final symlinks are refused; the descriptor pins the root during the read.
func ReadBounded(path string, max int64) ([]byte, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer func(root *os.Root) {
		_ = root.Close()
	}(root)

	f, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("file type or size exceeds allowed bounds")
	}

	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file size exceeds limit")
	}

	return b, nil
}

func RemoveRegular(path string) error {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer func(root *os.Root) {
		_ = root.Close()
	}(root)

	st, e := root.Lstat(filepath.Base(path))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("refusing to remove non-regular file")
	}
	if e = root.Remove(filepath.Base(path)); e != nil {
		return e
	}

	return syncDirectory(filepath.Dir(path))
}
