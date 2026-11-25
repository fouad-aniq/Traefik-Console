package providers

import (
	"os"
	"strings"
	"testing"
)

// The env file shipped with the Compose bundles must declare exactly the variables Console
// completes at startup, so Traefik never needs a variable it was created without.
func TestShippedDNSEnvFileDeclaresEveryProviderVariable(t *testing.T) {
	registry, err := LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, env := range registry.EnvVars() {
		want[env+"_FILE=/etc/traefik/secrets/"+strings.ToLower(env)+".secret"] = true
	}
	for _, folder := range []string{"traefik-console", "traefik-console-crowdsec"} {
		raw, err := os.ReadFile("../../../../../deploy/" + folder + "/config/traefik/dns-providers.env")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				got[line] = true
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d lines, want %d (run python3 tools/dns-env/generate.py)", folder, len(got), len(want))
		}
		for line := range want {
			if !got[line] {
				t.Fatalf("%s: missing %s (run python3 tools/dns-env/generate.py)", folder, line)
			}
		}
	}
}
