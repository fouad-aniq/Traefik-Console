package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/entrypoints"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

const mixedStatic = "entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\n  db: {address: ':5432'}\n  dns: {address: ':53/udp'}\ncertificatesResolvers:\n  le: {acme: {tlsChallenge: {}}}\n"

// protocolStack serves the real router and service managers over one real store.
func protocolStack(t *testing.T, rt liveRuntime) (http.Handler, db.Store) {
	t.Helper()
	root := t.TempDir()
	// The store refuses a parent writable by group or others, which a group-writable umask would produce.
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	database, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	store := db.NewStore(database, make([]byte, 32))
	applier := &enginetest.DirectApplier{Store: store}
	deps := routeDependencies(t, &routeCalls{})
	deps.ReadStaticConfig = func() ([]byte, error) { return []byte(mixedStatic), nil }
	deps.Services = services.NewServiceManager(store, rt, applier)
	deps.Routers = routers.NewRouterManager(store, staticFile(mixedStatic), rt, applier)
	deps.EntryPoints = entrypoints.NewEntryPointManager(store, staticFile(mixedStatic), applier)
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	return h, store
}

func routerOf(t *testing.T, w interface {
	Result() *http.Response
}, body []byte) models.UIResource[models.RouterConfig] {
	t.Helper()
	var saved models.WriteResult[models.RouterConfig]
	if err := json.Unmarshal(body, &saved); err != nil || saved.Resource == nil {
		t.Fatalf("not a saved router: %s", body)
	}
	return *saved.Resource
}

func listRoutersVia(t *testing.T, h http.Handler) map[string]models.UIResource[models.RouterConfig] {
	t.Helper()
	w := call(h, "GET", "/api/v1/routers", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var items []models.UIResource[models.RouterConfig]
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	out := map[string]models.UIResource[models.RouterConfig]{}
	for _, it := range items {
		out[it.Name] = it
	}
	return out
}

func TestCreateEditAndDeleteTCPAndUDPRoutersOverHTTP(t *testing.T) {
	scenarios := map[string]struct {
		protocol   string
		create     string
		edit       string
		wantRule   string
		wantEdited string
	}{
		"tcp, every connection": {"tcp",
			`{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"pg","entry_points":["db"]}`,
			`{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"pg2","entry_points":["db"],"priority":7}`,
			"HostSNI(`*`)", "pg2"},
		"tcp, passed-through TLS": {"tcp",
			`{"protocol":"tcp","domains":["vault.example.com"],"service":"pg","entry_points":["websecure"],"tls":{"enabled":true,"passthrough":true}}`,
			`{"protocol":"tcp","domains":["vault.example.com","v2.example.com"],"service":"pg","entry_points":["websecure"],"tls":{"enabled":true,"passthrough":true}}`,
			"HostSNI(`vault.example.com`)", "pg"},
		"tcp, terminated TLS": {"tcp",
			`{"protocol":"tcp","domains":["mqtt.example.com"],"service":"pg","entry_points":["websecure"],"tls":{"enabled":true,"cert_resolver":"le"}}`,
			`{"protocol":"tcp","domains":["mqtt.example.com"],"service":"pg","entry_points":["websecure"],"tls":{"enabled":true,"cert_resolver":"le","options":"modern"}}`,
			"HostSNI(`mqtt.example.com`)", "pg"},
		"udp": {"udp",
			`{"protocol":"udp","service":"dns","entry_points":["dns"]}`,
			`{"protocol":"udp","service":"dns2","entry_points":["dns"]}`,
			"", "dns2"},
		"udp, entrypoint chosen for it": {"udp",
			`{"protocol":"udp","service":"dns"}`,
			`{"protocol":"udp","service":"dns2"}`,
			"", "dns2"},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			h, store := protocolStack(t, liveRuntime{})

			w := call(h, "PUT", "/api/v1/routers/r1?dry_run=true", sc.create, create...)
			if w.Code != 200 {
				t.Fatalf("preview: %d %s", w.Code, w.Body.String())
			}
			if row, _ := store.GetItem(context.Background(), models.KindRouter, "r1"); row != nil {
				t.Fatal("a preview stored the router")
			}

			w = call(h, "PUT", "/api/v1/routers/r1", sc.create, create...)
			if w.Code != 200 {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			got := routerOf(t, w, w.Body.Bytes())
			if got.Config.Protocol != sc.protocol || got.Config.Rule != sc.wantRule || got.Version != 1 || len(got.Config.EntryPoints) != 1 {
				t.Fatalf("%+v", got)
			}
			if sc.protocol == "udp" && (got.Config.Rule != "" || got.Config.TLS != nil) {
				t.Fatalf("a UDP router carries only entrypoints and a service: %+v", got.Config)
			}

			listed := listRoutersVia(t, h)["r1"]
			if listed.Config.Protocol != sc.protocol || listed.Origin != "managed" {
				t.Fatalf("%+v", listed)
			}

			// Editing needs the version (the engine enforces which one; its tests cover that).
			if w := call(h, "PUT", "/api/v1/routers/r1", sc.edit); w.Code != 428 {
				t.Fatalf("edit without a version: %d", w.Code)
			}
			w = call(h, "PUT", "/api/v1/routers/r1", sc.edit, edit("1")...)
			if w.Code != 200 {
				t.Fatalf("edit: %d %s", w.Code, w.Body.String())
			}
			edited := routerOf(t, w, w.Body.Bytes())
			if edited.Version != 2 || edited.Config.Service != sc.wantEdited || edited.Config.Protocol != sc.protocol {
				t.Fatalf("%+v", edited)
			}

			if w := call(h, "DELETE", "/api/v1/routers/r1", "", edit("2")...); w.Code != 200 {
				t.Fatalf("delete: %d %s", w.Code, w.Body.String())
			}
			if _, still := listRoutersVia(t, h)["r1"]; still {
				t.Fatal("the router is still listed")
			}
			if w := call(h, "DELETE", "/api/v1/routers/r1", "", edit("2")...); w.Code != 404 {
				t.Fatalf("deleting it twice: %d", w.Code)
			}
		})
	}
}

func TestTCPAndUDPRouterMistakesAreReportedAs422(t *testing.T) {
	cases := map[string]string{
		"an unknown protocol":                `{"protocol":"sctp","service":"s","custom_rule":"HostSNI(` + "`*`" + `)"}`,
		"a TCP router without a service":     `{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","entry_points":["db"]}`,
		"a UDP router without a service":     `{"protocol":"udp","entry_points":["dns"]}`,
		"a TCP router with nothing to match": `{"protocol":"tcp","service":"s","entry_points":["db"]}`,
		"a hostname without TLS":             `{"protocol":"tcp","domains":["a.example.com"],"service":"s","entry_points":["db"]}`,
		"a TCP router on a UDP entrypoint":   `{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"s","entry_points":["dns"]}`,
		"a UDP router on a TCP entrypoint":   `{"protocol":"udp","service":"s","entry_points":["db"]}`,
		"a TCP router with the no backend":   `{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"noop@internal","entry_points":["db"]}`,
		"a UDP router with a rule":           `{"protocol":"udp","custom_rule":"HostSNI(` + "`*`" + `)","service":"s","entry_points":["dns"]}`,
		"a UDP router with TLS":              `{"protocol":"udp","service":"s","entry_points":["dns"],"tls":{"enabled":true}}`,
		"a UDP router with a priority":       `{"protocol":"udp","service":"s","entry_points":["dns"],"priority":1}`,
		"passthrough with a certificate":     `{"protocol":"tcp","domains":["a.example.com"],"service":"s","entry_points":["websecure"],"tls":{"enabled":true,"passthrough":true,"cert_resolver":"le"}}`,
		"passthrough on an HTTP router":      `{"domains":["a.example.com"],"service":"s","entry_points":["websecure"],"tls":{"enabled":true,"passthrough":true}}`,
		"an entrypoint that does not exist":  `{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"s","entry_points":["ghost"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h, store := protocolStack(t, liveRuntime{})
			w := call(h, "PUT", "/api/v1/routers/bad", body, create...)
			if w.Code != 422 || errorOf(t, w).Code != "ERR_VALIDATION_FAILED" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if w := call(h, "PUT", "/api/v1/routers/bad?dry_run=true", body, create...); w.Code != 422 {
				t.Fatalf("preview: %d", w.Code)
			}
			if row, _ := store.GetItem(context.Background(), models.KindRouter, "bad"); row != nil {
				t.Fatal("a refused router was stored")
			}
		})
	}
}

func TestARouterCannotChangeProtocolOverHTTP(t *testing.T) {
	h, _ := protocolStack(t, liveRuntime{})
	if w := call(h, "PUT", "/api/v1/routers/pg", `{"protocol":"tcp","custom_rule":"HostSNI(`+"`*`"+`)","service":"pg","entry_points":["db"]}`, create...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for name, body := range map[string]string{
		"to http": `{"domains":["a.example.com"],"service":"web","entry_points":["web"]}`,
		"to udp":  `{"protocol":"udp","service":"dns","entry_points":["dns"]}`,
	} {
		w := call(h, "PUT", "/api/v1/routers/pg", body, edit("1")...)
		if w.Code != 422 || errorOf(t, w).Code != "ERR_VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

func TestAServiceShowsAndIsProtectedByTheManagedRoutersThatUseIt(t *testing.T) {
	h, store := protocolStack(t, liveRuntime{})
	call(h, "PUT", "/api/v1/services/pg", `{"protocol":"tcp","servers":[{"address":"db:5432"}]}`, create...)
	call(h, "PUT", "/api/v1/services/dns", `{"protocol":"udp","servers":[{"address":"10.0.0.53:53"}]}`, create...)
	call(h, "PUT", "/api/v1/services/web", `{"servers":[{"url":"http://app"}]}`, create...)
	call(h, "PUT", "/api/v1/services/spare", `{"protocol":"tcp","servers":[{"address":"x:1"}]}`, create...)
	for path, body := range map[string]string{
		"pg":   `{"protocol":"tcp","custom_rule":"HostSNI(` + "`*`" + `)","service":"pg","entry_points":["db"]}`,
		"dns":  `{"protocol":"udp","service":"dns","entry_points":["dns"]}`,
		"site": `{"domains":["a.example.com"],"service":"web","entry_points":["web"]}`,
	} {
		if w := call(h, "PUT", "/api/v1/routers/"+path, body, create...); w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}

	got := listServicesVia(t, h)
	for service, router := range map[string]string{"pg": "pg", "dns": "dns", "web": "site"} {
		u := got[service].Config.UsedBy
		if u == nil || len(u.Routers) != 1 || u.Routers[0].Name != router || u.Routers[0].ReadOnly {
			t.Errorf("%s should be used by %s: %+v", service, router, u)
		}
	}
	if u := got["spare"].Config.UsedBy; u == nil || len(u.Routers) != 0 {
		t.Errorf("spare is known unused: %+v", u)
	}

	for _, name := range []string{"pg", "dns", "web"} {
		w := call(h, "DELETE", "/api/v1/services/"+name, "", edit("1")...)
		if w.Code != 409 || errorOf(t, w).Code != "ERR_CONFLICT" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if row, _ := store.GetItem(context.Background(), models.KindService, name); row == nil {
			t.Errorf("%s was deleted while a router used it", name)
		}
	}
	if w := call(h, "DELETE", "/api/v1/services/spare", "", edit("1")...); w.Code != 200 {
		t.Fatalf("an unused service could not be deleted: %d %s", w.Code, w.Body.String())
	}

	// Once the router is gone the service can follow.
	if w := call(h, "DELETE", "/api/v1/routers/pg", "", edit("1")...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(h, "DELETE", "/api/v1/services/pg", "", edit("1")...); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestTraefikReportedRoutersOfEveryProtocolAreListedReadOnly(t *testing.T) {
	rt := liveRuntime{snapshot: status.RuntimeSnapshot{
		HTTP: status.RuntimeResources{Routers: map[string]status.ExternalItem{"dash@docker": {Name: "dash", Provider: "docker", Status: "enabled", Raw: map[string]any{"rule": "Host(`d.example.com`)", "service": "dash"}}}},
		TCP:  status.RuntimeResources{Routers: map[string]status.ExternalItem{"redis@docker": {Name: "redis", Provider: "docker", Status: "enabled", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": "redis"}}}},
		UDP:  status.RuntimeResources{Routers: map[string]status.ExternalItem{"vpn@docker": {Name: "vpn", Provider: "docker", Status: "enabled", Raw: map[string]any{"service": "vpn"}}}},
	}}
	h, _ := protocolStack(t, rt)
	got := listRoutersVia(t, h)
	for name, protocol := range map[string]string{"dash@docker": "http", "redis@docker": "tcp", "vpn@docker": "udp"} {
		r, ok := got[name]
		if !ok || r.Config.Protocol != protocol || !r.ReadOnly || r.Origin != "external" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// A router Traefik reports cannot be changed from the console, whatever its protocol.
	for _, name := range []string{"dash@docker", "redis@docker", "vpn@docker"} {
		if w := call(h, "PUT", "/api/v1/routers/"+name, `{"protocol":"tcp","service":"s","custom_rule":"HostSNI(`+"`*`"+`)"}`, create...); w.Code == 200 {
			t.Errorf("%s was overwritten", name)
		}
	}
}

func TestEntryPointsSaySoWhichAreUDP(t *testing.T) {
	h, _ := protocolStack(t, liveRuntime{})
	w := call(h, "GET", "/api/v1/entrypoints", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		EntryPoints []string                                     `json:"entry_points"`
		Protocols   map[string]string                            `json:"protocols"`
		Items       []models.UIResource[models.EntryPointConfig] `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"web": "tcp", "websecure": "tcp", "db": "tcp", "dns": "udp"}
	if len(out.EntryPoints) != 4 || len(out.Protocols) != 4 {
		t.Fatalf("%+v", out)
	}
	for name, protocol := range want {
		if out.Protocols[name] != protocol {
			t.Errorf("%s = %q, want %q", name, out.Protocols[name], protocol)
		}
	}
	// The same call carries the full resources: declared in traefik.yml, so read-only but adoptable.
	if len(out.Items) != 4 {
		t.Fatalf("items: %+v", out.Items)
	}
	for _, item := range out.Items {
		if item.Config.Protocol != want[item.Name] || !item.ReadOnly || item.Origin != "external" || !item.Config.Adoptable {
			t.Errorf("%s: %+v", item.Name, item)
		}
	}
}
