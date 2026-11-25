package entrypoints

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
)

type staticFile string

func (s staticFile) ReadStaticConfig() ([]byte, error) { return []byte(s), nil }

const baseStatic = `entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
          permanent: true
  websecure:
    address: ":443"
    asDefault: true
    http3: {}
    http:
      tls:
        certResolver: le
  dns:
    address: ":53/udp"
  traefik:
    address: ":8080"
  legacy:
    address: ":9000"
    http:
      underscoreHeadersStrategy: delete
certificatesResolvers:
  le:
    acme:
      httpChallenge:
        entryPoint: web
`

func newManager(t *testing.T, static string) (EntryPointManager, db.Store) {
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
	return NewEntryPointManager(store, staticFile(static), &enginetest.DirectApplier{Store: store}), store
}

// fieldErrors returns the fields a validation failure names.
func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	failure, ok := errors.AsType[*engine.ValidationFailure](err)
	if !ok {
		t.Fatalf("not a validation failure: %v", err)
	}
	out := map[string]string{}
	for _, e := range failure.Errors {
		out[e.Field] = e.Message
	}
	return out
}

func TestRenderWritesOnlyWhatDiffersFromTraefikDefaults(t *testing.T) {
	in := models.EntryPointConfig{Name: "app", Port: 8443}
	if err := build(&in, references{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.RenderedNode, map[string]any{"address": ":8443"}) || in.Address != ":8443" || in.Protocol != "tcp" {
		t.Fatalf("%+v", in)
	}

	full := models.EntryPointConfig{
		Name: "edge", Host: "192.0.2.10", Port: 443, AsDefault: true, ReusePort: true, AllowACMEByPass: true,
		ForwardedHeaders: &models.EntryPointForwardedHeaders{TrustedIPs: []string{"10.0.0.0/8", "192.0.2.1"}, Connection: []string{"Upgrade"}, NotAppendXForwardedFor: true},
		ProxyProtocol:    &models.EntryPointProxyProtocol{TrustedIPs: []string{"10.0.0.0/8"}},
		Transport:        &models.EntryPointTransport{ReadTimeout: "30", IdleTimeout: "2m", GraceTimeout: "15s", KeepAliveMaxRequests: 100},
		HTTP: &models.EntryPointHTTP{
			Middlewares: []string{"secure@file"}, TLS: &models.EntryPointTLS{Options: "modern@file", CertResolver: "le", Domains: []models.EntryPointTLSDomain{{Main: "example.com", SANs: []string{"*.example.com"}}}},
			DeniedEncodedCharacters: []string{"slash", "nullCharacter"}, EncodeQuerySemicolons: true, MaxHeaderBytes: 2 << 20, AliasHeadersStrategy: "reject",
		},
		HTTP2:         &models.EntryPointHTTP2{MaxConcurrentStreams: 100},
		HTTP3:         &models.EntryPointHTTP3{AdvertisedPort: 8443},
		Observability: &models.EntryPointObservability{DisableAccessLogs: true, TraceVerbosity: "detailed"},
	}
	refs := references{Resolvers: map[string]bool{"le": true}, Middlewares: map[string]bool{"secure": true}}
	if err := build(&full, refs); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"address": "192.0.2.10:443", "asDefault": true, "reusePort": true, "allowACMEByPass": true,
		"forwardedHeaders": map[string]any{"trustedIPs": []string{"10.0.0.0/8", "192.0.2.1"}, "connection": []string{"Upgrade"}, "notAppendXForwardedFor": true},
		"proxyProtocol":    map[string]any{"trustedIPs": []string{"10.0.0.0/8"}},
		"transport": map[string]any{
			"respondingTimeouts": map[string]any{"readTimeout": "30s", "idleTimeout": "2m"},
			"lifeCycle":          map[string]any{"graceTimeOut": "15s"}, "keepAliveMaxRequests": 100,
		},
		"http": map[string]any{
			"middlewares":       []string{"secure@file"},
			"tls":               map[string]any{"options": "modern@file", "certResolver": "le", "domains": []map[string]any{{"main": "example.com", "sans": []string{"*.example.com"}}}},
			"encodedCharacters": map[string]any{"allowEncodedSlash": false, "allowEncodedNullCharacter": false}, "encodeQuerySemicolons": true,
			"maxHeaderBytes": 2 << 20, "aliasHeadersStrategy": "reject",
		},
		"http2": map[string]any{"maxConcurrentStreams": 100}, "http3": map[string]any{"advertisedPort": 8443},
		"observability": map[string]any{"accessLogs": false, "traceVerbosity": "detailed"},
	}
	if !reflect.DeepEqual(full.RenderedNode, want) {
		got, _ := json.MarshalIndent(full.RenderedNode, "", " ")
		t.Fatalf("rendered:\n%s", got)
	}
	// A middleware the console does not manage is allowed (it may live in another file) but flagged.
	other := models.EntryPointConfig{Name: "x", Port: 81, HTTP: &models.EntryPointHTTP{Middlewares: []string{"auth@docker", "missing@file"}, TLS: &models.EntryPointTLS{}}}
	if err := build(&other, references{}); err != nil {
		t.Fatal(err)
	}
	if tls, ok := other.RenderedNode["http"].(map[string]any)["tls"].(map[string]any); !ok || len(tls) != 0 {
		t.Fatal("bare TLS must render as an empty mapping, which turns TLS on", other.RenderedNode)
	}
	if joined := strings.Join(other.Warnings, "\n"); !strings.Contains(joined, "missing@file") || strings.Contains(joined, "auth@docker") {
		t.Fatal(other.Warnings)
	}
}

func TestOptionsThatDependOnEachOtherAreChecked(t *testing.T) {
	refs := references{
		Peers:     map[string]peer{"web": {Protocol: "tcp", Port: 80}, "dns": {Protocol: "udp", Port: 53}, "quic": {Protocol: "tcp", Port: 8443, HTTP3: true}, "local": {Protocol: "tcp", Host: "127.0.0.1", Port: 9000}},
		Resolvers: map[string]bool{"le": true},
	}
	cases := map[string]struct {
		in    models.EntryPointConfig
		field string
	}{
		"port out of range":             {models.EntryPointConfig{Port: 70000}, "port"},
		"port already taken":            {models.EntryPointConfig{Port: 80}, "port"},
		"same port on every interface":  {models.EntryPointConfig{Port: 9000}, "port"},
		"udp port taken by http3":       {models.EntryPointConfig{Protocol: "udp", Port: 8443}, "port"},
		"http3 needs the udp port":      {models.EntryPointConfig{Port: 53, HTTP3: &models.EntryPointHTTP3{}}, "http3"},
		"udp has no http":               {models.EntryPointConfig{Protocol: "udp", Port: 5000, HTTP: &models.EntryPointHTTP{EncodeQuerySemicolons: true}}, "http"},
		"udp is never a default":        {models.EntryPointConfig{Protocol: "udp", Port: 5000, AsDefault: true}, "as_default"},
		"udp has no http3":              {models.EntryPointConfig{Protocol: "udp", Port: 5000, HTTP3: &models.EntryPointHTTP3{}}, "http3"},
		"udp timeout must be positive":  {models.EntryPointConfig{Protocol: "udp", Port: 5000, UDP: &models.EntryPointUDP{Timeout: "0s"}}, "udp.timeout"},
		"tcp has no udp timeout":        {models.EntryPointConfig{Port: 5000, UDP: &models.EntryPointUDP{Timeout: "3s"}}, "udp.timeout"},
		"trust all excludes a list":     {models.EntryPointConfig{Port: 5000, ForwardedHeaders: &models.EntryPointForwardedHeaders{Insecure: true, TrustedIPs: []string{"10.0.0.1"}}}, "forwarded_headers.trusted_ips"},
		"trusted ip must be an address": {models.EntryPointConfig{Port: 5000, ProxyProtocol: &models.EntryPointProxyProtocol{TrustedIPs: []string{"lb.internal"}}}, "proxy_protocol.trusted_ips"},
		"bad duration":                  {models.EntryPointConfig{Port: 5000, Transport: &models.EntryPointTransport{ReadTimeout: "soon"}}, "transport.read_timeout"},
		"redirect to itself":            {models.EntryPointConfig{Name: "self", Port: 5000, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "self"}}}, "http.redirect.to"},
		"redirect to its own port":      {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: ":5000"}}}, "http.redirect.to"},
		"redirect to nothing":           {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "nope"}}}, "http.redirect.to"},
		"redirect to a udp entrypoint":  {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "dns"}}}, "http.redirect.to"},
		"middleware needs a provider":   {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{Middlewares: []string{"auth"}}}, "http.middlewares"},
		"unknown resolver":              {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{TLS: &models.EntryPointTLS{CertResolver: "gone"}}}, "http.tls.cert_resolver"},
		"domains need a resolver":       {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{TLS: &models.EntryPointTLS{Domains: []models.EntryPointTLSDomain{{Main: "example.com"}}}}}, "http.tls.domains"},
		"unknown encoded character":     {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{DeniedEncodedCharacters: []string{"tilde"}}}, "http.denied_encoded_characters"},
		"unknown strategy":              {models.EntryPointConfig{Port: 5000, HTTP: &models.EntryPointHTTP{AliasHeadersStrategy: "drop"}}, "http.alias_headers_strategy"},
		"unknown verbosity":             {models.EntryPointConfig{Port: 5000, Observability: &models.EntryPointObservability{TraceVerbosity: "loud"}}, "observability.trace_verbosity"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.in.Name == "" {
				tc.in.Name = "candidate"
			}
			err := build(&tc.in, refs)
			if err == nil {
				t.Fatal("accepted", tc.in.RenderedNode)
			}
			if got := fieldErrors(t, err); got[tc.field] == "" || len(got) != 1 {
				t.Fatalf("want only %s, got %v", tc.field, got)
			}
		})
	}
	// Allowed combinations: a specific interface next to another one, reusePort, a redirect to a port.
	for name, in := range map[string]models.EntryPointConfig{
		"other interface": {Name: "a", Host: "127.0.0.2", Port: 9000},
		"reuse port":      {Name: "a", Port: 80, ReusePort: true},
		"redirect port":   {Name: "a", Port: 5000, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: ":443", Scheme: "https", Temporary: true}}},
	} {
		if err := build(&in, refs); err != nil {
			t.Errorf("%s: %v", name, fieldErrors(t, err))
		}
	}
}

func TestDeclaredEntryPointsAreReadLosslesslyOrMarkedNotAdoptable(t *testing.T) {
	m, _ := newManager(t, baseStatic)
	items, err := m.ListEntryPoints(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]models.UIResource[models.EntryPointConfig]{}
	for _, item := range items {
		if item.Origin != "external" || !item.ReadOnly {
			t.Fatalf("%s must be read-only: %+v", item.Name, item)
		}
		got[item.Name] = item
	}
	web := got["web"].Config
	if web.Port != 80 || web.Protocol != "tcp" || web.HTTP == nil || web.HTTP.Redirect == nil || web.HTTP.Redirect.To != "websecure" || web.HTTP.Redirect.Scheme != "" || web.HTTP.Redirect.Temporary || !web.Adoptable {
		t.Fatalf("web: %+v", web)
	}
	// Used by the resolver's HTTP-01 challenge; websecure is the redirect target of web.
	if !reflect.DeepEqual(web.Usage.Resolvers, []string{"le"}) || !reflect.DeepEqual(got["websecure"].Config.Usage.EntryPoints, []string{"web"}) {
		t.Fatalf("usage: %+v / %+v", web.Usage, got["websecure"].Config.Usage)
	}
	secure := got["websecure"].Config
	if !secure.AsDefault || secure.HTTP3 == nil || secure.HTTP.TLS == nil || secure.HTTP.TLS.CertResolver != "le" || !secure.Adoptable {
		t.Fatalf("websecure: %+v", secure)
	}
	if dns := got["dns"].Config; dns.Protocol != "udp" || dns.Port != 53 || dns.Address != ":53/udp" {
		t.Fatalf("dns: %+v", dns)
	}
	if got["traefik"].Config.Adoptable || got["traefik"].Config.AdoptBlocker == "" {
		t.Fatal("the internal entrypoint must not be adoptable")
	}
	if legacy := got["legacy"].Config; legacy.Adoptable || !strings.Contains(legacy.AdoptBlocker, "http.underscoreHeadersStrategy") {
		t.Fatalf("legacy: %+v", legacy)
	}

	// Converting and rendering again yields the same Traefik meaning (defaults dropped).
	cfg := got["websecure"].Config
	if err := build(&cfg, references{Resolvers: map[string]bool{"le": true}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"address": ":443", "asDefault": true, "http3": map[string]any{}, "http": map[string]any{"tls": map[string]any{"certResolver": "le"}}}
	if !reflect.DeepEqual(cfg.RenderedNode, want) {
		t.Fatalf("%#v", cfg.RenderedNode)
	}
}

func TestCreateEditAndDeleteAManagedEntryPoint(t *testing.T) {
	ctx := context.Background()
	m, store := newManager(t, baseStatic)

	res, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "metrics", Port: 9100, Host: "127.0.0.1"}, models.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	created := res.Resource
	if created == nil || created.Origin != "managed" || created.ReadOnly || created.Config.Address != "127.0.0.1:9100" || created.Config.Adopted || created.Version != 1 {
		t.Fatalf("%+v", created)
	}
	if !strings.Contains(strings.Join(created.Config.Warnings, "\n"), "9100/tcp") {
		t.Fatal("a new port must remind the operator to publish it", created.Config.Warnings)
	}
	row, _ := store.GetItem(ctx, models.KindEntryPoint, "metrics")
	if row == nil || row.Scope != models.ScopeStatic {
		t.Fatalf("row: %+v", row)
	}

	// The protocol is fixed at creation; an unchanged port does not repeat the publish reminder.
	if _, err = m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "metrics", Port: 9100, Protocol: "udp"}, models.WriteOptions{}); !errors.Is(err, ErrEntryPointProtocol) {
		t.Fatal(err)
	}
	res, err = m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "metrics", Port: 9100, HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "websecure"}}}, models.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resource.Config.Warnings) != 0 {
		t.Fatal(res.Resource.Config.Warnings)
	}

	// A second entrypoint cannot take the same port, and websecure is now also used by metrics.
	if _, err = m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "dup", Port: 9100}, models.WriteOptions{}); fieldErrors(t, err)["port"] == "" {
		t.Fatal(err)
	}
	secure, _ := m.GetEntryPoint(ctx, "websecure")
	if !reflect.DeepEqual(secure.Config.Usage.EntryPoints, []string{"metrics", "web"}) {
		t.Fatalf("%+v", secure.Config.Usage)
	}

	// In use by a router: refused, naming the router.
	router, _ := json.Marshal(models.RouterConfig{EntryPoints: []string{"metrics"}})
	if _, err = store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{{ID: "router:app", Kind: models.KindRouter, Name: "app", Scope: models.ScopeDynamic, Payload: router}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.DeleteEntryPoint(ctx, "metrics", models.WriteOptions{}); !errors.Is(err, ErrEntryPointInUse) || !strings.Contains(err.Error(), "router 'app'") {
		t.Fatal(err)
	}
	if _, err = store.CommitOperation(ctx, db.Commit{Mutation: models.Mutation{Deletes: []models.ItemRef{{Kind: models.KindRouter, Name: "app"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.DeleteEntryPoint(ctx, "metrics", models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if row, _ = store.GetItem(ctx, models.KindEntryPoint, "metrics"); row != nil {
		t.Fatal("still stored")
	}
	// Declared-only and unknown names are not deletable.
	if _, err = m.DeleteEntryPoint(ctx, "web", models.WriteOptions{}); !errors.Is(err, files.ErrCannotModifyReadOnly) {
		t.Fatal(err)
	}
	if _, err = m.DeleteEntryPoint(ctx, "nope", models.WriteOptions{}); !errors.Is(err, files.ErrSourceFileNotFound) {
		t.Fatal(err)
	}
}

func TestAdoptingADeclaredEntryPointIsExplicitAndKeepsTheOriginal(t *testing.T) {
	ctx := context.Background()
	m, store := newManager(t, baseStatic)

	// Without the explicit flag a declared entrypoint stays read-only.
	if _, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "web", Port: 80}, models.WriteOptions{}); !errors.Is(err, files.ErrCannotModifyReadOnly) {
		t.Fatal(err)
	}
	for _, name := range []string{"traefik", "legacy"} {
		if _, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: name, Port: 9999, Adopted: true}, models.WriteOptions{}); !errors.Is(err, ErrEntryPointNotAdoptable) {
			t.Fatal(name, err)
		}
	}
	if _, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "dns", Port: 53, Protocol: "tcp", Adopted: true}, models.WriteOptions{}); !errors.Is(err, ErrEntryPointProtocol) {
		t.Fatal(err)
	}

	res, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "web", Port: 80, Adopted: true, AllowACMEByPass: true,
		HTTP: &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "websecure"}}}, models.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resource.Config.Adopted || res.Resource.Origin != "managed" || len(res.Resource.Config.Warnings) != 0 {
		t.Fatalf("%+v", res.Resource)
	}
	row, _ := store.GetItem(ctx, models.KindEntryPoint, "web")
	var stored models.EntryPointConfig
	if err = json.Unmarshal(row.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	redirect, _ := stored.Original["http"].(map[string]any)["redirections"].(map[string]any)["entryPoint"].(map[string]any)
	if stored.Original["address"] != ":80" || redirect["to"] != "websecure" || redirect["permanent"] != true {
		t.Fatalf("original definition lost: %#v", stored.Original)
	}

	// A later edit cannot drop the ownership record, and the caller cannot forge one on a new entrypoint.
	if _, err = m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "web", Port: 80}, models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	row, _ = store.GetItem(ctx, models.KindEntryPoint, "web")
	stored = models.EntryPointConfig{}
	_ = json.Unmarshal(row.Payload, &stored)
	if !stored.Adopted || stored.Original["address"] != ":80" {
		t.Fatalf("%+v", stored)
	}
	res, err = m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "fresh", Port: 7000, Adopted: true, Original: map[string]any{"address": ":1"}}, models.WriteOptions{})
	if err != nil || res.Resource.Config.Adopted {
		t.Fatal(err, res)
	}

	// Releasing is allowed although the resolver still uses it: the declaration comes back.
	if _, err = m.DeleteEntryPoint(ctx, "web", models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestTheLastEntryPointCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	m, _ := newManager(t, "")
	if _, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "only", Port: 80}, models.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteEntryPoint(ctx, "only", models.WriteOptions{}); !errors.Is(err, ErrEntryPointLast) {
		t.Fatal(err)
	}
	if _, err := m.UpsertEntryPoint(ctx, models.EntryPointConfig{Name: "Bad Name", Port: 81}, models.WriteOptions{}); !errors.Is(err, ErrEntryPointInvalidName) {
		t.Fatal(err)
	}
}

func TestTheTypeDecidesWhichOptionsApply(t *testing.T) {
	refs := references{Resolvers: map[string]bool{"le": true}}
	tls := &models.EntryPointHTTP{TLS: &models.EntryPointTLS{CertResolver: "le"}}
	rejected := map[string]struct {
		in    models.EntryPointConfig
		field string
	}{
		"unknown type":                {models.EntryPointConfig{Kind: "quic", Port: 5000}, "kind"},
		"http cannot listen on udp":   {models.EntryPointConfig{Kind: "http", Protocol: "udp", Port: 5000}, "protocol"},
		"https cannot listen on udp":  {models.EntryPointConfig{Kind: "https", Protocol: "udp", Port: 5000}, "protocol"},
		"tcp cannot listen on udp":    {models.EntryPointConfig{Kind: "tcp", Protocol: "udp", Port: 5000}, "protocol"},
		"udp cannot listen on tcp":    {models.EntryPointConfig{Kind: "udp", Protocol: "tcp", Port: 5000}, "protocol"},
		"https needs tls":             {models.EntryPointConfig{Kind: "https", Port: 5000}, "http.tls"},
		"http has no entrypoint tls":  {models.EntryPointConfig{Kind: "http", Port: 5000, HTTP: tls}, "http.tls"},
		"tcp service has no http":     {models.EntryPointConfig{Kind: "tcp", Port: 5000, HTTP: &models.EntryPointHTTP{EncodeQuerySemicolons: true}}, "http"},
		"tcp service has no http3":    {models.EntryPointConfig{Kind: "tcp", Port: 5000, HTTP3: &models.EntryPointHTTP3{}}, "http3"},
		"tcp service has no forwards": {models.EntryPointConfig{Kind: "tcp", Port: 5000, ForwardedHeaders: &models.EntryPointForwardedHeaders{Insecure: true}}, "forwarded_headers"},
	}
	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			tc.in.Name = "candidate"
			err := build(&tc.in, refs)
			if err == nil {
				t.Fatal("accepted", tc.in.RenderedNode)
			}
			if got := fieldErrors(t, err); got[tc.field] == "" || len(got) != 1 {
				t.Fatalf("want only %s, got %v", tc.field, got)
			}
		})
	}

	// The type alone implies the protocol; it is stored but never written to traefik.yml.
	udp := models.EntryPointConfig{Name: "dns", Kind: "udp", Port: 53}
	if err := build(&udp, refs); err != nil || udp.Protocol != "udp" || udp.Address != ":53/udp" {
		t.Fatal(err, udp)
	}
	plain := models.EntryPointConfig{Name: "db", Kind: "tcp", Port: 5432, ProxyProtocol: &models.EntryPointProxyProtocol{TrustedIPs: []string{"10.0.0.1"}}, Transport: &models.EntryPointTransport{IdleTimeout: "5m"}}
	if err := build(&plain, refs); err != nil || plain.Kind != "tcp" {
		t.Fatal(err)
	}
	if _, leaked := plain.RenderedNode["kind"]; leaked {
		t.Fatal("the type must not reach traefik.yml", plain.RenderedNode)
	}
	// Without a stated type it follows from the settings.
	for want, in := range map[string]models.EntryPointConfig{
		"https": {Name: "a", Port: 443, HTTP: tls}, "http": {Name: "a", Port: 80}, "udp": {Name: "a", Protocol: "udp", Port: 53},
	} {
		if err := build(&in, refs); err != nil || in.Kind != want {
			t.Fatalf("%s: %v %q", want, err, in.Kind)
		}
	}
}
