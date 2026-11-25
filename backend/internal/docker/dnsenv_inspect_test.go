package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInspectReportsWhetherTraefikLoadsTheDNSEnvFile(t *testing.T) {
	loader := "f=/etc/traefik/" + DNSEnvFileName + "; exec traefik --configFile=/etc/traefik/static/traefik.yml"
	for _, tc := range []struct {
		name            string
		entrypoint, cmd []string
		want            bool
	}{
		{"shipped start command", []string{"/bin/sh", "-c", loader}, nil, true},
		{"image default", []string{"/entrypoint.sh"}, []string{"traefik"}, false},
		{"plain command", nil, []string{"--configFile=/etc/traefik/static/traefik.yml"}, false},
	} {
		c, _ := New(registration("http://proxy:2375"))
		c.httpClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := json.Marshal(map[string]any{"Id": strings.Repeat("a", 64), "Name": "/traefik", "State": map[string]bool{"Running": true},
				"Config": map[string]any{"Entrypoint": tc.entrypoint, "Cmd": tc.cmd}})
			return response(200, string(raw)), nil
		})
		st, err := c.InspectTraefikContainer(context.Background())
		if err != nil || st.LoadsDNSEnvFile != tc.want {
			t.Fatalf("%s: err=%v loads=%v want %v", tc.name, err, st != nil && st.LoadsDNSEnvFile, tc.want)
		}
	}
}
