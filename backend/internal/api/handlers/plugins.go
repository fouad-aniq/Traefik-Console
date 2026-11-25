package handlers

import (
	"net/http"
	"unicode/utf8"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/models"
)

const maxCatalogQueryCharacters = 256

func (s *Server) handleSearchPluginCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if !utf8.ValidString(q) || utf8.RuneCountInString(q) > maxCatalogQueryCharacters {
		response.WriteError(w, response.ErrValidation("Catalog query must not exceed 256 characters", map[string]string{"q": "Maximum 256 characters"}))
		return
	}
	items, err := s.deps.Plugins.SearchCatalog(r.Context(), q)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleSyncPluginCatalog(w http.ResponseWriter, r *http.Request) {
	count, err := s.deps.Plugins.SyncCatalog(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]int{"synced_count": count})
}

func (s *Server) handleListInstalledPlugins(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Plugins.ListPluginViews(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleUpsertPlugin(w http.ResponseWriter, r *http.Request) {
	var in models.StaticPluginConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Plugins.UpsertPlugin(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleInstallPluginFromYAML(w http.ResponseWriter, r *http.Request) {
	var in plugins.InstallFromYAMLRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Plugins.InstallFromYAML(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	var in plugins.UpdatePluginRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Plugins.ApplyPluginUpdate(r.Context(), r.PathValue("name"), in.Version, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Plugins.DeletePlugin(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	var in plugins.CatalogInstallRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Plugins.InstallCatalogPlugin(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}
