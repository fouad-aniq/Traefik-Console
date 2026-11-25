package routers

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
)

type mixedFiles struct{}

func (mixedFiles) ReadStaticConfig() ([]byte, error) {
	return []byte("entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\n  db: {address: ':5432'}\n  dns: {address: ':53/udp'}\ncertificatesResolvers:\n  le: {acme: {tlsChallenge: {}}}\n"), nil
}

type snapshotRuntime struct{ snap status.RuntimeSnapshot }

func (s snapshotRuntime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	return &s.snap, nil
}

func newMixedManager(t *testing.T, rt runtimeObserver, rows ...*models.ConfigItemRow) (RouterManager, *stubStore) {
	t.Helper()
	store := newStubStore(rows...)
	return NewRouterManager(store, mixedFiles{}, rt, fakeApplier{store}), store
}

func upsertOK(t *testing.T, m RouterManager, name string, in models.RouterConfig) models.RouterConfig {
	t.Helper()
	res, err := m.UpsertRouter(context.Background(), name, in, models.WriteOptions{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res.Resource.Config
}

func TestATCPRouterIsStoredWithItsProtocolAndSNIRule(t *testing.T) {
	m, store := newMixedManager(t, emptyRuntime{})
	got := upsertOK(t, m, "vault", models.RouterConfig{Protocol: "tcp", Domains: []string{"Vault.example.com"}, Service: "vault", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Passthrough: true}})
	if got.Protocol != "tcp" || got.Rule != "HostSNI(`vault.example.com`)" || got.TLS == nil || !got.TLS.Passthrough || !got.TLS.Enabled {
		t.Fatalf("%+v", got)
	}
	if row := store.items["router:vault"]; row == nil || !contains(string(row.Payload), `"protocol":"tcp"`) {
		t.Fatalf("the stored payload lost the protocol: %v", row)
	}
}

func TestAUDPRouterNeedsOnlyAnEntryPointAndAService(t *testing.T) {
	m, _ := newMixedManager(t, emptyRuntime{})
	got := upsertOK(t, m, "dns", models.RouterConfig{Protocol: "udp", Service: "dns"})
	if got.Protocol != "udp" || got.Rule != "" || len(got.EntryPoints) != 1 || got.EntryPoints[0] != "dns" {
		t.Fatalf("the only UDP entrypoint should be chosen: %+v", got)
	}
}

func TestAnHTTPRouterReadsBackAsHTTP(t *testing.T) {
	m, _ := newMixedManager(t, emptyRuntime{})
	if got := upsertOK(t, m, "site", models.RouterConfig{Domains: []string{"a.example.com"}, Service: "web"}); got.Protocol != "http" {
		t.Fatalf("protocol = %q", got.Protocol)
	}
	// A router stored before protocols existed has none, and is listed as HTTP.
	legacy := activeRow(t, models.KindRouter, "old", models.RouterConfig{Domains: []string{"o.example.com"}, Service: "web", EntryPoints: []string{"web"}, Rule: "Host(`o.example.com`)"})
	m, _ = newMixedManager(t, emptyRuntime{}, legacy)
	list, err := m.ListRouters(context.Background())
	if err != nil || len(list) != 1 || list[0].Config.Protocol != "http" {
		t.Fatalf("%+v %v", list, err)
	}
}

func TestRouterProtocolIsValidatedAndCannotChange(t *testing.T) {
	m, _ := newMixedManager(t, emptyRuntime{})
	for _, p := range []string{"sctp", "TCP", " tcp"} {
		if _, err := m.UpsertRouter(context.Background(), "r", models.RouterConfig{Protocol: p, Service: "s", CustomRule: "HostSNI(`*`)"}, models.WriteOptions{}); !errors.Is(err, ErrRouterInvalidProtocol) {
			t.Errorf("protocol %q: %v", p, err)
		}
	}
	upsertOK(t, m, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}})
	for name, in := range map[string]models.RouterConfig{
		"to http": {Domains: []string{"a.example.com"}, Service: "pg"},
		"to udp":  {Protocol: "udp", Service: "pg", EntryPoints: []string{"dns"}},
	} {
		if _, err := m.UpsertRouter(context.Background(), "pg", in, models.WriteOptions{}); !errors.Is(err, ErrRouterInvalidProtocol) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The same protocol is a plain edit.
	upsertOK(t, m, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg2", EntryPoints: []string{"db"}})
}

func TestATCPOrUDPRouterStillNeedsAServiceAndItsOwnKindOfEntryPoint(t *testing.T) {
	m, _ := newMixedManager(t, emptyRuntime{})
	for _, p := range []string{"tcp", "udp"} {
		if _, err := m.UpsertRouter(context.Background(), "r", models.RouterConfig{Protocol: p, CustomRule: "HostSNI(`*`)"}, models.WriteOptions{}); !errors.Is(err, ErrRouterNoService) {
			t.Errorf("%s without service: %v", p, err)
		}
	}
	bad := map[string]models.RouterConfig{
		"tcp on a udp entrypoint": {Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"dns"}},
		"udp on a tcp entrypoint": {Protocol: "udp", Service: "s", EntryPoints: []string{"db"}},
		"udp with a rule":         {Protocol: "udp", CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"dns"}},
		"tcp with the no backend": {Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: models.NoopService, EntryPoints: []string{"db"}},
	}
	for name, in := range bad {
		if _, err := m.UpsertRouter(context.Background(), "r", in, models.WriteOptions{}); !errors.Is(err, ErrRouterRuleEmpty) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func extRouter(name, provider string, raw map[string]any) status.ExternalItem {
	return status.ExternalItem{Name: name, FullName: name + "@" + provider, Provider: provider, Status: "enabled", Raw: raw}
}

func TestEveryProtocolIsListedWithItsOwnLiveHealthAndExternalRouters(t *testing.T) {
	rt := snapshotRuntime{status.RuntimeSnapshot{
		HTTP: status.RuntimeResources{Routers: map[string]status.ExternalItem{
			"site@file":   extRouter("site", "file", nil),
			"dash@docker": extRouter("dash", "docker", map[string]any{"rule": "Host(`d.example.com`)", "service": "dash", "entryPoints": []any{"web"}}),
		}},
		TCP: status.RuntimeResources{Routers: map[string]status.ExternalItem{
			"pg@file":      {Name: "pg", Provider: "file", Status: "error", ErrorMsg: "service missing"},
			"redis@docker": extRouter("redis", "docker", map[string]any{"rule": "HostSNI(`*`)", "service": "redis", "entryPoints": []any{"db"}, "tls": map[string]any{"passthrough": true}}),
		}},
		UDP: status.RuntimeResources{Routers: map[string]status.ExternalItem{
			"dns@file":   extRouter("dns", "file", nil),
			"vpn@docker": extRouter("vpn", "docker", map[string]any{"service": "vpn", "entryPoints": []any{"dns"}}),
		}},
	}}
	m, _ := newMixedManager(t, rt,
		activeRow(t, models.KindRouter, "site", models.RouterConfig{Domains: []string{"a.example.com"}, Service: "web"}),
		activeRow(t, models.KindRouter, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}}),
		activeRow(t, models.KindRouter, "dns", models.RouterConfig{Protocol: "udp", Service: "dns", EntryPoints: []string{"dns"}}),
	)
	list, err := m.ListRouters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ protocol, origin, health string }
	got := map[string]row{}
	ids := map[string]string{}
	for _, r := range list {
		got[r.Name] = row{r.Config.Protocol, r.Origin, r.LiveHealth}
		ids[r.Name] = r.ID
	}
	want := map[string]row{
		"site":         {"http", "managed", "loaded"},
		"pg":           {"tcp", "managed", "error"},
		"dns":          {"udp", "managed", "loaded"},
		"dash@docker":  {"http", "external", "enabled"},
		"redis@docker": {"tcp", "external", "enabled"},
		"vpn@docker":   {"udp", "external", "enabled"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d listed, want %d: %+v", len(got), len(want), got)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %+v, want %+v", name, got[name], w)
		}
	}
	// External ids keep the protocols apart, so two routers with one name never collide.
	var all []string
	for _, id := range ids {
		all = append(all, id)
	}
	sort.Strings(all)
	for i := 1; i < len(all); i++ {
		if all[i] == all[i-1] {
			t.Fatalf("duplicate id %s", all[i])
		}
	}
	if ids["redis@docker"] != "external:tcp-router:redis@docker" || ids["vpn@docker"] != "external:udp-router:vpn@docker" || ids["dash@docker"] != "external:router:dash@docker" {
		t.Fatalf("%+v", ids)
	}
	for _, r := range list {
		if r.Name == "redis@docker" && (r.Config.TLS == nil || !r.Config.TLS.Passthrough || r.Config.Rule != "HostSNI(`*`)") {
			t.Fatalf("an external TCP router lost what Traefik reports: %+v", r.Config)
		}
	}
}

func TestAnExternalRouterOfAnotherProtocolIsNotMistakenForAManagedOne(t *testing.T) {
	// The managed router "pg" is TCP. Traefik also reports an unrelated HTTP router called pg@file:
	// it is not the managed one, so it must still be listed.
	rt := snapshotRuntime{status.RuntimeSnapshot{HTTP: status.RuntimeResources{Routers: map[string]status.ExternalItem{"pg@file": extRouter("pg", "file", nil)}}}}
	m, _ := newMixedManager(t, rt, activeRow(t, models.KindRouter, "pg", models.RouterConfig{Protocol: "tcp", CustomRule: "HostSNI(`*`)", Service: "pg", EntryPoints: []string{"db"}}))
	list, err := m.ListRouters(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("%d listed: %+v %v", len(list), list, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
