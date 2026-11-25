package status

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
)

func qualified(name string) string {
	if !strings.Contains(name, "@") {
		return name + "@file"
	}
	return name
}

// VerifyConvergence checks /api/rawdata to ensure all resources in `after` are loaded
// without errors, and any resources removed since `before` are gone from Traefik memory.
func (oc *ObservationClient) VerifyConvergence(ctx context.Context, before, after *yamlv3.Node) (VerificationOutcome, error) {
	raw, err := oc.fetchFreshRawData(ctx)
	if err != nil {
		return VerificationOutcome{Observed: false}, err
	}

	oldRoot := yaml.GetRootMapping(before)
	newRoot := yaml.GetRootMapping(after)

	for _, proto := range []string{"http", "tcp", "udp"} {
		oldProto := yaml.GetMappingValue(oldRoot, proto)
		newProto := yaml.GetMappingValue(newRoot, proto)

		for _, category := range []string{"routers", "services", "middlewares"} {
			if proto == "udp" && category == "middlewares" {
				continue
			}

			oldCat := yaml.GetMappingValue(oldProto, category)
			newCat := yaml.GetMappingValue(newProto, category)
			observed := extractCategoryMap(raw, proto, category)

			// 1. Verify all desired items in `after` exist and have no errors
			if newCat != nil && newCat.Kind == yamlv3.MappingNode {
				for i := 0; i+1 < len(newCat.Content); i += 2 {
					name := newCat.Content[i].Value
					desiredNode := newCat.Content[i+1]
					qName := qualified(name)

					var desired any
					if err := desiredNode.Decode(&desired); err != nil {
						return VerificationOutcome{Observed: true}, err
					}
					// Only what this operation created or changed is judged. A resource that was already in the
					// file, unchanged, may be unhealthy for a reason that has nothing to do with this change (a
					// router whose plugin middleware cannot start, say); blaming it would make every later write fail.
					if unchanged(oldCat, name, desired) {
						continue
					}

					effective, exists := observed[qName].(map[string]any)
					if !exists {
						effective, exists = observed[name].(map[string]any)
					}
					if !exists {
						// Traefik hasn't hot-reloaded the new resource yet
						return VerificationOutcome{Observed: true, Loaded: false}, nil
					}

					// Check for attributable errors reported by Traefik
					if status, _ := effective["status"].(string); status == "error" || status == "disabled" {
						errMsg := extractErrorString(effective)
						return VerificationOutcome{
							Observed:        true,
							AttributableErr: true,
							ErrorMessage:    fmt.Sprintf("%s.%s '%s' rejected by Traefik: %s", proto, category, qName, errMsg),
						}, nil
					}
					if errMsg := extractErrorString(effective); errMsg != "" {
						return VerificationOutcome{
							Observed:        true,
							AttributableErr: true,
							ErrorMessage:    fmt.Sprintf("%s.%s '%s' reported error: %s", proto, category, qName, errMsg),
						}, nil
					}

					if !desiredSubset(canonical(desired), canonical(effective), "") {
						return VerificationOutcome{Observed: true, Loaded: false}, nil
					}

				}
			}

			// 2. Verify any deleted items (present in `before`, absent in `after`) are gone
			if oldCat != nil && oldCat.Kind == yamlv3.MappingNode {
				for i := 0; i+1 < len(oldCat.Content); i += 2 {
					oldName := oldCat.Content[i].Value
					if yaml.GetMappingValue(newCat, oldName) == nil {
						if _, stillPresent := observed[qualified(oldName)]; stillPresent {
							return VerificationOutcome{Observed: true, Loaded: false}, nil
						}
					}
				}
			}
		}
	}

	return VerificationOutcome{Observed: true, Loaded: true}, nil
}

// VerifyConvergenceWithRetry polls VerifyConvergence every 150ms until Loaded or timeout.
// Runtime errors can be transient while the file provider publishes routers and middlewares;
// retain the latest attributable error and return it only if it persists until the deadline.
func (oc *ObservationClient) VerifyConvergenceWithRetry(ctx context.Context, before, after *yamlv3.Node, timeout time.Duration) (VerificationOutcome, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	var lastOutcome VerificationOutcome
	var lastErr error

	for {
		lastOutcome, lastErr = oc.VerifyConvergence(ctx, before, after)
		if lastOutcome.Loaded {
			return lastOutcome, lastErr
		}
		select {
		case <-ctx.Done():
			return lastOutcome, ctx.Err()
		case <-deadline.C:
			return lastOutcome, lastErr
		case <-ticker.C:
		}
	}
}

// unchanged reports whether the resource was already in the previous configuration with the same definition.
func unchanged(oldCat *yamlv3.Node, name string, desired any) bool {
	if oldCat == nil || oldCat.Kind != yamlv3.MappingNode {
		return false
	}
	node := yaml.GetMappingValue(oldCat, name)
	if node == nil {
		return false
	}
	var before any
	return node.Decode(&before) == nil && reflect.DeepEqual(canonical(before), canonical(desired))
}

func extractErrorString(effective map[string]any) string {
	if s, ok := effective["err"].(string); ok && s != "" {
		return s
	}
	if errs, ok := effective["errors"].([]any); ok && len(errs) > 0 {
		var parts []string
		for _, e := range errs {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "; ")
	}
	return ""
}
