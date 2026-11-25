package handlers

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/core/crowdsec"
	"github.com/traefik-console/internal/core/domains"
	"github.com/traefik-console/internal/core/entrypoints"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/core/resolvers"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

// These adapters deliberately return a real domain error. They check HTTP
// delegation and failure projection, not successful configuration execution.
type routeCalls struct {
	names []string
	opts  models.WriteOptions
}

func (c *routeCalls) called(name string) error {
	c.names = append(c.names, name)
	return engine.ErrValidationFailed
}

type resolverRoutes struct {
	resolvers.ResolverManager
	c *routeCalls
}

func (f resolverRoutes) ListResolvers(context.Context) ([]models.UIResource[models.ResolverInput], error) {
	return nil, f.c.called("resolvers")
}

type entryPointRoutes struct {
	entrypoints.EntryPointManager
	c *routeCalls
}

func (f entryPointRoutes) ListEntryPoints(context.Context) ([]models.UIResource[models.EntryPointConfig], error) {
	return nil, f.c.called("entrypoints")
}
func (f entryPointRoutes) GetEntryPoint(_ context.Context, name string) (*models.UIResource[models.EntryPointConfig], error) {
	return nil, f.c.called("entrypoint:" + name)
}
func (f entryPointRoutes) UpsertEntryPoint(_ context.Context, in models.EntryPointConfig, _ models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error) {
	return nil, f.c.called("entrypoint-upsert:" + in.Name)
}
func (f entryPointRoutes) DeleteEntryPoint(_ context.Context, name string, _ models.WriteOptions) (*models.WriteResult[models.EntryPointConfig], error) {
	return nil, f.c.called("entrypoint-delete:" + name)
}

type domainRoutes struct {
	domains.DomainManager
	c *routeCalls
}

func (f domainRoutes) SaveDomain(_ context.Context, name string, _ models.DomainConfig, opts models.WriteOptions) (*models.WriteResult[models.DomainConfig], error) {
	f.c.opts = opts
	return nil, f.c.called("domain:" + name)
}

type certificateRoutes struct {
	certificates.CertificateManager
	c *routeCalls
}

func (f certificateRoutes) ImportCertificate(_ context.Context, name, purpose, cert, key string, _ models.WriteOptions) (*models.WriteResult[models.CertificateConfig], error) {
	return nil, f.c.called("certificate:" + name + ":" + purpose + ":" + cert + ":" + key)
}

type routerRoutes struct {
	routers.RouterManager
	c *routeCalls
}

func (f routerRoutes) UpsertRouter(_ context.Context, name string, _ models.RouterConfig, _ models.WriteOptions) (*models.WriteResult[models.RouterConfig], error) {
	return nil, f.c.called("router:" + name)
}

type serviceRoutes struct {
	services.ServiceManager
	c *routeCalls
}

func (f serviceRoutes) UpsertService(_ context.Context, name string, _ models.ServiceConfig, _ models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	return nil, f.c.called("service:" + name)
}

type middlewareRoutes struct {
	middlewares.MiddlewareManager
	c *routeCalls
}

func (f middlewareRoutes) UpsertMiddleware(_ context.Context, name string, _ models.MiddlewareConfig, _ models.WriteOptions) (*models.WriteResult[models.MiddlewareConfig], error) {
	return nil, f.c.called("middleware:" + name)
}
func (f middlewareRoutes) UpsertChain(_ context.Context, name string, _ models.ChainConfig, _ models.WriteOptions) (*models.WriteResult[models.ChainConfig], error) {
	return nil, f.c.called("chain:" + name)
}

type pluginRoutes struct {
	plugins.PluginManager
	c *routeCalls
}

func (f pluginRoutes) InstallFromYAML(_ context.Context, in plugins.InstallFromYAMLRequest, _ models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	return nil, f.c.called("plugin:" + in.ShortName)
}
func (f pluginRoutes) ApplyPluginUpdate(_ context.Context, alias string, version string, _ models.WriteOptions) (*models.WriteResult[models.StaticPluginConfig], error) {
	return nil, f.c.called("plugin-update:" + alias)
}
func (f pluginRoutes) SearchCatalog(context.Context, string) ([]models.CatalogPluginView, error) {
	return nil, f.c.called("catalog")
}

type crowdSecRoutes struct {
	crowdsec.CrowdSecManager
	c *routeCalls
}

func (f crowdSecRoutes) AddDecision(_ context.Context, ip, duration, reason string) error {
	return f.c.called("decision:" + ip + ":" + duration + ":" + reason)
}
func (f crowdSecRoutes) DeleteDecision(_ context.Context, ip string) error {
	return f.c.called("delete-decision:" + ip)
}

type historyRoutes struct {
	db.HistoryReader
	c *routeCalls
}

func (f historyRoutes) ListSnapshots(_ context.Context, limit int, cursor int64) ([]models.SnapshotRow, error) {
	if limit != defaultPageLimit+1 {
		panic("snapshot page size changed")
	}
	return nil, f.c.called("snapshots")
}

func (f historyRoutes) ListOperations(_ context.Context, limit int, cursor int64) ([]models.OperationRow, error) {
	return nil, f.c.called("operations")
}

type containerRoutes struct {
	docker.TraefikContainerInspector
	c *routeCalls
}

func (f containerRoutes) InspectTraefikContainer(context.Context) (*docker.ContainerStatus, error) {
	return nil, f.c.called("container")
}

type operationRoutes struct {
	engine.Operations
	c *routeCalls
}

func (f operationRoutes) CreateSnapshot(_ context.Context, name, _ string) (*models.SnapshotRow, error) {
	return nil, f.c.called("snapshot-create:" + name)
}
func (f operationRoutes) RestoreSnapshot(_ context.Context, id int64, req models.RestoreRequest) (*models.RestoreResult, error) {
	if id != 12 {
		panic("snapshot ID lost")
	}
	return nil, f.c.called("restore:" + req.RestoreToken)
}
func (f operationRoutes) PreviewRestore(_ context.Context, id int64) (*models.OperationPreview, error) {
	return &models.OperationPreview{RestoreToken: "token-" + strings.Repeat("a", 8)}, nil
}
func (f operationRoutes) DeleteSnapshot(_ context.Context, id int64, force bool) error {
	return f.c.called("snapshot-delete:" + strconv.FormatInt(id, 10) + ":" + strconv.FormatBool(force))
}
func (f operationRoutes) Recover(_ context.Context, id int64) (*models.OperationRow, error) {
	return nil, f.c.called("recover:" + strconv.FormatInt(id, 10))
}
func (f operationRoutes) Abandon(_ context.Context, id int64) (*models.OperationRow, error) {
	return nil, f.c.called("abandon:" + strconv.FormatInt(id, 10))
}
func (f operationRoutes) State(context.Context) (*models.ConfigState, error) {
	return nil, f.c.called("state")
}
func (f operationRoutes) Adopt(context.Context) (*models.SnapshotRow, error) {
	return nil, f.c.called("adopt")
}
func (f operationRoutes) Restart(context.Context) error { return f.c.called("restart") }

func routeDependencies(t *testing.T, c *routeCalls) Dependencies {
	t.Helper()
	observation := status.NewObservationClient("http://127.0.0.1:1")
	t.Cleanup(observation.Close)
	return Dependencies{History: historyRoutes{c: c}, Observation: observation, Container: containerRoutes{c: c}, Operations: operationRoutes{c: c}, ReadStaticConfig: func() ([]byte, error) { return nil, c.called("static") }, Resolvers: resolverRoutes{c: c}, EntryPoints: entryPointRoutes{c: c}, Domains: domainRoutes{c: c}, Certificates: certificateRoutes{c: c}, Routers: routerRoutes{c: c}, Services: serviceRoutes{c: c}, Middlewares: middlewareRoutes{c: c}, Plugins: pluginRoutes{c: c}, CrowdSec: crowdSecRoutes{c: c}}
}
func TestRouteGroupsDelegateAndProjectFailures(t *testing.T) {
	calls := &routeCalls{}
	h, err := NewServer(routeDependencies(t, calls), middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body, want string }{
		{"GET", "/api/v1/resolvers", "", "resolvers"},
		{"PUT", "/api/v1/domains/routed", `{"name":"body-name"}`, "domain:routed"},
		{"PUT", "/api/v1/certificates/routed", `{"purpose":"server","cert_pem":"synthetic-cert","key_pem":"synthetic-key"}`, "certificate:routed:server:synthetic-cert:synthetic-key"},
		{"PUT", "/api/v1/routers/routed", `{}`, "router:routed"},
		{"PUT", "/api/v1/services/routed", `{}`, "service:routed"},
		{"PUT", "/api/v1/middlewares/routed", `{"spec":{"nullable":null}}`, "middleware:routed"},
		{"PUT", "/api/v1/chains/routed", `{}`, "chain:routed"},
		{"POST", "/api/v1/plugins/yaml", `{"short_name":"example","raw_yaml":"synthetic"}`, "plugin:example"},
		{"POST", "/api/v1/plugins/demo/update", `{"version":"v1.2.0"}`, "plugin-update:demo"},
		{"POST", "/api/v1/crowdsec/decisions", `{"ip":"192.0.2.2","duration":"1h","reason":"test"}`, "decision:192.0.2.2:1h:test"},
		{"DELETE", "/api/v1/crowdsec/decisions/192.0.2.0/24", "", "delete-decision:192.0.2.0/24"},
		{"GET", "/api/v1/snapshots", "", "snapshots"},
		{"GET", "/api/v1/operations", "", "operations"},
		{"POST", "/api/v1/snapshots", `{"name":"before-upgrade"}`, "snapshot-create:before-upgrade"},
		{"DELETE", "/api/v1/snapshots/12?force=true", "", "snapshot-delete:12:true"},
		{"POST", "/api/v1/snapshots/12/restore", `{"restore_token":"tok"}`, "restore:tok"},
		{"POST", "/api/v1/operations/7/recover", "", "recover:7"},
		{"POST", "/api/v1/operations/7/abandon", "", "abandon:7"},
		{"GET", "/api/v1/config/state", "", "state"},
		{"POST", "/api/v1/config/adopt", "", "adopt"},
		{"POST", "/api/v1/traefik/restart", "", "restart"},
		{"GET", "/api/v1/traefik/container", "", "container"},
		{"GET", "/api/v1/entrypoints", "", "static"},
		{"GET", "/api/v1/entrypoints/web", "", "entrypoint:web"},
		{"PUT", "/api/v1/entrypoints/web", `{"port":80}`, "entrypoint-upsert:web"},
		{"DELETE", "/api/v1/entrypoints/web", "", "entrypoint-delete:web"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			calls.names = nil
			r := httptest.NewRequest(tc.method, "http://localhost"+tc.path, strings.NewReader(tc.body))
			if tc.method == "PUT" || tc.method == "DELETE" && (strings.Contains(tc.path, "/resolvers/") || strings.Contains(tc.path, "/entrypoints/")) || strings.HasSuffix(tc.path, "/update") {
				r.Header.Set("If-Match", `"3"`)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 422 || strings.Join(calls.names, ",") != tc.want {
				t.Fatal(w.Code, calls.names, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "synthetic-key") {
				t.Fatal("request material leaked")
			}
		})
	}
}
func TestWritePreconditionPolicies(t *testing.T) {
	calls := &routeCalls{}
	h, err := NewServer(routeDependencies(t, calls), middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		ifMatch, ifNone string
		want            int
	}{
		{"missing", "", "", 428},
		{"quoted version", `"3"`, "", 422},
		{"bare version", "3", "", 422},
		{"create", "", "*", 422},
		{"zero version", `"0"`, "", 400},
		{"weak validator", `W/"3"`, "", 400},
		{"star in If-Match", "*", "", 400},
		{"non numeric", `"abc"`, "", 400},
		{"both headers", `"3"`, "*", 400},
		{"If-None-Match other than star", "", `"3"`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls.names = nil
			r := httptest.NewRequest("PUT", "http://localhost/api/v1/domains/test", strings.NewReader(`{}`))
			if tc.ifMatch != "" {
				r.Header.Set("If-Match", tc.ifMatch)
			}
			if tc.ifNone != "" {
				r.Header.Set("If-None-Match", tc.ifNone)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || (len(calls.names) > 0) != (tc.want == 422) {
				t.Fatal(w.Code, calls.names, w.Body.String())
			}
		})
	}
	r := httptest.NewRequest("PUT", "http://localhost/api/v1/domains/test", strings.NewReader(`{}`))
	r.Header.Add("If-Match", `"3"`)
	r.Header.Add("If-Match", `"3"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("multiple If-Match headers accepted")
	}
}

func TestDryRunQueryReachesTheManager(t *testing.T) {
	calls := &routeCalls{}
	h, _ := NewServer(routeDependencies(t, calls), middleware.Policy{})
	for _, tc := range []struct {
		query string
		want  bool
	}{{"", false}, {"?dry_run=true", true}, {"?dry_run=false", false}} {
		calls.opts = models.WriteOptions{}
		r := httptest.NewRequest("PUT", "http://localhost/api/v1/domains/test"+tc.query, strings.NewReader(`{}`))
		r.Header.Set("If-None-Match", "*")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if calls.opts.DryRun != tc.want {
			t.Fatal(tc.query, calls.opts)
		}
	}
}

func TestRoutingPoliciesAndRemovedDraftEndpoints(t *testing.T) {
	calls := &routeCalls{}
	h, err := NewServer(routeDependencies(t, calls), middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"PUT", "/api/v1/missing", 404}, {"PUT", "/api/v1/health", 405}, {"GET", "/api/v1/health", 200},
		{"GET", "/api/v1/draft/review", 410}, {"POST", "/api/v1/draft/apply", 410}, {"DELETE", "/api/v1/draft", 410},
		{"POST", "/api/v1/snapshots/12/rollback", 410}, {"GET", "/api/v1/snapshots/12/review", 410},
		{"GET", "/api/v1/snapshots/12/restore-preview", 200},
		{"POST", "/api/v1/snapshots/invalid/restore", 422}, {"GET", "/api/v1/snapshots?limit=0", 422}, {"GET", "/api/v1/snapshots?limit=101", 422}, {"GET", "/api/v1/snapshots?cursor=-1", 422},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, "http://localhost"+tc.path, strings.NewReader(`{}`)))
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	// Query length is characters, not UTF-8 byte count.
	for _, tc := range []struct {
		query string
		want  int
	}{{strings.Repeat("é", 256), 422}, {strings.Repeat("x", 257), 422}} {
		calls.names = nil
		r := httptest.NewRequest("GET", "http://localhost/api/v1/plugins/catalog?q="+tc.query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		delegated := len(calls.names) > 0
		if w.Code != tc.want || delegated != (len([]rune(tc.query)) == 256) {
			t.Fatal("catalog bound", w.Code, calls.names)
		}
	}
}

type restartRequiredServices struct {
	services.ServiceManager
	dry bool
}

func (f restartRequiredServices) UpsertService(context.Context, string, models.ServiceConfig, models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	if f.dry {
		return &models.WriteResult[models.ServiceConfig]{Preview: &models.OperationPreview{RequiresRestart: true}}, nil
	}
	return &models.WriteResult[models.ServiceConfig]{Operation: &models.OperationRow{Status: models.OpStatusRestartRequired}, RestartRequired: true}, nil
}

func TestManualRestartIsReportedAsAccepted(t *testing.T) {
	d := routeDependencies(t, &routeCalls{})
	d.Services = restartRequiredServices{}
	h, _ := NewServer(d, middleware.Policy{})
	r := httptest.NewRequest("PUT", "http://localhost/api/v1/services/app", strings.NewReader(`{}`))
	r.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || !strings.Contains(w.Body.String(), "ERR_MANUAL_RESTART_REQUIRED") || !strings.Contains(w.Body.String(), `"restart_required":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A dry run that needs a restart is still a plain 200 preview.
	d.Services = restartRequiredServices{dry: true}
	h, _ = NewServer(d, middleware.Policy{})
	r = httptest.NewRequest("PUT", "http://localhost/api/v1/services/app?dry_run=true", strings.NewReader(`{}`))
	r.Header.Set("If-None-Match", "*")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"requires_restart":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMissingDependenciesFailAtConstruction(t *testing.T) {
	if _, err := NewServer(Dependencies{}, middleware.Policy{}); err == nil || !strings.Contains(err.Error(), "History") {
		t.Fatal("missing dependency accepted", err)
	}
	calls := &routeCalls{}
	d := routeDependencies(t, calls)
	d.ReadStaticConfig = nil
	if _, err := NewServer(d, middleware.Policy{}); err == nil || !strings.Contains(err.Error(), "ReadStaticConfig") {
		t.Fatal("missing reader accepted", err)
	}
}
func TestConnectedPeerCannotBeBannedAndForwardedHeadersDoNotOverride(t *testing.T) {
	calls := &routeCalls{}
	h, err := NewServer(routeDependencies(t, calls), middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/v1/crowdsec/decisions", strings.NewReader(`{"ip":"192.0.2.0/24"}`))
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 422 || len(calls.names) != 0 {
		t.Fatal("peer protection bypassed")
	}
}
