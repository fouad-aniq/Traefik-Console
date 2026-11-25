package plugins

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/validation"
)

const traefikCatalogURL = "https://plugins.traefik.io/api/services/plugins"

type catalogEntry struct {
	Import        string         `json:"import"`
	Name          string         `json:"name"`        // e.g. "github.com/maxlerebourg/crowdsec-bouncer-traefik-plugin"
	DisplayName   string         `json:"displayName"` // e.g. "Crowdsec Bouncer Traefik Plugin"
	Summary       string         `json:"summary"`
	LatestVersion string         `json:"latestVersion"`
	Snippet       map[string]any `json:"snippet"`
	Versions      []string       `json:"versions"`
	Type          string         `json:"type"`
}

// CatalogClient fetches only the fixed public catalog; it has no persistence or staging access.
type CatalogClient struct{ client *http.Client }

var ErrCatalogUnavailable = errors.New("public plugin catalog unavailable")

// CatalogHTTPError exposes only the upstream status, never its response body.
type CatalogHTTPError struct {
	StatusCode int `json:"-"`
}

func (e *CatalogHTTPError) Error() string {
	return fmt.Sprintf("public plugin catalog returned HTTP %d", e.StatusCode)
}
func (e *CatalogHTTPError) Unwrap() error { return ErrCatalogUnavailable }

func NewCatalogClient() *CatalogClient {
	return &CatalogClient{client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 * 1024}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *CatalogClient) Close() { c.client.CloseIdleConnections() }

func (c *CatalogClient) Fetch(ctx context.Context) ([]models.CatalogPluginRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traefikCatalogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed", ErrCatalogUnavailable)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, &CatalogHTTPError{StatusCode: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("%w: response read failed", ErrCatalogUnavailable)
	}

	if len(body) > 8*1024*1024 {
		return nil, fmt.Errorf("%w: response exceeds limit", ErrCatalogUnavailable)
	}
	var entries []catalogEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("%w: invalid response", ErrCatalogUnavailable)
	}

	rows := make([]models.CatalogPluginRow, 0, len(entries))
	if len(entries) > 10000 {
		return nil, fmt.Errorf("%w: too many entries", ErrCatalogUnavailable)
	}
	for _, e := range entries {
		module := e.Import
		if module == "" {
			module = e.Name
		}
		if !validation.ValidPluginModule(module) || !validation.ValidPluginVersion(e.LatestVersion) || len(e.Summary) > 4096 || len(e.DisplayName) > 256 {
			continue
		}
		snippet, _ := e.Snippet["yaml"].(string)
		if len(snippet) > 1024*1024 {
			continue
		}
		if len(e.Versions) > 256 {
			return nil, fmt.Errorf("%w: too many versions", ErrCatalogUnavailable)
		}
		versions := []string{}
		seenVersions := map[string]bool{}
		for _, v := range append(e.Versions, e.LatestVersion) {
			if validation.ValidPluginVersion(v) && !seenVersions[v] {
				versions = append(versions, v)
				seenVersions[v] = true
			}
		}
		sort.SliceStable(versions, func(i, j int) bool { c, _ := validation.ComparePluginVersions(versions[i], versions[j]); return c > 0 })
		encoded, _ := json.Marshal(versions)
		pluginType := e.Type
		if pluginType != "middleware" && pluginType != "provider" {
			pluginType = ""
		}
		aliasesJSON, _ := json.Marshal(entryAliases(module, e.Name, snippet))
		rows = append(rows, models.CatalogPluginRow{
			VersionsJSON: string(encoded), PluginType: pluginType, AliasesJSON: string(aliasesJSON),
			ModuleName:    module,
			DisplayName:   e.DisplayName,
			Summary:       e.Summary,
			LatestVersion: e.LatestVersion,
			SnippetYAML:   snippet,
		})
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no usable entries", ErrCatalogUnavailable)
	}
	// The public catalog publishes old and new records for the same GitHub
	// repository under different casing. Keep the newest record, including its
	// exact import path, type and version list, independently of response order.
	sort.Slice(rows, func(i, j int) bool {
		a, b := validation.PluginModuleKey(rows[i].ModuleName), validation.PluginModuleKey(rows[j].ModuleName)
		if a != b {
			return a < b
		}
		if c, _ := validation.ComparePluginVersions(rows[i].LatestVersion, rows[j].LatestVersion); c != 0 {
			return c > 0
		}
		return rows[i].ModuleName < rows[j].ModuleName
	})
	unique := make([]models.CatalogPluginRow, 0, len(rows))
	seen := make(map[string]string, len(rows))
	exact := make(map[string]bool, len(rows))
	for _, row := range rows {
		key := validation.PluginModuleKey(row.ModuleName)
		if exact[row.ModuleName] {
			return nil, fmt.Errorf("%w: duplicate modules", ErrCatalogUnavailable)
		}
		exact[row.ModuleName] = true
		if pluginType, exists := seen[key]; exists {
			if pluginType != row.PluginType {
				return nil, fmt.Errorf("%w: conflicting plugin types", ErrCatalogUnavailable)
			}
			continue
		}
		seen[key] = row.PluginType
		unique = append(unique, row)
	}
	return unique, nil
}

// entryAliases returns the other valid module paths a catalog entry is published under: its name and the
// module declared in its snippet. They identify the same entry, so an installed plugin that uses one of
// them is matched to it even when the entry's import path differs.
func entryAliases(primary, name, snippet string) []string {
	out := []string{}
	seen := map[string]bool{validation.PluginModuleKey(primary): true}
	add := func(module string) {
		key := validation.PluginModuleKey(module)
		if module == "" || seen[key] || !validation.ValidPluginModule(module) {
			return
		}
		seen[key] = true
		out = append(out, module)
	}
	add(name)
	var doc struct {
		Experimental struct {
			Plugins map[string]struct {
				ModuleName string `yaml:"moduleName"`
			} `yaml:"plugins"`
		} `yaml:"experimental"`
	}
	if snippet != "" && yaml.Unmarshal([]byte(snippet), &doc) == nil {
		aliases := make([]string, 0, len(doc.Experimental.Plugins))
		for alias := range doc.Experimental.Plugins {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			add(doc.Experimental.Plugins[alias].ModuleName)
		}
	}
	return out
}
