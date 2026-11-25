package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/traefik-console/internal/config"
	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

// newOperations builds the operation engine, the only writer of resources and managed files.
func newOperations(opts *config.Options, store db.Store, filesMgr *files.Repository, secretsMgr *secrets.Vault,
	inspector *status.ObservationClient, validator *validation.ConfigValidator, runtimeClient *docker.Client,
) engine.Operations {
	generator := engine.NewGenerator(inspector, validator, runtimeClient)
	return engine.NewOperations(store, filesMgr, secretsMgr, generator, inspector, runtimeClient, opts.VerifyConvergenceTo, engine.Options{
		AutoSnapshotBeforeStatic:   opts.AutoSnapshotBeforeStatic,
		AutomaticSnapshotRetention: opts.SnapshotRetentionAuto,
		OperationHistoryDays:       opts.OperationHistoryDays,
	})
}

// prepareOperations runs the startup steps after migrations: import the retired apply journal as
// snapshots (once), finish or revert interrupted operations, then record the published-file baseline.
// It is shared by the server and the snapshot CLI; the first-run snapshot is taken by the server only.
// A recovery error is returned for logging; the engine keeps refusing writes until it is resolved.
func prepareOperations(ctx context.Context, store db.Store, ops engine.Operations) error {
	var problems []error
	if _, err := store.ImportLegacySnapshots(ctx); err != nil {
		problems = append(problems, fmt.Errorf("legacy snapshot import: %w", err))
	}
	if err := ops.RecoverOnStartup(ctx); err != nil {
		if errors.Is(err, engine.ErrOperationRecoveryRequired) {
			problems = append(problems, fmt.Errorf("an operation requires recovery: %w", err))
		} else {
			return fmt.Errorf("startup recovery: %w", err)
		}
	}
	if err := ops.SeedPublishedHashes(ctx); err != nil {
		problems = append(problems, fmt.Errorf("published baseline: %w", err))
	}
	return errors.Join(problems...)
}

// prepareServer is the server's startup: prepareOperations, then the first-run snapshot. The
// snapshot CLI calls prepareOperations only: a rollback must never record the state it is
// rolling back as a baseline. No initial snapshot is taken when preparation reported a problem.
func prepareServer(ctx context.Context, store db.Store, ops engine.Operations) error {
	if err := prepareOperations(ctx, store, ops); err != nil {
		return err
	}
	if _, err := ops.EnsureInitialSnapshot(ctx); err != nil {
		return fmt.Errorf("initial snapshot: %w", err)
	}
	return nil
}
