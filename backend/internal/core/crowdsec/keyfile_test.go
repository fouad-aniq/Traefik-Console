package crowdsec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/traefik-console/internal/redaction"
)

// A bouncer configured outside the Console reads the key from this file: the path is shown before
// the first save, and it is not masked like the key itself.
func TestStatusGivesTheKeyFileForAnOutsideBouncer(t *testing.T) {
	m := &crowdsecManager{store: noIntegration{}, secrets: noSecret{}, defaultLAPIURL: "http://crowdsec:8080", settings: Settings{RuntimeSecretsDir: "/etc/traefik/secrets"}}
	st, err := m.Status(context.Background(), Inventory{})
	if err != nil {
		t.Fatal(err)
	}
	if st.KeyFile != "/etc/traefik/secrets/crowdsec_lapi_key.secret" {
		t.Fatalf("key file %q", st.KeyFile)
	}
	raw, _ := json.Marshal(st)
	var generic any
	_ = json.Unmarshal(raw, &generic)
	out, _ := json.Marshal(redaction.Value(generic))
	if !strings.Contains(string(out), `"key_file":"/etc/traefik/secrets/crowdsec_lapi_key.secret"`) {
		t.Fatalf("the path must reach the browser unmasked: %s", out)
	}
	m.settings.APIKey = "from-environment"
	if st, _ = m.Status(context.Background(), Inventory{}); st.KeyFile != "" {
		t.Fatal("a key from CROWDSEC_API_KEY is not written to a file")
	}
}
