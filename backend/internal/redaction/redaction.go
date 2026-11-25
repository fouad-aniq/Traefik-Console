// Package redaction contains output-only transformations. Never feed its results
// back into persistence or the configuration generator.
package redaction

import (
	"bytes"
	"encoding/json"
	"github.com/traefik-console/internal/presentation"
	"net/url"
	"strings"
	"time"

	"github.com/traefik-console/internal/config/yaml"
	yamlv3 "gopkg.in/yaml.v3"
)

func sensitive(k string) bool {
	k = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(k))
	for _, s := range []string{"password", "secret", "token", "authorization", "cookie", "privatekey", "hmachash", "hmacencoded", "users"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return k == "key" || k == "apikey" || k == "lapikey" || k == "credentials"
}

// schemaFlags are field-description keys whose names contain a sensitive word
// but whose boolean value only says how a field is handled (for example the
// DNS provider registry's "is_secret"). A boolean carries no credential, so it
// is kept; any non-boolean value under these keys is still redacted.
var schemaFlags = map[string]bool{"is_secret": true, "present": true}

func crowdSecCountMetadataOnly(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok || len(fields) != 2 {
		return false
	}
	_, hasKey := fields["key"]
	_, hasCount := fields["count"]
	return hasKey && hasCount
}

func crowdSecPluginMetadataOnly(value any) bool {
	fields, ok := value.(map[string]any)
	if !ok || len(fields) == 0 {
		return false
	}
	allowed := map[string]bool{"state": true, "alias": true, "module_name": true, "version": true, "origin": true}
	for field := range fields {
		if !allowed[field] {
			return false
		}
	}
	return true
}

func Value(v any) any { return clean(v, false, 0) }

func clean(v any, opaque bool, depth int) any {
	if depth > 32 {
		return "[redacted]"
	}
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			if k == "created_at" || k == "updated_at" || k == "fetched_at" {
				if text, ok := v.(string); ok {
					if timestamp := presentation.ParseTimestamp(text); timestamp != nil {
						out[k] = timestamp.Format(time.RFC3339Nano)
						continue
					}
				}
			}
			if k == "custom_yaml" {
				if text, ok := v.(string); ok {
					out[k] = yamlAt([]byte(text), depth+1)
				} else {
					out[k] = "[redacted]"
				}
			} else if k == "snippet_yaml" {
				out[k] = clean(v, true, depth+1)
			} else if _, isBool := v.(bool); isBool && (k == "present" || schemaFlags[k] && !opaque) {
				out[k] = v
			} else if k == "key" && crowdSecCountMetadataOnly(x) {
				out[k] = clean(v, false, depth+1)
			} else if sensitive(k) {
				out[k] = clean(v, true, depth+1)
			} else {
				out[k] = clean(v, opaque || (k == "spec" && x["type"] == "plugin") || (strings.EqualFold(k, "plugin") && !crowdSecPluginMetadataOnly(v)) || strings.EqualFold(k, "customRequestHeaders") || strings.EqualFold(k, "customResponseHeaders"), depth+1)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = clean(v, opaque, depth+1)
		}
		return out
	case string:
		if strings.Contains(x, "-----BEGIN ") && strings.Contains(x, "PRIVATE KEY-----") {
			return "[redacted]"
		}
		if opaque {
			if strings.HasPrefix(x, "••••••••") {
				return "••••••••"
			}
			return "[redacted]"
		}
		if u, e := url.Parse(x); e == nil && u.Host != "" {
			u.User = nil
			u.Fragment = ""
			u.RawFragment = ""
			if u.RawQuery != "" {
				u.RawQuery = "redacted"
			}
			return u.String()
		}
		return x
	default:
		if opaque {
			return "[redacted]"
		}
		return v
	}
}

func JSON(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(&raw); e != nil {
		return nil, e
	}
	return json.Marshal(Value(raw))
}

func YAML(raw []byte) string { return yamlAt(raw, 0) }

func yamlAt(raw []byte, depth int) string {
	if depth > 32 {
		return "[redacted]"
	}
	doc, _, e := yaml.ParseBoundedYAML(strings.NewReader(string(raw)), yaml.DefaultParserOptions())
	if e != nil {
		return "[configuration unavailable]"
	}
	maskAliases(doc)
	var v any
	if e = doc.Decode(&v); e != nil {
		return "[configuration unavailable]"
	}
	root, _, _ := yaml.ParseBoundedYAML(strings.NewReader(""), yaml.DefaultParserOptions())
	if e = yaml.GetRootMapping(root).Encode(clean(v, false, depth+1)); e != nil {
		return "[configuration unavailable]"
	}
	b, e := yaml.RenderYAML(root)
	if e != nil {
		return "[configuration unavailable]"
	}
	return string(b)
}

// An alias can reuse a credential under an otherwise innocuous key. Mask the
// reference before decoding, rather than copying its potentially private value.
func maskAliases(node *yamlv3.Node) {
	if node.Kind == yamlv3.AliasNode {
		node.Kind, node.Tag, node.Value, node.Alias = yamlv3.ScalarNode, "!!str", "[redacted]", nil
		return
	}
	for _, child := range node.Content {
		maskAliases(child)
	}
}
