package filesystem

import "errors"

var (
	ErrPathTraversalEscape = errors.New("path traversal or symlink escape detected outside root directory")
	ErrInvalidFileName     = errors.New("invalid or empty file name")
	ErrAtomicWriteFailed   = errors.New("failed to complete atomic file write and directory sync")
)
