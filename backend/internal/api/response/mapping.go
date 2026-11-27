package response

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/traefik-console/internal/config/files"
	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/secrets"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/core/crowdsec"
	"github.com/traefik-console/internal/core/domains"
	"github.com/traefik-console/internal/core/entrypoints"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/core/resolvers"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/db"
	"github.com/traefik-console/internal/docker"
	"github.com/traefik-console/internal/engine"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
)

// MapDomainError translates sentinel errors from internal packages into structured APIErrors
func MapDomainError(err error) *Error {
	if apiErr, ok := errors.AsType[*Error](err); ok {
		return apiErr
	}

	if failure, ok := errors.AsType[*engine.DeployFailure](err); ok {
		statusCode, code := 422, "ERR_DEPLOY_ROLLED_BACK"
		if !failure.RolledBack {
			statusCode, code = 409, "ERR_RECOVERY_REQUIRED"
		}
		return &Error{Status: statusCode, Code: code, Message: failure.Error(), FieldErrors: map[string]string{"stage": failure.Stage, "reason_code": failure.ReasonCode, "operation_id": fmt.Sprint(failure.OperationID)}, Retryable: false, InternalErr: err}
	}

	if vf, ok := errors.AsType[*engine.ValidationFailure](err); ok {
		fields := map[string]string{}
		for _, e := range vf.Errors {
			key := e.ItemID
			if e.Field != "" {
				key += "." + e.Field
			}
			if _, dup := fields[key]; !dup {
				fields[key] = e.Message
			}
		}
		return ErrValidation("Configuration failed validation", fields)
	}

	switch {
	case errors.Is(err, catalog.ErrCatalogUnavailable):
		result := ErrExternal("ERR_PLUGIN_CATALOG_UNAVAILABLE", "The public Traefik plugin catalog could not be refreshed. Existing cached entries were preserved. Retry later or install by module path.", true, err)
		if upstream, ok := errors.AsType[*catalog.CatalogHTTPError](err); ok {
			result.FieldErrors = map[string]string{"upstream_status": fmt.Sprint(upstream.StatusCode)}
		}
		return result

	case errors.Is(err, engine.ErrPluginModuleInstalled):
		message := "This plugin module is already installed under another alias"
		if installed, ok := errors.AsType[*engine.PluginModuleError](err); ok {
			message = "This plugin module is already installed as \"" + installed.Alias + "\". Update or reuse that plugin instead of installing it again."
		}
		return ErrConflict("ERR_PLUGIN_ALREADY_INSTALLED", message)
	case errors.Is(err, plugins.ErrPluginNoUpdate):
		return ErrConflict("ERR_PLUGIN_NO_UPDATE", plugins.ErrPluginNoUpdate.Error())
	case errors.Is(err, plugins.ErrPluginNotInCatalog):
		return ErrConflict("ERR_PLUGIN_NOT_IN_CATALOG", plugins.ErrPluginNotInCatalog.Error())

	case errors.Is(err, status.ErrObservationUnavailable):
		return ErrExternal("ERR_TRAEFIK_UNAVAILABLE", "Traefik runtime observation is unavailable. Retry when the API is reachable.", true, err)
	// 403 Forbidden / Read-Only / Path Traversal
	case errors.Is(err, files.ErrPathTraversalEscape), errors.Is(err, filesystem.ErrPathTraversalEscape):
		return &Error{Status: http.StatusForbidden, Code: "ERR_PATH_ESCAPE", Message: "The operation could not be completed safely", Retryable: false}
	case errors.Is(err, files.ErrCannotModifyReadOnly):
		return &Error{Status: http.StatusForbidden, Code: "ERR_READ_ONLY", Message: "The operation could not be completed safely", Retryable: false}

	// 404 Not Found
	case errors.Is(err, files.ErrSourceFileNotFound),
		errors.Is(err, secrets.ErrSecretNotFound),
		errors.Is(err, domains.ErrDomainNotFound),
		errors.Is(err, certificates.ErrCertNotFound),
		errors.Is(err, engine.ErrSnapshotNotFound),
		errors.Is(err, engine.ErrOperationNotFound),
		errors.Is(err, crowdsec.ErrCrowdSecNotFound),
		errors.Is(err, status.ErrServiceNotObserved):
		return &Error{Status: http.StatusNotFound, Code: "ERR_NOT_FOUND", Message: "The operation could not be completed safely", Retryable: false}

		// 409 Conflict / Resource In Use / Busy Lock
	case errors.Is(err, db.ErrJournalCapacity):
		return &Error{Status: 503, Code: "ERR_HISTORY_CAPACITY", Message: db.ErrJournalCapacity.Error()}
	case errors.Is(err, db.ErrSnapshotQuota):
		return &Error{Status: http.StatusInsufficientStorage, Code: "ERR_SNAPSHOT_QUOTA", Message: db.ErrSnapshotQuota.Error()}
	// Entrypoint refusals say what blocks them: the message is the remedy.
	case errors.Is(err, entrypoints.ErrEntryPointInUse):
		return ErrConflict("ERR_ENTRYPOINT_IN_USE", err.Error())
	case errors.Is(err, entrypoints.ErrEntryPointLast):
		return ErrConflict("ERR_ENTRYPOINT_LAST", entrypoints.ErrEntryPointLast.Error())
	case errors.Is(err, entrypoints.ErrEntryPointNotAdoptable):
		return &Error{Status: http.StatusForbidden, Code: "ERR_ENTRYPOINT_NOT_ADOPTABLE", Message: entrypoints.ErrEntryPointNotAdoptable.Error()}
	case errors.Is(err, engine.ErrVersionConflict):
		return ErrConflict("ERR_VERSION_CONFLICT", engine.ErrVersionConflict.Error())
	case errors.Is(err, engine.ErrConfigDrift):
		return ErrConflict("ERR_CONFIG_DRIFT", engine.ErrConfigDrift.Error())
	case errors.Is(err, engine.ErrOperationRecoveryRequired):
		return &Error{Status: http.StatusLocked, Code: "ERR_OPERATION_RECOVERY_REQUIRED", Message: engine.ErrOperationRecoveryRequired.Error()}
	case errors.Is(err, engine.ErrRestoreStale):
		return ErrConflict("ERR_RESTORE_STALE", engine.ErrRestoreStale.Error())
	case errors.Is(err, engine.ErrSnapshotPinned):
		return ErrConflict("ERR_SNAPSHOT_PINNED", engine.ErrSnapshotPinned.Error())
	case errors.Is(err, engine.ErrOperationNotRecoverable):
		return ErrConflict("ERR_OPERATION_NOT_RECOVERABLE", engine.ErrOperationNotRecoverable.Error())
	case errors.Is(err, engine.ErrPreconditionRequired):
		return &Error{Status: 428, Code: "ERR_PRECONDITION_REQUIRED", Message: engine.ErrPreconditionRequired.Error()}
	case errors.Is(err, engine.ErrRecoveryRequired), errors.Is(err, engine.ErrRollbackFailed):
		return ErrConflict("ERR_RECOVERY_REQUIRED", engine.ErrRecoveryRequired.Error())
	case errors.Is(err, engine.ErrRecreateRequired):
		return ErrConflict("ERR_RECREATE_REQUIRED", engine.ErrRecreateRequired.Error())
	case errors.Is(err, files.ErrResourceConflict),
		errors.Is(err, resolvers.ErrResolverInUse),
		errors.Is(err, entrypoints.ErrEntryPointProtocol),
		errors.Is(err, services.ErrServiceInUse),
		errors.Is(err, middlewares.ErrMiddlewareInUse),
		errors.Is(err, domains.ErrDomainInUse),
		errors.Is(err, certificates.ErrCertInUse),
		errors.Is(err, resolvers.ErrEnvChangeForbidden),
		errors.Is(err, plugins.ErrPluginInUse),
		errors.Is(err, engine.ErrDeployInProgress),
		errors.Is(err, docker.ErrOperationBusy):
		return ErrConflict("ERR_CONFLICT", "Resource conflict; refresh and retry")

	// 422 Validation / Rollback Triggered
	case errors.Is(err, engine.ErrRollbackTriggered):
		return &Error{Status: http.StatusUnprocessableEntity, Code: "ERR_DEPLOY_ROLLED_BACK", Message: "The operation could not be completed safely", Retryable: false}
	// The detail names the user, never the password.
	case errors.Is(err, middlewares.ErrInvalidCredentials):
		return ErrValidation("Basic Auth users are invalid", map[string]string{"users": strings.TrimPrefix(err.Error(), middlewares.ErrInvalidCredentials.Error()+": ")})
	case errors.Is(err, files.ErrEmptySelection),
		errors.Is(err, secrets.ErrSecretEmptyValue),
		errors.Is(err, engine.ErrValidationFailed),
		errors.Is(err, resolvers.ErrResolverInvalidName),
		errors.Is(err, entrypoints.ErrEntryPointInvalidName),
		errors.Is(err, services.ErrServiceInvalidName),
		errors.Is(err, status.ErrInvalidServiceName),
		errors.Is(err, services.ErrServiceNoServers),
		errors.Is(err, services.ErrServiceInvalidURL),
		errors.Is(err, services.ErrServiceInvalidProtocol),
		errors.Is(err, services.ErrServiceInvalidAddress),
		errors.Is(err, services.ErrServiceInvalidOption),
		errors.Is(err, middlewares.ErrMiddlewareInvalidName),
		errors.Is(err, middlewares.ErrMiddlewareInvalidType),
		errors.Is(err, middlewares.ErrChainEmpty),
		errors.Is(err, middlewares.ErrChainSelfReference),
		errors.Is(err, routers.ErrRouterInvalidName),
		errors.Is(err, routers.ErrRouterRuleEmpty),
		errors.Is(err, routers.ErrRouterNoService),
		errors.Is(err, routers.ErrRouterNoBackend),
		errors.Is(err, routers.ErrRouterInvalidProtocol),
		errors.Is(err, middlewares.ErrMiddlewareInvalidProtocol),
		errors.Is(err, routers.ErrRouterDomainNotFound),
		errors.Is(err, domains.ErrDomainInvalidIdentity),
		errors.Is(err, domains.ErrDomainProbeInvalid),
		errors.Is(err, domains.ErrDomainMethodRequired),
		errors.Is(err, domains.ErrDomainResolverNotFound),
		errors.Is(err, domains.ErrDomainCertNotFound),
		errors.Is(err, domains.ErrDomainWildcardNeedsDNS01),
		errors.Is(err, certificates.ErrCertInvalidPEM),
		errors.Is(err, certificates.ErrCertInvalidKey),
		errors.Is(err, certificates.ErrCertInvalidName),
		errors.Is(err, crowdsec.ErrCrowdSecInvalidIP),
		errors.Is(err, crowdsec.ErrCrowdSecInvalidKey),
		errors.Is(err, plugins.ErrPluginInvalidName),
		errors.Is(err, plugins.ErrPluginInvalidModule),
		errors.Is(err, plugins.ErrPluginInvalidVersion),
		errors.Is(err, plugins.ErrPluginInvalidYAML):
		return ErrValidation("Invalid configuration or resource binding", nil)

	// Listing blocklist alerts is refused by size, not because CrowdSec failed: not retryable, and the UI
	// points to the decisions view.
	case errors.Is(err, crowdsec.ErrCrowdSecBlocklistTooLarge):
		return &Error{Status: http.StatusUnprocessableEntity, Code: "ERR_CROWDSEC_BLOCKLIST_TOO_LARGE", Message: "Blocklist alerts are too large to list; their bans are in the decisions view", Retryable: false}

	// Disabled optional capabilities are unavailable, not an executor failure.
	case errors.Is(err, docker.ErrCapabilityDisabled), errors.Is(err, crowdsec.ErrCrowdSecUnavailableCapability):
		return &Error{Status: 503, Code: "ERR_CAPABILITY_DISABLED", Message: "Optional integration is not configured", Retryable: false}
	// 502 External Dependency Error (Docker / CrowdSec / Traefik API)
	case errors.Is(err, docker.ErrRestartDisabled),
		errors.Is(err, docker.ErrUnavailable),
		errors.Is(err, docker.ErrInvalidRequest),
		errors.Is(err, crowdsec.ErrCrowdSecUnreachable),
		errors.Is(err, docker.ErrContainerNotFound),
		errors.Is(err, docker.ErrContainerNotRunning),
		errors.Is(err, docker.ErrRestartUnconfirmed),
		errors.Is(err, docker.ErrExecFailed):
		return ErrExternal("ERR_EXTERNAL_SERVICE", "Registered integration unavailable", true, err)

	default:
		return ErrInternal("An unexpected internal error occurred", err)
	}
}
