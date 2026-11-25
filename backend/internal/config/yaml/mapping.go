package yaml

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// GetRootMapping returns the root mapping node under a DocumentNode.
func GetRootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if doc.Content[0].Kind == yaml.MappingNode {
			return doc.Content[0]
		}
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

// GetMappingValue finds the node value for a given key inside a MappingNode.
func GetMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && i+1 < len(mapping.Content) {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMappingValue sets or updates a key-value in a MappingNode while preserving comments.
func setMappingValue(mapping *yaml.Node, key string, valNode *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && i+1 < len(mapping.Content) {
			mapping.Content[i+1] = valNode
			return
		}
	}
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: key,
	}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// deleteMappingKey removes a key-value pair from a MappingNode.
func deleteMappingKey(mapping *yaml.Node, key string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

// ensureMappingPath walks (or creates) a chain of MappingNodes from the document root.
// Example: ensureMappingPath(doc, "certificatesResolvers", "letsencrypt", "acme")
func ensureMappingPath(doc *yaml.Node, path ...string) *yaml.Node {
	curr := GetRootMapping(doc)
	if curr == nil {
		return nil
	}
	for _, segment := range path {
		next := GetMappingValue(curr, segment)
		if next == nil || next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(curr, segment, next)
		}
		curr = next
	}
	return curr
}

// SetNestedValue encodes any Go struct/map into a *yaml.Node and sets it at path.
// Example: SetNestedValue(doc, resolverStruct, "certificatesResolvers", "letsencrypt-cf", "acme")
func SetNestedValue(doc *yaml.Node, val any, path ...string) error {
	if len(path) == 0 {
		return fmt.Errorf("path cannot be empty")
	}
	parent := ensureMappingPath(doc, path[:len(path)-1]...)
	if parent == nil {
		return fmt.Errorf("document does not contain a root mapping")
	}

	var valNode yaml.Node
	if err := valNode.Encode(val); err != nil {
		return fmt.Errorf("failed to encode value into yaml.Node: %w", err)
	}

	setMappingValue(parent, path[len(path)-1], &valNode)
	return nil
}

// DeleteNestedKey deletes a key at the end of a nested mapping path.
// Example: DeleteNestedKey(doc, "http", "routers", "old-router")
func DeleteNestedKey(doc *yaml.Node, path ...string) bool {
	if len(path) == 0 {
		return false
	}
	curr := GetRootMapping(doc)
	for _, segment := range path[:len(path)-1] {
		curr = GetMappingValue(curr, segment)
		if curr == nil || curr.Kind != yaml.MappingNode {
			return false
		}
	}
	return deleteMappingKey(curr, path[len(path)-1])
}
