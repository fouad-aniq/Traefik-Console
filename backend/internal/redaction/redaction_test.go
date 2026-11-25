package redaction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactionPreservesRetainMarkersShapeAndIntegerPrecision(t *testing.T) {
	raw, e := JSON(map[string]any{"values": map[string]string{"API_TOKEN": "••••••••"}, "users": []string{"synthetic-secret"}, "enrollment_ids": []int64{9007199254740991}, "config": map[string]any{"type": "plugin", "spec": map[string]any{"license": "synthetic-secret", "enabled": true}}})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "synthetic-secret") || !strings.Contains(string(raw), "9007199254740991") {
		t.Fatal("secret or numeric corruption")
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal(raw, &decoded) != nil {
		t.Fatal("invalid JSON")
	}
	if !strings.Contains(string(decoded["values"]), "••••••••") || !strings.HasPrefix(string(decoded["users"]), "[") {
		t.Fatal("retain contract shape changed")
	}
}
func TestDiffInputRedactsSecretsAndComments(t *testing.T) {
	safe := YAML([]byte("# synthetic-secret\ncertificatesResolvers:\n  test:\n    acme:\n      eab:\n        hmacEncoded: synthetic-secret\n"))
	if strings.Contains(safe, "synthetic-secret") {
		t.Fatal("YAML leaked secret")
	}
}

func TestYAMLFullDocumentMasksPrivateLeavesAndAliases(t *testing.T) {
	raw := `# synthetic-canary
password: &credential synthetic-canary
copy: *credential
api-key: synthetic-canary
private.key: synthetic-canary
nested:
  keyFile: "-----BEGIN PRIVATE KEY----- synthetic-canary"
url: https://user:synthetic-canary@example.invalid/path?q=synthetic-canary#synthetic-canary
`
	safe := YAML([]byte(raw))
	// References must be masked before decoding can expand a credential under an
	// innocuous name.
	if strings.Contains(safe, "synthetic-canary") {
		t.Fatal("secret/alias escaped YAML masking")
	}
}

func TestCrowdSecStatusMetadataAndOpaquePluginContentRemainDistinct(t *testing.T) {
	out := Value(map[string]any{
		"plugin":       map[string]any{"state": "missing", "module_name": "github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin", "version": "v1.8.1"},
		"key":          map[string]any{"present": false},
		"sources":      map[string]any{"bouncer_authentication": "environment", "machine": "saved"},
		"snippet_yaml": "crowdsecLapiKey: synthetic-secret",
		"nested":       map[string]any{"plugin": map[string]any{"crowdsecLapiKey": "synthetic-secret"}},
		"ranking":      []any{map[string]any{"key": "FR", "count": 1}},
		"lapi_key":     "synthetic-secret",
	}).(map[string]any)
	plugin := out["plugin"].(map[string]any)
	key := out["key"].(map[string]any)
	if plugin["state"] != "missing" || key["present"] != false {
		t.Fatalf("safe status metadata was hidden: %#v", out)
	}
	sources := out["sources"].(map[string]any)
	if sources["bouncer_authentication"] != "environment" || sources["machine"] != "saved" {
		t.Fatalf("credential source metadata was hidden: %#v", sources)
	}
	nested := out["nested"].(map[string]any)["plugin"].(map[string]any)
	ranking := out["ranking"].([]any)[0].(map[string]any)
	if out["snippet_yaml"] != "[redacted]" || nested["crowdsecLapiKey"] != "[redacted]" || out["lapi_key"] != "[redacted]" {
		t.Fatalf("opaque plugin content was not masked: %#v", out)
	}
	if ranking["key"] != "FR" {
		t.Fatalf("non-secret ranking metadata was hidden: %#v", ranking)
	}
}

// The DNS provider registry describes each field with an "is_secret" flag. The
// key contains "secret", but the boolean is schema metadata: redacting it to a
// string made every provider field render as a password in the console.
func TestSchemaSecretFlagIsKeptButItsNonBooleanValuesAreNot(t *testing.T) {
	out := Value(map[string]any{
		"fields": []any{
			map[string]any{"env_var": "CLOUDFLARE_TTL", "is_secret": false},
			map[string]any{"env_var": "CLOUDFLARE_DNS_API_TOKEN", "is_secret": true},
		},
		"is_secret": "not-a-flag",
		"secret":    true,
	}).(map[string]any)
	fields := out["fields"].([]any)
	if got := fields[0].(map[string]any)["is_secret"]; got != false {
		t.Fatalf("is_secret false became %#v", got)
	}
	if got := fields[1].(map[string]any)["is_secret"]; got != true {
		t.Fatalf("is_secret true became %#v", got)
	}
	if out["is_secret"] != "[redacted]" {
		t.Fatalf("non-boolean is_secret leaked: %#v", out["is_secret"])
	}
	if out["secret"] != "[redacted]" {
		t.Fatalf("boolean under a credential key leaked: %#v", out["secret"])
	}
}
