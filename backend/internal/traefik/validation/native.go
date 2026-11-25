package validation

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Native field inventory is copied byte-for-byte from the maintained repository
// middleware generator output for Traefik v3.7.14. See schema-provenance.json.
//
//go:embed middleware_schema.json
var nativeFiles embed.FS

type nativeField struct {
	Type   string                 `json:"type"`
	Fields map[string]nativeField `json:"fields"`
}

var nativeHTTP = func() map[string]map[string]nativeField {
	b, e := nativeFiles.ReadFile("middleware_schema.json")
	if e != nil {
		panic(e)
	}
	var v map[string]map[string]map[string]nativeField
	if e = json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v["http"]
}()

// nativeTCP holds the TCP middleware types Traefik documents (inFlightConn, ipAllowList and the
// deprecated ipWhiteList), from the same generated inventory.
var nativeTCP = func() map[string]map[string]nativeField {
	b, e := nativeFiles.ReadFile("middleware_schema.json")
	if e != nil {
		panic(e)
	}
	var v map[string]map[string]map[string]nativeField
	if e = json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v["tcp"]
}()

// ValidateNativeTCPMiddleware checks a TCP middleware: known fields of the right type, and the
// value each type cannot work without.
func ValidateNativeTCPMiddleware(kind string, spec map[string]any) error {
	fields, ok := nativeTCP[kind]
	if !ok {
		return fmt.Errorf("unsupported TCP middleware")
	}
	if e := validateFields(spec, fields, 0); e != nil {
		return e
	}
	switch kind {
	case "inFlightConn":
		n, ok := spec["amount"].(float64)
		if !ok {
			if i, isInt := spec["amount"].(int); isInt {
				n, ok = float64(i), true
			}
		}
		if !ok || n < 1 || n != math.Trunc(n) {
			return fmt.Errorf("amount must be a positive whole number")
		}
	case "ipAllowList", "ipWhiteList":
		ranges, _ := spec["sourceRange"].([]any)
		if len(ranges) == 0 {
			if s, isStrings := spec["sourceRange"].([]string); !isStrings || len(s) == 0 {
				return fmt.Errorf("sourceRange needs at least one address or range")
			}
		}
	}
	return nil
}

func ValidateNativeMiddleware(kind string, spec map[string]any) error {
	if kind == "plugin" {
		return nil
	}
	fields, ok := nativeHTTP[kind]
	if !ok {
		return fmt.Errorf("unsupported native middleware")
	}
	if e := validateFields(spec, fields, 0); e != nil {
		return e
	}
	// Minimum semantic constraints for forms; structural validity alone isn't enough.
	required := map[string][]string{"addPrefix": {"prefix"}, "stripPrefix": {"prefixes"}, "stripPrefixRegex": {"regex"}, "replacePath": {"path"}, "replacePathRegex": {"regex", "replacement"}, "redirectRegex": {"regex", "replacement"}, "redirectScheme": {"scheme"}, "forwardAuth": {"address"}, "errors": {"status", "service", "query"}, "circuitBreaker": {"expression"}}
	for _, k := range required[kind] {
		v, ok := spec[k]
		if !ok || v == nil || v == "" {
			return fmt.Errorf("missing required middleware field")
		}
	}
	if kind == "basicAuth" || kind == "digestAuth" {
		if _, ok := spec["users"]; !ok {
			if _, ok = spec["usersFile"]; !ok {
				return fmt.Errorf("authentication users are required")
			}
		}
	}
	return nil
}
func validateFields(v map[string]any, fields map[string]nativeField, depth int) error {
	if depth > 32 {
		return fmt.Errorf("native schema depth exceeded")
	}
	for k, x := range v {
		f, ok := fields[k]
		if !ok {
			return fmt.Errorf("unknown native field")
		}
		if e := validateType(x, f, depth+1); e != nil {
			return e
		}
	}
	return nil
}
func validateType(v any, f nativeField, depth int) error {
	if v == nil {
		return fmt.Errorf("native field must not be null")
	}
	typ := strings.TrimPrefix(f.Type, "*")
	if strings.HasPrefix(typ, "[]") || strings.HasSuffix(typ, ".Users") {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var a []any
		if json.Unmarshal(b, &a) != nil {
			return fmt.Errorf("expected native array")
		}
		child := f
		child.Type = strings.TrimPrefix(typ, "[]")
		if strings.HasSuffix(typ, ".Users") {
			child.Type = "string"
		}
		for _, x := range a {
			if e = validateType(x, child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if strings.HasPrefix(typ, "map[string]") {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil || m == nil {
			return fmt.Errorf("expected native map")
		}
		child := f
		child.Type = strings.TrimPrefix(typ, "map[string]")
		for _, x := range m {
			if e = validateType(x, child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	if len(f.Fields) > 0 {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil || m == nil {
			return fmt.Errorf("expected native object")
		}
		return validateFields(m, f.Fields, depth)
	}
	switch {
	case typ == "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case typ == "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	case strings.HasPrefix(typ, "int") || strings.HasPrefix(typ, "uint"):
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var n float64
		if json.Unmarshal(b, &n) != nil || math.Trunc(n) != n || n > float64(1<<53) || n < -float64(1<<53) {
			return fmt.Errorf("expected bounded integer")
		}
		if strings.HasPrefix(typ, "uint") && n < 0 {
			return fmt.Errorf("expected nonnegative integer")
		}
	case strings.HasPrefix(typ, "float"):
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var n float64
		if json.Unmarshal(b, &n) != nil {
			return fmt.Errorf("expected number")
		}
	case strings.HasSuffix(typ, ".Duration"):
		if s, ok := v.(string); ok {
			d, e := time.ParseDuration(s)
			if e != nil || d < 0 {
				return fmt.Errorf("invalid duration")
			}
		} else {
			return fmt.Errorf("expected duration string")
		}
	default:
		return fmt.Errorf("unreviewed native schema type")
	}
	return nil
}
