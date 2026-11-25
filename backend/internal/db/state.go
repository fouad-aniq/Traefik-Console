package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/traefik-console/internal/models"
)

// State is private recovery/snapshot material, never an HTTP response.
// Credentials remain AEAD ciphertext even inside the encrypted journal.
//
// A snapshot stores only Items and Secrets. An operation's prior state also
// carries the file/runtime preimages needed to revert it.
type State struct {
	// Encrypted recovery preimages; never exported by the API.
	DomainFileTracked          bool
	DomainFileBefore           []byte
	DomainFileAfter            []byte
	Items                      []models.ConfigItemRow
	Secrets                    []SecretState
	Runtime                    map[string][]byte
	AttemptRuntime             map[string][]byte
	DesiredIntegration         []byte
	AttemptEnrollmentID        int64
	AttemptMachineEnrollmentID int64
	CleanupEnrollmentIDs       []int64
	CleanupMachineEnrollmentID int64
}

// SecretState holds one secret's metadata and its active ciphertext.
type SecretState struct {
	Metadata models.SecretRow
	Active   []byte
}

func (s *sqlStore) CaptureState(ctx context.Context) (*State, error) { return captureState(ctx, s.db) }

type stateQuery interface {
	QueryxContext(context.Context, string, ...any) (*sqlx.Rows, error)
	SelectContext(context.Context, any, string, ...any) error
}

const itemColumns = "id, kind, name, scope, payload, version, created_at, updated_at"

func scanItem(r interface{ Scan(...any) error }) (models.ConfigItemRow, error) {
	var v models.ConfigItemRow
	e := r.Scan(&v.ID, &v.Kind, &v.Name, &v.Scope, rawScanner{&v.Payload}, &v.Version, &v.CreatedAt, &v.UpdatedAt)
	return v, e
}

// captureState reads the committed items and secrets (there is no other kind).
func captureState(ctx context.Context, q stateQuery) (*State, error) {
	state := &State{Items: []models.ConfigItemRow{}, Secrets: []SecretState{}}
	rows, e := q.QueryxContext(ctx, "SELECT "+itemColumns+" FROM config_items ORDER BY kind,name")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		v, e := scanItem(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		state.Items = append(state.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	var secrets []models.SecretRow
	if e = q.SelectContext(ctx, &secrets, "SELECT name,purpose,env_var_name,file_path,masked_preview,encrypted_value,updated_at FROM secrets ORDER BY name"); e != nil {
		return nil, e
	}
	for _, v := range secrets {
		state.Secrets = append(state.Secrets, SecretState{Metadata: v, Active: v.EncryptedValue})
	}
	return state, nil
}

// ReplaceState replaces every config item and secret with the given state in one
// transaction. A row that already exists continues from its current version (+1)
// and a new row starts at 1, so a client holding an old If-Match never matches a
// payload it did not read.
func (s *sqlStore) ReplaceState(ctx context.Context, state *State) error {
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = replaceState(ctx, tx, state); e != nil {
		return e
	}
	return tx.Commit()
}

func replaceState(ctx context.Context, tx *sqlx.Tx, state *State) ([]models.ConfigItemRow, error) {
	current := map[string]int64{}
	rows, e := tx.QueryxContext(ctx, "SELECT kind,name,version FROM config_items")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var kind, name string
		var version int64
		if e = rows.Scan(&kind, &name, &version); e != nil {
			rows.Close()
			return nil, e
		}
		current[kind+":"+name] = version
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM config_items"); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM secrets"); e != nil {
		return nil, e
	}
	written := make([]models.ConfigItemRow, 0, len(state.Items))
	for _, v := range state.Items {
		if v.ID == "" {
			v.ID = fmt.Sprintf("%s:%s", v.Kind, v.Name)
		}
		v.Version = current[string(v.Kind)+":"+v.Name] + 1
		if _, e = tx.NamedExecContext(ctx, `INSERT INTO config_items(id,kind,name,scope,payload,version,created_at,updated_at) VALUES(:id,:kind,:name,:scope,:payload,:version,COALESCE(NULLIF(:created_at,''),datetime('now','utc')),datetime('now','utc'))`, v); e != nil {
			return nil, e
		}
		written = append(written, v)
	}
	for _, v := range state.Secrets {
		m := v.Metadata
		m.EncryptedValue = v.Active
		if m.EncryptedValue == nil {
			m.EncryptedValue = []byte{}
		}
		if _, e = tx.NamedExecContext(ctx, `INSERT INTO secrets(name,purpose,env_var_name,file_path,masked_preview,encrypted_value,updated_at) VALUES(:name,:purpose,:env_var_name,:file_path,:masked_preview,:encrypted_value,COALESCE(NULLIF(:updated_at,''),datetime('now','utc')))`, m); e != nil {
			return nil, e
		}
	}
	return written, nil
}

// HashBytes is the file/state fingerprint used by drift detection and snapshots.
// An absent or empty file hashes to "none".
func HashBytes(b []byte) string {
	if len(b) == 0 {
		return "none"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
