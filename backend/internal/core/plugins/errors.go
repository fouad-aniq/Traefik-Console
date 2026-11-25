package plugins

import "errors"

var (
	ErrPluginInvalidName    = errors.New("plugin short name must be 1-63 lowercase alphanumeric characters, hyphens, or underscores")
	ErrPluginInvalidModule  = errors.New("plugin moduleName must be a valid Go module path (e.g. github.com/org/repo)")
	ErrPluginInvalidVersion = errors.New("plugin version must start with 'v' (e.g. v1.2.0)")
	ErrPluginInvalidYAML    = errors.New("failed to parse plugin YAML snippet; expected experimental.plugins or http.middlewares block")
	ErrPluginNoUpdate       = errors.New("no newer version of this plugin is available in the catalog")
	ErrPluginNotInCatalog   = errors.New("this plugin is not in the cached catalog; refresh the catalog first")
	ErrPluginInUse          = errors.New("plugin cannot be removed because an active middleware instance still uses it")
)
