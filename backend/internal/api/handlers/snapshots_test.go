package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

// storeOperations serves snapshot reads from a real store, like the engine does.
type storeOperations struct {
	engine.Operations
	store db.Store
}

func (s storeOperations) SnapshotConfig(ctx context.Context, id int64) (*models.SnapshotConfig, error) {
	row, err := s.store.GetSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, engine.ErrSnapshotNotFound
	}
	return &models.SnapshotConfig{ID: row.ID, StaticYAML: string(row.StaticYAML), DynamicYAML: string(row.DynamicYAML), DomainsYAML: string(row.DomainsYAML)}, nil
}

func openHistory(t *testing.T) (db.Store, func(db.Store) http.Handler) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := db.InitDB(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn, make([]byte, 32))
	build := func(reader db.Store) http.Handler {
		deps := routeDependencies(t, &routeCalls{})
		deps.History = reader
		deps.Operations = storeOperations{Operations: deps.Operations, store: reader}
		handler, err := NewServer(deps, middleware.Policy{})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	return store, build
}

func TestSnapshotConfigIsRedactedAndNeverMutated(t *testing.T) {
	store, build := openHistory(t)
	ctx := context.Background()
	// Only synthetic canaries, never real credentials. The stored files keep them byte-for-byte;
	// only the HTTP projection redacts.
	snap := &models.SnapshotRow{Name: "canary", Origin: models.SnapshotManual, State: []byte(`{}`),
		StaticYAML:  []byte("# synthetic-canary\nentryPoints:\n  web:\n    address: ':80'\ncertificatesResolvers:\n  test:\n    acme:\n      eab:\n        hmacEncoded: synthetic-canary\n"),
		DynamicYAML: []byte("http:\n  services:\n    app:\n      loadBalancer:\n        servers:\n          - url: https://user:synthetic-canary@example.invalid/path?q=synthetic-canary#synthetic-canary\n  middlewares:\n    auth:\n      basicAuth:\n        users: [synthetic-canary]\n    custom:\n      plugin:\n        arbitrary:\n          license: synthetic-canary\n          enabled: true\n"),
		DomainsYAML: []byte("tls: {}\n")}
	if _, err := store.CreateSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	handler := build(store)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost"+path, nil))
		return w
	}
	w := get("/api/v1/snapshots/" + strconv.FormatInt(snap.ID, 10) + "/config")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("configuration read failed", w.Code)
	}
	var got models.SnapshotConfig
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != snap.ID {
		t.Fatal("invalid config response")
	}
	if strings.Contains(w.Body.String(), "synthetic-canary") {
		t.Fatal("secret leaked through the snapshot config")
	}
	if !strings.Contains(got.StaticYAML, "entryPoints:") || !strings.Contains(got.DynamicYAML, "https://example.invalid/path?redacted") || !strings.Contains(got.DynamicYAML, "[redacted]") {
		t.Fatal("redacted structure missing", got)
	}
	original, err := store.GetSnapshot(ctx, snap.ID)
	if err != nil || string(original.StaticYAML) != string(snap.StaticYAML) || string(original.DynamicYAML) != string(snap.DynamicYAML) {
		t.Fatal("read mutated history")
	}
	for _, tc := range []struct {
		id     string
		status int
		code   string
	}{
		{"0", 422, "ERR_VALIDATION_FAILED"}, {"-1", 422, "ERR_VALIDATION_FAILED"}, {"bad", 422, "ERR_VALIDATION_FAILED"}, {"9223372036854775808", 422, "ERR_VALIDATION_FAILED"}, {"999999", 404, "ERR_NOT_FOUND"},
	} {
		result := get("/api/v1/snapshots/" + tc.id + "/config")
		if result.Code != tc.status || !strings.Contains(result.Body.String(), tc.code) || !strings.Contains(result.Body.String(), "operation_id") {
			t.Fatal("wrong failure projection", tc.id, result.Code)
		}
	}
}

func TestUnsafeSnapshotYAMLFailsClosed(t *testing.T) {
	store, build := openHistory(t)
	snap := &models.SnapshotRow{Name: "bad", Origin: models.SnapshotManual, State: []byte(`{}`), StaticYAML: []byte("password: [synthetic-canary"), DynamicYAML: []byte(strings.Repeat("x", 1024*1024+1))}
	if _, err := store.CreateSnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	build(store).ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/snapshots/"+strconv.FormatInt(snap.ID, 10)+"/config", nil))
	var got models.SnapshotConfig
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.StaticYAML != "[configuration unavailable]" || got.DynamicYAML != "[configuration unavailable]" || strings.Contains(w.Body.String(), "synthetic-canary") {
		t.Fatal("unsafe YAML did not fail closed", w.Code, w.Body.String())
	}
}

func TestSnapshotListPagesAndNeverExposesFileContent(t *testing.T) {
	store, build := openHistory(t)
	ctx := context.Background()
	for i := range 3 {
		if _, err := store.CreateSnapshot(ctx, &models.SnapshotRow{Name: "s" + strconv.Itoa(i), Origin: models.SnapshotManual, State: []byte(`{}`), StaticYAML: []byte("synthetic-canary: 1\n")}); err != nil {
			t.Fatal(err)
		}
	}
	handler := build(store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/snapshots?limit=2", nil))
	var first struct {
		Items      []models.SnapshotRow `json:"items"`
		NextCursor int64                `json:"next_cursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &first) != nil || len(first.Items) != 2 || first.NextCursor == 0 || strings.Contains(w.Body.String(), "synthetic-canary") || strings.Contains(w.Body.String(), "static_yaml") {
		t.Fatal("first page", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/snapshots?limit=2&cursor="+strconv.FormatInt(first.NextCursor, 10), nil))
	var second struct {
		Items      []models.SnapshotRow `json:"items"`
		NextCursor int64                `json:"next_cursor"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &second) != nil || len(second.Items) != 1 || second.NextCursor != 0 {
		t.Fatal("second page", w.Code, w.Body.String())
	}
}
