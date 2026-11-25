package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// schema is the complete auth.db layout. Versioned statements are appended,
// never edited, so an existing database upgrades in place.
var schema = []string{
	// v1
	`CREATE TABLE admin (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		username TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		credential_epoch INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		csrf TEXT NOT NULL,
		credential_epoch INTEGER NOT NULL,
		method TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE setup_token (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		token_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE oauth_provider (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		enabled INTEGER NOT NULL,
		kind TEXT NOT NULL,
		display_name TEXT NOT NULL,
		issuer TEXT NOT NULL DEFAULT '',
		authorize_url TEXT NOT NULL DEFAULT '',
		token_url TEXT NOT NULL DEFAULT '',
		userinfo_url TEXT NOT NULL DEFAULT '',
		client_id TEXT NOT NULL,
		sealed_client_secret BLOB,
		scopes TEXT NOT NULL,
		identity_claim TEXT NOT NULL,
		allowed_identities TEXT NOT NULL,
		redirect_url TEXT NOT NULL,
		revision INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at INTEGER NOT NULL,
		event TEXT NOT NULL,
		outcome TEXT NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT ''
	);`,
	// v2: any number of external providers, each shown as its own sign-in
	// button; sessions remember which provider opened them.
	`CREATE TABLE oauth_providers (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		display_name TEXT NOT NULL,
		issuer TEXT NOT NULL DEFAULT '',
		authorize_url TEXT NOT NULL DEFAULT '',
		token_url TEXT NOT NULL DEFAULT '',
		userinfo_url TEXT NOT NULL DEFAULT '',
		client_id TEXT NOT NULL,
		sealed_client_secret BLOB,
		scopes TEXT NOT NULL,
		identity_claim TEXT NOT NULL,
		allowed_identities TEXT NOT NULL,
		redirect_url TEXT NOT NULL,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	INSERT INTO oauth_providers(id,kind,display_name,issuer,authorize_url,token_url,userinfo_url,client_id,sealed_client_secret,scopes,identity_claim,allowed_identities,redirect_url,revision,created_at,updated_at)
		SELECT 'default',kind,display_name,issuer,authorize_url,token_url,userinfo_url,client_id,sealed_client_secret,scopes,identity_claim,allowed_identities,redirect_url,revision,updated_at,updated_at
		FROM oauth_provider WHERE enabled = 1;
	DROP TABLE oauth_provider;
	ALTER TABLE sessions ADD COLUMN provider TEXT NOT NULL DEFAULT '';
	UPDATE sessions SET provider = 'default' WHERE method = 'oauth';`,
}

// Store owns auth.db: the single administrator, sessions, the pending setup
// token, the external sign-in provider and a bounded audit log. It is a
// separate file from the configuration database so session checks never wait
// behind a configuration operation, and snapshot restore can never touch
// credentials.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// OpenStore opens (creating when absent) a private auth database. The parent
// directory must exist and must not be writable by group or others; the file
// is created 0600 and refused when it is a symlink or has wider permissions.
func OpenStore(path string) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("auth database directory: %w", err)
	}
	if parent.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("auth database directory must not be writable by group or others")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("auth database: %w", err)
	}
	st, err := f.Stat()
	f.Close()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("auth database must be a private regular file (0600)")
	}
	u := url.URL{Scheme: "file", Path: path}
	dsn := u.String() + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	database, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	s := &Store{db: database, now: time.Now}
	if err = s.migrate(); err != nil {
		database.Close()
		return nil, fmt.Errorf("initialize auth database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS auth_schema (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT version FROM auth_schema`).Scan(&version)
	if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO auth_schema(version) VALUES(0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if version > len(schema) {
		return fmt.Errorf("auth database schema %d is newer than this binary supports (%d)", version, len(schema))
	}
	for i := version; i < len(schema); i++ {
		if _, err = tx.ExecContext(ctx, schema[i]); err != nil {
			return fmt.Errorf("auth schema v%d: %w", i+1, err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_schema SET version=?`, len(schema)); err != nil {
		return err
	}
	return tx.Commit()
}

// audit records one security event. It never stores secrets; failures are
// returned so callers can log them but never block the user flow.
func (s *Store) audit(ctx context.Context, event, outcome, ip, detail string) error {
	if len(detail) > 256 {
		detail = detail[:256]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log(at,event,outcome,ip,detail) VALUES(?,?,?,?,?)`, s.now().Unix(), event, outcome, ip, detail)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE id <= (SELECT id FROM audit_log ORDER BY id DESC LIMIT 1 OFFSET 500)`)
	}
	return err
}
