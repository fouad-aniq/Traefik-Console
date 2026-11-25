package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

type fakeRuntime struct{}

func (fakeRuntime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	return &status.RuntimeSnapshot{}, nil
}

type fakeObservation struct {
	rejectOnce bool
	alwaysFail bool
	// onVerify, when set, runs at the start of every verification (tests inspect the pipeline order).
	onVerify func()
	verified int
}

func (o *fakeObservation) VerifyConvergenceWithRetry(context.Context, *yamlNode, *yamlNode, time.Duration) (status.VerificationOutcome, error) {
	o.verified++
	if o.onVerify != nil {
		o.onVerify()
	}
	if o.alwaysFail {
		return status.VerificationOutcome{}, errors.New("offline")
	}
	if o.rejectOnce {
		o.rejectOnce = false
		return status.VerificationOutcome{Observed: true, AttributableErr: true}, nil
	}
	return status.VerificationOutcome{Observed: true, Loaded: true}, nil
}

type fakeDocker struct {
	restarts    int
	restartFail bool
}

func (d *fakeDocker) InspectTraefikContainer(context.Context) (*docker.ContainerStatus, error) {
	return &docker.ContainerStatus{Running: true, RestartAllowed: true}, nil
}
func (d *fakeDocker) RestartTraefikContainerAndConfirm(context.Context) (*docker.ContainerStatus, error) {
	d.restarts++
	if d.restartFail {
		return nil, docker.ErrRestartUnconfirmed
	}
	return &docker.ContainerStatus{Running: true, RestartAllowed: true}, nil
}
func (d *fakeDocker) ProbeTraefikSecretFile(context.Context, string) error {
	return docker.ErrCapabilityDisabled
}

type fixture struct {
	root     string
	sql      *sqlx.DB
	store    db.Store
	files    ConfigFiles
	secrets  *secrets.Vault
	observer *fakeObservation
	docker   *fakeDocker
	ops      *operations
}

func openFixture(t *testing.T, root string, opts ...Options) *fixture {
	t.Helper()
	key := make([]byte, 32)
	key[0] = 1
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	database, e := db.InitDB(filepath.Join(root, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	s := db.NewStore(database, key)
	fm, e := files.New(filepath.Join(root, "static", "traefik.yml"), filepath.Join(root, "dynamic"))
	if e != nil {
		t.Fatal(e)
	}
	sm, e := secrets.New(filepath.Join(root, "secrets"), filepath.Join(root, "secrets"), key, s)
	if e != nil {
		t.Fatal(e)
	}
	d := &fakeDocker{}
	o := &fakeObservation{}
	g := NewGenerator(fakeRuntime{}, validation.NewConfigValidator(), d)
	c := newOperations(s, fm, sm, g, o, d, time.Second, opts...)
	return &fixture{root, database, s, fm, sm, o, d, c}
}

func newFixture(t *testing.T) *fixture { return openFixture(t, t.TempDir()) }

func serviceRow(name, url string) models.ConfigItemRow {
	return models.ConfigItemRow{ID: "service:" + name, Kind: models.KindService, Name: name, Scope: models.ScopeDynamic, Payload: []byte(`{"servers":[{"url":"` + url + `"}]}`)}
}

func pluginRow(name, module string) models.ConfigItemRow {
	return models.ConfigItemRow{ID: "plugin:" + name, Kind: models.KindPlugin, Name: name, Scope: models.ScopeStatic, Payload: []byte(`{"module_name":"` + module + `","version":"v1.0.0"}`)}
}

func upsert(rows ...models.ConfigItemRow) models.Mutation {
	return models.Mutation{Summary: "test", Upserts: rows}
}

func (f *fixture) apply(t *testing.T, m models.Mutation) *models.OperationResult {
	t.Helper()
	r, e := f.ops.Apply(context.Background(), m)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func (f *fixture) dynamic(t *testing.T) string {
	t.Helper()
	b, e := f.files.ReadManagedDynamicConfig()
	if e != nil && !errors.Is(e, files.ErrSourceFileNotFound) {
		t.Fatal(e)
	}
	return string(b)
}

func (f *fixture) item(t *testing.T, kind models.ItemKind, name string) *models.ConfigItemRow {
	t.Helper()
	v, e := f.store.GetItem(context.Background(), kind, name)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
