package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func InitDB(path string) (*sqlx.DB, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	parent, e := os.Stat(filepath.Dir(path))
	if e != nil || parent.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("database parent must not be writable by group or others")
	}
	f, e := root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	f.Close()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("database must be a private regular file (0600)")
	}
	u := url.URL{Scheme: "file", Path: path}
	dsn := u.String() + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	database, e := sqlx.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = database.PingContext(ctx); e == nil {
		e = runMigrations(database)
	}
	if e != nil {
		database.Close()
		return nil, fmt.Errorf("initialize database: %w", e)
	}
	return database, nil
}
func runMigrations(database *sqlx.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// A single transaction prevents partial upgrades and migration bookkeeping drift.
	tx, e := database.BeginTxx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY, checksum TEXT NOT NULL)`); e != nil {
		return e
	}
	entries, e := migrationFiles.ReadDir("migrations")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, e := migrationFiles.ReadFile("migrations/" + entry.Name())
		if e != nil {
			return e
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(content))
		var old string
		rows, e := tx.QueryContext(ctx, "SELECT checksum FROM schema_migrations WHERE name=?", entry.Name())
		if e != nil {
			return e
		}
		if rows.Next() {
			e = rows.Scan(&old)
		}
		rows.Close()
		if e != nil {
			return e
		}
		if old != "" {
			if old != sum {
				return fmt.Errorf("migration checksum changed: %s", entry.Name())
			}
			continue
		}
		// Connection pragmas are set by DSN. journal_mode cannot change inside a transaction.
		sql := strings.ReplaceAll(string(content), "PRAGMA journal_mode = WAL;", "")
		if _, e = tx.ExecContext(ctx, sql); e != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES(?,?)", entry.Name(), sum); e != nil {
			return e
		}
	}
	return tx.Commit()
}
