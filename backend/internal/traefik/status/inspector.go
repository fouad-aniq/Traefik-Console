package status

import (
	"context"
	"strings"
)

// RuntimeResources belongs to one protocol. Provider-qualified names stay intact.
type RuntimeResources struct {
	Routers     map[string]ExternalItem
	Services    map[string]ExternalItem
	Middlewares map[string]ExternalItem
}

type RuntimeSnapshot struct{ HTTP, TCP, UDP RuntimeResources }

// ObserveRuntime fetches one fresh snapshot; failures never masquerade as empty data.
func (oc *ObservationClient) ObserveRuntime(ctx context.Context) (*RuntimeSnapshot, error) {
	raw, err := oc.FetchRawData(ctx)
	if err != nil {
		return nil, err
	}
	return &RuntimeSnapshot{HTTP: discoverRuntime(raw, "http"), TCP: discoverRuntime(raw, "tcp"), UDP: discoverRuntime(raw, "udp")}, nil
}

func discoverRuntime(raw map[string]any, proto string) RuntimeResources {
	resources := RuntimeResources{Routers: map[string]ExternalItem{}, Services: map[string]ExternalItem{}, Middlewares: map[string]ExternalItem{}}
	parseRuntimeCategory(extractCategoryMap(raw, proto, "routers"), resources.Routers)
	parseRuntimeCategory(extractCategoryMap(raw, proto, "services"), resources.Services)
	if proto != "udp" {
		parseRuntimeCategory(extractCategoryMap(raw, proto, "middlewares"), resources.Middlewares)
	}
	return resources
}

// ValidationMaps exposes only this protocol's references. Bare names alias @file only.
func (s *RuntimeResources) ValidationMaps() (services map[string]bool, middlewares map[string]bool) {
	services = make(map[string]bool, len(s.Services)*2)
	for fullKey, item := range s.Services {
		services[fullKey] = true
		if item.Provider == "file" {
			services[item.Name] = true
		}
	}

	middlewares = make(map[string]bool, len(s.Middlewares)*2)
	for fullKey, item := range s.Middlewares {
		middlewares[fullKey] = true
		if item.Provider == "file" {
			middlewares[item.Name] = true
		}
	}
	return services, middlewares
}

func parseRuntimeCategory(rawCategory map[string]any, dest map[string]ExternalItem) {
	for fullKey, rawVal := range rawCategory {
		itemMap, ok := rawVal.(map[string]any)
		if !ok {
			continue
		}

		bareName, provider := splitProviderKey(fullKey)
		status := boundedStatus(itemMap["status"])

		dest[fullKey] = ExternalItem{
			Name:     bareName,
			FullName: fullKey,
			Provider: provider,
			Status:   status,
			ErrorMsg: extractErrorString(itemMap),
			Raw:      itemMap,
		}
	}
}

func splitProviderKey(fullKey string) (name string, provider string) {
	if idx := strings.LastIndex(fullKey, "@"); idx != -1 {
		return fullKey[:idx], fullKey[idx+1:]
	}
	return fullKey, "file"
}

func extractCategoryMap(raw map[string]any, proto, category string) map[string]any {
	table := category
	if proto != "http" {
		table = proto + strings.ToUpper(category[:1]) + category[1:]
	}
	if m, ok := raw[table].(map[string]any); ok {
		return m
	}
	if nested, ok := raw[proto].(map[string]any); ok {
		if m, ok := nested[category].(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}
