package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/traefik/status"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "authentication": "deferred"})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	obs := s.deps.Observation.GetOverview(r.Context())
	raw, e := s.deps.ReadStaticConfig()
	if e != nil {
		obs.CertificatesPartial = true
	} else {
		inventory := s.deps.ACMEObserver.Observe(raw)
		obs.CertificatesPartial = inventory.Partial
		for _, cert := range inventory.Certificates {
			if cert.Status == "expired" || cert.Status == "expiring_soon" {
				obs.CertsExpiring30d++
			}
		}
	}
	if csCfg, err := s.deps.CrowdSec.GetConfig(r.Context()); err == nil && csCfg.ActiveEnabled {
		obs.CrowdSecStatus = "configured"
		if decs, err := s.deps.CrowdSec.ListDecisions(r.Context()); err == nil {
			obs.CrowdSecDecisions = len(decs)
		}
	}
	response.WriteJSON(w, http.StatusOK, obs)
}

func (s *Server) handleInspectTraefikContainer(w http.ResponseWriter, r *http.Request) {
	st, err := s.deps.Container.InspectTraefikContainer(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, st)
}

func (s *Server) handleRestartTraefik(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Operations.Restart(r.Context()); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

func (s *Server) handleTraefikLogs(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, map[string]string{"logs": "Container log content is restricted to operator tooling to avoid exposing credentials."})
}

func (s *Server) handleListEntryPoints(w http.ResponseWriter, r *http.Request) {
	raw, err := s.deps.ReadStaticConfig()
	if err != nil {
		response.WriteError(w, err)
		return
	}
	snap, err := status.DiscoverStatic(raw)
	if err != nil {
		response.WriteError(w, response.ErrValidation("Invalid static configuration", nil))
		return
	}
	// entry_points and protocols are the original shape (pickers); items carries the full resources.
	items, err := s.deps.EntryPoints.ListEntryPoints(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]any{"entry_points": snap.EntryPoints, "protocols": snap.EntryPointProtocols, "items": items})
}
