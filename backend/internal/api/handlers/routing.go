package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/services"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleListRouters(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Routers.ListRouters(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetRouter(w http.ResponseWriter, r *http.Request) {
	item, err := s.deps.Routers.GetRouter(r.Context(), r.PathValue("name"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, item)
}

func (s *Server) handleUpsertRouter(w http.ResponseWriter, r *http.Request) {
	var in models.RouterConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Routers.UpsertRouter(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteRouter(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Routers.DeleteRouter(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Services.ListServices(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	// Usage is advisory: when a protocol's routers cannot be read its services are returned without
	// it (deleting is still guarded by the server). Routers only use their own protocol's services.
	// The router list holds the managed routers of every protocol; Traefik's own view of TCP and UDP
	// routers backs it up, joined by name so no router is counted twice.
	routers := map[string][]models.UIResource[models.RouterConfig]{}
	readable := map[string]bool{}
	listed := false
	if list, err := s.deps.Routers.ListRouters(r.Context()); err == nil {
		listed = true
		for _, protocol := range []string{models.ServiceHTTP, models.ServiceTCP, models.ServiceUDP} {
			readable[protocol] = true
		}
		for _, item := range list {
			protocol := models.RouterProtocol(item.Config)
			routers[protocol] = append(routers[protocol], item)
		}
	}
	for _, protocol := range []string{models.ServiceTCP, models.ServiceUDP} {
		if !listed {
			break // without the managed routers the usage would look emptier than it is
		}
		list, err := s.deps.Services.RuntimeRouters(r.Context(), protocol)
		if err != nil {
			continue
		}
		known := map[string]bool{}
		for _, item := range routers[protocol] {
			known[item.Name] = true
		}
		for _, item := range list {
			if !known[item.Name] {
				routers[protocol] = append(routers[protocol], item)
			}
		}
		readable[protocol] = true
	}
	for i := range items {
		protocol := models.ServiceProtocol(items[i].Config)
		if readable[protocol] {
			items[i].Config.UsedBy = services.Usage(items[i], routers[protocol])
		}
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	item, err := s.deps.Services.GetService(r.Context(), r.PathValue("name"), r.URL.Query().Get("protocol"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, item)
}

func (s *Server) handleDiagnoseService(w http.ResponseWriter, r *http.Request) {
	diag, err := s.deps.Services.DiagnoseService(r.Context(), r.PathValue("name"), r.URL.Query().Get("protocol"))
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, diag)
}

func (s *Server) handleUpsertService(w http.ResponseWriter, r *http.Request) {
	var in models.ServiceConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Services.UpsertService(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Services.DeleteService(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}
