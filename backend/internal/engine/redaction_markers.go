package engine

import (
	"encoding/json"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

// A redacted leaf is a retain instruction, never a new credential or setting.
// Resolve it under the same lock and revision check as the batch mutation.
func retainLeaves(next, old any, present bool) (any, error) {
	if s, ok := next.(string); ok && (s == "[redacted]" || s == "••••••••") {
		if !present {
			return nil, ErrValidationFailed
		}
		return old, nil
	}
	switch n := next.(type) {
	case map[string]any:
		previous, _ := old.(map[string]any)
		result := map[string]any{}
		for k, v := range n {
			old, exists := previous[k]
			resolved, e := retainLeaves(v, old, exists)
			if e != nil {
				return nil, e
			}
			result[k] = resolved
		}
		return result, nil
	case []any:
		previous, _ := old.([]any)
		result := make([]any, len(n))
		for i, v := range n {
			var before any
			exists := i < len(previous)
			if exists {
				before = previous[i]
			}
			resolved, e := retainLeaves(v, before, exists)
			if e != nil {
				return nil, e
			}
			result[i] = resolved
		}
		return result, nil
	default:
		return next, nil
	}
}

// resolveMarkers rewrites redaction markers in middleware upserts with the committed value.
func resolveMarkers(m *models.Mutation, prior *db.State) error {
	for i := range m.Upserts {
		row := &m.Upserts[i]
		if row.Kind != models.KindMiddleware {
			continue
		}
		var next models.MiddlewareConfig
		if json.Unmarshal(row.Payload, &next) != nil {
			return ErrValidationFailed
		}
		var old models.MiddlewareConfig
		for _, previous := range prior.Items {
			if previous.Kind == row.Kind && previous.Name == row.Name {
				if json.Unmarshal(previous.Payload, &old) != nil {
					return ErrValidationFailed
				}
			}
		}
		resolved, e := retainLeaves(next.Spec, old.Spec, old.Spec != nil)
		if e != nil {
			return e
		}
		next.Spec, _ = resolved.(map[string]any)
		if next.Type == "plugin" {
			next.Spec, e = validation.PluginSettings(next.Spec)
			if e != nil {
				return ErrValidationFailed
			}
		}
		raw, e := json.Marshal(next)
		if e != nil {
			return e
		}
		row.Payload = raw
	}
	return nil
}
