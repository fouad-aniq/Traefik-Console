package db

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/models"
)

func closeLegacy(t *testing.T, conn *sqlx.DB, path string) {
	t.Helper()
	if e := conn.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
}

// openLegacyDB builds a database exactly as migrations 0001-0004 left it, so the 0005/0006 upgrade
// path can be tested against realistic pre-upgrade data.
func openLegacyDB(t *testing.T, path string) *sqlx.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	conn, e := sqlx.Open("sqlite", u.String()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if e != nil {
		t.Fatal(e)
	}
	conn.SetMaxOpenConns(1)
	if _, e = conn.Exec(`CREATE TABLE schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"0001_core_schema.sql", "0002_transaction_journal.sql", "0003_plugin_metadata.sql", "0004_plugin_catalog_aliases.sql"} {
		content, e := migrationFiles.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = conn.Exec(string(content)); e != nil {
			t.Fatal(name, e)
		}
		if _, e = conn.Exec(`INSERT INTO schema_migrations(name,checksum) VALUES(?,?)`, name, fmt.Sprintf("%x", sha256.Sum256(content))); e != nil {
			t.Fatal(e)
		}
	}
	return conn
}

func TestMigrationArchivesDraftsAndKeepsActiveData(t *testing.T) {
	path := filepath.Join(privateRoot(t), "state.db")
	legacy := openLegacyDB(t, path)
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, e := legacy.Exec(q, args...); e != nil {
			t.Fatal(q, e)
		}
	}
	mustExec(`INSERT INTO config_items(id,kind,name,scope,status,payload,draft_payload) VALUES
('service:live','service','live','dynamic','active','{"servers":[]}',NULL),
('service:edited','service','edited','dynamic','draft_modified','{"servers":[]}','{"servers":[{"url":"http://new"}]}'),
('service:created','service','created','dynamic','draft_new',NULL,'{"servers":[]}'),
('service:removed','service','removed','dynamic','draft_deleted','{"servers":[]}',NULL)`)
	mustExec(`INSERT INTO secrets(name,purpose,file_path,masked_preview,encrypted_value,draft_encrypted_value) VALUES ('s1','p','/s1','m',x'01',x'02'),('s2','p','/s2','m',x'',x'03')`)
	closeLegacy(t, legacy, path)

	conn, e := InitDB(path)
	if e != nil {
		t.Fatal("upgrade failed", e)
	}
	defer conn.Close()
	ctx := context.Background()
	s := NewStore(conn, make([]byte, 32))

	items, e := s.ListAllItems(ctx)
	if e != nil {
		t.Fatal(e)
	}
	byName := map[string]models.ConfigItemRow{}
	for _, it := range items {
		byName[it.Name] = it
	}
	// Drafts are discarded exactly as the retired DiscardAllDrafts did: edits revert, creations vanish, deletions revert.
	if len(byName) != 3 || byName["created"].Name != "" || string(byName["edited"].Payload) != `{"servers":[]}` || byName["removed"].Name == "" || byName["live"].Version != 1 {
		t.Fatal("unexpected active set after upgrade", byName)
	}
	drafts, secretDrafts, e := s.CountLegacyArchive(ctx)
	if e != nil || drafts != 3 || secretDrafts != 2 {
		t.Fatal("drafts were not archived", drafts, secretDrafts, e)
	}
	var count int
	if e = conn.Get(&count, `SELECT count(*) FROM schema_migrations`); e != nil || count != 6 {
		t.Fatal("migration count", count, e)
	}
	for _, col := range []string{"status", "draft_payload"} {
		if e = conn.Get(&count, `SELECT count(*) FROM pragma_table_info('config_items') WHERE name=?`, col); e != nil || count != 0 {
			t.Fatal("draft column still present", col)
		}
	}
	// Replaying the migrations on the upgraded database is a no-op.
	conn.Close()
	again, e := InitDB(path)
	if e != nil {
		t.Fatal("migration replay", e)
	}
	again.Close()
}

func TestLegacyApplyHistoryBecomesSnapshotsOnce(t *testing.T) {
	path := filepath.Join(privateRoot(t), "state.db")
	key := make([]byte, 32)
	legacy := openLegacyDB(t, path)
	seal := NewStore(legacy, key).(*sqlStore)
	state, _ := seal.seal([]byte(`{"Items":[{"id":"service:app","kind":"service","name":"app","scope":"dynamic","payload":{"servers":[]}}],"Secrets":[]}`))
	static, _ := seal.seal([]byte("api: {}\n"))
	dynamic, _ := seal.seal([]byte("http: {}\n"))
	if _, e := legacy.Exec(`INSERT INTO snapshots(status,summary,static_yaml,dynamic_yaml,items_backup_json,candidate_hash,result_state) VALUES ('working','Added app',?,?,'[]','h',?),('rolled_back','Failed',?,?,'[]','h2',?)`, static, dynamic, state, static, dynamic, state); e != nil {
		t.Fatal(e)
	}
	closeLegacy(t, legacy, path)

	conn, e := InitDB(path)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	s := NewStore(conn, key)
	ctx := context.Background()
	n, e := s.ImportLegacySnapshots(ctx)
	if e != nil || n != 1 {
		t.Fatal("only working applies become snapshots", n, e)
	}
	again, e := s.ImportLegacySnapshots(ctx)
	if e != nil || again != 0 {
		t.Fatal("import is not idempotent", again, e)
	}
	rows, _ := s.ListSnapshots(ctx, 10, 0)
	if len(rows) != 1 || rows[0].Origin != models.SnapshotLegacyImport || rows[0].ItemCount != 1 {
		t.Fatal(rows)
	}
	full, e := s.GetSnapshot(ctx, rows[0].ID)
	if e != nil || string(full.StaticYAML) != "api: {}\n" {
		t.Fatal("legacy file bytes not preserved", e)
	}
	op, e := s.GetOperation(ctx, 1)
	if e != nil || op == nil || op.Kind != models.OpLegacyApply {
		t.Fatal("legacy journal row not kept as an operation", op, e)
	}
}
