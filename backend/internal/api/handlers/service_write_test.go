package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

/* ------------------------------------------------------------------ a recording manager */

type upsertCall struct {
	name string
	cfg  models.ServiceConfig
	opts models.WriteOptions
}

type recordingServices struct {
	services.ServiceManager
	upserts []upsertCall
	deletes []upsertCall
	reads   []string // "get:<protocol>:<name>" and "diagnose:<protocol>:<name>"
	result  *models.WriteResult[models.ServiceConfig]
	err     error
}

func (r *recordingServices) UpsertService(_ context.Context, name string, cfg models.ServiceConfig, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	r.upserts = append(r.upserts, upsertCall{name, cfg, opts})
	return r.answer(name)
}

func (r *recordingServices) DeleteService(_ context.Context, name string, opts models.WriteOptions) (*models.WriteResult[models.ServiceConfig], error) {
	r.deletes = append(r.deletes, upsertCall{name: name, opts: opts})
	return r.answer(name)
}

func (r *recordingServices) answer(name string) (*models.WriteResult[models.ServiceConfig], error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.result != nil {
		return r.result, nil
	}
	return &models.WriteResult[models.ServiceConfig]{Resource: &models.UIResource[models.ServiceConfig]{ID: "service:" + name, Name: name, Origin: "managed", Version: 1}}, nil
}

func (r *recordingServices) GetService(_ context.Context, name, protocol string) (*models.UIResource[models.ServiceConfig], error) {
	r.reads = append(r.reads, "get:"+protocol+":"+name)
	if r.err != nil {
		return nil, r.err
	}
	return &models.UIResource[models.ServiceConfig]{Name: name}, nil
}

func (r *recordingServices) DiagnoseService(_ context.Context, name, protocol string) (status.ServiceDiagnostic, error) {
	r.reads = append(r.reads, "diagnose:"+protocol+":"+name)
	return status.ServiceDiagnostic{Name: name, Protocol: protocol}, r.err
}

func serviceServer(t *testing.T, svc services.ServiceManager) http.Handler {
	t.Helper()
	deps := routeDependencies(t, &routeCalls{})
	deps.Services = svc
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// call sends one request; headers are name, value pairs.
func call(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type apiError struct {
	Code        string            `json:"code"`
	FieldErrors map[string]string `json:"field_errors"`
	OperationID string            `json:"operation_id"`
}

func errorOf(t *testing.T, w *httptest.ResponseRecorder) apiError {
	t.Helper()
	var e apiError
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an API error: %s", w.Body.String())
	}
	return e
}

var create = []string{"If-None-Match", "*"}

func edit(version string) []string { return []string{"If-Match", `"` + version + `"`} }

/* ------------------------------------------------------------------ request contract */

func TestServiceBodiesReachTheManagerAsTheDialogsSendThem(t *testing.T) {
	no := false
	cases := map[string]struct {
		body string
		want models.ServiceConfig
	}{
		"http": {
			`{"protocol":"http","servers":[{"url":"http://a:80"},{"url":"https://b:443/x"}],"pass_host_header":false,"insecure_skip_verify":true,"health_check":{"path":"/up","interval":"10s","timeout":"3s"}}`,
			models.ServiceConfig{Protocol: "http", Servers: []models.ServerEndpoint{{URL: "http://a:80"}, {URL: "https://b:443/x"}}, PassHostHeader: &no, InsecureSkipVerify: true, HealthCheck: &models.ServiceHealth{Path: "/up", Interval: "10s", Timeout: "3s"}},
		},
		"http without a protocol": {
			`{"servers":[{"url":"http://a"}]}`,
			models.ServiceConfig{Servers: []models.ServerEndpoint{{URL: "http://a"}}},
		},
		"tcp with every option": {
			`{"protocol":"tcp","servers":[{"address":"db:5432"},{"address":"[::1]:5432"}],"insecure_skip_verify":true,"tcp":{"tls":true,"server_name":"db.example","proxy_protocol":2,"dial_timeout":"5s","termination_delay":"1s","health_check":{"interval":"10s","timeout":"2s","port":8008,"send":"PING","expect":"PONG"}}}`,
			models.ServiceConfig{Protocol: "tcp", Servers: []models.ServerEndpoint{{Address: "db:5432"}, {Address: "[::1]:5432"}}, InsecureSkipVerify: true,
				TCP: &models.TCPServiceOptions{TLS: true, ServerName: "db.example", ProxyProtocol: 2, DialTimeout: "5s", TerminationDelay: "1s",
					HealthCheck: &models.TCPHealthCheck{Interval: "10s", Timeout: "2s", Port: 8008, Send: "PING", Expect: "PONG"}}},
		},
		"plain tcp":  {`{"protocol":"tcp","servers":[{"address":"db:5432"}]}`, models.ServiceConfig{Protocol: "tcp", Servers: []models.ServerEndpoint{{Address: "db:5432"}}}},
		"udp":        {`{"protocol":"udp","servers":[{"address":"dns:53"}]}`, models.ServiceConfig{Protocol: "udp", Servers: []models.ServerEndpoint{{Address: "dns:53"}}}},
		"usage sent": {`{"protocol":"udp","servers":[{"address":"dns:53"}],"used_by":{"routers":[{"name":"x","tls":false,"read_only":false}]}}`, models.ServiceConfig{Protocol: "udp", Servers: []models.ServerEndpoint{{Address: "dns:53"}}, UsedBy: &models.ServiceUsage{Routers: []models.RouterRef{{Name: "x"}}}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &recordingServices{}
			w := call(serviceServer(t, rec), "PUT", "/api/v1/services/svc", c.body, create...)
			if w.Code != 200 || len(rec.upserts) != 1 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			got := rec.upserts[0]
			if got.name != "svc" || got.opts.DryRun || !reflect.DeepEqual(got.cfg, c.want) {
				t.Fatalf("%+v\nwant %+v", got.cfg, c.want)
			}
			var out models.WriteResult[models.ServiceConfig]
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Resource == nil || out.Resource.Name != "svc" {
				t.Fatalf("%s", w.Body.String())
			}
		})
	}
}

func TestTheServiceNameComesFromThePathAndIsDecoded(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/services/my-app":         "my-app",
		"/api/v1/services/api%40internal": "api@internal",
		"/api/v1/services/with%20space":   "with space",
	} {
		rec := &recordingServices{}
		h := serviceServer(t, rec)
		call(h, "PUT", path, `{"servers":[{"url":"http://a"}]}`, create...)
		call(h, "DELETE", path, "", edit("1")...)
		if len(rec.upserts) != 1 || rec.upserts[0].name != want || len(rec.deletes) != 1 || rec.deletes[0].name != want {
			t.Fatalf("%s: %+v %+v", path, rec.upserts, rec.deletes)
		}
	}
}

func TestDryRunIsOnlyTheExplicitQueryFlag(t *testing.T) {
	for query, want := range map[string]bool{"": false, "?dry_run=true": true, "?dry_run=1": true, "?dry_run=false": false, "?dry_run=0": false, "?dry_run=yes": false, "?dry_run=TRUE": false, "?dry_run=": false, "?other=true": false} {
		rec := &recordingServices{}
		h := serviceServer(t, rec)
		call(h, "PUT", "/api/v1/services/svc"+query, `{"servers":[{"url":"http://a"}]}`, create...)
		call(h, "DELETE", "/api/v1/services/svc"+query, "", edit("1")...)
		if rec.upserts[0].opts.DryRun != want || rec.deletes[0].opts.DryRun != want {
			t.Errorf("%q: upsert %v delete %v, want %v", query, rec.upserts[0].opts.DryRun, rec.deletes[0].opts.DryRun, want)
		}
	}
}

func TestEveryServiceWriteNeedsAVersionPrecondition(t *testing.T) {
	body := `{"servers":[{"url":"http://a"}]}`
	cases := []struct {
		name    string
		headers []string
		want    int
	}{
		{"missing", nil, 428},
		{"blank If-Match", []string{"If-Match", " "}, 428},
		{"create", create, 200},
		{"quoted version", edit("3"), 200},
		{"bare version", []string{"If-Match", "3"}, 200},
		{"zero version", edit("0"), 400},
		{"negative version", edit("-1"), 400},
		{"weak validator", []string{"If-Match", `W/"3"`}, 400},
		{"star in If-Match", []string{"If-Match", "*"}, 400},
		{"not a number", edit("abc"), 400},
		{"a decimal", edit("1.5"), 400},
		{"two If-Match headers", []string{"If-Match", `"1"`, "If-Match", `"2"`}, 400},
		{"both headers", []string{"If-Match", `"3"`, "If-None-Match", "*"}, 400},
		{"If-None-Match other than star", []string{"If-None-Match", `"3"`}, 400},
	}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, c := range cases {
			t.Run(method+" "+c.name, func(t *testing.T) {
				rec := &recordingServices{}
				payload := body
				if method == "DELETE" {
					payload = ""
				}
				w := call(serviceServer(t, rec), method, "/api/v1/services/svc", payload, c.headers...)
				if w.Code != c.want {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				if called := len(rec.upserts) + len(rec.deletes); (c.want == 200) != (called == 1) {
					t.Fatalf("the manager was called %d times", called)
				}
				if c.want == 428 && errorOf(t, w).Code != "ERR_PRECONDITION_REQUIRED" {
					t.Fatal(w.Body.String())
				}
			})
		}
	}
}

func TestMalformedServiceBodiesAreRefusedBeforeTheManager(t *testing.T) {
	bad := map[string]string{
		"empty":                  ``,
		"blank":                  `   `,
		"an array":               `[]`,
		"a string":               `"x"`,
		"not json":               `{servers: nope`,
		"truncated":              `{"servers":[{"url":"http://a"`,
		"trailing data":          `{"servers":[{"url":"http://a"}]} {}`,
		"an unknown field":       `{"servers":[{"url":"http://a"}],"weight":3}`,
		"an unknown tcp field":   `{"protocol":"tcp","servers":[{"address":"a:1"}],"tcp":{"keepalive":true}}`,
		"an unknown server key":  `{"servers":[{"host":"a","port":1}]}`,
		"a null servers":         `{"servers":null}`,
		"a null tcp":             `{"protocol":"tcp","servers":[{"address":"a:1"}],"tcp":null}`,
		"a duplicate key":        `{"servers":[{"url":"http://a"}],"servers":[{"url":"http://b"}]}`,
		"servers as an object":   `{"servers":{"url":"http://a"}}`,
		"a numeric url":          `{"servers":[{"url":80}]}`,
		"a string port count":    `{"protocol":"tcp","servers":[{"address":"a:1"}],"tcp":{"proxy_protocol":"2"}}`,
		"a boolean for a string": `{"protocol":"tcp","servers":[{"address":"a:1"}],"tcp":{"server_name":true}}`,
		"invalid utf-8":          "{\"servers\":[{\"url\":\"http://a\xff\"}]}",
		"too deep":               `{"servers":` + strings.Repeat("[", 40) + strings.Repeat("]", 40) + `}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			rec := &recordingServices{}
			w := call(serviceServer(t, rec), "PUT", "/api/v1/services/svc", body, create...)
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if len(rec.upserts) != 0 {
				t.Fatal("a malformed body reached the manager")
			}
			if errorOf(t, w).Code != "BAD_REQUEST" {
				t.Fatal(w.Body.String())
			}
		})
	}
	t.Run("a body over 1 MiB", func(t *testing.T) {
		rec := &recordingServices{}
		huge := `{"servers":[{"url":"http://a/` + strings.Repeat("x", 1<<20) + `"}]}`
		if w := call(serviceServer(t, rec), "PUT", "/api/v1/services/svc", huge, create...); w.Code != 413 || len(rec.upserts) != 0 {
			t.Fatal(w.Code)
		}
	})
}

func TestOnlyPutAndDeleteChangeAService(t *testing.T) {
	rec := &recordingServices{}
	h := serviceServer(t, rec)
	for _, method := range []string{"POST", "PATCH"} {
		if w := call(h, method, "/api/v1/services/svc", `{"servers":[{"url":"http://a"}]}`, create...); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, w.Code)
		}
	}
	if w := call(h, "PUT", "/api/v1/services", `{}`, create...); w.Code == 200 {
		t.Error("a PUT without a name was accepted")
	}
	if len(rec.upserts)+len(rec.deletes) != 0 {
		t.Fatal("reached the manager")
	}
}

/* ------------------------------------------------------------------ response contract */

func TestServiceWriteErrorsMapToStableStatusesAndCodes(t *testing.T) {
	rolledBack := &engine.DeployFailure{OperationID: 42, Stage: "runtime_verification", ReasonCode: "RUNTIME_REJECTED", RolledBack: true}
	notRolledBack := &engine.DeployFailure{OperationID: 43, Stage: "runtime_verification", ReasonCode: "RUNTIME_REJECTED"}
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid name", services.ErrServiceInvalidName, 422, "ERR_VALIDATION_FAILED"},
		{"no servers", services.ErrServiceNoServers, 422, "ERR_VALIDATION_FAILED"},
		{"invalid url", services.ErrServiceInvalidURL, 422, "ERR_VALIDATION_FAILED"},
		{"invalid address", services.ErrServiceInvalidAddress, 422, "ERR_VALIDATION_FAILED"},
		{"invalid protocol", services.ErrServiceInvalidProtocol, 422, "ERR_VALIDATION_FAILED"},
		{"invalid option", services.ErrServiceInvalidOption, 422, "ERR_VALIDATION_FAILED"},
		{"wrapped detail", errors.Join(services.ErrServiceInvalidAddress, errors.New("server[1] 'db'")), 422, "ERR_VALIDATION_FAILED"},
		{"failed pre-flight", &engine.ValidationFailure{Errors: []models.ValidationError{{ItemID: "router:shop", Field: "service", Message: "unknown service"}}}, 422, "ERR_VALIDATION_FAILED"},
		{"in use", services.ErrServiceInUse, 409, "ERR_CONFLICT"},
		{"version conflict", engine.ErrVersionConflict, 409, "ERR_VERSION_CONFLICT"},
		{"missing precondition", engine.ErrPreconditionRequired, 428, "ERR_PRECONDITION_REQUIRED"},
		{"read-only", files.ErrCannotModifyReadOnly, 403, "ERR_READ_ONLY"},
		{"unknown", files.ErrSourceFileNotFound, 404, "ERR_NOT_FOUND"},
		{"hand-written file in the way", files.ErrResourceConflict, 409, "ERR_CONFLICT"},
		{"another change in progress", engine.ErrDeployInProgress, 409, "ERR_CONFLICT"},
		{"recovery pending", engine.ErrOperationRecoveryRequired, 423, "ERR_OPERATION_RECOVERY_REQUIRED"},
		{"rolled back", rolledBack, 422, "ERR_DEPLOY_ROLLED_BACK"},
		{"rollback not confirmed", notRolledBack, 409, "ERR_RECOVERY_REQUIRED"},
		{"traefik unreachable", status.ErrObservationUnavailable, 502, "ERR_TRAEFIK_UNAVAILABLE"},
		{"something unexpected", errors.New("disk exploded: /secret/path"), 500, "ERR_INTERNAL"},
	}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, c := range cases {
			t.Run(method+" "+c.name, func(t *testing.T) {
				rec := &recordingServices{err: c.err}
				body, headers := `{"servers":[{"url":"http://a"}]}`, create
				if method == "DELETE" {
					body, headers = "", edit("1")
				}
				w := call(serviceServer(t, rec), method, "/api/v1/services/svc", body, headers...)
				got := errorOf(t, w)
				if w.Code != c.status || got.Code != c.code {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				if got.OperationID == "" {
					t.Fatal("an error must carry an operation id for support")
				}
				if strings.Contains(w.Body.String(), "/secret/path") {
					t.Fatalf("an internal detail leaked: %s", w.Body.String())
				}
			})
		}
	}
	t.Run("a rollback names the operation and stage", func(t *testing.T) {
		rec := &recordingServices{err: notRolledBack}
		got := errorOf(t, call(serviceServer(t, rec), "PUT", "/api/v1/services/svc", `{"servers":[{"url":"http://a"}]}`, create...))
		if got.FieldErrors["operation_id"] != "43" || got.FieldErrors["stage"] != "runtime_verification" || got.FieldErrors["reason_code"] != "RUNTIME_REJECTED" {
			t.Fatalf("%+v", got.FieldErrors)
		}
	})
}

func TestASavedChangeThatNeedsARestartIsAcceptedNotFailed(t *testing.T) {
	result := &models.WriteResult[models.ServiceConfig]{Resource: &models.UIResource[models.ServiceConfig]{Name: "svc"}, RestartRequired: true}
	for _, method := range []string{"PUT", "DELETE"} {
		rec := &recordingServices{result: result}
		body, headers := `{"servers":[{"url":"http://a"}]}`, create
		if method == "DELETE" {
			body, headers = "", edit("1")
		}
		w := call(serviceServer(t, rec), method, "/api/v1/services/svc", body, headers...)
		var out struct {
			Resource *models.UIResource[models.ServiceConfig] `json:"resource"`
			Error    struct{ Code string }                    `json:"error"`
		}
		if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Error.Code != "ERR_MANUAL_RESTART_REQUIRED" || out.Resource == nil {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
	}
	t.Run("but a preview never is", func(t *testing.T) {
		rec := &recordingServices{result: &models.WriteResult[models.ServiceConfig]{Preview: &models.OperationPreview{}, RestartRequired: true}}
		if w := call(serviceServer(t, rec), "PUT", "/api/v1/services/svc?dry_run=true", `{"servers":[{"url":"http://a"}]}`, create...); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	})
}

func TestReadingAServiceNarrowsByProtocol(t *testing.T) {
	rec := &recordingServices{}
	h := serviceServer(t, rec)
	for path, want := range map[string]string{
		"/api/v1/services/db":                          "get::db",
		"/api/v1/services/db?protocol=tcp":             "get:tcp:db",
		"/api/v1/services/dns?protocol=udp":            "get:udp:dns",
		"/api/v1/services/db/diagnostics":              "diagnose::db",
		"/api/v1/services/db/diagnostics?protocol=tcp": "diagnose:tcp:db",
		"/api/v1/services/api%40internal/diagnostics":  "diagnose::api@internal",
	} {
		rec.reads = nil
		if w := call(h, "GET", path, ""); w.Code != 200 || len(rec.reads) != 1 || rec.reads[0] != want {
			t.Errorf("%s: %d %v, want %s", path, w.Code, rec.reads, want)
		}
	}
}

func TestReadErrorsAreMapped(t *testing.T) {
	for name, c := range map[string]struct {
		err    error
		status int
	}{
		"not found":           {files.ErrSourceFileNotFound, 404},
		"never observed":      {status.ErrServiceNotObserved, 404},
		"bad name":            {status.ErrInvalidServiceName, 422},
		"traefik unreachable": {status.ErrObservationUnavailable, 502},
	} {
		t.Run(name, func(t *testing.T) {
			h := serviceServer(t, &recordingServices{err: c.err})
			for _, path := range []string{"/api/v1/services/x", "/api/v1/services/x/diagnostics"} {
				if w := call(h, "GET", path, ""); w.Code != c.status {
					t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
				}
			}
		})
	}
}

/* ------------------------------------------------------------------ the whole stack: handlers, manager, store */

type liveRuntime struct{ snapshot status.RuntimeSnapshot }

func (r liveRuntime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	s := r.snapshot
	return &s, nil
}
func (r liveRuntime) DiagnoseNamedService(_ context.Context, protocol, name string) (status.ServiceDiagnostic, error) {
	return status.ServiceDiagnostic{Protocol: protocol, Name: name, Observed: true}, nil
}

func stack(t *testing.T, rt liveRuntime) (http.Handler, db.Store) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	database, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	store := db.NewStore(database, make([]byte, 32))
	deps := routeDependencies(t, &routeCalls{})
	deps.Services = services.NewServiceManager(store, rt, &enginetest.DirectApplier{Store: store})
	deps.Routers = listedRouters{}
	h, err := NewServer(deps, middleware.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	return h, store
}

func listServicesVia(t *testing.T, h http.Handler) map[string]models.UIResource[models.ServiceConfig] {
	t.Helper()
	w := call(h, "GET", "/api/v1/services", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var items []models.UIResource[models.ServiceConfig]
	if e := json.Unmarshal(w.Body.Bytes(), &items); e != nil {
		t.Fatal(e)
	}
	out := map[string]models.UIResource[models.ServiceConfig]{}
	for _, it := range items {
		out[it.Name] = it
	}
	return out
}

func TestCreateEditAndDeleteEveryProtocolOverHTTP(t *testing.T) {
	scenarios := map[string]struct {
		create, update string
		check          func(*testing.T, models.ServiceConfig)
	}{
		"web": {
			`{"servers":[{"url":"http://a:80"}],"health_check":{"path":"/up","interval":"10s"}}`,
			`{"protocol":"http","servers":[{"url":"https://b:443/x"},{"url":"http://c:81"}],"pass_host_header":false,"insecure_skip_verify":true}`,
			func(t *testing.T, c models.ServiceConfig) {
				if c.Protocol != "http" || len(c.Servers) != 2 || c.Servers[0].URL != "https://b:443/x" || c.PassHostHeader == nil || *c.PassHostHeader || !c.InsecureSkipVerify || c.HealthCheck != nil {
					t.Fatalf("%+v", c)
				}
			},
		},
		"db": {
			`{"protocol":"tcp","servers":[{"address":"db:5432"}],"insecure_skip_verify":true,"tcp":{"tls":true,"server_name":"db.example","proxy_protocol":2,"health_check":{"interval":"10s","port":8008}}}`,
			`{"protocol":"tcp","servers":[{"address":"db2:5433"},{"address":"[::1]:5434"}],"tcp":{"dial_timeout":"3s"}}`,
			func(t *testing.T, c models.ServiceConfig) {
				if c.Protocol != "tcp" || len(c.Servers) != 2 || c.Servers[1].Address != "[::1]:5434" || c.TCP == nil || c.TCP.DialTimeout != "3s" || c.TCP.TLS || c.TCP.HealthCheck != nil || c.InsecureSkipVerify {
					t.Fatalf("%+v %+v", c, c.TCP)
				}
			},
		},
		"dns": {
			`{"protocol":"udp","servers":[{"address":"dns:53"}]}`,
			`{"protocol":"udp","servers":[{"address":"dns2:53"},{"address":"dns3:5353"}]}`,
			func(t *testing.T, c models.ServiceConfig) {
				if c.Protocol != "udp" || len(c.Servers) != 2 || c.Servers[1].Address != "dns3:5353" || c.TCP != nil {
					t.Fatalf("%+v", c)
				}
			},
		},
	}
	h, _ := stack(t, liveRuntime{})
	for name, s := range scenarios {
		t.Run(name, func(t *testing.T) {
			path := "/api/v1/services/" + name
			if w := call(h, "PUT", path, s.create, create...); w.Code != 200 {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			got := listServicesVia(t, h)[name]
			if got.Version != 1 || got.Origin != "managed" || got.Config.UsedBy == nil || len(got.Config.UsedBy.Routers) != 0 {
				t.Fatalf("%+v", got)
			}

			if w := call(h, "PUT", path, s.update, edit("1")...); w.Code != 200 {
				t.Fatalf("edit: %d %s", w.Code, w.Body.String())
			}
			got = listServicesVia(t, h)[name]
			s.check(t, got.Config)

			// What the dialog reads back for one service is what the list shows.
			w := call(h, "GET", path+"?protocol="+got.Config.Protocol, "")
			var one models.UIResource[models.ServiceConfig]
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &one) != nil || one.Name != name {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			s.check(t, one.Config)

			if w := call(h, "DELETE", path, "", edit("2")...); w.Code != 200 {
				t.Fatalf("delete: %d %s", w.Code, w.Body.String())
			}
			if _, still := listServicesVia(t, h)[name]; still {
				t.Fatal("still listed after the delete")
			}
			if w := call(h, "GET", path, ""); w.Code != 404 {
				t.Fatalf("a deleted service answered %d", w.Code)
			}
			if w := call(h, "DELETE", path, "", edit("2")...); w.Code != 404 {
				t.Fatalf("deleting it again answered %d", w.Code)
			}
		})
	}
}

func TestInvalidServicesAreRefusedWith422ForEveryProtocol(t *testing.T) {
	h, store := stack(t, liveRuntime{})
	bad := map[string]string{
		"http, no servers":               `{"servers":[]}`,
		"http, not a url":                `{"servers":[{"url":"app:8080"}]}`,
		"http, a tcp scheme":             `{"servers":[{"url":"tcp://a:1"}]}`,
		"http, an address":               `{"servers":[{"address":"a:1"}]}`,
		"http, a port out of range":      `{"servers":[{"url":"http://a:70000"}]}`,
		"http, a bad health path":        `{"servers":[{"url":"http://a"}],"health_check":{"path":"up"}}`,
		"http, a bad health interval":    `{"servers":[{"url":"http://a"}],"health_check":{"path":"/","interval":"soon"}}`,
		"http, tcp options":              `{"servers":[{"url":"http://a"}],"tcp":{"proxy_protocol":1}}`,
		"tcp, no servers":                `{"protocol":"tcp","servers":[]}`,
		"tcp, no port":                   `{"protocol":"tcp","servers":[{"address":"db"}]}`,
		"tcp, a url":                     `{"protocol":"tcp","servers":[{"url":"http://db:5432"}]}`,
		"tcp, a duplicate":               `{"protocol":"tcp","servers":[{"address":"db:1"},{"address":"db:1"}]}`,
		"tcp, a server name without tls": `{"protocol":"tcp","servers":[{"address":"db:1"}],"tcp":{"server_name":"x"}}`,
		"tcp, proxy protocol 3":          `{"protocol":"tcp","servers":[{"address":"db:1"}],"tcp":{"proxy_protocol":3}}`,
		"tcp, a bad dial timeout":        `{"protocol":"tcp","servers":[{"address":"db:1"}],"tcp":{"dial_timeout":"2h"}}`,
		"tcp, expect without send":       `{"protocol":"tcp","servers":[{"address":"db:1"}],"tcp":{"health_check":{"expect":"PONG"}}}`,
		"tcp, an http health check":      `{"protocol":"tcp","servers":[{"address":"db:1"}],"health_check":{"path":"/"}}`,
		"udp, no servers":                `{"protocol":"udp","servers":[]}`,
		"udp, no port":                   `{"protocol":"udp","servers":[{"address":"dns"}]}`,
		"udp, tcp options":               `{"protocol":"udp","servers":[{"address":"dns:53"}],"tcp":{"proxy_protocol":1}}`,
		"an unknown protocol":            `{"protocol":"sctp","servers":[{"address":"a:1"}]}`,
		"an upper-case protocol":         `{"protocol":"TCP","servers":[{"address":"a:1"}]}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			w := call(h, "PUT", "/api/v1/services/svc", body, create...)
			if w.Code != 422 || errorOf(t, w).Code != "ERR_VALIDATION_FAILED" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			// A dry run refuses it too.
			if w := call(h, "PUT", "/api/v1/services/svc?dry_run=true", body, create...); w.Code != 422 {
				t.Fatalf("preview: %d", w.Code)
			}
			if row, _ := store.GetItem(context.Background(), models.KindService, "svc"); row != nil {
				t.Fatal("an invalid service was stored")
			}
		})
	}
	for _, name := range []string{"App", "1app", "my.app", "-x", strings.Repeat("a", 64)} {
		if w := call(h, "PUT", "/api/v1/services/"+name, `{"servers":[{"url":"http://a"}]}`, create...); w.Code != 422 {
			t.Errorf("name %q: %d", name, w.Code)
		}
	}
}

func TestAServiceKeepsItsProtocolOverHTTP(t *testing.T) {
	h, _ := stack(t, liveRuntime{})
	call(h, "PUT", "/api/v1/services/db", `{"protocol":"tcp","servers":[{"address":"db:5432"}]}`, create...)
	call(h, "PUT", "/api/v1/services/web", `{"servers":[{"url":"http://a"}]}`, create...)
	for path, body := range map[string]string{
		"/api/v1/services/db":  `{"protocol":"udp","servers":[{"address":"db:5432"}]}`,
		"/api/v1/services/web": `{"protocol":"tcp","servers":[{"address":"a:80"}]}`,
	} {
		w := call(h, "PUT", path, body, edit("1")...)
		if w.Code != 422 || errorOf(t, w).Code != "ERR_VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	got := listServicesVia(t, h)
	if got["db"].Config.Protocol != "tcp" || got["web"].Config.Protocol != "http" || got["db"].Version != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestDeletingAServiceThatIsInUseIsRefusedOverHTTP(t *testing.T) {
	rt := liveRuntime{snapshot: status.RuntimeSnapshot{
		TCP: status.RuntimeResources{Routers: map[string]status.ExternalItem{"pg@docker": {Name: "pg", FullName: "pg@docker", Provider: "docker", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": "db@file", "entryPoints": []any{"postgres"}}}}},
		UDP: status.RuntimeResources{Routers: map[string]status.ExternalItem{"dnsr@docker": {Name: "dnsr", FullName: "dnsr@docker", Provider: "docker", Raw: map[string]any{"service": "dns@file"}}}},
	}}
	h, store := stack(t, rt)
	call(h, "PUT", "/api/v1/services/db", `{"protocol":"tcp","servers":[{"address":"db:5432"}]}`, create...)
	call(h, "PUT", "/api/v1/services/dns", `{"protocol":"udp","servers":[{"address":"dns:53"}]}`, create...)
	call(h, "PUT", "/api/v1/services/free", `{"protocol":"tcp","servers":[{"address":"free:1"}]}`, create...)
	if _, e := store.CommitOperation(context.Background(), db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{
		{ID: "service:web", Kind: models.KindService, Name: "web", Scope: models.ScopeDynamic, Payload: []byte(`{"servers":[{"url":"http://a"}]}`)},
		{ID: "router:shop", Kind: models.KindRouter, Name: "shop", Scope: models.ScopeDynamic, Payload: []byte(`{"rule":"Host(` + "`shop.example`" + `)","service":"web"}`)},
	}}}); e != nil {
		t.Fatal(e)
	}

	for _, name := range []string{"db", "dns", "web"} {
		w := call(h, "DELETE", "/api/v1/services/"+name, "", edit("1")...)
		if w.Code != 409 || errorOf(t, w).Code != "ERR_CONFLICT" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if row, _ := store.GetItem(context.Background(), models.KindService, name); row == nil {
			t.Errorf("%s was deleted", name)
		}
	}
	if w := call(h, "DELETE", "/api/v1/services/free", "", edit("1")...); w.Code != 200 {
		t.Fatalf("an unused service could not be deleted: %d %s", w.Code, w.Body.String())
	}
	// A delete preview is refused for the same reason.
	if w := call(h, "DELETE", "/api/v1/services/db?dry_run=true", "", edit("1")...); w.Code != 409 {
		t.Fatalf("preview: %d", w.Code)
	}
}

func TestServicesOfEveryProtocolAreListedTogetherWithTheirOwnRouters(t *testing.T) {
	rt := liveRuntime{snapshot: status.RuntimeSnapshot{
		TCP: status.RuntimeResources{Routers: map[string]status.ExternalItem{"pg@docker": {Name: "pg", FullName: "pg@docker", Provider: "docker", Raw: map[string]any{"rule": "HostSNI(`*`)", "service": "db@file"}}}},
	}}
	h, _ := stack(t, rt)
	call(h, "PUT", "/api/v1/services/db", `{"protocol":"tcp","servers":[{"address":"db:5432"}]}`, create...)
	call(h, "PUT", "/api/v1/services/dns", `{"protocol":"udp","servers":[{"address":"dns:53"}]}`, create...)
	call(h, "PUT", "/api/v1/services/web", `{"servers":[{"url":"http://a"}]}`, create...)
	got := listServicesVia(t, h)
	if len(got) != 3 || got["db"].Config.Protocol != "tcp" || got["dns"].Config.Protocol != "udp" || got["web"].Config.Protocol != "http" {
		t.Fatalf("%+v", got)
	}
	if u := got["db"].Config.UsedBy; u == nil || len(u.Routers) != 1 || u.Routers[0].Name != "pg@docker" || !u.Routers[0].ReadOnly {
		t.Fatalf("%+v", u)
	}
	for _, name := range []string{"dns", "web"} {
		if u := got[name].Config.UsedBy; u == nil || len(u.Routers) != 0 {
			t.Fatalf("%s: %+v", name, u)
		}
	}
	// Nothing about usage is ever stored: it is recomputed on every read.
	raw := call(h, "GET", "/api/v1/services", "").Body.String()
	if strings.Count(raw, `"used_by"`) != 3 {
		t.Fatalf("%s", raw)
	}
}
