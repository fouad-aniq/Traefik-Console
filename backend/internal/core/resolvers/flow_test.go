package resolvers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/engine/enginetest"
	"github.com/traefik-console/internal/models"
)

// traefik.yml as found on disk: two entry points and one resolver the console does not manage.
const staticFixture = `entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
certificatesResolvers:
  existing-external:
    acme:
      email: ext@example.invalid
      storage: /etc/traefik/acme/ext.json
      tlsChallenge: {}
`

type staticBytes string

func (s staticBytes) ReadStaticConfig() ([]byte, error) { return []byte(s), nil }

type managerFixture struct {
	store   db.Store
	applier *enginetest.DirectApplier
	m       ResolverManager
}

func newManagerFixture(t *testing.T) *managerFixture {
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
	key := make([]byte, 32)
	s := db.NewStore(database, key)
	sm, e := secrets.New(filepath.Join(root, "secrets"), filepath.Join(root, "secrets"), key, s)
	if e != nil {
		t.Fatal(e)
	}
	a := &enginetest.DirectApplier{Store: s}
	m, e := NewResolverManager(s, staticBytes(staticFixture), sm, a, "/etc/traefik/acme")
	if e != nil {
		t.Fatal(e)
	}
	return &managerFixture{store: s, applier: a, m: m}
}

func httpResolver(name, env string) models.ResolverInput {
	return models.ResolverInput{Name: name, Type: "acme", Environment: env, Email: "ops@example.invalid", Challenge: "http01", EntryPoint: "web"}
}

func cloudflareResolver(name, token string) models.ResolverInput {
	return models.ResolverInput{Name: name, Type: "acme", Environment: "staging", Email: "ops@example.invalid", Challenge: "dns01", Provider: "cloudflare", Values: map[string]string{"CLOUDFLARE_DNS_API_TOKEN": token}}
}

func (f *managerFixture) save(t *testing.T, in models.ResolverInput) *models.WriteResult[models.ResolverInput] {
	t.Helper()
	res, e := f.m.UpsertResolver(context.Background(), in, models.WriteOptions{})
	if e != nil {
		t.Fatal(e)
	}
	return res
}

func (f *managerFixture) put(t *testing.T, kind models.ItemKind, name, payload string) {
	t.Helper()
	row := models.ConfigItemRow{ID: string(kind) + ":" + name, Kind: kind, Name: name, Scope: models.ScopeDynamic, Payload: []byte(payload)}
	if _, e := f.store.CommitOperation(context.Background(), db.Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{row}}}); e != nil {
		t.Fatal(e)
	}
}

func TestCreateResolverAppliesOneStaticMutationAndReturnsIt(t *testing.T) {
	f := newManagerFixture(t)
	res := f.save(t, httpResolver("le", "staging"))
	if len(f.applier.Applied) != 1 {
		t.Fatal("one create must be one operation", len(f.applier.Applied))
	}
	up := f.applier.Applied[0].Upserts
	if len(up) != 1 || up[0].Kind != models.KindResolver || up[0].Scope != models.ScopeStatic || up[0].ID != "resolver:le" {
		t.Fatal("unexpected mutation", up)
	}
	if res.Resource == nil || res.Resource.Origin != "managed" || res.Resource.ReadOnly {
		t.Fatal("created resolver not returned as managed", res.Resource)
	}
	acme, _ := res.Resource.Config.RenderedNode["acme"].(map[string]any)
	if acme["storage"] != "/etc/traefik/acme/le-staging.json" || acme["caServer"] != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatal("rendered node wrong", acme)
	}
	if challenge, _ := acme["httpChallenge"].(map[string]any); challenge["entryPoint"] != "web" {
		t.Fatal("HTTP-01 entry point not rendered", acme)
	}
}

func TestCreateDNSResolverStoresCredentialAsSecretNeverInPayload(t *testing.T) {
	f := newManagerFixture(t)
	res := f.save(t, cloudflareResolver("cf", "synthetic-dns-token"))
	if res.Resource.Config.Values["CLOUDFLARE_DNS_API_TOKEN"] != "••••••••" {
		t.Fatal("credential not masked in the response")
	}
	if strings.Contains(string(f.applier.Applied[0].Upserts[0].Payload), "synthetic-dns-token") {
		t.Fatal("raw credential stored in the resolver payload")
	}
	secretsOut := f.applier.Applied[0].Secrets
	found := false
	for _, s := range secretsOut {
		found = found || s.EnvVarName == "CLOUDFLARE_DNS_API_TOKEN_FILE"
	}
	if !found {
		t.Fatal("credential not delivered as a _FILE secret", secretsOut)
	}
}

func TestEditResolverKeepsStoredCredentialWhenMaskedValueIsSent(t *testing.T) {
	f := newManagerFixture(t)
	f.save(t, cloudflareResolver("cf", "synthetic-dns-token"))
	edit := cloudflareResolver("cf", "••••••••")
	edit.Delay = "30s"
	res := f.save(t, edit)
	for _, s := range f.applier.Applied[1].Secrets {
		if s.Name == "cloudflare_dns_api_token" {
			t.Fatal("masked value replaced the stored credential")
		}
	}
	if res.Resource.Config.Delay != "30s" || res.Resource.Config.Values["CLOUDFLARE_DNS_API_TOKEN"] != "••••••••" {
		t.Fatal("edit not applied or credential lost", res.Resource.Config)
	}
}

func TestEditRequiresCredentialWhenNoneIsStored(t *testing.T) {
	f := newManagerFixture(t)
	_, e := f.m.UpsertResolver(context.Background(), cloudflareResolver("cf", "••••••••"), models.WriteOptions{})
	if !errors.Is(e, engine.ErrValidationFailed) || len(f.applier.Applied) != 0 {
		t.Fatal("a masked value without a stored credential was accepted", e)
	}
}

func TestDryRunPreviewsWithoutApplying(t *testing.T) {
	f := newManagerFixture(t)
	res, e := f.m.UpsertResolver(context.Background(), httpResolver("le", "staging"), models.WriteOptions{DryRun: true})
	if e != nil || res.Preview == nil || res.Operation != nil {
		t.Fatal("dry run did not return a preview", res, e)
	}
	if len(f.applier.Applied) != 0 {
		t.Fatal("dry run applied a change")
	}
	if row, _ := f.store.GetItem(context.Background(), models.KindResolver, "le"); row != nil {
		t.Fatal("dry run stored the resolver")
	}
}

func TestUpsertRejectionsWriteNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   func() models.ResolverInput
		want error
	}{
		{"invalid name", func() models.ResolverInput { return httpResolver("Bad Name", "staging") }, ErrResolverInvalidName},
		{"name too long", func() models.ResolverInput { return httpResolver("a"+strings.Repeat("b", 63), "staging") }, ErrResolverInvalidName},
		{"external resolver is read-only", func() models.ResolverInput { return httpResolver("existing-external", "staging") }, files.ErrCannotModifyReadOnly},
		{"missing email", func() models.ResolverInput { r := httpResolver("le", "staging"); r.Email = ""; return r }, engine.ErrValidationFailed},
		{"unknown entry point", func() models.ResolverInput { r := httpResolver("le", "staging"); r.EntryPoint = "nope"; return r }, engine.ErrValidationFailed},
		{"HTTP-01 without entry point", func() models.ResolverInput { r := httpResolver("le", "staging"); r.EntryPoint = ""; return r }, engine.ErrValidationFailed},
		{"unknown challenge", func() models.ResolverInput { r := httpResolver("le", "staging"); r.Challenge = "smtp"; return r }, engine.ErrValidationFailed},
		{"custom CA over http", func() models.ResolverInput {
			r := httpResolver("le", "custom")
			r.CAURL = "http://ca.invalid"
			return r
		}, engine.ErrValidationFailed},
		{"unsupported key type", func() models.ResolverInput { r := httpResolver("le", "staging"); r.KeyType = "DSA"; return r }, engine.ErrValidationFailed},
		{"unknown DNS provider field", func() models.ResolverInput { r := cloudflareResolver("cf", "t"); r.Values["EVIL"] = "x"; return r }, engine.ErrValidationFailed},
		{"bad nameserver", func() models.ResolverInput {
			r := cloudflareResolver("cf", "t")
			r.Nameservers = []string{"dns.invalid"}
			return r
		}, engine.ErrValidationFailed},
		{"propagation delay too long", func() models.ResolverInput { r := cloudflareResolver("cf", "t"); r.Delay = "11m"; return r }, engine.ErrValidationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagerFixture(t)
			if _, e := f.m.UpsertResolver(context.Background(), tc.in(), models.WriteOptions{}); !errors.Is(e, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, e)
			}
			if len(f.applier.Applied) != 0 {
				t.Fatal("a rejected resolver reached the engine")
			}
		})
	}
}

func TestEnvironmentSwitchKeepsTheResolverAndMovesToItsOwnStorage(t *testing.T) {
	f := newManagerFixture(t)
	f.save(t, httpResolver("le", "staging"))
	res := f.save(t, httpResolver("le", "production"))
	if len(f.applier.Applied) != 2 {
		t.Fatal("staging -> production must be one more operation", len(f.applier.Applied))
	}
	up := f.applier.Applied[1].Upserts
	if len(up) != 1 || up[0].ID != "resolver:le" || up[0].Scope != models.ScopeStatic {
		t.Fatal("the switch must update the same resolver as a static change", up)
	}
	acme, _ := res.Resource.Config.RenderedNode["acme"].(map[string]any)
	// A new storage file: no staging certificate can be served by the production resolver.
	if acme["storage"] != "/etc/traefik/acme/le-production.json" || acme["caServer"] != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatal("production resolver must use the production CA and its own storage", acme)
	}
	// And back: the staging storage is found again.
	res = f.save(t, httpResolver("le", "staging"))
	if acme, _ = res.Resource.Config.RenderedNode["acme"].(map[string]any); acme["storage"] != "/etc/traefik/acme/le-staging.json" {
		t.Fatal("switching back must reuse the staging storage", acme)
	}
}

func TestCustomCAAddressCannotChangeInPlace(t *testing.T) {
	f := newManagerFixture(t)
	custom := httpResolver("corp", "custom")
	custom.CAURL = "https://ca-one.example.invalid/directory"
	f.save(t, custom)
	custom.CAURL = "https://ca-two.example.invalid/directory"
	if _, e := f.m.UpsertResolver(context.Background(), custom, models.WriteOptions{}); !errors.Is(e, ErrEnvChangeForbidden) {
		t.Fatal("two custom CAs would share one storage file", e)
	}
	if len(f.applier.Applied) != 1 {
		t.Fatal("a refused edit reached the engine")
	}
	// Leaving the custom CA for Let's Encrypt is a normal switch.
	if res := f.save(t, httpResolver("corp", "production")); res.Resource.Config.Environment != "production" {
		t.Fatal("custom -> production not applied")
	}
}

func TestEngineFailureIsReturnedUnchanged(t *testing.T) {
	f := newManagerFixture(t)
	f.applier.Err = engine.ErrRollbackTriggered
	if _, e := f.m.UpsertResolver(context.Background(), httpResolver("le", "staging"), models.WriteOptions{}); !errors.Is(e, engine.ErrRollbackTriggered) {
		t.Fatal("engine failure masked", e)
	}
	if row, _ := f.store.GetItem(context.Background(), models.KindResolver, "le"); row != nil {
		t.Fatal("failed create left a resolver behind")
	}
}

func TestDeleteResolver(t *testing.T) {
	ctx := context.Background()
	t.Run("unused managed resolver is deleted", func(t *testing.T) {
		f := newManagerFixture(t)
		f.save(t, httpResolver("le", "staging"))
		if _, e := f.m.DeleteResolver(ctx, "le", models.WriteOptions{}); e != nil {
			t.Fatal(e)
		}
		del := f.applier.Applied[1].Deletes
		if len(del) != 1 || del[0].Kind != models.KindResolver || del[0].Name != "le" {
			t.Fatal("unexpected delete mutation", del)
		}
		if row, _ := f.store.GetItem(ctx, models.KindResolver, "le"); row != nil {
			t.Fatal("resolver still stored")
		}
	})
	t.Run("blocked while a router requests certificates from it", func(t *testing.T) {
		f := newManagerFixture(t)
		f.save(t, httpResolver("le", "staging"))
		f.put(t, models.KindRouter, "portainer", `{"rule":"Host(`+"`"+`p.example.invalid`+"`"+`)","service":"p","entry_points":["websecure"],"tls":{"enabled":true,"cert_resolver":"le"}}`)
		_, e := f.m.DeleteResolver(ctx, "le", models.WriteOptions{})
		if !errors.Is(e, ErrResolverInUse) || !strings.Contains(e.Error(), "portainer") {
			t.Fatal("delete not blocked by the router", e)
		}
		if len(f.applier.Applied) != 1 {
			t.Fatal("a blocked delete reached the engine")
		}
	})
	t.Run("blocked while a domain uses it", func(t *testing.T) {
		f := newManagerFixture(t)
		f.save(t, httpResolver("le", "staging"))
		f.put(t, models.KindDomain, "site", `{"name":"site","main":"example.invalid","method":"resolver","resolver":"le","state":"configured_unverified"}`)
		if _, e := f.m.DeleteResolver(ctx, "le", models.WriteOptions{}); !errors.Is(e, ErrResolverInUse) || !strings.Contains(e.Error(), "site") {
			t.Fatal("delete not blocked by the domain", e)
		}
	})
	t.Run("a router on another resolver does not block", func(t *testing.T) {
		f := newManagerFixture(t)
		f.save(t, httpResolver("le", "staging"))
		f.put(t, models.KindRouter, "other", `{"rule":"Host(`+"`"+`o.example.invalid`+"`"+`)","service":"o","entry_points":["websecure"],"tls":{"enabled":true,"cert_resolver":"zerossl"}}`)
		f.put(t, models.KindDomain, "files-site", `{"name":"files-site","main":"f.invalid","method":"files","certificate":"le","state":"configured_unverified"}`)
		if _, e := f.m.DeleteResolver(ctx, "le", models.WriteOptions{}); e != nil {
			t.Fatal("unrelated references blocked the delete", e)
		}
	})
	t.Run("external resolver cannot be deleted", func(t *testing.T) {
		f := newManagerFixture(t)
		if _, e := f.m.DeleteResolver(ctx, "existing-external", models.WriteOptions{}); !errors.Is(e, files.ErrCannotModifyReadOnly) {
			t.Fatal(e)
		}
		if len(f.applier.Applied) != 0 {
			t.Fatal("external delete reached the engine")
		}
	})
	t.Run("dry run deletes nothing", func(t *testing.T) {
		f := newManagerFixture(t)
		f.save(t, httpResolver("le", "staging"))
		if res, e := f.m.DeleteResolver(ctx, "le", models.WriteOptions{DryRun: true}); e != nil || res.Preview == nil {
			t.Fatal(e)
		}
		if row, _ := f.store.GetItem(ctx, models.KindResolver, "le"); row == nil || len(f.applier.Applied) != 1 {
			t.Fatal("dry run deleted the resolver")
		}
	})
}

func TestListResolversMergesManagedAndExternal(t *testing.T) {
	f := newManagerFixture(t)
	f.save(t, httpResolver("le", "staging"))
	all, e := f.m.ListResolvers(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]models.UIResource[models.ResolverInput]{}
	for _, r := range all {
		got[r.Name] = r
	}
	if len(all) != 2 || got["le"].ReadOnly || got["le"].Origin != "managed" || !got["existing-external"].ReadOnly || got["existing-external"].Origin != "external" {
		t.Fatal("list does not mark managed vs external", all)
	}
}
