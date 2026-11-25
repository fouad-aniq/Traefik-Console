package validation

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPluginSettingsPreservesBareConfigurationAndExtractsOneSnippet(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"enabled":true,"nested":{"limit":5}}`, `{"enabled":true,"nested":{"limit":5}}`},
		{`{"http":{"timeout":5}}`, `{"http":{"timeout":5}}`},
		{`{"http":{"middlewares":{"example":{"plugin":{"shop-alias":{"enabled":true,"nested":{"limit":5}}}}}}}`, `{"enabled":true,"nested":{"limit":5}}`},
	} {
		var input, want map[string]any
		json.Unmarshal([]byte(tc.input), &input)
		json.Unmarshal([]byte(tc.want), &want)
		original, _ := json.Marshal(input)
		got, err := PluginSettings(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("incorrect plugin settings", err)
		}
		after, _ := json.Marshal(input)
		if string(original) != string(after) {
			t.Fatal("input mutated")
		}
	}
}

func TestPluginSettingsRejectsAmbiguousSnippetsWithoutDroppingResources(t *testing.T) {
	for _, input := range []string{
		`{"http":{"middlewares":{}}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":{}}},"b":{"plugin":{"p":{}}}}}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":{}}}},"routers":{}}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":{}}}}},"experimental":{}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":{},"other":{}}}}}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":{}} ,"headers":{}}}}}`,
		`{"http":{"middlewares":{"a":{"headers":{}}}}}`,
		`{"http":{"middlewares":{"a":{"plugin":{"p":true}}}}}`,
	} {
		var spec map[string]any
		if err := json.Unmarshal([]byte(input), &spec); err != nil {
			t.Fatal(err)
		}
		if _, err := PluginSettings(spec); err == nil {
			t.Fatal("ambiguous snippet accepted")
		}
	}
}
