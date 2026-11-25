package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCatalogUsesFixedEndpointAndBoundedDecoding(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		code, want int
		fail       bool
	}{
		{`[{"name":"example.invalid/plugin","latestVersion":"v1.0.0","displayName":"Example"},{"name":"bad","latestVersion":"latest"}]`, 200, 1, false},
		{`[{"name":"github.com/org/repo","import":"github.com/org/repo/plugin","latestVersion":"v1.0.0","snippet":{"yaml":"http: {}\n"}}]`, 200, 1, false},
		{`null`, 200, 0, true}, {`[]`, 200, 0, true}, {`[{"name":"bad","latestVersion":"latest"}]`, 200, 0, true},
		{`[{"name":"example.invalid/plugin","latestVersion":"v1.0.0"},{"name":"example.invalid/plugin","latestVersion":"v1.0.0"}]`, 200, 0, true},
		{`{`, 200, 0, true}, {`[]`, 503, 0, true}, {strings.Repeat(" ", 8*1024*1024+1), 200, 0, true},
	} {
		c := NewCatalogClient()
		c.client.Transport = catalogTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != traefikCatalogURL || r.Method != http.MethodGet {
				t.Fatal("unregistered endpoint")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.raw)), Header: make(http.Header)}, nil
		})
		got, err := c.Fetch(context.Background())
		c.Close()
		if tc.fail && !errors.Is(err, ErrCatalogUnavailable) {
			t.Fatal("missing catalog error classification")
		}
		if len(got) > 0 && strings.Contains(tc.raw, `"import"`) && (got[0].ModuleName != "github.com/org/repo/plugin" || got[0].SnippetYAML != "http: {}\n") {
			t.Fatal("upstream import/snippet mapping wrong")
		}
		if (err != nil) != tc.fail || len(got) != tc.want {
			t.Fatalf("catalog decode: %d %v", len(got), err)
		}
	}
	c := NewCatalogClient()
	defer c.Close()
	if err := c.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("redirect allowed")
	}
	transport := c.client.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || c.client.Timeout == 0 {
		t.Fatal("catalog transport policy weakened")
	}
}

func TestCatalogReconcilesGitHubCasingAndPreservesType(t *testing.T) {
	entries := []catalogEntry{
		{Import: "github.com/PascalMinder/GeoBlock", LatestVersion: "v0.2.2", Type: "middleware"},
		{Import: "github.com/PascalMinder/geoblock", LatestVersion: "v0.3.8", Type: "middleware", Versions: []string{"v0.3.8", "v0.3.7"}},
		{Import: "github.com/Hongbo-Miao/traefik-plugin-disable-graphql-introspection", LatestVersion: "v0.2.0", Type: "middleware"},
		{Import: "github.com/hongbo-miao/traefik-plugin-disable-graphql-introspection", LatestVersion: "v0.3.0", Type: "middleware"},
		{Import: "github.com/org/provider", LatestVersion: "v1.0.0", Type: "provider"},
	}
	fetch := func(entries []catalogEntry) ([]models.CatalogPluginRow, error) {
		raw, _ := json.Marshal(entries)
		c := NewCatalogClient()
		defer c.Close()
		c.client.Transport = catalogTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})
		return c.Fetch(context.Background())
	}
	first, err := fetch(entries)
	if err != nil || len(first) != 3 {
		t.Fatalf("case variants broke catalog: %+v %v", first, err)
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	second, err := fetch(entries)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if err != nil || string(a) != string(b) {
		t.Fatalf("catalog depends on upstream order: %s %s %v", a, b, err)
	}
	byModule := map[string]models.CatalogPluginRow{}
	for _, row := range first {
		byModule[row.ModuleName] = row
	}
	geo := byModule["github.com/PascalMinder/geoblock"]
	graphql := byModule["github.com/hongbo-miao/traefik-plugin-disable-graphql-introspection"]
	if geo.LatestVersion != "v0.3.8" || geo.PluginType != "middleware" || geo.VersionsJSON != `["v0.3.8","v0.3.7"]` {
		t.Fatalf("newest middleware metadata lost: %+v", geo)
	}
	if graphql.LatestVersion != "v0.3.0" || graphql.PluginType != "middleware" || byModule["github.com/org/provider"].PluginType != "provider" {
		t.Fatalf("plugin types/versions lost: %+v", first)
	}
	if _, err := fetch([]catalogEntry{
		{Import: "github.com/org/demo", LatestVersion: "v1.0.0", Type: "middleware"},
		{Import: "github.com/ORG/DEMO", LatestVersion: "v2.0.0", Type: "provider"},
	}); !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatal("conflicting types silently accepted")
	}
}
