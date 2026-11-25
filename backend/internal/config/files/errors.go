package files

import "errors"

var (
	ErrPathTraversalEscape  = errors.New("path traversal or symlink escape detected outside dynamic root")
	ErrResourceConflict     = errors.New("resource name conflict detected during adoption")
	ErrSourceFileNotFound   = errors.New("source configuration file not found")
	ErrCannotModifyReadOnly = errors.New("docker-provider objects and external files are read-only until adopted")
	ErrEmptySelection       = errors.New("empty resource selection rejected: explicit selection required")
)
