package engine

import (
	"bytes"
	"fmt"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/models"
)

// External files are never rewritten. Their references still protect managed
// resources from being removed underneath an unmanaged consumer.
func (c *operations) protectExternalReferences(removed []models.ItemRef) error {
	deleted := map[string]bool{}
	for _, v := range removed {
		kind := string(v.Kind)
		if v.Kind == models.KindChain {
			kind = string(models.KindMiddleware)
		}
		deleted[kind+":"+v.Name] = true
	}
	if len(deleted) == 0 {
		return nil
	}
	external, e := c.files.ListExternalDynamicFiles()
	if e != nil {
		return e
	}
	check := func(kind, name string) error {
		if strings.Contains(name, "@") && !strings.HasSuffix(name, "@file") {
			return nil
		}
		if deleted[kind+":"+strings.TrimSuffix(name, "@file")] {
			return fmt.Errorf("%w: unmanaged configuration references the resource", files.ErrResourceConflict)
		}
		return nil
	}
	var walk func(*yamlNode, string) error
	walk = func(node *yamlNode, parent string) error {
		if node == nil {
			return nil
		}
		if node.Kind == yamlv3.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				k, v := node.Content[i].Value, node.Content[i+1]
				if k == "service" || k == "fallback" && parent == "failover" || k == "name" && (parent == "services" || parent == "mirrors") {
					if e = check(string(models.KindService), v.Value); e != nil {
						return e
					}
				}
				if k == "middlewares" && v.Kind == yamlv3.SequenceNode {
					for _, member := range v.Content {
						if e = check(string(models.KindMiddleware), member.Value); e != nil {
							return e
						}
					}
				}
				if parent == "plugin" {
					if e = check(string(models.KindPlugin), k); e != nil {
						return e
					}
				}
				if e = walk(v, k); e != nil {
					return e
				}
			}
		} else {
			for _, child := range node.Content {
				if e = walk(child, parent); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, file := range external {
		raw, e := c.files.ReadExternalDynamicFile(file.Filename)
		if e != nil {
			return e
		}
		doc, _, e := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
		if e != nil {
			return e
		}
		if e = walk(doc, ""); e != nil {
			return e
		}
	}
	return nil
}
