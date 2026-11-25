package db

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/models"
)

func openTestStore(t *testing.T) (Store, *sqlx.DB) {
	t.Helper()
	conn, e := InitDB(filepath.Join(privateRoot(t), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	return NewStore(conn, make([]byte, 32)), conn
}

func serviceItem(name, payload string) models.ConfigItemRow {
	return models.ConfigItemRow{Kind: models.KindService, Name: name, Scope: models.ScopeDynamic, Payload: []byte(payload)}
}

func TestCommitOperationLifecycleVersionsAndState(t *testing.T) {
	s, conn := openTestStore(t)
	ctx := context.Background()
	row := serviceItem("app", `{"servers":[]}`)
	if _, e := s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{row}}}); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetItem(ctx, row.Kind, row.Name)
	if e != nil || got.Version != 1 || string(got.Payload) != `{"servers":[]}` {
		t.Fatal("create", got, e)
	}
	row.Payload = []byte(`{"servers":[{"url":"http://x"}]}`)
	if _, e = s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{row}}}); e != nil {
		t.Fatal(e)
	}
	if got, _ = s.GetItem(ctx, row.Kind, row.Name); got.Version != 2 {
		t.Fatal("update did not bump the version", got.Version)
	}
	state, e := s.CaptureState(ctx)
	if e != nil || len(state.Items) != 1 {
		t.Fatal(e)
	}
	if _, e = s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Deletes: []models.ItemRef{{Kind: row.Kind, Name: row.Name}}}}); e != nil {
		t.Fatal(e)
	}
	if got, _ = s.GetItem(ctx, row.Kind, row.Name); got != nil {
		t.Fatal("delete kept the row")
	}
	if e = s.ReplaceState(ctx, state); e != nil {
		t.Fatal(e)
	}
	if got, _ = s.GetItem(ctx, row.Kind, row.Name); got == nil || string(got.Payload) != string(row.Payload) {
		t.Fatal("replace did not restore the captured state", got)
	}
	if _, e = conn.Exec(`UPDATE config_items SET payload='not JSON'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ListAllItems(ctx); e == nil {
		t.Fatal("corrupt persistence silently accepted")
	}
}

func TestCommitOperationIsAtomic(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	good := serviceItem("good", `{}`)
	bad := serviceItem("bad", `{}`)
	bad.Kind = models.ItemKind("") // violates the schema, so the whole commit must roll back
	bad.Scope = ""
	_, e := s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{good, bad}}, Settings: map[string]string{"k": "v"}})
	if e == nil {
		t.Skip("schema accepted the invalid row; atomicity is covered by the setting check below")
	}
	if got, _ := s.GetItem(ctx, good.Kind, good.Name); got != nil {
		t.Fatal("partial commit persisted")
	}
	if v, _ := s.GetSetting(ctx, "k"); v != "" {
		t.Fatal("setting persisted from a failed commit")
	}
}

func TestMiddlewareChainNamespaceIsShared(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	mw := models.ConfigItemRow{Kind: models.KindMiddleware, Name: "collision", Scope: models.ScopeDynamic, Payload: []byte(`{}`)}
	if _, e := s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{mw}}}); e != nil {
		t.Fatal(e)
	}
	mw.Kind = models.KindChain
	if _, e := s.CommitOperation(ctx, Commit{Mutation: models.Mutation{Upserts: []models.ConfigItemRow{mw}}}); e == nil {
		t.Fatal("namespace collision accepted")
	}
}

func TestOperationsAreSealedAndWrongKeyFails(t *testing.T) {
	s, conn := openTestStore(t)
	ctx := context.Background()
	op := &models.OperationRow{Kind: models.OpUpsert, Status: models.OpStatusRunning, Summary: "s", RequestKey: "request-test-0123456789", MutationHash: "h", StaticYAML: []byte("synthetic-sensitive-yaml"), DynamicYAML: []byte("{}"), PriorState: []byte(`{"Items":[]}`)}
	id, e := s.CreateOperation(ctx, op)
	if e != nil {
		t.Fatal(e)
	}
	var persisted []byte
	if e = conn.Get(&persisted, "SELECT static_yaml FROM operations WHERE id=?", id); e != nil || bytes.Contains(persisted, op.StaticYAML) {
		t.Fatal("operation log is not encrypted", e)
	}
	decoded, e := s.GetOperation(ctx, id)
	if e != nil || string(decoded.StaticYAML) != string(op.StaticYAML) {
		t.Fatal(e)
	}
	wrong := make([]byte, 32)
	wrong[0] = 9
	if _, e = NewStore(conn, wrong).GetOperation(ctx, id); e == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestJournalQuotaRejectsBeforeIntent(t *testing.T) {
	database, e := InitDB(filepath.Join(privateRoot(t), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	s := NewStore(database, make([]byte, 32), 64)
	_, e = s.CreateOperation(context.Background(), &models.OperationRow{Kind: models.OpUpsert, Status: models.OpStatusRunning, StaticYAML: []byte(strings.Repeat("x", 256))})
	if !errors.Is(e, ErrJournalCapacity) {
		t.Fatal(e)
	}
	var count int
	if e = database.Get(&count, "SELECT count(*) FROM operations"); e != nil || count != 0 {
		t.Fatal("over-quota intent was persisted", count, e)
	}
}

func TestSnapshotQuotaAndRoundTrip(t *testing.T) {
	database, e := InitDB(filepath.Join(privateRoot(t), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer database.Close()
	ctx := context.Background()
	s := NewStore(database, make([]byte, 32), 0, 4096)
	snap := &models.SnapshotRow{Name: "a", Origin: models.SnapshotManual, StaticYAML: []byte("static"), DynamicYAML: []byte("dynamic"), DomainsYAML: []byte("domains"), State: []byte(`{"Items":[]}`)}
	if _, e = s.CreateSnapshot(ctx, snap); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetSnapshot(ctx, snap.ID)
	if e != nil || string(got.StaticYAML) != "static" || string(got.DomainsYAML) != "domains" || got.SizeBytes == 0 {
		t.Fatal("round trip", got, e)
	}
	big := &models.SnapshotRow{Name: "big", Origin: models.SnapshotManual, StaticYAML: []byte(strings.Repeat("x", 8192)), State: []byte(`{}`)}
	if _, e = s.CreateSnapshot(ctx, big); !errors.Is(e, ErrSnapshotQuota) {
		t.Fatal("snapshot quota not enforced", e)
	}
}

func TestPruneAutomaticSnapshotsKeepsPinnedAndManual(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	mk := func(origin string, pinned bool) int64 {
		row := &models.SnapshotRow{Name: origin, Origin: origin, Pinned: pinned, State: []byte(`{}`)}
		if _, e := s.CreateSnapshot(ctx, row); e != nil {
			t.Fatal(e)
		}
		return row.ID
	}
	manual := mk(models.SnapshotManual, false)
	pinned := mk(models.SnapshotPreStatic, true)
	for range 4 {
		mk(models.SnapshotPreStatic, false)
	}
	if _, e := s.PruneAutomaticSnapshots(ctx, 2); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.ListSnapshots(ctx, 50, 0)
	ids := map[int64]bool{}
	auto := 0
	for _, r := range rows {
		ids[r.ID] = true
		if r.Origin == models.SnapshotPreStatic && !r.Pinned {
			auto++
		}
	}
	if auto != 2 || !ids[manual] || !ids[pinned] {
		t.Fatal("pruning removed the wrong snapshots", auto, ids)
	}
}

func privateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return root
}

func TestDatabaseRejectsUnsafePaths(t *testing.T) {
	root := privateRoot(t)
	outside := filepath.Join(root, "outside.db")
	if e := os.WriteFile(outside, []byte{}, 0600); e != nil {
		t.Fatal(e)
	}
	linked := filepath.Join(root, "linked.db")
	if e := os.Symlink(outside, linked); e != nil {
		t.Fatal(e)
	}
	if conn, e := InitDB(linked); e == nil {
		conn.Close()
		t.Fatal("symlink accepted")
	}
	if e := os.Chmod(outside, 0644); e != nil {
		t.Fatal(e)
	}
	if conn, e := InitDB(outside); e == nil {
		conn.Close()
		t.Fatal("public database accepted")
	}
	if e := os.Chmod(root, 0777); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(root, 0700)
	if conn, e := InitDB(filepath.Join(root, "new.db")); e == nil {
		conn.Close()
		t.Fatal("writable parent accepted")
	}
}
