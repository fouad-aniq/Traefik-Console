package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleListDNSProviders(w http.ResponseWriter, r *http.Request) {
	formSupported, customCodes := s.deps.Resolvers.ListProviders()
	response.WriteJSON(w, http.StatusOK, map[string]any{
		"providers":             formSupported,
		"custom_provider_codes": customCodes,
	})
}

func (s *Server) handleListResolvers(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Resolvers.ListResolvers(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetResolver(w http.ResponseWriter, r *http.Request) {
	item, err := s.deps.Resolvers.GetResolver(r.Context(), r.PathValue("name"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, item)
}

func (s *Server) handleUpsertResolver(w http.ResponseWriter, r *http.Request) {
	var in models.ResolverInput
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}

	in.Name = r.PathValue("name")
	res, err := s.deps.Resolvers.UpsertResolver(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteResolver(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Resolvers.DeleteResolver(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}
