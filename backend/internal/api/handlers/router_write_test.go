package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
)

type staticFile string

func (s staticFile) ReadStaticConfig() ([]byte, error) { return []byte(s), nil }

// routerStack serves the real router manager over HTTP, on a real store.
func routerStack(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	// The store refuses a parent writable by group or others, which a group-writable umask would produce.
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	database, err := db.InitDB(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := db.NewStore(database, make([]byte, 32))
	deps := routeDependencies(t, &routeCalls{})
	deps.Routers = routers.NewRouterManager(store, staticFile("entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\n"), liveRuntime{}, &enginetest.DirectApplier{Store: store})
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestARouterWithoutABackendNeedsAMiddlewareOverHTTP(t *testing.T) {
	h := routerStack(t)
	redirect := `{"entry_points":["web"],"domains":["old.example.com"],"service":"noop@internal","middlewares":["to-https"]}`
	bare := `{"entry_points":["web"],"domains":["old.example.com"],"service":"noop@internal"}`

	w := call(h, "PUT", "/api/v1/routers/old-site", bare, create...)
	if w.Code != 422 || errorOf(t, w).Code != "ERR_VALIDATION_FAILED" {
		t.Fatalf("a router that goes nowhere was accepted: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "PUT", "/api/v1/routers/old-site?dry_run=true", bare, create...); w.Code != 422 {
		t.Fatalf("preview: %d", w.Code)
	}

	w = call(h, "PUT", "/api/v1/routers/old-site", redirect, create...)
	if w.Code != 200 {
		t.Fatalf("a redirect-only router was refused: %d %s", w.Code, w.Body.String())
	}
	var saved models.WriteResult[models.RouterConfig]
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Resource == nil {
		t.Fatalf("%s", w.Body.String())
	}
	if c := saved.Resource.Config; c.Service != "noop@internal" || c.Rule != "Host(`old.example.com`)" || len(c.Middlewares) != 1 {
		t.Fatalf("%+v", c)
	}

	// Editing it back to no middleware is refused too, and what was stored stays.
	if w := call(h, "PUT", "/api/v1/routers/old-site", bare, edit("1")...); w.Code != 422 {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
}

func TestARouterNeedsAServiceOverHTTP(t *testing.T) {
	h := routerStack(t)
	for name, body := range map[string]string{
		"missing": `{"entry_points":["web"],"domains":["a.example.com"]}`,
		"empty":   `{"entry_points":["web"],"domains":["a.example.com"],"service":""}`,
		"blank":   `{"entry_points":["web"],"domains":["a.example.com"],"service":"  ","middlewares":["to-https"]}`,
	} {
		if w := call(h, "PUT", "/api/v1/routers/r1", body, create...); w.Code != 422 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}
