package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

func (f *fixture) snapshot(t *testing.T, name string) *models.SnapshotRow {
	t.Helper()
	s, e := f.ops.CreateSnapshot(context.Background(), name, "")
	if e != nil {
		t.Fatal(e)
	}
	return s
}

var restoreSeq int

func (f *fixture) restore(t *testing.T, id int64, skip bool) *models.RestoreResult {
	t.Helper()
	p, e := f.ops.PreviewRestore(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	restoreSeq++
	r, e := f.ops.RestoreSnapshot(context.Background(), id, models.RestoreRequest{RestoreToken: p.RestoreToken, SkipBackup: skip, IdempotencyKey: fmt.Sprintf("restore-test-key-%08d", restoreSeq)})
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestSnapshotRestoreIsByteIdentical(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	snap := f.snapshot(t, "good")
	dynamicThen := f.dynamic(t)
	f.apply(t, upsert(serviceRow("app", "http://two.invalid"), serviceRow("extra", "http://extra.invalid")))
	f.restore(t, snap.ID, false)
	if f.dynamic(t) != dynamicThen {
		t.Fatal("restored file differs from the snapshot bytes", f.dynamic(t))
	}
	if f.item(t, models.KindService, "extra") != nil || !bytes.Contains(f.item(t, models.KindService, "app").Payload, []byte("one.invalid")) {
		t.Fatal("restored state differs from the snapshot")
	}
}

func TestRestoreTakesPreRestoreSnapshotUnlessSkipped(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	snap := f.snapshot(t, "good")
	f.apply(t, upsert(serviceRow("app", "http://two.invalid")))
	r := f.restore(t, snap.ID, false)
	if r.BackupSnapshotID == nil {
		t.Fatal("no backup snapshot id")
	}
	backup, e := f.store.GetSnapshotMeta(context.Background(), *r.BackupSnapshotID)
	if e != nil || backup == nil || backup.Origin != models.SnapshotPreRestore {
		t.Fatal("backup is not a pre_restore snapshot", backup, e)
	}
	// The backup restores the state the restore replaced.
	f.restore(t, backup.ID, true)
	if !strings.Contains(f.dynamic(t), "two.invalid") {
		t.Fatal("undoing the restore failed")
	}
	skipped := f.restore(t, snap.ID, true)
	if skipped.BackupSnapshotID != nil {
		t.Fatal("backup taken although skip_backup was set")
	}
}

func TestRestoreRefusesStalePreview(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	snap := f.snapshot(t, "good")
	p, e := f.ops.PreviewRestore(context.Background(), snap.ID)
	if e != nil {
		t.Fatal(e)
	}
	f.apply(t, upsert(serviceRow("app", "http://two.invalid")))
	if _, e = f.ops.RestoreSnapshot(context.Background(), snap.ID, models.RestoreRequest{RestoreToken: p.RestoreToken, IdempotencyKey: "restore-stale-key-00001"}); !errors.Is(e, ErrRestoreStale) {
		t.Fatal("stale preview accepted", e)
	}
}

func TestSnapshotRefusedWhileDrifted(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	if e := f.files.WriteManagedDynamicConfig([]byte("# hand edit\n")); e != nil {
		t.Fatal(e)
	}
	if _, e := f.ops.CreateSnapshot(context.Background(), "x", ""); !errors.Is(e, ErrConfigDrift) {
		t.Fatal("snapshot of drifted files accepted", e)
	}
}

func TestPinnedSnapshotNeedsForceToDelete(t *testing.T) {
	f := newFixture(t)
	snap := f.snapshot(t, "keep")
	pin := true
	if _, e := f.ops.UpdateSnapshot(context.Background(), snap.ID, models.SnapshotPatch{Pinned: &pin}); e != nil {
		t.Fatal(e)
	}
	if e := f.ops.DeleteSnapshot(context.Background(), snap.ID, false); !errors.Is(e, ErrSnapshotPinned) {
		t.Fatal(e)
	}
	if e := f.ops.DeleteSnapshot(context.Background(), snap.ID, true); e != nil {
		t.Fatal(e)
	}
}

func TestAutomaticSnapshotRetention(t *testing.T) {
	f := openFixture(t, t.TempDir(), Options{AutoSnapshotBeforeStatic: true, AutomaticSnapshotRetention: 2, OperationHistoryDays: 90})
	for i, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0"} {
		row := pluginRow("geo", "example.invalid/geoblock")
		row.Payload = []byte(`{"module_name":"example.invalid/geoblock","version":"` + v + `"}`)
		f.apply(t, upsert(row))
		_ = i
	}
	manual := f.snapshot(t, "mine")
	snaps, _ := f.store.ListSnapshots(context.Background(), 50, 0)
	auto := 0
	for _, s := range snaps {
		if s.Origin == models.SnapshotPreStatic {
			auto++
		}
	}
	if auto != 2 || manual.Origin != models.SnapshotManual {
		t.Fatal("retention did not keep exactly the newest automatic snapshots", auto, manual.Origin)
	}
}

func TestCompareSnapshotsReportsResourceDifferences(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	a := f.snapshot(t, "a")
	f.apply(t, upsert(serviceRow("app", "http://two.invalid"), serviceRow("extra", "http://extra.invalid")))
	b := f.snapshot(t, "b")
	p, e := f.ops.CompareSnapshots(context.Background(), a.ID, b.ID)
	if e != nil || len(p.Added) != 1 || p.Added[0].Name != "extra" || len(p.Changed) != 1 || p.Changed[0].Name != "app" {
		t.Fatal(p, e)
	}
}

func TestSnapshotConfigIsRedactedByTheHandlerNotStoredRedacted(t *testing.T) {
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://one.invalid")))
	snap := f.snapshot(t, "x")
	cfg, e := f.ops.SnapshotConfig(context.Background(), snap.ID)
	if e != nil || cfg.DynamicYAML != f.dynamic(t) {
		t.Fatal("snapshot did not keep the exact file bytes", e)
	}
}

func TestInitialSnapshotOnlyOnFirstRun(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if e := f.ops.SeedPublishedHashes(ctx); e != nil {
		t.Fatal(e)
	}
	first, e := f.ops.EnsureInitialSnapshot(ctx)
	if e != nil || first == nil || first.Origin != models.SnapshotInitial {
		t.Fatal("initial snapshot not created", first, e)
	}
	again, e := f.ops.EnsureInitialSnapshot(ctx)
	if e != nil || again != nil {
		t.Fatal("initial snapshot repeated", again, e)
	}
	f.apply(t, upsert(pluginRow("geo", "example.invalid/geoblock")))
	snaps, _ := f.store.ListSnapshots(ctx, 10, 0)
	if len(snaps) != 1 || snaps[0].ID != first.ID {
		t.Fatal("a write added a snapshot or removed the initial one", snaps)
	}
	// Never pruned: it is a baseline, not an automatic safety net.
	if _, e = f.store.PruneAutomaticSnapshots(ctx, 0); e != nil {
		t.Fatal(e)
	}
	if snaps, _ = f.store.ListSnapshots(ctx, 10, 0); len(snaps) != 1 {
		t.Fatal("initial snapshot pruned", snaps)
	}
}

func TestInitialSnapshotCapturesStartupStateAndRestoresIt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://app.invalid")))
	initial, e := f.ops.EnsureInitialSnapshot(ctx)
	if e != nil || initial == nil || initial.ItemCount != 1 || initial.Name != "Initial configuration" {
		t.Fatal("initial snapshot does not capture the startup state", initial, e)
	}
	if !initial.KnownGood {
		t.Fatal("a configuration Traefik serves should be marked known good")
	}
	baseline := f.static(t)
	f.apply(t, upsert(resolverRow(t, "le", "ops@example.invalid")))
	f.restore(t, initial.ID, true)
	if f.item(t, models.KindResolver, "le") != nil || f.item(t, models.KindService, "app") == nil || f.static(t) != baseline {
		t.Fatal("restoring the initial snapshot did not return to the startup state")
	}
}

func TestInitialSnapshotSkippedWhenFilesDrifted(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.apply(t, upsert(serviceRow("app", "http://app.invalid")))
	if e := f.files.WriteManagedDynamicConfig([]byte("http: {}\n")); e != nil {
		t.Fatal(e)
	}
	if s, e := f.ops.EnsureInitialSnapshot(ctx); e != nil || s != nil {
		t.Fatal("baseline recorded over drifted files", s, e)
	}
	if snaps, _ := f.store.ListSnapshots(ctx, 10, 0); len(snaps) != 0 {
		t.Fatal(snaps)
	}
	// Once the operator adopts the files, the adopted snapshot is the baseline; no initial one follows.
	if _, e := f.ops.Adopt(ctx); e != nil {
		t.Fatal(e)
	}
	if s, _ := f.ops.EnsureInitialSnapshot(ctx); s != nil {
		t.Fatal("initial snapshot added after adoption")
	}
}

func TestInitialSnapshotSkippedWhileRecoveryIsPending(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.observer.alwaysFail = true
	if _, e := f.ops.Apply(ctx, upsert(serviceRow("app", "http://app.invalid"))); !errors.Is(e, ErrRollbackFailed) {
		t.Fatal("setup: expected a failed recovery", e)
	}
	if s, e := f.ops.EnsureInitialSnapshot(ctx); e != nil || s != nil {
		t.Fatal("baseline recorded while an operation needs recovery", s, e)
	}
}

func TestInitialSnapshotNotTakenWhenAnySnapshotExists(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.snapshot(t, "mine")
	if s, e := f.ops.EnsureInitialSnapshot(ctx); e != nil || s != nil {
		t.Fatal("initial snapshot added next to an existing one", s, e)
	}
}

func TestInitialSnapshotSurvivesAutomaticRetention(t *testing.T) {
	ctx := context.Background()
	f := openFixture(t, t.TempDir(), Options{AutoSnapshotBeforeStatic: true, AutomaticSnapshotRetention: 1, OperationHistoryDays: 90})
	initial, e := f.ops.EnsureInitialSnapshot(ctx)
	if e != nil || initial == nil {
		t.Fatal(e)
	}
	for _, email := range []string{"a@example.invalid", "b@example.invalid", "c@example.invalid"} {
		f.apply(t, upsert(resolverRow(t, "le", email)))
	}
	snaps, _ := f.store.ListSnapshots(ctx, 10, 0)
	found := false
	for _, s := range snaps {
		found = found || s.ID == initial.ID
	}
	if !found || len(snaps) != 2 {
		t.Fatal("retention must keep the initial snapshot and prune only automatic ones", snaps)
	}
}
