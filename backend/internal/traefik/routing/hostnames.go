package routing

import (
	"errors"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

func NormalizeDomain(raw string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(raw), ".")
	wild := strings.HasPrefix(s, "*.")
	if wild {
		s = strings.TrimPrefix(s, "*.")
	}
	if strings.Contains(s, "*") || net.ParseIP(s) != nil {
		return "", errors.New("use a DNS hostname; wildcard is permitted only as the first label")
	}
	a, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", errors.New("invalid IDNA domain name")
	}
	a = strings.ToLower(a)
	if len(a) > 253 || !strings.Contains(a, ".") {
		return "", errors.New("fully qualified DNS hostname required")
	}
	for _, l := range strings.Split(a, ".") {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", errors.New("invalid DNS label")
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", errors.New("invalid DNS label")
			}
		}
	}
	if wild {
		return "*." + a, nil
	}
	return a, nil
}

func NormalizeSANs(main string, names []string) (string, []string, error) {
	if len(names) > 50 {
		return "", nil, errors.New("too many alternative names (max 50)")
	}
	m, err := NormalizeDomain(main)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{m: true}
	var a []string
	for _, s := range names {
		v, err := NormalizeDomain(s)
		if err != nil {
			return "", nil, err
		}
		if seen[v] {
			return "", nil, errors.New("duplicate domain found in SANs")
		}
		seen[v] = true
		a = append(a, v)
	}
	return m, a, nil
}
