package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

func TestTimeoutBudgetLeavesRoomForConvergenceAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		convergence time.Duration
		apply       time.Duration
		recovery    time.Duration
		httpWrite   time.Duration
	}{
		{name: "default", convergence: 0, apply: 75 * time.Second, recovery: 45 * time.Second, httpWrite: 135 * time.Second},
		{name: "cold plugin maximum", convergence: 60 * time.Second, apply: 120 * time.Second, recovery: 90 * time.Second, httpWrite: 225 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := NewTimeoutBudget(tc.convergence)
			if budget.Apply != tc.apply || budget.Recovery != tc.recovery || budget.HTTPWrite != tc.httpWrite {
				t.Fatalf("unexpected operation budget: %+v", budget)
			}
			if budget.Recovery < tc.convergence || budget.HTTPWrite <= budget.Apply+budget.Recovery {
				t.Fatal("response envelope cannot contain apply and recovery")
			}
		})
	}
}

func TestApplyPublishesDynamicFileAndBumpsVersion(t *testing.T) {
	f := newFixture(t)
	r := f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	if r.Operation.Status != models.OpStatusSucceeded || !strings.Contains(f.dynamic(t), "http://one.invalid") {
		t.Fatal(r.Operation, f.dynamic(t))
	}
	if got := f.item(t, models.KindService, "app"); got == nil || got.Version != 1 {
		t.Fatal("version after create", got)
	}
	f.apply(t, upsert(serviceRow("app", "http://two.invalid")))
	if got := f.item(t, models.KindService, "app"); got.Version != 2 || !strings.Contains(f.dynamic(t), "two.invalid") {
		t.Fatal("version after update", got.Version)
	}
}

func TestDeleteIsAbsenceFromTheRender(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid"), serviceRow("keep", "http://keep.invalid")))
	f.apply(t, models.Mutation{Summary: "delete", Deletes: []models.ItemRef{{Kind: models.KindService, Name: "app"}}})
	if f.item(t, models.KindService, "app") != nil || strings.Contains(f.dynamic(t), "one.invalid") || !strings.Contains(f.dynamic(t), "keep.invalid") {
		t.Fatal("delete did not remove only the target", f.dynamic(t))
	}
}

func TestExpectedVersionIsCheckedUnderTheOperationLock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	ref := models.ItemRef{Kind: models.KindService, Name: "app"}
	if _, e := f.ops.Apply(WithExpectedVersion(ctx, ref, 0, true), upsert(serviceRow("app", "http://one.invalid"))); e != nil {
		t.Fatal("create precondition", e)
	}
	// Creating again while it exists, and updating from a stale version, both conflict and change nothing.
	if _, e := f.ops.Apply(WithExpectedVersion(ctx, ref, 0, true), upsert(serviceRow("app", "http://x.invalid"))); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("create over existing", e)
	}
	if _, e := f.ops.Apply(WithExpectedVersion(ctx, ref, 7, false), upsert(serviceRow("app", "http://x.invalid"))); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("stale update", e)
	}
	if _, e := f.ops.Apply(WithExpectedVersion(ctx, ref, 1, false), upsert(serviceRow("app", "http://two.invalid"))); e != nil {
		t.Fatal("current version rejected", e)
	}
	if _, e := f.ops.Apply(WithExpectedVersion(ctx, ref, 1, false), models.Mutation{Deletes: []models.ItemRef{ref}}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("stale delete", e)
	}
	if strings.Contains(f.dynamic(t), "x.invalid") {
		t.Fatal("conflicting write reached the file")
	}
}

func TestDryRunPreviewChangesNothing(t *testing.T) {
	f := newFixture(t)
	p, e := f.ops.Preview(context.Background(), upsert(serviceRow("app", "http://one.invalid")))
	if e != nil || len(p.Changes) != 1 || p.Changes[0].Action != "created" || p.RequiresRestart {
		t.Fatal(p, e)
	}
	ops, _ := f.store.ListOperations(context.Background(), 10, 0)
	if f.item(t, models.KindService, "app") != nil || f.dynamic(t) != "" || len(ops) != 0 {
		t.Fatal("preview persisted state")
	}
}

func TestIdenticalMutationIsNoChange(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	r := f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	ops, _ := f.store.ListOperations(context.Background(), 10, 0)
	if !r.NoChange || len(ops) != 1 || f.item(t, models.KindService, "app").Version != 1 {
		t.Fatal("no-op was journaled or bumped the version", r, len(ops))
	}
}

func TestIdempotencyKeyReplaysTheStoredOperation(t *testing.T) {
	f := newFixture(t)
	ctx := WithRequestKey(context.Background(), "replay-key-0123456789")
	first, e := f.ops.Apply(ctx, upsert(serviceRow("app", "http://one.invalid")))
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.ops.Apply(ctx, upsert(serviceRow("app", "http://one.invalid")))
	if e != nil || second.Operation.ID != first.Operation.ID {
		t.Fatal("replay created a second operation", e)
	}
	if _, e = f.ops.Apply(ctx, upsert(serviceRow("app", "http://other.invalid"))); e == nil {
		t.Fatal("same key with a different mutation accepted")
	}
}

func TestInvalidMutationWritesNothing(t *testing.T) {
	f := newFixture(t)
	router := models.ConfigItemRow{ID: "router:r", Kind: models.KindRouter, Name: "r", Scope: models.ScopeDynamic, Payload: []byte(`{"custom_rule":"Host(` + "`x.example.com`" + `)","service":"missing"}`)}
	_, e := f.ops.Apply(context.Background(), upsert(router))
	if e == nil {
		t.Fatal("router pointing to a missing service accepted")
	}
	if f.item(t, models.KindRouter, "r") != nil || f.dynamic(t) != "" {
		t.Fatal("rejected mutation left state behind")
	}
}

type permissionFiles struct{ ConfigFiles }

func (f permissionFiles) WriteManagedDynamicConfig([]byte) error {
	return &os.PathError{Op: "rename", Path: "synthetic-private-canary", Err: syscall.EACCES}
}

type failingCommit struct{ db.Store }

func (s failingCommit) CommitOperation(context.Context, db.Commit) ([]models.ConfigItemRow, error) {
	return nil, errors.New("synthetic-private-canary?token=synthetic-private-canary")
}

func TestApplyFailurePreservesSafeCauseAcrossRollback(t *testing.T) {
	for _, which := range []string{"permission", "commit", "runtime", "recovery"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			f.apply(t, upsert(serviceRow("base", "http://base.invalid")))
			baseline := f.dynamic(t)
			stage, code := "dynamic_publication", "PERMISSION_DENIED"
			switch which {
			case "permission":
				f.ops.files = permissionFiles{f.files}
			case "commit":
				f.ops.store = failingCommit{f.store}
				stage, code = "database_commit", "INTERNAL_FAILURE"
			case "runtime":
				f.observer.rejectOnce = true
				stage, code = "runtime_verification", "RUNTIME_REJECTED"
			case "recovery":
				f.observer.alwaysFail = true
				stage, code = "runtime_verification", "INTERNAL_FAILURE"
			}
			_, err := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid")))
			failure, ok := errors.AsType[*DeployFailure](err)
			if !ok || failure.Stage != stage || failure.ReasonCode != code || strings.Contains(failure.Error(), "synthetic-private-canary") {
				t.Fatal("safe diagnostic missing or corrupted", err)
			}
			op, e := f.store.GetOperation(context.Background(), failure.OperationID)
			if e != nil || op == nil || !strings.Contains(op.ErrorLog, stage) || strings.Contains(op.ErrorLog, "synthetic-private-canary") {
				t.Fatal("operation log lost the cause or leaked dependency text", e)
			}
			if which == "recovery" {
				if failure.RolledBack || op.Status != models.OpStatusRecovery || !errors.Is(err, ErrRollbackFailed) {
					t.Fatal("failed recovery reported as restored", op.Status)
				}
				return
			}
			if !failure.RolledBack || op.Status != models.OpStatusRolledBack || !errors.Is(err, ErrRollbackTriggered) {
				t.Fatal("rollback result wrong", op.Status)
			}
			if f.item(t, models.KindService, "app") != nil || f.dynamic(t) != baseline {
				t.Fatal("rollback left the failed change behind")
			}
		})
	}
}

func TestFailedRecoveryBlocksWritesUntilAbandoned(t *testing.T) {
	f := newFixture(t)
	f.observer.alwaysFail = true
	_, err := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid")))
	failure, ok := errors.AsType[*DeployFailure](err)
	if !ok || failure.RolledBack {
		t.Fatal("expected unrecovered failure", err)
	}
	f.observer.alwaysFail = false
	if _, e := f.ops.Apply(context.Background(), upsert(serviceRow("other", "http://o.invalid"))); !errors.Is(e, ErrOperationRecoveryRequired) {
		t.Fatal("writes allowed while an operation needs recovery", e)
	}
	if _, e := f.ops.Abandon(context.Background(), failure.OperationID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.ops.Apply(context.Background(), upsert(serviceRow("other", "http://o.invalid"))); e != nil {
		t.Fatal("still blocked after abandon", e)
	}
}

func TestRecoveryRefusesExternalEdits(t *testing.T) {
	f := newFixture(t)
	f.ops.faultHook = func(point string) {
		if point == "dynamic_written" {
			_ = f.files.WriteManagedDynamicConfig([]byte("# external edit\n"))
			f.observer.alwaysFail = true
		}
	}
	if _, e := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid"))); !errors.Is(e, ErrRollbackFailed) {
		t.Fatal(e)
	}
	if f.dynamic(t) != "# external edit\n" {
		t.Fatal("recovery overwrote an external edit")
	}
}

func TestExclusiveOperationLock(t *testing.T) {
	f := newFixture(t)
	f.ops.mu.Lock()
	defer f.ops.mu.Unlock()
	if _, e := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid"))); !errors.Is(e, ErrDeployInProgress) {
		t.Fatal(e)
	}
}

func TestDriftBlocksWritesUntilAdopted(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	if e := f.files.WriteManagedDynamicConfig([]byte(f.dynamic(t) + "# edited by hand\n")); e != nil {
		t.Fatal(e)
	}
	st, e := f.ops.State(context.Background())
	if e != nil || !st.Drifted || len(st.DriftedFiles) != 1 {
		t.Fatal("drift not reported", st, e)
	}
	if _, e = f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://two.invalid"))); !errors.Is(e, ErrConfigDrift) {
		t.Fatal("write allowed over drifted files", e)
	}
	if _, e = f.ops.Adopt(context.Background()); e != nil {
		t.Fatal(e)
	}
	if st, _ = f.ops.State(context.Background()); st.Drifted {
		t.Fatal("still drifted after adopt")
	}
	f.apply(t, upsert(serviceRow("app", "http://two.invalid")))
}

func TestStaticChangeRestartFailureKeepsTheChange(t *testing.T) {
	f := newFixture(t)
	f.docker.restartFail = true
	r := f.apply(t, upsert(pluginRow("geo", "example.invalid/geoblock")))
	if r.Operation.Status != models.OpStatusRestartRequired || !r.RestartRequired() {
		t.Fatal("restart failure not reported", r.Operation.Status)
	}
	if f.item(t, models.KindPlugin, "geo") == nil {
		t.Fatal("change was discarded after the restart failed")
	}
	st, _ := f.ops.State(context.Background())
	if !st.RestartRequired {
		t.Fatal("state does not report the pending manual restart")
	}
	f.docker.restartFail = false
	if e := f.ops.Restart(context.Background()); e != nil {
		t.Fatal(e)
	}
	if st, _ = f.ops.State(context.Background()); st.RestartRequired {
		t.Fatal("restart_required not cleared by a successful restart")
	}
}

func TestStaticChangeTakesAutomaticSnapshotOnlyWhenEnabled(t *testing.T) {
	// Default: a static change (e.g. a resolver) is journaled and rolled back on failure, no snapshot.
	f := newFixture(t)
	f.apply(t, upsert(pluginRow("geo", "example.invalid/geoblock")))
	if snaps, _ := f.store.ListSnapshots(context.Background(), 10, 0); len(snaps) != 0 {
		t.Fatal("automatic snapshot taken by default", snaps)
	}
	on := openFixture(t, t.TempDir(), Options{AutoSnapshotBeforeStatic: true, AutomaticSnapshotRetention: 20, OperationHistoryDays: 90})
	on.apply(t, upsert(pluginRow("geo", "example.invalid/geoblock")))
	snaps, _ := on.store.ListSnapshots(context.Background(), 10, 0)
	if len(snaps) != 1 || snaps[0].Origin != models.SnapshotPreStatic {
		t.Fatal("pre_static snapshot missing when enabled", snaps)
	}
}

type unavailableRuntime struct{}

func (unavailableRuntime) ObserveRuntime(context.Context) (*status.RuntimeSnapshot, error) {
	return nil, status.ErrObservationUnavailable
}

func TestFailedObservationPublishesNothing(t *testing.T) {
	f := newFixture(t)
	f.ops.builder = NewGenerator(unavailableRuntime{}, validation.NewConfigValidator(), f.docker)
	if _, e := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid"))); !errors.Is(e, status.ErrObservationUnavailable) {
		t.Fatal("failed observation accepted", e)
	}
	if f.item(t, models.KindService, "app") != nil || f.dynamic(t) != "" {
		t.Fatal("failed observation left state behind")
	}
}

func TestProcessCrashRecovery(t *testing.T) {
	if os.Getenv("CORE_V2_CRASH_CHILD") == "1" {
		f := openFixture(t, os.Getenv("CORE_V2_CRASH_ROOT"))
		f.ops.faultHook = func(point string) {
			if point == os.Getenv("CORE_V2_CRASH_POINT") {
				os.Exit(77)
			}
		}
		_, e := f.ops.Apply(context.Background(), upsert(serviceRow("app", "http://one.invalid")))
		t.Fatal("crash hook did not fire", e)
	}
	for _, point := range []string{"journaled", "secrets_written", "dynamic_written", "domains_written", "observed", "finalized"} {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCrashRecovery$")
			cmd.Env = append(os.Environ(), "CORE_V2_CRASH_CHILD=1", "CORE_V2_CRASH_ROOT="+root, "CORE_V2_CRASH_POINT="+point)
			e := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 77 {
				t.Fatal("child did not crash at the checkpoint", e)
			}
			f := openFixture(t, root)
			if e = f.ops.RecoverOnStartup(context.Background()); e != nil {
				t.Fatal(e)
			}
			got := f.item(t, models.KindService, "app")
			if point == "finalized" {
				if got == nil || !strings.Contains(f.dynamic(t), "one.invalid") {
					t.Fatal("recovery lost a committed operation")
				}
				return
			}
			if got != nil || strings.Contains(f.dynamic(t), "one.invalid") {
				t.Fatal("recovery kept a half-applied operation")
			}
			// The system is writable again after recovery.
			f.apply(t, upsert(serviceRow("app", "http://two.invalid")))
		})
	}
}
