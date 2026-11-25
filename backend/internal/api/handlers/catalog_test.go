package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogPagesUseActualSQLiteCountsAndStableFreshness(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := db.InitDB(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := db.NewStore(conn, make([]byte, 32))
	ctx := context.Background()
	rows := make([]models.CatalogPluginRow, 600)
	for i := range rows {
		rows[i] = models.CatalogPluginRow{ModuleName: fmt.Sprintf("example.invalid/p%03d", i), DisplayName: fmt.Sprintf("Plugin%03d", i), LatestVersion: "v1.0.0"}
	}
	if err = store.ReplacePluginCatalog(ctx, rows); err != nil {
		t.Fatal(err)
	}
	deps := routeDependencies(t, &routeCalls{})
	deps.Plugins = plugins.NewManager(store, nil, nil, nil)
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/plugins/catalog/page"+query, nil))
		return w
	}
	var page models.CatalogPage
	first := get("?limit=25&offset=500")
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || page.Total != 600 || len(page.Items) != 25 || page.Items[0].ModuleName != "example.invalid/p500" || page.LastSyncedAt == "" {
		t.Fatal("page beyond legacy500 limit failed")
	}
	timestamp := page.LastSyncedAt
	next := get("?q=does-not-exist")
	if next.Code != 200 || json.Unmarshal(next.Body.Bytes(), &page) != nil || page.Total != 0 || len(page.Items) != 0 || page.LastSyncedAt != timestamp {
		t.Fatal("filtered freshness/count mismatch")
	}
	next = get("?module=example.invalid%2Fp599&limit=1")
	if next.Code != 200 || json.Unmarshal(next.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Total != 1 || page.Items[0].ModuleName != "example.invalid/p599" {
		t.Fatal("exact module resolution failed")
	}
	next = get("?order=desc&limit=1")
	if next.Code != 200 || json.Unmarshal(next.Body.Bytes(), &page) != nil || page.Items[0].ModuleName != "example.invalid/p599" {
		t.Fatal("descending order failed")
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=bad", "?offset=-1", "?offset=1000001", "?offset=bad", "?order=other", "?q=a&module=b", "?q=" + strings.Repeat("a", 257)} {
		if get(q).Code != 422 {
			t.Fatal("unsafe page accepted", q)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = store.ReadPluginCatalogPage(canceled, "", "", 25, 0, false); err == nil {
		t.Fatal("cancellation ignored")
	}
}
