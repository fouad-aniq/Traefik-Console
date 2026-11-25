package handlers

import (
	"context"
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/certificates"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Domains.ListDomains(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	src := s.domainSources(r.Context())
	for i := range items {
		certificates.AnnotateDomain(&items[i], src)
	}
	response.WriteJSON(w, http.StatusOK, items)
}

// domainSources reads what the domains list is annotated with. Every source is advisory:
// one that cannot be read is reported as unknown, never as a failure of the list.
func (s *Server) domainSources(ctx context.Context) certificates.DomainSources {
	var src certificates.DomainSources
	if routers, err := s.deps.Routers.ListRouters(ctx); err == nil {
		src.Routers = routers
	}
	if uploaded, err := s.deps.Certificates.ListCertificates(ctx); err == nil {
		src.Uploaded, src.UploadedKnown = uploaded, true
	}
	if raw, err := s.deps.ReadStaticConfig(); err == nil {
		inventory := s.deps.ACMEObserver.Observe(raw)
		src.ACME = &inventory
	}
	return src
}

func (s *Server) handleUpsertDomain(w http.ResponseWriter, r *http.Request) {
	var in models.DomainConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Domains.SaveDomain(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleProbeDomain(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Domains.ProbeDomain(r.Context(), r.PathValue("name"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Domains.DeleteDomain(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}
