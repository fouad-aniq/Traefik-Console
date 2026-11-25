package plugins

import "github.com/traefik-console/internal/traefik/validation"

func compareVersions(a, b string) (int, bool) { return validation.ComparePluginVersions(a, b) }
