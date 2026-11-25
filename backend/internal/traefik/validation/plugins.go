package validation

import "regexp"

var pluginModule = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)+$`)

func ValidPluginModule(s string) bool  { return pluginModule.MatchString(s) }
func ValidPluginVersion(s string) bool { return versionParts(s) != nil }
