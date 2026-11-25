package docker

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

type HubItem struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type HubManager interface {
	ListHubItems(context.Context) ([]HubItem, error)
}

var supportedHubTypes = map[string]bool{
	"collections": true, "parsers": true, "scenarios": true, "postoverflows": true,
	"appsec-configs": true, "appsec-rules": true, "contexts": true,
}

type hubItemJSON struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

func validHubItem(item HubItem) bool {
	return supportedHubTypes[item.Type] && len(item.Name) > 0 && len(item.Name) <= 512 && len(item.Version) <= 64 && len(item.Status) <= 64 && !strings.ContainsAny(item.Name+item.Version+item.Status, "\x00\r\n")
}

func parseHubItems(raw []byte) ([]HubItem, error) {
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil {
		return nil, ErrExecFailed
	}
	result := []HubItem{}
	for kind, content := range document {
		if !supportedHubTypes[kind] {
			continue
		}
		var rows []hubItemJSON
		if err := json.Unmarshal(content, &rows); err != nil {
			var named map[string]hubItemJSON
			if json.Unmarshal(content, &named) != nil {
				return nil, ErrExecFailed
			}
			for name, item := range named {
				item.Name = name
				rows = append(rows, item)
			}
		}
		for _, row := range rows {
			item := HubItem{Name: row.Name, Type: kind, Version: row.Version, Status: row.Status}
			if !validHubItem(item) {
				return nil, ErrExecFailed
			}
			result = append(result, item)
			if len(result) > 1000 {
				return nil, ErrExecFailed
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (m *Client) ListHubItems(ctx context.Context) ([]HubItem, error) {
	release, err := m.lockOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	raw, err := m.execCrowdSec(ctx, "hub", "list", "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseHubItems(raw)
}
