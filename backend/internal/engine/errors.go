package engine

import (
	"errors"
	"fmt"

	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/models"
)

var (
	ErrDeployInProgress  = errors.New("a configuration operation is already in progress")
	ErrValidationFailed  = errors.New("configuration failed pre-flight validation")
	ErrRollbackTriggered = errors.New("traefik rejected the new configuration; automatic rollback was executed")
	ErrRollbackFailed    = errors.New("critical: traefik failed to converge and automatic rollback encountered an error")
	ErrSnapshotNotFound  = errors.New("requested snapshot was not found")
	ErrOperationNotFound = errors.New("requested operation was not found")
)

var (
	ErrPreconditionRequired = errors.New("an If-Match version, If-None-Match: * or other required precondition is missing")
	ErrRecoveryRequired     = errors.New("operation requires recovery before further changes")
	ErrRecreateRequired     = errors.New("required credential environment/mounts are not confirmed; recreate the approved container using the reviewed mappings")

	// ErrVersionConflict: the resource changed since the client read it.
	ErrVersionConflict = errors.New("resource changed since it was read; reload it and retry")
	// ErrConfigDrift: a managed file differs from what the console last published.
	ErrConfigDrift = errors.New("managed configuration files changed outside the console; review and adopt them first")
	// ErrOperationRecoveryRequired: an earlier operation could not be reverted; recover or abandon it first.
	ErrOperationRecoveryRequired = errors.New("an earlier operation needs recovery; recover or abandon it before making changes")
	// ErrSnapshotQuota re-exports the persistence quota error for HTTP mapping.
	ErrSnapshotQuota = db.ErrSnapshotQuota
	// ErrRestoreStale: the system changed since the restore preview was produced.
	ErrRestoreStale = errors.New("configuration changed since the restore preview; preview the restore again")
	// ErrSnapshotPinned: pinned snapshots need an explicit force to delete.
	ErrSnapshotPinned = errors.New("snapshot is pinned; unpin it or delete with force")
	// ErrManualRestartRequired is reported (HTTP 202) when a static change was saved but Traefik could not be restarted.
	ErrManualRestartRequired = errors.New("The change is saved, but the console could not restart Traefik. Restart Traefik manually to load it.")
	// ErrOperationNotRecoverable: the operation is not in recovery_required.
	ErrOperationNotRecoverable = errors.New("operation is not waiting for recovery")
)

// ValidationFailure carries the per-resource problems that blocked a write.
type ValidationFailure struct{ Errors []models.ValidationError }

func (e *ValidationFailure) Error() string        { return ErrValidationFailed.Error() }
func (e *ValidationFailure) Is(target error) bool { return target == ErrValidationFailed }

// ErrPluginModuleInstalled rejects a second plugin declaration for a module that is already installed
// under another alias (managed, declared in traefik.yml, or in the same operation).
var ErrPluginModuleInstalled = errors.New("plugin module is already installed under another alias")

// PluginModuleError names the alias that already provides the module so the operator can reuse it.
type PluginModuleError struct{ Module, Alias string }

func (e *PluginModuleError) Error() string {
	return fmt.Sprintf("plugin module %s is already installed as %q", e.Module, e.Alias)
}
func (e *PluginModuleError) Is(target error) bool { return target == ErrPluginModuleInstalled }
