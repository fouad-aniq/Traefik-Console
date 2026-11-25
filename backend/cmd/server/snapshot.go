package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/core/crowdsec"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/traefik/status"
	"github.com/traefik-console/internal/traefik/validation"
)

// runCLISnapshot implements `server snapshot list|create <name>|restore <id> [--no-backup]`
// and its `rollback` alias (restore the latest known-good snapshot, else the latest manual one).
func runCLISnapshot(args []string, rollbackAlias bool) error {
	if !rollbackAlias && len(args) == 0 {
		return fmt.Errorf("usage: server snapshot list | create <name> | restore <id> [--no-backup]")
	}
	opts, runtimeClient, err := loadOptions()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	defer runtimeClient.Close()

	state, err := openState(opts)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer state.Close()

	store := state.store
	filesMgr, err := files.New(opts.TraefikStaticPath, opts.TraefikDynamicDir)
	if err != nil {
		return fmt.Errorf("configuration files: %w", err)
	}
	secretsMgr, err := secrets.New(opts.SecretsDir, opts.RuntimeSecretsDir, state.key, store)
	if err != nil {
		return fmt.Errorf("runtime secrets: %w", err)
	}
	inspector := status.NewObservationClient(opts.TraefikAPIURL)
	defer inspector.Close()

	ops := newOperations(opts, store, filesMgr, secretsMgr, inspector, validation.NewConfigValidator(), runtimeClient)
	timeoutBudget := engine.NewTimeoutBudget(opts.VerifyConvergenceTo)
	ctx, cancel := context.WithTimeout(context.Background(), timeoutBudget.HTTPWrite)
	defer cancel()

	if err := prepareOperations(ctx, store, ops); err != nil {
		diagLog.Errorf("startup: %v", err)
	}
	verifier, err := crowdsec.NewCrowdSecManagerWithOptions(store, secretsMgr, runtimeClient, opts.CrowdSec.LAPIURL, integrationSettings(opts), ops, opts.ProtectedCIDRs)
	if err != nil {
		return fmt.Errorf("core recovery capabilities unavailable")
	}
	ops.SetBouncerVerifier(verifier)

	if rollbackAlias {
		latest, err := store.LatestSnapshot(ctx, true)
		if err == nil && latest == nil {
			latest, err = store.LatestSnapshot(ctx, false)
		}
		if err != nil {
			return fmt.Errorf("failed to query snapshots: %v", err)
		}
		if latest == nil {
			return fmt.Errorf("no snapshot found to restore")
		}
		return restoreSnapshot(ctx, ops, latest.ID, args)
	}

	switch args[0] {
	case "list":
		rows, err := store.ListSnapshots(ctx, 100, 0)
		if err != nil {
			return err
		}
		for _, s := range rows {
			flags := ""
			if s.Pinned {
				flags += " pinned"
			}
			if s.KnownGood {
				flags += " known-good"
			}
			cliOut.Infof("#%d\t%s\t%s\t%s%s", s.ID, s.CreatedAt, s.Origin, s.Name, flags)
		}
		return nil
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: server snapshot create <name>")
		}
		row, err := ops.CreateSnapshot(ctx, strings.Join(args[1:], " "), "")
		if err != nil {
			return fmt.Errorf("snapshot failed: %v", err)
		}
		cliOut.Infof("created snapshot #%d (%s)", row.ID, row.Name)
		return nil
	case "restore":
		if len(args) < 2 {
			return fmt.Errorf("usage: server snapshot restore <id> [--no-backup]")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid snapshot id %q", args[1])
		}
		return restoreSnapshot(ctx, ops, id, args[2:])
	default:
		return fmt.Errorf("unknown snapshot command %q; usage: server snapshot list | create <name> | restore <id> [--no-backup]", args[0])
	}
}

func restoreSnapshot(ctx context.Context, ops interface {
	PreviewRestore(context.Context, int64) (*models.OperationPreview, error)
	RestoreSnapshot(context.Context, int64, models.RestoreRequest) (*models.RestoreResult, error)
}, id int64, flags []string) error {
	skip := false
	for _, f := range flags {
		if f != "--no-backup" {
			return fmt.Errorf("unknown option %q", f)
		}
		skip = true
	}
	preview, err := ops.PreviewRestore(ctx, id)
	if err != nil {
		return fmt.Errorf("restore preview failed: %v", err)
	}
	res, err := ops.RestoreSnapshot(ctx, id, models.RestoreRequest{
		RestoreToken:   preview.RestoreToken,
		IdempotencyKey: fmt.Sprintf("offline-restore-%d", time.Now().UnixNano()),
		SkipBackup:     skip,
	})
	if err != nil {
		return fmt.Errorf("restore failed: %v", err)
	}
	cliOut.Infof("restored snapshot #%d", id)
	if res.BackupSnapshotID != nil {
		cliOut.Infof("previous state saved as snapshot #%d", *res.BackupSnapshotID)
	}
	if res.Operation != nil && res.Operation.Status == models.OpStatusRestartRequired {
		fmt.Fprintln(os.Stderr, "restart Traefik manually to load the restored static configuration")
	}
	return nil
}
