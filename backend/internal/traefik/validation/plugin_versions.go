package validation

import (
	"cmp"
	"regexp"
	"strings"
)

var semanticPluginVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func versionParts(v string) []string {
	if len(v) > 128 {
		return nil
	}
	p := semanticPluginVersion.FindStringSubmatch(v)
	if p == nil {
		return nil
	}
	for _, id := range strings.Split(p[4], ".") {
		if numeric(id) && len(id) > 1 && id[0] == '0' {
			return nil
		}
	}
	return p
}
func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func compareNumber(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func ComparePluginVersions(a, b string) (int, bool) {
	x, y := versionParts(a), versionParts(b)
	if x == nil || y == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		if c := compareNumber(x[i], y[i]); c != 0 {
			return c, true
		}
	}
	if x[4] == y[4] {
		return 0, true
	}
	if x[4] == "" {
		return 1, true
	}
	if y[4] == "" {
		return -1, true
	}
	xp, yp := strings.Split(x[4], "."), strings.Split(y[4], ".")
	for i := 0; i < len(xp) && i < len(yp); i++ {
		a, b := xp[i], yp[i]
		c := 0
		switch {
		case numeric(a) && numeric(b):
			c = compareNumber(a, b)
		case numeric(a):
			c = -1
		case numeric(b):
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c, true
		}
	}
	return cmp.Compare(len(xp), len(yp)), true
}

// Fold only GitHub repository identity; preserve import subpaths and other hosts.
func PluginModuleKey(module string) string {
	p := strings.Split(strings.TrimSpace(module), "/")
	if len(p) >= 3 && strings.EqualFold(p[0], "github.com") {
		for i := 0; i < 3; i++ {
			p[i] = strings.ToLower(p[i])
		}
	}
	return strings.Join(p, "/")
}
