package yaml

import "errors"

var (
	ErrYAMLExceedsSizeLimit  = errors.New("YAML payload exceeds maximum allowed size (1 MiB)")
	ErrYAMLExceedsDepth      = errors.New("YAML exceeds maximum allowed nesting depth (32)")
	ErrYAMLDuplicateKey      = errors.New("duplicate key detected in YAML mapping")
	ErrYAMLExcessiveAliases  = errors.New("excessive YAML alias expansion detected")
	ErrYAMLMultipleDocuments = errors.New("multi-document YAML is not supported: exactly one document is required")
)
