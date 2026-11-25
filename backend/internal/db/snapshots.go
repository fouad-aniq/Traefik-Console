package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	domainfile "github.com/traefik-console/internal/config/domains"
	"github.com/traefik-console/internal/models"
)

// SnapshotStore persists user snapshots: named restore points that hold the
// full console state plus the exact bytes of the three managed files.
type SnapshotStore interface {
	CreateSnapshot(context.Context, *models.SnapshotRow) (int64, error)
	GetSnapshot(context.Context, int64) (*models.SnapshotRow, error)
	GetSnapshotMeta(context.Context, int64) (*models.SnapshotRow, error)
	ListSnapshots(ctx context.Context, limit int, cursor int64) ([]models.SnapshotRow, error)
	UpdateSnapshot(context.Context, int64, models.SnapshotPatch) (*models.SnapshotRow, error)
	DeleteSnapshot(context.Context, int64) error
	LatestSnapshot(ctx context.Context, knownGoodOnly bool) (*models.SnapshotRow, error)
	PruneAutomaticSnapshots(ctx context.Context, keep int) (int64, error)
	ImportLegacySnapshots(context.Context) (int, error)
}

const snapshotMetaColumns = "id,name,description,origin,pinned,known_good,static_hash,dynamic_hash,domains_hash,state_hash,item_count,size_bytes,source_operation_id,created_at"
const snapshotColumns = snapshotMetaColumns + ",static_yaml,dynamic_yaml,domains_yaml,state"

func scanSnapshotMeta(row interface{ Scan(...any) error }, extra ...any) (*models.SnapshotRow, error) {
	var v models.SnapshotRow
	var source sql.NullInt64
	dest := append([]any{&v.ID, &v.Name, &v.Description, &v.Origin, &v.Pinned, &v.KnownGood, &v.StaticHash, &v.DynamicHash, &v.DomainsHash, &v.StateHash, &v.ItemCount, &v.SizeBytes, &source, &v.CreatedAt}, extra...)
	if e := row.Scan(dest...); e != nil {
		return nil, e
	}
	if source.Valid {
		v.SourceOperationID = &source.Int64
	}
	return &v, nil
}

func (s *sqlStore) scanSnapshot(row interface{ Scan(...any) error }) (*models.SnapshotRow, error) {
	var st, dy, dom, state []byte
	v, e := scanSnapshotMeta(row, &st, &dy, &dom, &state)
	if e != nil {
		return nil, e
	}
	for _, t := range []struct {
		src []byte
		dst *[]byte
	}{{st, &v.StaticYAML}, {dy, &v.DynamicYAML}, {dom, &v.DomainsYAML}, {state, &v.State}} {
		if *t.dst, e = s.open(t.src); e != nil {
			return nil, fmt.Errorf("snapshot authentication failed")
		}
	}
	return v, nil
}

// CreateSnapshot seals the content and enforces MAX_SNAPSHOT_BYTES. The caller supplies
// hashes and counts; the store computes the stored size.
func (s *sqlStore) CreateSnapshot(ctx context.Context, v *models.SnapshotRow) (int64, error) {
	if v.Origin == "" {
		v.Origin = models.SnapshotManual
	}
	sealed := make([][]byte, 4)
	var incoming int64
	for i, raw := range [][]byte{v.StaticYAML, v.DynamicYAML, v.DomainsYAML, v.State} {
		if raw == nil {
			raw = []byte{}
		}
		b, e := s.seal(raw)
		if e != nil {
			return 0, e
		}
		sealed[i] = b
		incoming += int64(len(b))
	}
	var stored int64
	if e := s.db.GetContext(ctx, &stored, `SELECT COALESCE(SUM(size_bytes),0) FROM snapshots`); e != nil {
		return 0, e
	}
	if incoming > s.maxSnapshotBytes || stored > s.maxSnapshotBytes-incoming {
		return 0, ErrSnapshotQuota
	}
	v.SizeBytes = incoming
	r, e := s.db.ExecContext(ctx, `INSERT INTO snapshots(name,description,origin,pinned,known_good,static_yaml,dynamic_yaml,domains_yaml,state,static_hash,dynamic_hash,domains_hash,state_hash,item_count,size_bytes,source_operation_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.Name, v.Description, v.Origin, v.Pinned, v.KnownGood, sealed[0], sealed[1], sealed[2], sealed[3], v.StaticHash, v.DynamicHash, v.DomainsHash, v.StateHash, v.ItemCount, v.SizeBytes, v.SourceOperationID)
	if e != nil {
		return 0, e
	}
	v.ID, e = r.LastInsertId()
	return v.ID, e
}

func (s *sqlStore) GetSnapshot(ctx context.Context, id int64) (*models.SnapshotRow, error) {
	v, e := s.scanSnapshot(s.db.QueryRowxContext(ctx, "SELECT "+snapshotColumns+" FROM snapshots WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return v, e
}

func (s *sqlStore) GetSnapshotMeta(ctx context.Context, id int64) (*models.SnapshotRow, error) {
	v, e := scanSnapshotMeta(s.db.QueryRowxContext(ctx, "SELECT "+snapshotMetaColumns+" FROM snapshots WHERE id=?", id))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return v, e
}

// ListSnapshots pages newest first. cursor is the last id of the previous page (0 = first page).
func (s *sqlStore) ListSnapshots(ctx context.Context, limit int, cursor int64) ([]models.SnapshotRow, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	query := "SELECT " + snapshotMetaColumns + " FROM snapshots"
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
	out := []models.SnapshotRow{}
	for rows.Next() {
		v, e := scanSnapshotMeta(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *sqlStore) UpdateSnapshot(ctx context.Context, id int64, p models.SnapshotPatch) (*models.SnapshotRow, error) {
	sets, args := []string{}, []any{}
	if p.Name != nil {
		sets, args = append(sets, "name=?"), append(args, *p.Name)
	}
	if p.Description != nil {
		sets, args = append(sets, "description=?"), append(args, *p.Description)
	}
	if p.Pinned != nil {
		sets, args = append(sets, "pinned=?"), append(args, *p.Pinned)
	}
	if len(sets) > 0 {
		r, e := s.db.ExecContext(ctx, "UPDATE snapshots SET "+strings.Join(sets, ",")+" WHERE id=?", append(args, id)...)
		if e != nil {
			return nil, e
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return nil, nil
		}
	}
	return s.GetSnapshotMeta(ctx, id)
}

func (s *sqlStore) DeleteSnapshot(ctx context.Context, id int64) error {
	r, e := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE id=?`, id)
	if e != nil {
		return e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// LatestSnapshot returns the newest manual, initial or legacy snapshot (the ones a CLI rollback may pick).
func (s *sqlStore) LatestSnapshot(ctx context.Context, knownGoodOnly bool) (*models.SnapshotRow, error) {
	query := "SELECT " + snapshotColumns + " FROM snapshots WHERE origin IN ('manual','initial','legacy_import','pre_restore','pre_static','scheduled','adopted')"
	if knownGoodOnly {
		query += " AND known_good=1"
	} else {
		query += " AND origin IN ('manual','initial','legacy_import')"
	}
	v, e := s.scanSnapshot(s.db.QueryRowxContext(ctx, query+" ORDER BY id DESC LIMIT 1"))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return v, e
}

// PruneAutomaticSnapshots keeps the newest `keep` unpinned automatic snapshots. Manual,
// pinned and imported snapshots are never pruned.
func (s *sqlStore) PruneAutomaticSnapshots(ctx context.Context, keep int) (int64, error) {
	if keep < 0 {
		return 0, nil
	}
	r, e := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE pinned=0 AND origin IN ('pre_restore','pre_static','scheduled','adopted') AND id NOT IN (SELECT id FROM snapshots WHERE pinned=0 AND origin IN ('pre_restore','pre_static','scheduled','adopted') ORDER BY id DESC LIMIT ?)`, keep)
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

// ImportLegacySnapshots converts the retired apply journal into restore points.
// It is idempotent (guarded by the legacy_snapshots_imported setting) and needs
// the master key because legacy states are sealed.
func (s *sqlStore) ImportLegacySnapshots(ctx context.Context) (int, error) {
	done, e := s.GetSetting(ctx, "legacy_snapshots_imported")
	if e != nil || done == "1" {
		return 0, e
	}
	rows, e := s.db.QueryxContext(ctx, `SELECT id,summary,static_yaml,dynamic_yaml,result_state,candidate_hash,created_at FROM operations WHERE kind='legacy_apply' AND status IN ('working','saved_unverified') AND candidate_hash<>'' AND result_state IS NOT NULL ORDER BY id`)
	if e != nil {
		return 0, e
	}
	type legacy struct {
		id                      int64
		summary, hash, created  string
		static, dynamic, result []byte
	}
	var found []legacy
	for rows.Next() {
		var l legacy
		if e = rows.Scan(&l.id, &l.summary, &l.static, &l.dynamic, &l.result, &l.hash, &l.created); e != nil {
			rows.Close()
			return 0, e
		}
		found = append(found, l)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	imported := 0
	for _, l := range found {
		static, e1 := s.open(l.static)
		dynamic, e2 := s.open(l.dynamic)
		raw, e3 := s.open(l.result)
		if e1 != nil || e2 != nil || e3 != nil || len(raw) == 0 {
			continue // A row that cannot be authenticated is never imported.
		}
		var state State
		if json.Unmarshal(raw, &state) != nil {
			continue
		}
		items := make([]models.ConfigItemRow, 0, len(state.Items))
		for _, item := range state.Items {
			if len(item.Payload) > 0 {
				items = append(items, item)
			}
		}
		secrets := make([]SecretState, 0, len(state.Secrets))
		for _, sec := range state.Secrets {
			if len(sec.Active) > 0 {
				secrets = append(secrets, sec)
			}
		}
		clean := State{Items: items, Secrets: secrets}
		domains, e := domainfile.Render(items)
		if e != nil {
			continue
		}
		stateBytes, e := json.Marshal(clean)
		if e != nil {
			continue
		}
		id := l.id
		row := &models.SnapshotRow{Name: fmt.Sprintf("Apply #%d: %s", l.id, l.summary), Origin: models.SnapshotLegacyImport, StaticYAML: static, DynamicYAML: dynamic, DomainsYAML: domains, State: stateBytes, StaticHash: HashBytes(static), DynamicHash: HashBytes(dynamic), DomainsHash: HashBytes(domains), StateHash: HashBytes(stateBytes), ItemCount: len(items), SourceOperationID: &id}
		if _, e = s.CreateSnapshot(ctx, row); e != nil {
			if errors.Is(e, ErrSnapshotQuota) {
				break
			}
			return imported, e
		}
		if _, e = s.db.ExecContext(ctx, `UPDATE snapshots SET created_at=? WHERE id=?`, l.created, row.ID); e != nil {
			return imported, e
		}
		imported++
	}
	return imported, s.SetSetting(ctx, "legacy_snapshots_imported", "1")
}
