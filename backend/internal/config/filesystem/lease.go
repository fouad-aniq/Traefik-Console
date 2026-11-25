package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireLease prevents a second manager/CLI from mutating the same state directory.
// Never unlink the lock file: its inode is the identity shared by all processes.
func AcquireLease(path string) (*os.File, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer func(root *os.Root) {
		_ = root.Close()
	}(root)

	f, e := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("invalid manager lease")
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another manager or operator CLI owns this database")
	}

	return f, nil
}
