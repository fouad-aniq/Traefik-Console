package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

type startupStore struct {
	db.Store
	importErr error
}

func (s startupStore) ImportLegacySnapshots(context.Context) (int, error) { return 0, s.importErr }

type startupOps struct {
	engine.Operations
	calls                        *[]string
	recoverErr, seedErr, initErr error
}

func (o startupOps) RecoverOnStartup(context.Context) error {
	*o.calls = append(*o.calls, "recover")
	return o.recoverErr
}
func (o startupOps) SeedPublishedHashes(context.Context) error {
	*o.calls = append(*o.calls, "seed")
	return o.seedErr
}
func (o startupOps) EnsureInitialSnapshot(context.Context) (*models.SnapshotRow, error) {
	*o.calls = append(*o.calls, "initial")
	return nil, o.initErr
}

func TestServerStartupTakesInitialSnapshotLast(t *testing.T) {
	var calls []string
	if err := prepareServer(context.Background(), startupStore{}, startupOps{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "recover,seed,initial" {
		t.Fatal("startup order", calls)
	}
}

func TestSnapshotCLIPreparationNeverTakesTheInitialSnapshot(t *testing.T) {
	var calls []string
	if err := prepareOperations(context.Background(), startupStore{}, startupOps{calls: &calls}); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c == "initial" {
			t.Fatal("a CLI rollback would record the state it rolls back as a baseline")
		}
	}
}

func TestServerStartupSkipsInitialSnapshotWhenPreparationReportsAProblem(t *testing.T) {
	for name, tc := range map[string]struct {
		store db.Store
		ops   func(*[]string) startupOps
	}{
		"recovery pending": {startupStore{}, func(c *[]string) startupOps {
			return startupOps{calls: c, recoverErr: engine.ErrOperationRecoveryRequired}
		}},
		"baseline not recorded": {startupStore{}, func(c *[]string) startupOps { return startupOps{calls: c, seedErr: errors.New("unreadable files")} }},
		"legacy import failed":  {startupStore{importErr: errors.New("corrupt journal")}, func(c *[]string) startupOps { return startupOps{calls: c} }},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			if err := prepareServer(context.Background(), tc.store, tc.ops(&calls)); err == nil {
				t.Fatal("problem not reported")
			}
			for _, c := range calls {
				if c == "initial" {
					t.Fatal("initial snapshot taken despite a startup problem", calls)
				}
			}
		})
	}
}

func TestServerStartupReportsInitialSnapshotFailure(t *testing.T) {
	var calls []string
	err := prepareServer(context.Background(), startupStore{}, startupOps{calls: &calls, initErr: db.ErrSnapshotQuota})
	if !errors.Is(err, db.ErrSnapshotQuota) || !strings.Contains(err.Error(), "initial snapshot") {
		t.Fatal(err)
	}
}
