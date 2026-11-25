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

// resolverRow is a resolver as the resolver manager stores it: the validated input with the
// pre-rendered certificatesResolvers.<name> node.
func resolverRow(t *testing.T, name, email string) models.ConfigItemRow {
	t.Helper()
	in := models.ResolverInput{
		Name: name, Type: "acme", Environment: "staging", Email: email, Challenge: "tlsalpn01",
		RenderedNode: map[string]any{"acme": map[string]any{
			"email": email, "caServer": "https://acme-staging-v02.api.letsencrypt.org/directory",
			"storage": "/etc/traefik/acme/" + name + "-staging.json", "tlsChallenge": map[string]any{},
		}},
	}
	raw, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	return models.ConfigItemRow{ID: "resolver:" + name, Kind: models.KindResolver, Name: name, Scope: models.ScopeStatic, Payload: raw}
}

func (f *fixture) static(t *testing.T) string {
	t.Helper()
	b, e := f.files.ReadStaticConfig()
	if e != nil && !errors.Is(e, files.ErrSourceFileNotFound) {
		t.Fatal(e)
	}
	return string(b)
}

func deleteResolver(name string) models.Mutation {
	return models.Mutation{Summary: "test", Deletes: []models.ItemRef{{Kind: models.KindResolver, Name: name}}}
}

func TestResolverCreateRunsJournalFilesRestartVerifyThenCommit(t *testing.T) {
	f := newFixture(t)
	var atVerify struct {
		static   string
		restarts int
		stored   bool
		running  bool
	}
	f.observer.onVerify = func() {
		atVerify.static = f.static(t)
		atVerify.restarts = f.docker.restarts
		atVerify.stored = f.item(t, models.KindResolver, "le") != nil
		ops, _ := f.store.ListOperations(context.Background(), 1, 0)
		atVerify.running = len(ops) == 1 && ops[0].Status == models.OpStatusRunning
	}

	r := f.apply(t, upsert(resolverRow(t, "le", "ops@example.invalid")))

	// Verification happens after the files are written and Traefik restarted, before the DB commit,
	// while the operation is journaled as running.
	if !strings.Contains(atVerify.static, "certificatesResolvers") || !strings.Contains(atVerify.static, "ops@example.invalid") {
		t.Fatal("traefik.yml not written before verification", atVerify.static)
	}
	if atVerify.restarts != 1 || !atVerify.running || atVerify.stored {
		t.Fatalf("pipeline order wrong at verification: restarts=%d running=%t stored=%t", atVerify.restarts, atVerify.running, atVerify.stored)
	}
	// After: committed, succeeded, no pending restart, no drift, no snapshot.
	if r.Operation.Status != models.OpStatusSucceeded || r.RestartRequired() {
		t.Fatal("operation not succeeded", r.Operation.Status)
	}
	if f.item(t, models.KindResolver, "le") == nil || f.observer.verified != 1 || f.docker.restarts != 1 {
		t.Fatal("resolver not committed exactly once")
	}
	st, e := f.ops.State(context.Background())
	if e != nil || st.Drifted || st.RestartRequired || st.RunningOperation {
		t.Fatal("state after create", st, e)
	}
	if snaps, _ := f.store.ListSnapshots(context.Background(), 10, 0); len(snaps) != 0 {
		t.Fatal("a resolver write must not create a snapshot by default", snaps)
	}
}

func TestResolverEditRewritesStaticAndBumpsVersion(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(resolverRow(t, "le", "old@example.invalid")))
	before := f.item(t, models.KindResolver, "le").Version
	f.apply(t, upsert(resolverRow(t, "le", "new@example.invalid")))
	static := f.static(t)
	if !strings.Contains(static, "new@example.invalid") || strings.Contains(static, "old@example.invalid") {
		t.Fatal("edit not published", static)
	}
	if after := f.item(t, models.KindResolver, "le").Version; after != before+1 {
		t.Fatal("version not bumped", before, after)
	}
	if f.docker.restarts != 2 {
		t.Fatal("each static change restarts Traefik once", f.docker.restarts)
	}
}

func TestResolverDeleteRemovesItFromStaticAndRestarts(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(resolverRow(t, "le", "ops@example.invalid"), resolverRow(t, "keep", "keep@example.invalid")))
	r := f.apply(t, deleteResolver("le"))
	static := f.static(t)
	if strings.Contains(static, "ops@example.invalid") || !strings.Contains(static, "keep@example.invalid") {
		t.Fatal("delete did not remove exactly the resolver", static)
	}
	if f.item(t, models.KindResolver, "le") != nil || r.Operation.Status != models.OpStatusSucceeded || f.docker.restarts != 2 {
		t.Fatal("delete not committed", r.Operation.Status, f.docker.restarts)
	}
}

func TestResolverRejectedByTraefikIsRolledBack(t *testing.T) {
	f := newFixture(t)
	baseline := f.static(t)
	f.observer.rejectOnce = true
	_, err := f.ops.Apply(context.Background(), upsert(resolverRow(t, "le", "ops@example.invalid")))
	failure, ok := errors.AsType[*DeployFailure](err)
	if !ok || !failure.RolledBack || failure.Stage != "runtime_verification" || !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal("rejection not rolled back", err)
	}
	if f.static(t) != baseline || f.item(t, models.KindResolver, "le") != nil {
		t.Fatal("rollback left the resolver behind")
	}
	op, _ := f.store.GetOperation(context.Background(), failure.OperationID)
	if op == nil || op.Status != models.OpStatusRolledBack {
		t.Fatal("operation not recorded as rolled back", op)
	}
	// Rolling back a static change restarts Traefik on the restored file as well.
	if f.docker.restarts < 2 {
		t.Fatal("Traefik not restarted on the restored configuration", f.docker.restarts)
	}
	if st, _ := f.ops.State(context.Background()); st.Drifted || st.RecoveryOperation != nil {
		t.Fatal("a completed rollback must leave a clean state", st)
	}
}

func TestResolverEditFailureRestoresThePreviousResolver(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(resolverRow(t, "le", "old@example.invalid")))
	before, version := f.static(t), f.item(t, models.KindResolver, "le").Version
	f.observer.rejectOnce = true
	if _, err := f.ops.Apply(context.Background(), upsert(resolverRow(t, "le", "new@example.invalid"))); !errors.Is(err, ErrRollbackTriggered) {
		t.Fatal("failed edit not rolled back", err)
	}
	if f.static(t) != before || f.item(t, models.KindResolver, "le").Version != version {
		t.Fatal("failed edit changed the published resolver")
	}
}

func TestResolverUnverifiableChangeRequiresRecoveryAndBlocksWrites(t *testing.T) {
	f := newFixture(t)
	f.observer.alwaysFail = true
	_, err := f.ops.Apply(context.Background(), upsert(resolverRow(t, "le", "ops@example.invalid")))
	if !errors.Is(err, ErrRollbackFailed) {
		t.Fatal("unverifiable rollback reported as success", err)
	}
	st, _ := f.ops.State(context.Background())
	if st.RecoveryOperation == nil {
		t.Fatal("state does not expose the operation needing recovery")
	}
	f.observer.alwaysFail = false
	if _, err = f.ops.Apply(context.Background(), upsert(resolverRow(t, "other", "x@example.invalid"))); !errors.Is(err, ErrOperationRecoveryRequired) {
		t.Fatal("writes accepted while recovery is pending", err)
	}
}

func TestResolverRestartFailureKeepsChangeWithoutVerification(t *testing.T) {
	f := newFixture(t)
	f.docker.restartFail = true
	r := f.apply(t, upsert(resolverRow(t, "le", "ops@example.invalid")))
	if r.Operation.Status != models.OpStatusRestartRequired || !r.RestartRequired() {
		t.Fatal("manual restart not reported", r.Operation.Status)
	}
	if f.observer.verified != 0 {
		t.Fatal("verified a configuration Traefik has not loaded")
	}
	if f.item(t, models.KindResolver, "le") == nil || !strings.Contains(f.static(t), "ops@example.invalid") {
		t.Fatal("saved change was discarded")
	}
}
