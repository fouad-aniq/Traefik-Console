package status

import (
	"bytes"
	"sort"
	"strings"

	"github.com/traefik-console/internal/config/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// StaticSnapshot records configured resources, not proof they are loaded.
type StaticSnapshot struct {
	EntryPoints []string
	// EntryPointProtocols maps each entrypoint to tcp, or udp when its address ends in /udp.
	EntryPointProtocols map[string]string
	Resolvers           map[string]ExternalItem
	StaticPlugins       map[string]ExternalItem
}

// DiscoverStatic performs bounded parsing only. It never reads files or calls HTTP.
func DiscoverStatic(raw []byte) (*StaticSnapshot, error) {
	snap := &StaticSnapshot{EntryPoints: []string{}, EntryPointProtocols: map[string]string{}, Resolvers: map[string]ExternalItem{}, StaticPlugins: map[string]ExternalItem{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return snap, nil
	}
	doc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
	if err != nil {
		return nil, err
	}
	parseStaticAST(yaml.GetRootMapping(doc), snap)
	return snap, nil
}

func parseStaticAST(root *yamlv3.Node, snap *StaticSnapshot) {
	if root == nil {
		return
	}

	// 1. EntryPoints (e.g., "web", "websecure")
	if epNode := yaml.GetMappingValue(root, "entryPoints"); epNode != nil && epNode.Kind == yamlv3.MappingNode {
		for i := 0; i+1 < len(epNode.Content); i += 2 {
			name := epNode.Content[i].Value
			snap.EntryPoints = append(snap.EntryPoints, name)
			snap.EntryPointProtocols[name] = "tcp"
			if address := yaml.GetMappingValue(epNode.Content[i+1], "address"); address != nil && strings.HasSuffix(strings.ToLower(address.Value), "/udp") {
				snap.EntryPointProtocols[name] = "udp"
			}
		}
		sort.Strings(snap.EntryPoints)
	}

	// 2. Certificate Resolvers (certificatesResolvers.<name>.acme)
	if resNode := yaml.GetMappingValue(root, "certificatesResolvers"); resNode != nil && resNode.Kind == yamlv3.MappingNode {
		for i := 0; i+1 < len(resNode.Content); i += 2 {
			name := resNode.Content[i].Value
			valNode := resNode.Content[i+1]
			var rawMap map[string]any
			if acmeNode := yaml.GetMappingValue(valNode, "acme"); acmeNode != nil {
				_ = acmeNode.Decode(&rawMap)
			} else {
				_ = valNode.Decode(&rawMap)
			}
			snap.Resolvers[name] = ExternalItem{
				Name:     name,
				FullName: name,
				Provider: "static",
				Status:   "enabled",
				Raw:      rawMap,
			}
		}
	}

	// 3. Static Plugins (experimental.plugins.<name>)
	if expNode := yaml.GetMappingValue(root, "experimental"); expNode != nil {
		if plugNode := yaml.GetMappingValue(expNode, "plugins"); plugNode != nil && plugNode.Kind == yamlv3.MappingNode {
			for i := 0; i+1 < len(plugNode.Content); i += 2 {
				name := plugNode.Content[i].Value
				valNode := plugNode.Content[i+1]
				var rawMap map[string]any
				_ = valNode.Decode(&rawMap)
				snap.StaticPlugins[name] = ExternalItem{
					Name:     name,
					FullName: name,
					Provider: "static",
					Status:   "enabled",
					Raw:      rawMap,
				}
			}
		}
	}
}
