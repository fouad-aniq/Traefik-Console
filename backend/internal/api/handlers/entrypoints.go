package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleGetEntryPoint(w http.ResponseWriter, r *http.Request) {
	item, err := s.deps.EntryPoints.GetEntryPoint(r.Context(), r.PathValue("name"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, item)
}

func (s *Server) handleUpsertEntryPoint(w http.ResponseWriter, r *http.Request) {
	var in models.EntryPointConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	in.Name = r.PathValue("name")
	res, err := s.deps.EntryPoints.UpsertEntryPoint(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteEntryPoint(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.EntryPoints.DeleteEntryPoint(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}
