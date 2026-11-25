package engine

import (
	"github.com/traefik-console/internal/config/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

// pruneEmptyPluginDeclarations only removes empty remote-plugin containers.
// Other empty YAML maps (such as api: {}) can enable Traefik features.
func pruneEmptyPluginDeclarations(doc *yamlv3.Node) {
	experimental := yaml.GetMappingValue(yaml.GetRootMapping(doc), "experimental")
	plugins := yaml.GetMappingValue(experimental, "plugins")
	if plugins == nil || plugins.Kind != yamlv3.MappingNode || len(plugins.Content) != 0 {
		return
	}
	yaml.DeleteNestedKey(doc, "experimental", "plugins")
	if experimental.Kind == yamlv3.MappingNode && len(experimental.Content) == 0 {
		yaml.DeleteNestedKey(doc, "experimental")
	}
}
