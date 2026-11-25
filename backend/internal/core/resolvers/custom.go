package resolvers

import (
	"fmt"
	"net/url"

	"github.com/traefik-console/internal/core/resolvers/providers"
)

func validateCustomACME(v map[string]any, ack bool, reg *providers.Registry) error {
	allowed := map[string]string{"email": "string", "storage": "string", "caServer": "string", "keyType": "string", "preferredChain": "string", "certificatesDuration": "int", "httpChallenge": "object", "tlsChallenge": "object", "dnsChallenge": "object"}
	for k, x := range v {
		t, ok := allowed[k]
		if !ok {
			return fmt.Errorf("unsupported custom ACME field; secrets must be externally configured")
		}
		switch t {
		case "string":
			if _, ok = x.(string); !ok {
				return fmt.Errorf("invalid custom ACME value")
			}
		case "int":
			if n, ok := x.(int); !ok || n <= 0 {
				return fmt.Errorf("invalid certificate duration")
			}
		case "object":
			if _, ok = x.(map[string]any); !ok {
				return fmt.Errorf("invalid custom challenge")
			}
		}
	}
	if raw, ok := v["caServer"].(string); ok {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid custom ACME directory")
		}
	}
	challenges := 0
	for _, key := range []string{"httpChallenge", "tlsChallenge", "dnsChallenge"} {
		raw, ok := v[key]
		if !ok {
			continue
		}
		challenges++
		m := raw.(map[string]any)
		switch key {
		case "tlsChallenge":
			if len(m) != 0 {
				return fmt.Errorf("unknown TLS challenge fields")
			}
		case "httpChallenge":
			if len(m) != 1 {
				return fmt.Errorf("HTTP challenge requires entryPoint only")
			}
			if ep, ok := m["entryPoint"].(string); !ok || ep == "" {
				return fmt.Errorf("HTTP entryPoint required")
			}
		case "dnsChallenge":
			if !ack {
				return fmt.Errorf("custom DNS requires external credential acknowledgement")
			}
			p, ok := m["provider"].(string)
			if !ok || !reg.IsCustomProvider(p) {
				return fmt.Errorf("disallowed custom DNS provider")
			}
			for k, x := range m {
				switch k {
				case "provider":
				case "resolvers":
					a, ok := x.([]any)
					if !ok || len(a) > 8 {
						return fmt.Errorf("invalid custom nameservers")
					}
					for _, entry := range a {
						if _, ok = entry.(string); !ok {
							return fmt.Errorf("invalid nameserver")
						}
					}
				case "propagation":
					prop, ok := x.(map[string]any)
					if !ok {
						return fmt.Errorf("invalid propagation options")
					}
					for k, x := range prop {
						switch k {
						case "delayBeforeChecks":
							if _, ok = x.(string); !ok {
								return fmt.Errorf("invalid propagation delay")
							}
						case "disableChecks", "disableANSChecks", "requireAllRNS":
							if _, ok = x.(bool); !ok {
								return fmt.Errorf("invalid propagation boolean")
							}
						default:
							return fmt.Errorf("unknown propagation option")
						}
					}
				default:
					return fmt.Errorf("unknown custom DNS field")
				}
			}
		}
	}
	if challenges != 1 {
		return fmt.Errorf("exactly one ACME challenge is required")
	}
	return nil
}
