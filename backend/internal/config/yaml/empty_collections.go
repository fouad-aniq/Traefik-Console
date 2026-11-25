package yaml

import "gopkg.in/yaml.v3"

// withoutEmptyResourceCollections removes only empty resource dictionaries at
// protocol scope. Traefik's file provider rejects these standalone elements.
// Empty options INSIDE a resource (tls: {}, headers: {}, etc.) are significant.
// The input is never mutated; all retained comments/nodes are copied.
func withoutEmptyResourceCollections(node *yaml.Node) (*yaml.Node, bool) {
	var copyNode func(*yaml.Node) *yaml.Node
	copyNode = func(n *yaml.Node) *yaml.Node {
		if n == nil {
			return nil
		}

		c := *n
		c.Content = make([]*yaml.Node, len(n.Content))

		for i, child := range n.Content {
			c.Content[i] = copyNode(child)
		}

		return &c
	}

	out := copyNode(node)
	root := out
	if root != nil && root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root == nil || root.Kind != yaml.MappingNode {
		return out, false
	}

	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		protocol, section := root.Content[i].Value, root.Content[i+1]
		if (protocol != "http" && protocol != "tcp" && protocol != "udp") || section.Kind != yaml.MappingNode {
			continue
		}

		for j := 0; j+1 < len(section.Content); {
			key, value := section.Content[j], section.Content[j+1]
			collection := key.Value == "routers" || key.Value == "services" || (protocol != "udp" && key.Value == "middlewares") || (protocol != "udp" && key.Value == "serversTransports")
			if collection && value.Kind == yaml.MappingNode && len(value.Content) == 0 {
				for _, comment := range []string{key.HeadComment, key.LineComment, key.FootComment, value.HeadComment, value.LineComment, value.FootComment} {
					if comment != "" {
						if section.HeadComment != "" {
							section.HeadComment += "\n"
						}
						section.HeadComment += comment
					}
				}
				section.Content = append(section.Content[:j], section.Content[j+2:]...)
				changed = true
			} else {
				j += 2
			}
		}
	}

	return out, changed
}
