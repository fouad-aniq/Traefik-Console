package response

import (
	"fmt"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	catalog "github.com/traefik-console/internal/traefik/plugins"
	"github.com/traefik-console/internal/traefik/status"
	"strings"
	"testing"

	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/core/crowdsec"
	"github.com/traefik-console/internal/core/domains"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/core/resolvers"
	"github.com/traefik-console/internal/core/routers"
	"github.com/traefik-console/internal/core/services"
)

func TestResourceErrorMappingsRetainStatusAndWrapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"ErrResolverInvalidName", resolvers.ErrResolverInvalidName, 422},
		{"ErrResolverInUse", resolvers.ErrResolverInUse, 409},
		{"ErrEnvChangeForbidden", resolvers.ErrEnvChangeForbidden, 409},
		{"ErrDomainInvalidIdentity", domains.ErrDomainInvalidIdentity, 422},
		{"ErrDomainNotFound", domains.ErrDomainNotFound, 404},
		{"ErrDomainMethodRequired", domains.ErrDomainMethodRequired, 422},
		{"ErrDomainProbeInvalid", domains.ErrDomainProbeInvalid, 422},
		{"ErrDomainInUse", domains.ErrDomainInUse, 409},
		{"ErrDomainResolverNotFound", domains.ErrDomainResolverNotFound, 422},
		{"ErrDomainCertNotFound", domains.ErrDomainCertNotFound, 422},
		{"ErrDomainWildcardNeedsDNS01", domains.ErrDomainWildcardNeedsDNS01, 422},
		{"ErrCertInvalidPEM", certificates.ErrCertInvalidPEM, 422},
		{"ErrCertInvalidKey", certificates.ErrCertInvalidKey, 422},
		{"ErrCertInUse", certificates.ErrCertInUse, 409},
		{"ErrCertInvalidName", certificates.ErrCertInvalidName, 422},
		{"ErrCertNotFound", certificates.ErrCertNotFound, 404},
		{"ErrRouterInvalidName", routers.ErrRouterInvalidName, 422},
		{"ErrRouterRuleEmpty", routers.ErrRouterRuleEmpty, 422},
		{"ErrRouterNoService", routers.ErrRouterNoService, 422},
		{"ErrRouterDomainNotFound", routers.ErrRouterDomainNotFound, 422},
		{"ErrServiceInvalidName", services.ErrServiceInvalidName, 422},
		{"ErrServiceNoServers", services.ErrServiceNoServers, 422},
		{"ErrServiceInvalidURL", services.ErrServiceInvalidURL, 422},
		{"ErrServiceInUse", services.ErrServiceInUse, 409},
		{"ErrMiddlewareInvalidName", middlewares.ErrMiddlewareInvalidName, 422},
		{"ErrMiddlewareInvalidType", middlewares.ErrMiddlewareInvalidType, 422},
		{"ErrMiddlewareInvalidProtocol", middlewares.ErrMiddlewareInvalidProtocol, 422},
		{"ErrRouterInvalidProtocol", routers.ErrRouterInvalidProtocol, 422},
		{"ErrMiddlewareInUse", middlewares.ErrMiddlewareInUse, 409},
		{"ErrChainEmpty", middlewares.ErrChainEmpty, 422},
		{"ErrChainSelfReference", middlewares.ErrChainSelfReference, 422},
		{"ErrCrowdSecUnreachable", crowdsec.ErrCrowdSecUnreachable, 502},
		{"ErrCrowdSecInvalidKey", crowdsec.ErrCrowdSecInvalidKey, 422},
		{"ErrCrowdSecInvalidIP", crowdsec.ErrCrowdSecInvalidIP, 422},
		{"ErrCrowdSecBlocklistTooLarge", crowdsec.ErrCrowdSecBlocklistTooLarge, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, err := range []error{tc.err, fmt.Errorf("resource operation: %w", tc.err)} {
				mapped := MapDomainError(err)
				if mapped.Status != tc.status {
					t.Fatalf("%s mapping: %+v", tc.name, mapped)
				}
			}
		})
	}
}

func TestOperationErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{engine.ErrVersionConflict, 409, "ERR_VERSION_CONFLICT"},
		{engine.ErrConfigDrift, 409, "ERR_CONFIG_DRIFT"},
		{engine.ErrOperationRecoveryRequired, 423, "ERR_OPERATION_RECOVERY_REQUIRED"},
		{engine.ErrSnapshotQuota, 507, "ERR_SNAPSHOT_QUOTA"},
		{engine.ErrRestoreStale, 409, "ERR_RESTORE_STALE"},
		{engine.ErrSnapshotPinned, 409, "ERR_SNAPSHOT_PINNED"},
		{engine.ErrOperationNotRecoverable, 409, "ERR_OPERATION_NOT_RECOVERABLE"},
		{engine.ErrOperationNotFound, 404, "ERR_NOT_FOUND"},
		{status.ErrServiceNotObserved, 404, "ERR_NOT_FOUND"},
		{status.ErrInvalidServiceName, 422, "ERR_VALIDATION_FAILED"},
	} {
		got := MapDomainError(fmt.Errorf("wrapped: %w", tc.err))
		if got.Status != tc.status || got.Code != tc.code {
			t.Fatalf("%v mapped to %d %s", tc.err, got.Status, got.Code)
		}
	}
}

func TestValidationFailureMapsFieldErrors(t *testing.T) {
	got := MapDomainError(&engine.ValidationFailure{Errors: []models.ValidationError{{ItemID: "router:a", Field: "rule", Message: "bad rule"}}})
	if got.Status != 422 || got.FieldErrors["router:a.rule"] != "bad rule" {
		t.Fatal("field errors lost", got)
	}
}

func TestCatalogUnavailableMapping(t *testing.T) {
	for _, err := range []error{catalog.ErrCatalogUnavailable, fmt.Errorf("wrapped: %w", &catalog.CatalogHTTPError{StatusCode: 404})} {
		got := MapDomainError(err)
		if got.Status != 502 || got.Code != "ERR_PLUGIN_CATALOG_UNAVAILABLE" || !got.Retryable {
			t.Fatal("catalog mapping missing")
		}
	}
}

func TestDeployFailureMappingKeepsSafeStageAndReason(t *testing.T) {
	failure := &engine.DeployFailure{OperationID: 42, Stage: "static_publication", ReasonCode: "PERMISSION_DENIED", Reason: "Permission was denied for the registered operation", RolledBack: true}
	got := MapDomainError(failure)
	if got.Status != 422 || got.Code != "ERR_DEPLOY_ROLLED_BACK" || got.FieldErrors["stage"] != "static_publication" || got.FieldErrors["operation_id"] != "42" || !strings.Contains(got.Message, "PERMISSION_DENIED") {
		t.Fatal("failure details lost")
	}
	failure.RolledBack = false
	failure.RecoveryCode = "RECOVERY_REQUIRED"
	got = MapDomainError(failure)
	if got.Status != 409 || got.Code != "ERR_RECOVERY_REQUIRED" || !strings.Contains(got.Message, "recover or abandon") {
		t.Fatal("failed recovery hidden")
	}
}
