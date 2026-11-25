package docker

import "testing"

func TestParseHubItemsSupportsNativeShapesAndBounds(t *testing.T) {
	items, err := parseHubItems([]byte(`{"collections":[{"name":"crowdsecurity/traefik","version":"0.1.0","status":"installed"}],"scenarios":{"crowdsecurity/http-probing":{"version":"0.2.0","status":"installed"}}}`))
	if err != nil || len(items) != 2 {
		t.Fatalf("native hub shapes rejected: %+v %v", items, err)
	}
	if items[0].Type != "collections" || items[0].Name != "crowdsecurity/traefik" || items[1].Type != "scenarios" || items[1].Name != "crowdsecurity/http-probing" {
		t.Fatalf("unexpected hub projection: %+v", items)
	}
	for _, raw := range []string{`{"collections":[{"name":"bad\nname"}]}`, `{"unknown":[{"name":"x"}]}`, `{"collections":[{"version":"0.1.0"}]}`} {
		items, err = parseHubItems([]byte(raw))
		if raw == `{"unknown":[{"name":"x"}]}` {
			if err != nil || len(items) != 0 {
				t.Fatalf("unknown hub type was fabricated: %+v %v", items, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("invalid hub item accepted: %s", raw)
		}
	}
}
