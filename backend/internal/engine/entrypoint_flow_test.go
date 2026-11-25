package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/models"
)

const entryPointStatic = `# kept comment
entryPoints:
  web:
    address: ":80" # public HTTP
  websecure:
    address: ":443"
certificatesResolvers:
  le:
    acme:
      tlsChallenge: {}
`

// entryPointRow is an entrypoint as its manager stores it: the validated input with the rendered node.
func entryPointRow(t *testing.T, cfg models.EntryPointConfig) models.ConfigItemRow {
	t.Helper()
	raw, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return models.ConfigItemRow{ID: "entrypoint:" + cfg.Name, Kind: models.KindEntryPoint, Name: cfg.Name, Scope: models.ScopeStatic, Payload: raw}
}

func deleteEntryPoint(name string) models.Mutation {
	return models.Mutation{Summary: "test", Deletes: []models.ItemRef{{Kind: models.KindEntryPoint, Name: name}}}
}

func TestEntryPointCreateEditDeleteRewritesTraefikYAMLAndRestarts(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte(entryPointStatic)); e != nil {
		t.Fatal(e)
	}
	metrics := models.EntryPointConfig{Name: "metrics", Protocol: "tcp", Port: 9100, RenderedNode: map[string]any{"address": ":9100"}}
	r := f.apply(t, upsert(entryPointRow(t, metrics)))
	if r.Operation.Status != models.OpStatusSucceeded || !r.Operation.RequiresRestart || f.docker.restarts != 1 {
		t.Fatalf("create: %+v restarts=%d", r.Operation, f.docker.restarts)
	}
	static := f.static(t)
	// The rest of the file, comments included, is untouched.
	for _, want := range []string{"# kept comment", "# public HTTP", "metrics:", "address: :9100", "websecure:", "tlsChallenge"} {
		if !strings.Contains(static, want) {
			t.Fatalf("missing %q in\n%s", want, static)
		}
	}

	metrics.RenderedNode = map[string]any{"address": ":9100", "http": map[string]any{"redirections": map[string]any{"entryPoint": map[string]any{"to": "websecure"}}}}
	metrics.HTTP = &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "websecure"}}
	f.apply(t, upsert(entryPointRow(t, metrics)))
	if static = f.static(t); !strings.Contains(static, "to: websecure") || f.docker.restarts != 2 {
		t.Fatalf("edit not published:\n%s", static)
	}
	// Saving the same thing again publishes nothing and does not restart Traefik.
	if again := f.apply(t, upsert(entryPointRow(t, metrics))); !again.NoChange || f.docker.restarts != 2 {
		t.Fatalf("no-op restarted Traefik: %+v", again)
	}

	f.apply(t, deleteEntryPoint("metrics"))
	if static = f.static(t); strings.Contains(static, "metrics") || !strings.Contains(static, "# public HTTP") || f.docker.restarts != 3 {
		t.Fatalf("delete not published:\n%s", static)
	}
	if st, e := f.ops.State(context.Background()); e != nil || st.Drifted {
		t.Fatal("drift after entrypoint operations", st, e)
	}
}

func TestDeclaredEntryPointNeedsExplicitAdoptionAndIsReleasedToItsOriginal(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte(entryPointStatic)); e != nil {
		t.Fatal(e)
	}
	// A row that does not declare the adoption collides with traefik.yml, like any other static resource.
	plain := models.EntryPointConfig{Name: "web", Protocol: "tcp", Port: 80, RenderedNode: map[string]any{"address": ":80"}}
	if _, e := f.ops.Apply(context.Background(), upsert(entryPointRow(t, plain))); !errors.Is(e, files.ErrCannotModifyReadOnly) {
		t.Fatal(e)
	}

	adopted := models.EntryPointConfig{
		Name: "web", Protocol: "tcp", Port: 80, Adopted: true, Original: map[string]any{"address": ":80"},
		HTTP:         &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "websecure"}},
		RenderedNode: map[string]any{"address": ":80", "http": map[string]any{"redirections": map[string]any{"entryPoint": map[string]any{"to": "websecure"}}}},
	}
	f.apply(t, upsert(entryPointRow(t, adopted)))
	if static := f.static(t); !strings.Contains(static, "to: websecure") {
		t.Fatalf("adoption not published:\n%s", static)
	}

	// Releasing puts the original definition back; the listener is never dropped.
	f.apply(t, deleteEntryPoint("web"))
	static := f.static(t)
	if strings.Contains(static, "redirections") || !strings.Contains(static, "web:") || !strings.Contains(static, `address: :80`) && !strings.Contains(static, `address: ":80"`) {
		t.Fatalf("original not restored:\n%s", static)
	}
	if f.item(t, models.KindEntryPoint, "web") != nil {
		t.Fatal("row kept")
	}
}

func TestEntryPointReferencesMustExistInTheCandidate(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte(entryPointStatic)); e != nil {
		t.Fatal(e)
	}
	apply := func(cfg models.EntryPointConfig) error {
		_, e := f.ops.Apply(context.Background(), upsert(entryPointRow(t, cfg)))
		return e
	}
	base := models.EntryPointConfig{Name: "app", Protocol: "tcp", Port: 8443, RenderedNode: map[string]any{"address": ":8443"}}

	missingResolver := base
	missingResolver.HTTP = &models.EntryPointHTTP{TLS: &models.EntryPointTLS{CertResolver: "gone"}}
	if e := apply(missingResolver); !errors.Is(e, ErrValidationFailed) {
		t.Fatal(e)
	}
	missingTarget := base
	missingTarget.HTTP = &models.EntryPointHTTP{Redirect: &models.EntryPointRedirect{To: "gone"}}
	if e := apply(missingTarget); !errors.Is(e, ErrValidationFailed) {
		t.Fatal(e)
	}
	// A resolver and an entrypoint declared in traefik.yml, and a bare port, are valid targets.
	ok := base
	ok.HTTP = &models.EntryPointHTTP{TLS: &models.EntryPointTLS{CertResolver: "le"}, Redirect: &models.EntryPointRedirect{To: ":443"}}
	if e := apply(ok); e != nil {
		t.Fatal(e)
	}
	// A router can use the new entrypoint as soon as it is part of the candidate.
	router := models.ConfigItemRow{ID: "router:r", Kind: models.KindRouter, Name: "r", Scope: models.ScopeDynamic, Payload: []byte(`{"custom_rule":"Host(` + "`a.example.com`" + `)","service":"svc","entry_points":["app"]}`)}
	f.apply(t, upsert(serviceRow("svc", "http://10.0.0.1:80"), router))
	if !strings.Contains(f.dynamic(t), "- app") {
		t.Fatal(f.dynamic(t))
	}
	// Removing it underneath that router is refused by the engine as well as by the manager.
	if _, e := f.ops.Apply(context.Background(), deleteEntryPoint("app")); e == nil {
		t.Fatal("entrypoint removed while a router uses it")
	}
}

func TestRestoringASnapshotFromBeforeAnAdoptionKeepsTheListener(t *testing.T) {
	f := newFixture(t)
	if e := f.files.WriteStaticConfig([]byte(entryPointStatic)); e != nil {
		t.Fatal(e)
	}
	before := f.snapshot(t, "before adoption")
	f.apply(t, upsert(entryPointRow(t, models.EntryPointConfig{
		Name: "web", Protocol: "tcp", Port: 8080, Adopted: true, Original: map[string]any{"address": ":80"},
		RenderedNode: map[string]any{"address": ":8080"},
	})))
	if !strings.Contains(f.static(t), ":8080") {
		t.Fatal(f.static(t))
	}
	f.restore(t, before.ID, true)
	static := f.static(t)
	if strings.Contains(static, ":8080") || !strings.Contains(static, "web:") || !strings.Contains(static, "websecure:") {
		t.Fatalf("restore dropped or kept the wrong listener:\n%s", static)
	}
	if f.item(t, models.KindEntryPoint, "web") != nil {
		t.Fatal("adoption survived the restore")
	}
}
