package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/models"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	if s.deps.Web != nil {
		mux.Handle("GET /{$}", s.deps.Web)
		mux.Handle("GET /assets/{file}", s.deps.Web)
	}
	mux.HandleFunc("GET /api/v1/health", handleHealth)

	// 1. Overview & Container Operations
	mux.HandleFunc("GET /api/v1/overview", s.handleOverview)
	mux.HandleFunc("GET /api/v1/entrypoints", s.handleListEntryPoints)
	mux.HandleFunc("GET /api/v1/entrypoints/{name}", s.handleGetEntryPoint)
	mux.HandleFunc("PUT /api/v1/entrypoints/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindEntryPoint, request.PathName, s.handleUpsertEntryPoint)))
	mux.HandleFunc("DELETE /api/v1/entrypoints/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindEntryPoint, request.PathName, s.handleDeleteEntryPoint)))
	mux.HandleFunc("GET /api/v1/traefik/container", s.handleInspectTraefikContainer)
	mux.HandleFunc("POST /api/v1/traefik/restart", s.handleRestartTraefik)
	mux.HandleFunc("GET /api/v1/traefik/logs", s.handleTraefikLogs)

	// 2. Snapshots, operations log, config state
	mux.HandleFunc("GET /api/v1/snapshots", s.handleListSnapshots)
	mux.HandleFunc("POST /api/v1/snapshots", s.handleCreateSnapshot)
	mux.HandleFunc("GET /api/v1/snapshots/compare/{from}/{to}", s.handleCompareSnapshots)
	mux.HandleFunc("GET /api/v1/snapshots/{id}", s.handleGetSnapshot)
	mux.HandleFunc("PATCH /api/v1/snapshots/{id}", s.handlePatchSnapshot)
	mux.HandleFunc("DELETE /api/v1/snapshots/{id}", s.handleDeleteSnapshot)
	mux.HandleFunc("GET /api/v1/snapshots/{id}/config", s.handleSnapshotConfig)
	mux.HandleFunc("GET /api/v1/snapshots/{id}/resources", s.handleSnapshotResources)
	mux.HandleFunc("GET /api/v1/snapshots/{id}/restore-preview", s.handleRestorePreview)
	mux.HandleFunc("POST /api/v1/snapshots/{id}/restore", request.WithIdempotencyKey(s.handleRestoreSnapshot))
	mux.HandleFunc("GET /api/v1/operations", s.handleListOperations)
	mux.HandleFunc("GET /api/v1/operations/{id}", s.handleGetOperation)
	mux.HandleFunc("POST /api/v1/operations/{id}/recover", s.handleRecoverOperation)
	mux.HandleFunc("POST /api/v1/operations/{id}/abandon", s.handleAbandonOperation)
	mux.HandleFunc("GET /api/v1/config/state", s.handleConfigState)
	mux.HandleFunc("POST /api/v1/config/adopt", s.handleAdoptConfig)

	// Removed draft protocol: 410 for one release.
	mux.HandleFunc("GET /api/v1/draft/review", handleDraftGone)
	mux.HandleFunc("POST /api/v1/draft/apply", handleDraftGone)
	mux.HandleFunc("DELETE /api/v1/draft", handleDraftGone)
	mux.HandleFunc("POST /api/v1/snapshots/{id}/rollback", handleDraftGone)
	mux.HandleFunc("GET /api/v1/snapshots/{id}/review", handleDraftGone)

	// 3. Resolvers & ACME Certificates
	mux.HandleFunc("GET /api/v1/resolvers/providers", s.handleListDNSProviders)
	mux.HandleFunc("GET /api/v1/resolvers", s.handleListResolvers)
	mux.HandleFunc("GET /api/v1/resolvers/{name}", s.handleGetResolver)
	mux.HandleFunc("PUT /api/v1/resolvers/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindResolver, request.PathName, s.handleUpsertResolver)))
	mux.HandleFunc("DELETE /api/v1/resolvers/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindResolver, request.PathName, s.handleDeleteResolver)))
	mux.HandleFunc("GET /api/v1/acme/certificates", s.handleListACMECertificates)

	// 4. Domains & Custom Imported Certificates
	mux.HandleFunc("GET /api/v1/domains", s.handleListDomains)
	mux.HandleFunc("PUT /api/v1/domains/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindDomain, request.PathName, s.handleUpsertDomain)))
	mux.HandleFunc("POST /api/v1/domains/{name}/probe", s.handleProbeDomain)
	mux.HandleFunc("DELETE /api/v1/domains/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindDomain, request.PathName, s.handleDeleteDomain)))
	mux.HandleFunc("GET /api/v1/certificates", s.handleListImportedCerts)
	mux.HandleFunc("PUT /api/v1/certificates/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindTLS, request.PathName, s.handleImportCert)))
	mux.HandleFunc("DELETE /api/v1/certificates/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindTLS, request.PathName, s.handleDeleteImportedCert)))

	// 5. Routers
	mux.HandleFunc("GET /api/v1/routers", s.handleListRouters)
	mux.HandleFunc("GET /api/v1/routers/{name}", s.handleGetRouter)
	mux.HandleFunc("PUT /api/v1/routers/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindRouter, request.PathName, s.handleUpsertRouter)))
	mux.HandleFunc("DELETE /api/v1/routers/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindRouter, request.PathName, s.handleDeleteRouter)))

	// 6. Services
	mux.HandleFunc("GET /api/v1/services", s.handleListServices)
	mux.HandleFunc("GET /api/v1/services/{name}", s.handleGetService)
	mux.HandleFunc("GET /api/v1/services/{name}/diagnostics", s.handleDiagnoseService)
	mux.HandleFunc("PUT /api/v1/services/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindService, request.PathName, s.handleUpsertService)))
	mux.HandleFunc("DELETE /api/v1/services/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindService, request.PathName, s.handleDeleteService)))

	// 7. Middlewares & Middleware Chains
	mux.HandleFunc("GET /api/v1/middlewares", s.handleListMiddlewares)
	mux.HandleFunc("PUT /api/v1/middlewares/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindMiddleware, request.PathName, s.handleUpsertMiddleware)))
	mux.HandleFunc("DELETE /api/v1/middlewares/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindMiddleware, request.PathName, s.handleDeleteMiddleware)))
	mux.HandleFunc("GET /api/v1/chains", s.handleListChains)
	mux.HandleFunc("PUT /api/v1/chains/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindChain, request.PathName, s.handleUpsertChain)))
	mux.HandleFunc("DELETE /api/v1/chains/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindChain, request.PathName, s.handleDeleteChain)))

	// 8. Plugins (Traefik Shop & YAML Snippet Installer)
	mux.HandleFunc("POST /api/v1/plugins/catalog/install", request.WithIdempotencyKey(s.handleCatalogInstall))

	mux.HandleFunc("GET /api/v1/plugins/catalog/page", s.handlePluginCatalogPage)
	mux.HandleFunc("GET /api/v1/plugins/catalog", s.handleSearchPluginCatalog)
	mux.HandleFunc("POST /api/v1/plugins/catalog/sync", s.handleSyncPluginCatalog)

	mux.HandleFunc("GET /api/v1/plugins", s.handleListInstalledPlugins)
	mux.HandleFunc("PUT /api/v1/plugins/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindPlugin, request.PathName, s.handleUpsertPlugin)))
	mux.HandleFunc("POST /api/v1/plugins/yaml", request.WithIdempotencyKey(s.handleInstallPluginFromYAML))
	mux.HandleFunc("POST /api/v1/plugins/{name}/update", request.WithIdempotencyKey(request.WithResourceVersion(models.KindPlugin, request.PathName, s.handleUpdatePlugin)))
	mux.HandleFunc("DELETE /api/v1/plugins/{name}", request.WithIdempotencyKey(request.WithResourceVersion(models.KindPlugin, request.PathName, s.handleDeletePlugin)))

	// 9. CrowdSec Integration
	mux.HandleFunc("GET /api/v1/crowdsec/config", s.handleGetCrowdSecConfig)
	mux.HandleFunc("PUT /api/v1/crowdsec/config", request.WithIdempotencyKey(request.WithResourceVersion(models.KindCrowdSec, request.Singleton("integration"), s.handleConfigureCrowdSec)))
	mux.HandleFunc("PUT /api/v1/crowdsec/connection", request.WithIdempotencyKey(request.WithResourceVersion(models.KindCrowdSec, request.Singleton("integration"), s.handleConfigureCrowdSecConnection)))
	mux.HandleFunc("POST /api/v1/crowdsec/connection/test", s.handleTestCrowdSecConnection)
	mux.HandleFunc("GET /api/v1/crowdsec/status", s.handleCrowdSecStatus)
	mux.HandleFunc("GET /api/v1/crowdsec/stats", s.handleCrowdSecStats)
	mux.HandleFunc("GET /api/v1/crowdsec/metrics", s.handleCrowdSecMetrics)
	mux.HandleFunc("GET /api/v1/crowdsec/hub", s.handleListCrowdSecHubItems)
	mux.HandleFunc("GET /api/v1/crowdsec/alerts", s.handleListCrowdSecAlerts)
	mux.HandleFunc("GET /api/v1/crowdsec/alerts/{id}", s.handleGetCrowdSecAlert)
	mux.HandleFunc("GET /api/v1/crowdsec/decisions", s.handleListCrowdSecDecisions)
	mux.HandleFunc("POST /api/v1/crowdsec/decisions", s.handleAddCrowdSecDecision)
	mux.HandleFunc("DELETE /api/v1/crowdsec/decisions/id/{id}", s.handleDeleteCrowdSecDecisionByID)
	mux.HandleFunc("DELETE /api/v1/crowdsec/decisions/{ip...}", s.handleDeleteCrowdSecDecision)
	mux.HandleFunc("GET /api/v1/crowdsec/allowlists", s.handleListCrowdSecAllowlists)
	mux.HandleFunc("POST /api/v1/crowdsec/allowlists", s.handleCreateCrowdSecAllowlist)
	mux.HandleFunc("POST /api/v1/crowdsec/allowlists/check", s.handleCheckCrowdSecAllowlist)
	mux.HandleFunc("POST /api/v1/crowdsec/allowlists/{name}/entries", s.handleAddCrowdSecAllowlistEntry)
	mux.HandleFunc("DELETE /api/v1/crowdsec/allowlists/{name}/entries", s.handleRemoveCrowdSecAllowlistEntry)
	mux.HandleFunc("DELETE /api/v1/crowdsec/allowlists/{name}", s.handleDeleteCrowdSecAllowlist)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		middleware.SetRoute(r.Context(), pattern)
		mux.ServeHTTP(w, r)
	})
}
