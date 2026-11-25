package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/traefik-console/internal/models"
)

// Statuses that still own side effects. Startup recovery walks them.
var inFlightStatuses = []string{models.OpStatusRunning, "deploying", models.OpStatusRestoring, models.OpStatusCleanupPending, "applied_unobserved"}

const operationColumns = "id,kind,target,status,summary,requires_restart,stage,reason_code,static_yaml,dynamic_yaml,COALESCE(error_log,'') AS error_log,created_at,finished_at,request_key,candidate_hash,prior_static,prior_dynamic,prior_domains,prior_state"

// listColumns omit the sealed blobs: history pages never need them.
const operationListColumns = "id,kind,target,status,summary,requires_restart,stage,reason_code,COALESCE(error_log,'') AS error_log,created_at,finished_at,request_key,candidate_hash"

func (s *sqlStore) scanOperation(row interface{ Scan(...any) error }) (*models.OperationRow, error) {
	var v models.OperationRow
	var st, dy, ps, pd, pdom, pstate []byte
	var finished sql.NullString
	if e := row.Scan(&v.ID, &v.Kind, &v.Target, &v.Status, &v.Summary, &v.RequiresRestart, &v.Stage, &v.ReasonCode, &st, &dy, &v.ErrorLog, &v.CreatedAt, &finished, &v.RequestKey, &v.MutationHash, &ps, &pd, &pdom, &pstate); e != nil {
		return nil, e
	}
	if finished.Valid {
		v.FinishedAt = &finished.String
	}
	// Rows written before the journal existed cannot prove matching state: metadata only.
	if v.MutationHash == "" {
		return &v, nil
	}
	v.Journaled = true
	targets := []struct {
		src []byte
		dst *[]byte
	}{{st, &v.StaticYAML}, {dy, &v.DynamicYAML}, {ps, &v.PriorStatic}, {pd, &v.PriorDynamic}, {pdom, &v.PriorDomains}, {pstate, &v.PriorState}}
	for _, t := range targets {
		var e error
		if *t.dst, e = s.open(t.src); e != nil {
			return nil, fmt.Errorf("journal authentication failed")
		}
	}
	return &v, nil
}

func scanOperationMeta(row interface{ Scan(...any) error }) (*models.OperationRow, error) {
	var v models.OperationRow
	var finished sql.NullString
	if e := row.Scan(&v.ID, &v.Kind, &v.Target, &v.Status, &v.Summary, &v.RequiresRestart, &v.Stage, &v.ReasonCode, &v.ErrorLog, &v.CreatedAt, &finished, &v.RequestKey, &v.MutationHash); e != nil {
		return nil, e
	}
	if finished.Valid {
		v.FinishedAt = &finished.String
	}
	return &v, nil
}

// CreateOperation journals the intent and its sealed preimages BEFORE any side effect.
func (s *sqlStore) CreateOperation(ctx context.Context, v *models.OperationRow) (int64, error) {
	values := [][]byte{v.StaticYAML, v.DynamicYAML, v.PriorStatic, v.PriorDynamic, v.PriorDomains, v.PriorState}
	var incoming int64 = int64(len(v.Summary))
	for i := range values {
		var e error
		if values[i], e = s.seal(values[i]); e != nil {
			return 0, e
		}
		incoming += int64(len(values[i]))
	}
	var stored int64
	if e := s.db.GetContext(ctx, &stored, `SELECT COALESCE(SUM(length(static_yaml)+length(dynamic_yaml)+COALESCE(length(prior_state),0)+COALESCE(length(prior_static),0)+COALESCE(length(prior_dynamic),0)+COALESCE(length(prior_domains),0)+length(summary)+COALESCE(length(error_log),0)),0) FROM operations`); e != nil {
		return 0, e
	}
	if incoming > s.maxJournalBytes || stored > s.maxJournalBytes-incoming {
		return 0, ErrJournalCapacity
	}
	nz := func(b []byte) []byte {
		if b == nil {
			return []byte{}
		}
		return b
	}
	r, e := s.db.ExecContext(ctx, `INSERT INTO operations(kind,target,status,summary,requires_restart,stage,reason_code,static_yaml,dynamic_yaml,items_backup_json,error_log,request_key,candidate_hash,prior_static,prior_dynamic,prior_domains,prior_state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.Kind, v.Target, v.Status, v.Summary, v.RequiresRestart, v.Stage, v.ReasonCode, nz(values[0]), nz(values[1]), []byte{}, v.ErrorLog, v.RequestKey, v.MutationHash, values[2], values[3], values[4], values[5])
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}

// UpdateOperation records progress or a terminal state. Preimages stay until PruneOperationBlobs.
func (s *sqlStore) UpdateOperation(ctx context.Context, id int64, status, stage, reasonCode, errorLog string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE operations SET status=?,stage=?,reason_code=?,error_log=?,finished_at=CASE WHEN ? IN ('running','deploying','restoring','cleanup_pending','recovery_required','applied_unobserved') THEN NULL ELSE datetime('now','utc') END WHERE id=?`, status, stage, reasonCode, errorLog, status, id)
	return e
}

// PruneOperationBlobs drops the sealed preimages of a finished operation. The metadata stays as the audit trail.
func (s *sqlStore) PruneOperationBlobs(ctx context.Context, id int64) error {
	_, e := s.db.ExecContext(ctx, `UPDATE operations SET static_yaml=x'',dynamic_yaml=x'',items_backup_json=x'',prior_static=NULL,prior_dynamic=NULL,prior_domains=NULL,prior_state=NULL,result_state=NULL WHERE id=?`, id)
	return e
}

func (s *sqlStore) GetOperation(ctx context.Context, id int64) (*models.OperationRow, error) {
	v, e := s.scanOperation(s.db.QueryRowxContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return v, e
}

func (s *sqlStore) FindOperationByKey(ctx context.Context, key string) (*models.OperationRow, error) {
	if key == "" {
		return nil, nil
	}
	v, e := s.scanOperation(s.db.QueryRowxContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE request_key=?", key))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return v, e
}

// PendingOperations returns operations that still own side effects (oldest first).
func (s *sqlStore) PendingOperations(ctx context.Context) ([]models.OperationRow, error) {
	rows, e := s.db.QueryxContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE status IN (?,?,?,?,?) ORDER BY id", toAny(inFlightStatuses)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []models.OperationRow{}
	for rows.Next() {
		v, e := s.scanOperation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// RecoveryOperations returns operations stuck in recovery_required (oldest first).
func (s *sqlStore) RecoveryOperations(ctx context.Context) ([]models.OperationRow, error) {
	rows, e := s.db.QueryxContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE status=? ORDER BY id", models.OpStatusRecovery)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []models.OperationRow{}
	for rows.Next() {
		v, e := s.scanOperation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// ListOperations pages the audit trail newest first. cursor is the last id of the previous page (0 = first page).
func (s *sqlStore) ListOperations(ctx context.Context, limit int, cursor int64) ([]models.OperationRow, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	query := "SELECT " + operationListColumns + " FROM operations"
	args := []any{}
	if cursor > 0 {
		query += " WHERE id < ?"
		args = append(args, cursor)
	}
	rows, e := s.db.QueryxContext(ctx, query+" ORDER BY id DESC LIMIT ?", append(args, limit)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []models.OperationRow{}
	for rows.Next() {
		v, e := scanOperationMeta(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// PruneOperations deletes finished operations older than the retention window.
func (s *sqlStore) PruneOperations(ctx context.Context, days int) (int64, error) {
	if days < 1 {
		return 0, nil
	}
	cutoff := fmt.Sprintf("-%d days", days)
	tx, e := s.db.BeginTxx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	const old = `status NOT IN ('running','deploying','restoring','cleanup_pending','recovery_required','applied_unobserved') AND created_at < datetime('now','utc',?)`
	if _, e = tx.ExecContext(ctx, `UPDATE snapshots SET source_operation_id=NULL WHERE source_operation_id IN (SELECT id FROM operations WHERE `+old+`)`, cutoff); e != nil {
		return 0, e
	}
	r, e := tx.ExecContext(ctx, `DELETE FROM operations WHERE `+old, cutoff)
	if e != nil {
		return 0, e
	}
	n, _ := r.RowsAffected()
	return n, tx.Commit()
}

func toAny(v []string) []any {
	out := make([]any, len(v))
	for i := range v {
		out[i] = v[i]
	}
	return out
}
