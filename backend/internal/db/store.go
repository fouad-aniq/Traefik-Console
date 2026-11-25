package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/models"
)

// Store composes persistence capabilities; resource managers depend on their narrow subset.
type Store interface {
	CatalogStore
	ItemReader
	SecretReader
	OperationStore
	SnapshotStore
	CaptureState(context.Context) (*State, error)
	ReplaceState(context.Context, *State) error
	CommitOperation(context.Context, Commit) ([]models.ConfigItemRow, error)

	// Settings & plugin catalog cache
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	CountLegacyArchive(ctx context.Context) (drafts, secretDrafts int, err error)
}

// OperationStore is the operations log (the renamed journal).
type OperationStore interface {
	CreateOperation(context.Context, *models.OperationRow) (int64, error)
	UpdateOperation(ctx context.Context, id int64, status, stage, reasonCode, errorLog string) error
	PruneOperationBlobs(context.Context, int64) error
	GetOperation(context.Context, int64) (*models.OperationRow, error)
	FindOperationByKey(context.Context, string) (*models.OperationRow, error)
	PendingOperations(context.Context) ([]models.OperationRow, error)
	RecoveryOperations(context.Context) ([]models.OperationRow, error)
	ListOperations(ctx context.Context, limit int, cursor int64) ([]models.OperationRow, error)
	PruneOperations(ctx context.Context, days int) (int64, error)
}

type sqlStore struct {
	db               *sqlx.DB
	key              []byte
	maxJournalBytes  int64
	maxSnapshotBytes int64
}

// NewStore returns a fully initialized Store implementation. The optional
// arguments are the journal quota and the snapshot quota, in bytes.
func NewStore(database *sqlx.DB, key []byte, quota ...int64) Store {
	journal, snapshots := int64(512*1024*1024), int64(1024*1024*1024)
	if len(quota) > 0 && quota[0] > 0 {
		journal = quota[0]
	}
	if len(quota) > 1 && quota[1] > 0 {
		snapshots = quota[1]
	}
	return &sqlStore{db: database, key: append([]byte(nil), key...), maxJournalBytes: journal, maxSnapshotBytes: snapshots}
}

// ============================================================================
// 1. CONFIG ITEMS
// ============================================================================

func (s *sqlStore) ListItemsByKind(ctx context.Context, kind models.ItemKind) ([]models.ConfigItemRow, error) {
	return s.listItems(ctx, "WHERE kind = ? ORDER BY name", kind)
}
func (s *sqlStore) ListAllItems(ctx context.Context) ([]models.ConfigItemRow, error) {
	return s.listItems(ctx, "ORDER BY kind, name")
}
func (s *sqlStore) listItems(ctx context.Context, suffix string, args ...any) ([]models.ConfigItemRow, error) {
	rows, e := s.db.QueryxContext(ctx, "SELECT "+itemColumns+" FROM config_items "+suffix, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := make([]models.ConfigItemRow, 0)
	for rows.Next() {
		v, e := scanItem(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *sqlStore) GetItem(ctx context.Context, k models.ItemKind, n string) (*models.ConfigItemRow, error) {
	v, e := scanItem(s.db.QueryRowxContext(ctx, "SELECT "+itemColumns+" FROM config_items WHERE kind=? AND name=?", k, n))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &v, nil
}

// Commit is everything one finished operation changes, applied in ONE SQLite
// transaction: either the rows, versions, hashes and operation status all land,
// or none of them do.
type Commit struct {
	Operation *models.OperationRow // ID plus final Status/Stage/ReasonCode/ErrorLog/RequiresRestart
	Mutation  models.Mutation
	// Replace, when set, swaps every item and secret for this state (restore).
	Replace *State
	// Settings are written verbatim (published file hashes, markers).
	Settings map[string]string
	// KeepJournal keeps the sealed preimages (cleanup_pending still needs them).
	KeepJournal bool
}

// CommitOperation applies a finished operation and returns the committed rows.
func (s *sqlStore) CommitOperation(ctx context.Context, c Commit) ([]models.ConfigItemRow, error) {
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var written []models.ConfigItemRow
	if c.Replace != nil {
		if written, e = replaceState(ctx, tx, c.Replace); e != nil {
			return nil, e
		}
	} else {
		if written, e = applyMutation(ctx, tx, c.Mutation); e != nil {
			return nil, e
		}
	}
	for key, value := range c.Settings {
		if _, e = tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now', 'utc')) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now', 'utc')`, key, value); e != nil {
			return nil, e
		}
	}
	if op := c.Operation; op != nil && op.ID != 0 {
		if _, e = tx.ExecContext(ctx, `UPDATE operations SET status=?,stage=?,reason_code=?,error_log=?,requires_restart=?,finished_at=CASE WHEN ?='cleanup_pending' THEN NULL ELSE datetime('now','utc') END WHERE id=?`, op.Status, op.Stage, op.ReasonCode, op.ErrorLog, op.RequiresRestart, op.Status, op.ID); e != nil {
			return nil, e
		}
		if !c.KeepJournal {
			if _, e = tx.ExecContext(ctx, `UPDATE operations SET static_yaml=x'',dynamic_yaml=x'',items_backup_json=x'',prior_static=NULL,prior_dynamic=NULL,prior_domains=NULL,prior_state=NULL,result_state=NULL WHERE id=?`, op.ID); e != nil {
				return nil, e
			}
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return written, nil
}

func applyMutation(ctx context.Context, tx *sqlx.Tx, m models.Mutation) ([]models.ConfigItemRow, error) {
	written := make([]models.ConfigItemRow, 0, len(m.Upserts))
	for _, ref := range m.Deletes {
		if _, e := tx.ExecContext(ctx, `DELETE FROM config_items WHERE kind=? AND name=?`, ref.Kind, ref.Name); e != nil {
			return nil, e
		}
	}
	for _, v := range m.Upserts {
		if v.ID == "" {
			v.ID = fmt.Sprintf("%s:%s", v.Kind, v.Name)
		}
		var version int64
		e := tx.GetContext(ctx, &version, `SELECT version FROM config_items WHERE kind=? AND name=?`, v.Kind, v.Name)
		switch {
		case errors.Is(e, sql.ErrNoRows):
			v.Version = 1
			_, e = tx.NamedExecContext(ctx, `INSERT INTO config_items(id,kind,name,scope,payload,version) VALUES(:id,:kind,:name,:scope,:payload,:version)`, v)
		case e == nil:
			v.Version = version + 1
			_, e = tx.NamedExecContext(ctx, `UPDATE config_items SET scope=:scope,payload=:payload,version=:version,updated_at=datetime('now','utc') WHERE kind=:kind AND name=:name`, v)
		}
		if e != nil {
			return nil, e
		}
		written = append(written, v)
	}
	for _, v := range m.Secrets {
		if len(v.EncryptedValue) == 0 {
			return nil, fmt.Errorf("empty secret value")
		}
		if _, e := tx.NamedExecContext(ctx, `INSERT INTO secrets(name,purpose,env_var_name,file_path,masked_preview,encrypted_value) VALUES(:name,:purpose,:env_var_name,:file_path,:masked_preview,:encrypted_value) ON CONFLICT(name) DO UPDATE SET purpose=excluded.purpose,env_var_name=excluded.env_var_name,file_path=excluded.file_path,masked_preview=excluded.masked_preview,encrypted_value=excluded.encrypted_value,updated_at=datetime('now','utc')`, v); e != nil {
			return nil, e
		}
	}
	for _, name := range m.SecretDeletes {
		if _, e := tx.ExecContext(ctx, `DELETE FROM secrets WHERE name=?`, name); e != nil {
			return nil, e
		}
	}
	return written, nil
}

// ============================================================================
// 2. SECRETS
// ============================================================================

func (s *sqlStore) GetSecret(ctx context.Context, name string) (*models.SecretRow, error) {
	var sec models.SecretRow
	err := s.db.GetContext(ctx, &sec, `SELECT name,purpose,env_var_name,file_path,masked_preview,encrypted_value,updated_at FROM secrets WHERE name = ?`, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &sec, err
}

func (s *sqlStore) ListSecrets(ctx context.Context) ([]models.SecretRow, error) {
	secrets := make([]models.SecretRow, 0)
	err := s.db.SelectContext(ctx, &secrets, `SELECT name,purpose,env_var_name,file_path,masked_preview,encrypted_value,updated_at FROM secrets ORDER BY name ASC`)
	return secrets, err
}

// CountLegacyArchive reports what the 0005 migration archived, so the UI can say so once.
func (s *sqlStore) CountLegacyArchive(ctx context.Context) (int, int, error) {
	var drafts, secretDrafts int
	if e := s.db.GetContext(ctx, &drafts, `SELECT COUNT(*) FROM legacy_drafts`); e != nil {
		return 0, 0, e
	}
	if e := s.db.GetContext(ctx, &secretDrafts, `SELECT COUNT(*) FROM legacy_secret_drafts`); e != nil {
		return 0, 0, e
	}
	return drafts, secretDrafts, nil
}

// ============================================================================
// 5. SETTINGS & PLUGIN CATALOG CACHE
// ============================================================================

func (s *sqlStore) GetSetting(ctx context.Context, key string) (string, error) {
	var val string
	err := s.db.GetContext(ctx, &val, `SELECT value FROM settings WHERE key = ?`, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return val, err
}

func (s *sqlStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES (?, ?, datetime('now', 'utc'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now', 'utc')
	`, key, value)
	return err
}

func (s *sqlStore) ReplacePluginCatalog(ctx context.Context, plugins []models.CatalogPluginRow) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func(tx *sqlx.Tx) {
		_ = tx.Rollback()
	}(tx)

	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_catalog_cache`); err != nil {
		return err
	}

	insert := `
	INSERT INTO plugin_catalog_cache (module_name, display_name, summary, latest_version, snippet_yaml, fetched_at, versions_json, plugin_type, module_aliases_json)
	VALUES (:module_name, :display_name, :summary, :latest_version, :snippet_yaml, datetime('now', 'utc'), :versions_json, :plugin_type, :module_aliases_json)`

	for _, p := range plugins {
		if _, err := tx.NamedExecContext(ctx, insert, p); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE plugin_catalog_refresh SET last_success_at=datetime('now','utc'), last_attempt_at=datetime('now','utc'), last_attempt_failed=0 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) SearchPluginCatalog(ctx context.Context, query string) ([]models.CatalogPluginRow, error) {
	plugins := make([]models.CatalogPluginRow, 0)
	if query == "" {
		err := s.db.SelectContext(ctx, &plugins, `SELECT * FROM plugin_catalog_cache ORDER BY display_name ASC LIMIT 500`)
		return plugins, err
	}

	like := "%" + query + "%"
	err := s.db.SelectContext(ctx, &plugins, `
		SELECT * FROM plugin_catalog_cache 
		WHERE display_name LIKE ? OR module_name LIKE ? OR summary LIKE ?
		ORDER BY display_name ASC LIMIT 500`, like, like, like)
	return plugins, err
}
