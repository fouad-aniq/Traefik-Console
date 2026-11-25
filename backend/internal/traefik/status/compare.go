package status

import (
	"encoding/json"
	"reflect"
	"strconv"
	"time"
)

// desiredSubset ignores upstream status/default fields but compares every desired
// setting. Lists preserve order and length, including middleware chain ordering.
func desiredSubset(want, actual any, key string) bool {
	if key == "plugin" {
		return pluginSubset(want, actual)
	}
	switch w := want.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if !desiredSubset(v, a[k], k) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(w) {
			return false
		}
		for i, v := range w {
			if !desiredSubset(v, a[i], key) {
				return false
			}
		}
		return true
	case string:
		a, ok := actual.(string)
		if ok {
			// References may come back provider-qualified (x@file) for what the file wrote as x.
			if key == "service" || key == "middlewares" || key == "serversTransport" {
				return qualified(w) == qualified(a)
			}
			if w == a {
				return true
			}
			dw, ew := time.ParseDuration(w)
			da, ea := time.ParseDuration(a)
			return ew == nil && ea == nil && dw == da
		}
		if n, ok := actual.(float64); ok {
			if d, e := time.ParseDuration(w); e == nil {
				return float64(d) == n
			}
		}
		return false
	default:
		return reflect.DeepEqual(want, actual)
	}
}

// Traefik's file decoder exposes plugin scalar settings as strings. Compare
// those representations only inside opaque plugin configuration; native fields
// and plugin string values retain their existing strict comparison.
func pluginSubset(want, actual any) bool {
	switch w := want.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			observed, exists := a[key]
			if !exists || !pluginSubset(value, observed) {
				return false
			}
		}
		return true
	case []any:
		// Traefik's file decoder exposes an empty plugin string slice as "".
		// Preserve strict comparison for every populated list.
		if len(w) == 0 && actual == "" {
			return true
		}
		a, ok := actual.([]any)
		if !ok || len(w) != len(a) {
			return false
		}
		for i := range w {
			if !pluginSubset(w[i], a[i]) {
				return false
			}
		}
		return true
	case bool:
		if text, ok := actual.(string); ok {
			return strconv.FormatBool(w) == text
		}
	case float64:
		if text, ok := actual.(string); ok {
			n, err := strconv.ParseFloat(text, 64)
			return err == nil && n == w
		}
	}
	return reflect.DeepEqual(want, actual)
}
func canonical(v any) any {
	b, e := json.Marshal(v)
	if e != nil {
		return nil
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}
