package yaml

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// RenderYAML encodes a yaml.Node AST back to bytes, preserving comments and formatting.
func RenderYAML(node *yaml.Node) ([]byte, error) {
	node, _ = withoutEmptyResourceCollections(node)

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(node); err != nil {
		return nil, fmt.Errorf("failed to encode YAML AST: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("failed to close YAML encoder: %w", err)
	}

	return buf.Bytes(), nil
}
