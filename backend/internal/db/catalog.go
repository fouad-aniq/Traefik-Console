package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/traefik-console/internal/models"
)

// ReadPluginCatalogSnapshot captures bounded metadata and freshness in one transaction.
func (s *sqlStore) ReadPluginCatalogSnapshot(ctx context.Context) (*models.PluginCatalogSnapshot, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func(tx *sqlx.Tx) {
		_ = tx.Rollback()
	}(tx)

	out := &models.PluginCatalogSnapshot{Items: []models.CatalogPluginRow{}}
	if err = tx.SelectContext(ctx, &out.Items, `SELECT * FROM plugin_catalog_cache ORDER BY display_name, module_name LIMIT 10001`); err != nil {
		return nil, err
	}

	if len(out.Items) > 10000 {
		return nil, fmt.Errorf("plugin catalog exceeds inventory limit")
	}

	if err = tx.GetContext(ctx, &out.Refresh, `SELECT last_attempt_at,last_success_at,last_attempt_failed FROM plugin_catalog_refresh WHERE id=1`); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// Persist incomplete attempts too, avoiding a retry storm after failure or restart.
func (s *sqlStore) ClaimPluginCatalogRefresh(ctx context.Context, now time.Time, interval time.Duration) (bool, error) {
	cutoff := now.UTC().Add(-interval).Format("2006-01-02 15:04:05")
	result, err := s.db.ExecContext(ctx, `UPDATE plugin_catalog_refresh SET last_attempt_at=?,last_attempt_failed=1 WHERE id=1 AND (last_attempt_at='' OR last_attempt_at<=?)`, now.UTC().Format("2006-01-02 15:04:05"), cutoff)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ReadPluginCatalogPage uses one read transaction so rows/count/freshness agree.
// SQL fragments are selected by server-owned enums; all input values are bound.
func (s *sqlStore) ReadPluginCatalogPage(ctx context.Context, query, module string, limit, offset int, descending bool) (*models.CatalogPage, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return nil, fmt.Errorf("invalid catalog page bounds")
	}
	where := ""
	args := []any{}
	if module != "" {
		where = " WHERE module_name=?"
		args = append(args, module)
	} else if query != "" {
		where = " WHERE display_name LIKE ? OR module_name LIKE ? OR summary LIKE ?"
		for range 3 {
			args = append(args, "%"+query+"%")
		}
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	page := &models.CatalogPage{Items: []models.CatalogPluginRow{}, Limit: limit, Offset: offset}
	if err = tx.GetContext(ctx, &page.Total, "SELECT count(*) FROM plugin_catalog_cache"+where, args...); err != nil {
		return nil, err
	}
	if err = tx.GetContext(ctx, &page.LastSyncedAt, "SELECT COALESCE(MAX(fetched_at),'') FROM plugin_catalog_cache"); err != nil {
		return nil, err
	}
	order := " ASC"
	if descending {
		order = " DESC"
	}
	values := append(append([]any{}, args...), limit, offset)
	if err = tx.SelectContext(ctx, &page.Items, "SELECT * FROM plugin_catalog_cache"+where+" ORDER BY display_name"+order+", module_name"+order+" LIMIT ? OFFSET ?", values...); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return page, nil
}
