package validation

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPluginVersionsUseReleaseFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../../tests/fixtures/plugins-releases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Stable, Prerelease, Invalid []string }
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, v := range append(fixture.Stable, fixture.Prerelease...) {
		if !ValidPluginVersion(v) {
			t.Error("valid version refused", v)
		}
	}
	for _, v := range fixture.Invalid {
		if ValidPluginVersion(v) {
			t.Error("invalid version accepted", v)
		}
	}
	if c, ok := ComparePluginVersions("v1.0.0-alpha.99999999999999999999999999", "v1.0.0-alpha.99999999999999999999999998"); !ok || c != 1 {
		t.Fatal(c, ok)
	}
	if PluginModuleKey("GitHub.com/Org/Repo/sub/v2") != "github.com/org/repo/sub/v2" || PluginModuleKey("example.com/Case/Module") != "example.com/Case/Module" || PluginModuleKey("github.com/org/repo/Sub") == PluginModuleKey("github.com/org/repo/sub") {
		t.Fatal("module identity collapsed")
	}
}
