package validation

import "errors"

// PluginSettings accepts bare settings or an unambiguous single-middleware
// Plugin Shop snippet. It never discards sibling resources or settings.
func PluginSettings(spec map[string]any) (map[string]any, error) {
	http, ok := spec["http"].(map[string]any)
	if !ok {
		return spec, nil
	}
	if _, exists := http["middlewares"]; !exists {
		return spec, nil
	}
	invalid := errors.New("plugin snippet must contain exactly one HTTP plugin middleware and no other resources")
	middlewares, ok := http["middlewares"].(map[string]any)
	if !ok || len(spec) != 1 || len(http) != 1 || len(middlewares) != 1 {
		return nil, invalid
	}
	for _, value := range middlewares {
		definition, ok := value.(map[string]any)
		if !ok || len(definition) != 1 {
			return nil, invalid
		}
		plugins, ok := definition["plugin"].(map[string]any)
		if !ok || len(plugins) != 1 {
			return nil, invalid
		}
		for _, config := range plugins {
			settings, ok := config.(map[string]any)
			if !ok {
				return nil, invalid
			}
			return settings, nil
		}
	}
	return nil, invalid
}
