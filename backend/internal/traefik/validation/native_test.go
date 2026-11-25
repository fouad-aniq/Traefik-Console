package validation

import (
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/yaml"
)

func TestNativeNestedValidationAndQualifiedMissingReferences(t *testing.T) {
	for _, tc := range []struct {
		kind string
		spec map[string]any
		ok   bool
	}{{"headers", map[string]any{"frameDeny": true}, true}, {"headers", map[string]any{"frameDeny": "yes"}, false}, {"forwardAuth", map[string]any{"address": "http://auth.invalid", "tls": map[string]any{"unknown": true}}, false}, {"addPrefix", map[string]any{}, false}, {"chain", map[string]any{"middlewares": []string{"a"}}, true}} {
		e := ValidateNativeMiddleware(tc.kind, tc.spec)
		if (e == nil) != tc.ok {
			t.Fatal(tc.kind, e)
		}
	}
	doc, _, e := yaml.ParseBoundedYAML(strings.NewReader("http:\n  routers:\n    test:\n      rule: Host(`example.invalid`)\n      service: missing@file\n"), yaml.DefaultParserOptions())
	if e != nil {
		t.Fatal(e)
	}
	if NewConfigValidator().ValidateDynamicAST(doc).Valid {
		t.Fatal("qualified missing reference accepted")
	}
}
