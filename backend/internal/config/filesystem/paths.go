package filesystem

import (
	"os"
	"path/filepath"
	"strings"
)

func SafeResolvePath(root, name string) (string, error) {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return "", ErrInvalidFileName
	}

	st, e := os.Lstat(root)
	if e != nil {
		return "", e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", ErrPathTraversalEscape
	}

	target := filepath.Join(root, name)
	if st, e = os.Lstat(target); e == nil {
		if !st.Mode().IsRegular() {
			return "", ErrPathTraversalEscape
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}

	return target, nil
}
