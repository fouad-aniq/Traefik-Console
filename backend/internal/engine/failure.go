package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/traefik/status"
)

// DeployFailure exports only controlled diagnostics. Raw dependency errors may
// contain credentials or file contents and never enter Error(), journal or API.
type DeployFailure struct {
	OperationID  int64
	Stage        string
	ReasonCode   string
	Reason       string
	RolledBack   bool
	RecoveryCode string
	cause        error
}

func (e *DeployFailure) Error() string {
	outcome := "Previous configuration restored."
	if !e.RolledBack {
		outcome = "Automatic recovery did not finish; recover or abandon the operation (" + e.RecoveryCode + ")."
	}
	return fmt.Sprintf("Apply failed at %s: %s [%s]. %s Operation %d.", e.Stage, e.Reason, e.ReasonCode, outcome, e.OperationID)
}
func (e *DeployFailure) Unwrap() []error {
	if e.RolledBack {
		return []error{ErrRollbackTriggered, e.cause}
	}
	return []error{ErrRollbackFailed, e.cause}
}

var (
	errConvergenceTimeout = errors.New("candidate did not converge before the deadline")
	errRuntimeRejected    = errors.New("runtime rejected a candidate resource")
)

// runtimeRejection is a candidate Traefik loaded but rejected or disabled, with Traefik's own
// message (one line, bounded) so the operator sees the actual cause.
type runtimeRejection struct{ detail string }

func (r *runtimeRejection) Error() string        { return errRuntimeRejected.Error() }
func (r *runtimeRejection) Is(target error) bool { return target == errRuntimeRejected }

// newRuntimeRejection keeps Traefik's message on one line and at most 300 characters.
func newRuntimeRejection(detail string) *runtimeRejection {
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	return &runtimeRejection{detail: detail}
}

func safeFailureReason(err error) (string, string) {
	for _, entry := range []struct {
		err        error
		code, text string
	}{
		{context.Canceled, "CANCELED", "The operation was canceled"},
		{context.DeadlineExceeded, "DEADLINE_EXCEEDED", "The operation exceeded its deadline"},
		{os.ErrPermission, "PERMISSION_DENIED", "Permission was denied for the registered operation"},
		{syscall.ENOSPC, "STORAGE_FULL", "Configuration storage is full"},
		{syscall.EROFS, "READ_ONLY_STORAGE", "Configuration storage is read-only"},
		{files.ErrResourceConflict, "CONFIGURATION_CONFLICT", "Configuration changed since review"},
		{filesystem.ErrPathTraversalEscape, "UNSAFE_PATH", "A registered path failed its safety checks"},
		{filesystem.ErrAtomicWriteFailed, "ATOMIC_WRITE_FAILED", "The configuration could not be published atomically"},
		{secrets.ErrRuntimeConflict, "SECRET_FILE_CONFLICT", "A registered secret file changed outside the Console"},
		{secrets.ErrDecryptionFailed, "SECRET_DECRYPTION_FAILED", "The stored credential could not be decrypted"},
		{ErrRecreateRequired, "SECRET_MAPPING_UNCONFIRMED", "Required credential delivery could not be confirmed"},
		{docker.ErrRestartUnconfirmed, "RESTART_UNCONFIRMED", "The registered Traefik restart could not be confirmed"},
		{docker.ErrContainerNotRunning, "CONTAINER_NOT_RUNNING", "The registered Traefik container is not running"},
		{docker.ErrContainerNotFound, "TARGET_NOT_FOUND", "The registered container identity could not be confirmed"},
		{docker.ErrRestartDisabled, "RESTART_DISABLED", "The registered restart capability is disabled"},
		{docker.ErrCapabilityDisabled, "CAPABILITY_DISABLED", "The required Docker capability is disabled"},
		{docker.ErrExecFailed, "SECRET_PROBE_FAILED", "The restricted container operation failed"},
		{docker.ErrUnavailable, "DOCKER_UNAVAILABLE", "The registered Docker endpoint is unavailable"},
		{status.ErrObservationUnavailable, "OBSERVATION_UNAVAILABLE", "The configured Traefik API could not be observed"},
		{errConvergenceTimeout, "CONVERGENCE_TIMEOUT", "Traefik did not report the candidate configuration before the deadline"},
		{errRuntimeRejected, "RUNTIME_REJECTED", "Traefik reported a rejected or disabled candidate resource"},
		{ErrValidationFailed, "VALIDATION_FAILED", "The candidate failed runtime validation"},
		{ErrRecoveryRequired, "RECOVERY_REQUIRED", "Recovery checks could not be completed"},
	} {
		if errors.Is(err, entry.err) {
			return entry.code, entry.text
		}
	}
	return "INTERNAL_FAILURE", "An internal dependency failed; no unsafe error text was exposed"
}
