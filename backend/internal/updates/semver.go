package updates

import (
	"strconv"
	"strings"
)

// semver is a parsed MAJOR.MINOR.PATCH[-PRERELEASE] version (build metadata is ignored).
type semver struct {
	core [3]int
	pre  []string
}

func parseSemver(raw string) (semver, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	raw, _, _ = strings.Cut(raw, "+")
	main, pre, hasPre := strings.Cut(raw, "-")
	parts := strings.Split(main, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return semver{}, false
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return semver{}, false
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
				return semver{}, false
			}
		}
	}
	return v, true
}

func (v semver) prerelease() bool { return len(v.pre) > 0 }

// compare follows semver precedence: -1 when v < o, 0 when equal, 1 when v > o.
func (v semver) compare(o semver) int {
	for i := range v.core {
		if v.core[i] != o.core[i] {
			return sign(v.core[i] - o.core[i])
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(o.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		a, b := v.pre[i], o.pre[i]
		an, aErr := strconv.Atoi(a)
		bn, bErr := strconv.Atoi(b)
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	return sign(len(v.pre) - len(o.pre))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
