package status

import (
	context "context"
	errors "errors"
	fmt "fmt"
	regexp "regexp"
	sort "sort"
	strings "strings"
	time "time"
)

var routingNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// qualifiedNamePattern is a provider-qualified runtime name (api@internal, whoami@docker): Traefik
// derives these from container or Kubernetes names, so they are not held to the managed rule.
var qualifiedNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}@[a-z][a-z0-9-]{0,31}$`)

var (
	// ErrInvalidServiceName is returned for a name that is neither a managed nor a qualified name.
	ErrInvalidServiceName = errors.New("invalid service name")
	// ErrServiceNotObserved is returned when Traefik does not report the service.
	ErrServiceNotObserved = errors.New("service is not observed by Traefik")
)

// runtimeKey resolves a diagnostics name to Traefik's runtime key: a bare name is the managed
// file service (name@file); a qualified name is used as is.
func runtimeKey(name string) (key, display string, ok bool) {
	if routingNamePattern.MatchString(name) {
		return name + "@file", name, true
	}
	if qualifiedNamePattern.MatchString(name) {
		return name, strings.TrimSuffix(name, "@file"), true
	}
	return "", "", false
}

// routerProvider is the provider part of a runtime router key ("" when it has none).
func routerProvider(key string) string {
	if at := strings.LastIndex(key, "@"); at >= 0 {
		return key[at+1:]
	}
	return ""
}

func boundedStatus(v any) string {
	if s, ok := v.(string); ok && len(s) <= 32 {
		switch strings.ToLower(s) {
		case "enabled", "error", "disabled", "warning", "up", "down", "healthy", "unhealthy":
			return strings.ToLower(s)
		}
	}
	return "unknown"
}

// DiagnoseNamedService returns only allowlisted status fields for an existing
// file-provider service and its configured routers. Backend health is unknown
// when Traefik has no health state; configuration observation is not traffic proof.
func (oc *ObservationClient) DiagnoseNamedService(ctx context.Context, protocol, name string) (ServiceDiagnostic, error) {
	if protocol != "http" && protocol != "tcp" && protocol != "udp" {
		return ServiceDiagnostic{}, fmt.Errorf("unsupported protocol")
	}

	key, display, valid := runtimeKey(name)
	if !valid {
		return ServiceDiagnostic{}, ErrInvalidServiceName
	}

	data, err := oc.FetchRawData(ctx)
	if err != nil {
		return ServiceDiagnostic{}, err
	}

	services := extractCategoryMap(data, protocol, "services")

	value, ok := services[key].(map[string]any)
	if !ok {
		return ServiceDiagnostic{}, ErrServiceNotObserved
	}

	d := ServiceDiagnostic{
		Protocol:      protocol,
		Name:          display,
		Observed:      true,
		RuntimeStatus: boundedStatus(value["status"]),
		HealthStatus:  "unknown",
		ServerStatus:  map[string]int{},
		Routers:       []ServiceRouterObservation{},
		ObservedAt:    time.Now().UTC(),
	}

	if raw, ok := value["serverStatus"].(map[string]any); ok {
		up, down, unknown := 0, 0, 0
		keys := make([]string, 0, len(raw))
		for server := range raw {
			if len(server) <= 256 {
				keys = append(keys, server)
			}
		}

		sort.Strings(keys)
		if len(keys) > 64 {
			keys = keys[:64]
		}

		for _, server := range keys {
			state := strings.ToUpper(boundedStatus(raw[server]))
			switch state {
			case "UP", "HEALTHY":
				d.ServerStatus["UP"]++
				up++
			case "DOWN", "UNHEALTHY":
				d.ServerStatus["DOWN"]++
				down++
			default:
				d.ServerStatus["UNKNOWN"]++
				unknown++
			}
		}

		if len(raw) > 64 || unknown > 0 {
			d.HealthStatus = "unknown"
		} else if up > 0 && down > 0 {
			d.HealthStatus = "mixed"
		} else if up > 0 {
			d.HealthStatus = "healthy"
		} else if down > 0 {
			d.HealthStatus = "unhealthy"
		}
	}

	routers := extractCategoryMap(data, protocol, "routers")
	for route, item := range routers {
		r, ok := item.(map[string]any)
		if !ok {
			continue
		}

		// A bare reference belongs to the router's own provider, as Traefik resolves it.
		service, _ := r["service"].(string)
		if service == "" {
			continue
		}
		if !strings.Contains(service, "@") {
			provider := routerProvider(route)
			if provider == "" {
				provider = "file"
			}
			service += "@" + provider
		}
		if service != key {
			continue
		}

		if _, _, valid := runtimeKey(route); !valid {
			continue
		}

		entrypoints := []string{}
		if raw, ok := r["entryPoints"].([]any); ok {
			for _, ep := range raw {
				if s, ok := ep.(string); ok && len(s) <= 63 {
					entrypoints = append(entrypoints, s)
				}
			}
		}

		d.Routers = append(d.Routers, ServiceRouterObservation{Name: strings.TrimSuffix(route, "@file"), Status: boundedStatus(r["status"]), EntryPoints: entrypoints})
	}

	sort.Slice(d.Routers, func(i, j int) bool { return d.Routers[i].Name < d.Routers[j].Name })

	if len(d.Routers) > 64 {
		d.Routers = d.Routers[:64]
		d.RoutersTruncated = true
	}

	return d, nil
}
