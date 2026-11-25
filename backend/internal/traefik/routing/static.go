package routing

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/traefik-console/internal/config/yaml"
)

var ErrInvalidRouter = errors.New("invalid router binding or reference")

// EntryPoint is a static entrypoint: TCP unless its address ends in /udp.
type EntryPoint struct{ TCP, UDP, Default bool }
type Static struct {
	EntryPoints map[string]EntryPoint
	Resolvers   map[string]string
}

// ParseStatic reads only routing-relevant registered static settings, without IO.
func ParseStatic(raw []byte) (Static, error) {
	out := Static{EntryPoints: map[string]EntryPoint{}, Resolvers: map[string]string{}}
	doc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(raw), yaml.DefaultParserOptions())
	if err != nil {
		return out, err
	}
	root := yaml.GetRootMapping(doc)
	eps := yaml.GetMappingValue(root, "entryPoints")
	if eps != nil {
		for i := 0; i+1 < len(eps.Content); i += 2 {
			node := eps.Content[i+1]
			address := yaml.GetMappingValue(node, "address")
			def := yaml.GetMappingValue(node, "asDefault")
			udp := address != nil && strings.HasSuffix(strings.ToLower(address.Value), "/udp")
			out.EntryPoints[eps.Content[i].Value] = EntryPoint{TCP: !udp, UDP: udp, Default: def != nil && strings.EqualFold(def.Value, "true")}
		}
	}
	resolvers := yaml.GetMappingValue(root, "certificatesResolvers")
	if resolvers != nil {
		for i := 0; i+1 < len(resolvers.Content); i += 2 {
			acme := yaml.GetMappingValue(resolvers.Content[i+1], "acme")
			challenge := ""
			for _, pair := range [][2]string{{"dnsChallenge", "dns01"}, {"httpChallenge", "http01"}, {"tlsChallenge", "tlsalpn01"}} {
				if yaml.GetMappingValue(acme, pair[0]) != nil {
					challenge = pair[1]
				}
			}
			out.Resolvers[resolvers.Content[i].Value] = challenge
		}
	}
	return out, nil
}

func (s Static) selectEntryPoints(cfgTLS bool) ([]string, error) {
	var defaults, available []string
	for name, ep := range s.EntryPoints {
		if ep.TCP {
			available = append(available, name)
			if ep.Default {
				defaults = append(defaults, name)
			}
		}
	}
	if len(defaults) > 0 {
		sort.Strings(defaults)
		return defaults, nil
	}
	legacy := "web"
	if cfgTLS {
		legacy = "websecure"
	}
	if ep, ok := s.EntryPoints[legacy]; ok && ep.TCP {
		return []string{legacy}, nil
	}
	if len(available) == 1 {
		return available, nil
	}
	return nil, errors.New("select explicit registered TCP entrypoints")
}

// selectEntryPointsFor picks the entrypoints of a TCP or UDP router that named none: the ones
// flagged as default for its kind, else the only one of that kind. Never a guess between several.
func (s Static) selectEntryPointsFor(protocol string) ([]string, error) {
	var defaults, available []string
	for name, ep := range s.EntryPoints {
		if (protocol == "udp" && ep.UDP) || (protocol != "udp" && ep.TCP) {
			available = append(available, name)
			if ep.Default {
				defaults = append(defaults, name)
			}
		}
	}
	sort.Strings(defaults)
	sort.Strings(available)
	if len(defaults) > 0 {
		return defaults, nil
	}
	if len(available) == 1 {
		return available, nil
	}
	return nil, fmt.Errorf("select explicit registered %s entrypoints", strings.ToUpper(protocol))
}
