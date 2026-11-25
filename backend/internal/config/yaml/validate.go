package yaml

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// validateNode walks the decoded AST enforcing the structural boundaries:
// recursion depth, duplicate mapping keys, and total alias expansion.
func validateNode(node *yaml.Node, currentDepth, maxDepth int, aliasCount *int, maxAliases int) error {
	if node == nil {
		return nil
	}

	if currentDepth > maxDepth {
		return ErrYAMLExceedsDepth
	}

	if node.Kind == yaml.AliasNode {
		*aliasCount++
		if *aliasCount > maxAliases {
			return ErrYAMLExcessiveAliases
		}
	}

	if node.Kind == yaml.MappingNode {
		// MappingNode contents are pairs: key, value, key, value...
		keys := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			keyVal := keyNode.Value

			if _, exists := keys[keyVal]; exists {
				return fmt.Errorf("%w: '%s' at line %d, column %d", ErrYAMLDuplicateKey, keyVal, keyNode.Line, keyNode.Column)
			}
			keys[keyVal] = struct{}{}

			// Validate key node
			if err := validateNode(keyNode, currentDepth+1, maxDepth, aliasCount, maxAliases); err != nil {
				return err
			}

			// Validate value node if present
			if i+1 < len(node.Content) {
				valNode := node.Content[i+1]
				if err := validateNode(valNode, currentDepth+1, maxDepth, aliasCount, maxAliases); err != nil {
					return err
				}
			}
		}
		return nil
	}

	// SequenceNode or DocumentNode
	for _, child := range node.Content {
		if err := validateNode(child, currentDepth+1, maxDepth, aliasCount, maxAliases); err != nil {
			return err
		}
	}

	return nil
}
