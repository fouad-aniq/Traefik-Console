package handlers

import (
	"net"
	"net/http"
	"strings"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/crowdsec"
)

type crowdSecDecisionRequest struct {
	IP       string `json:"ip"`
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
}

func (s *Server) handleGetCrowdSecConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.deps.CrowdSec.GetConfig(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleConfigureCrowdSec(w http.ResponseWriter, r *http.Request) {
	var in crowdsec.CrowdSecConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.CrowdSec.ConfigureBouncer(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleListCrowdSecDecisions(w http.ResponseWriter, r *http.Request) {
	decs, err := s.deps.CrowdSec.ListDecisions(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, decs)
}

func (s *Server) handleAddCrowdSecDecision(w http.ResponseWriter, r *http.Request) {
	var in crowdSecDecisionRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	client := net.ParseIP(host)
	value := strings.TrimSpace(in.IP)
	_, network, _ := net.ParseCIDR(value)
	if client != nil && (net.ParseIP(value) != nil && net.ParseIP(value).Equal(client) || network != nil && network.Contains(client)) {
		response.WriteError(w, response.ErrValidation("Cannot ban the connected management peer", nil))
		return
	}
	if err := s.deps.CrowdSec.AddDecision(r.Context(), value, in.Duration, in.Reason); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, map[string]bool{"added": true})
}

func (s *Server) handleDeleteCrowdSecDecision(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.CrowdSec.DeleteDecision(r.Context(), r.PathValue("ip")); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleCrowdSecStatus reports plugin, middleware, key, LAPI and engine separately. An unreadable
// inventory is an error, never "missing": reporting a declared plugin as absent would make a client add
// a duplicate.
func (s *Server) handleCrowdSecStatus(w http.ResponseWriter, r *http.Request) {
	plugins, err := s.deps.Plugins.ListInstalledPlugins(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	mws, err := s.deps.Middlewares.ListMiddlewares(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	routers, err := s.deps.Routers.ListRouters(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	status, err := s.deps.CrowdSec.Status(r.Context(), crowdsec.Inventory{Plugins: plugins, Middlewares: mws, Routers: routers})
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, status)
}

func (s *Server) handleCrowdSecStats(w http.ResponseWriter, r *http.Request) {
	window := 24
	if raw := r.URL.Query().Get("window"); raw != "" {
		switch raw {
		case "1h":
			window = 1
		case "24h":
			window = 24
		case "7d":
			window = 168
		case "30d":
			window = 720
		default:
			response.WriteError(w, response.ErrValidation("window must be 1h, 24h, 7d or 30d", nil))
			return
		}
	}
	stats, err := s.deps.CrowdSec.Stats(r.Context(), window)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, stats)
}

func (s *Server) handleConfigureCrowdSecConnection(w http.ResponseWriter, r *http.Request) {
	var in crowdsec.ConnectionSettings
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.CrowdSec.ConfigureConnection(r.Context(), in, request.DryRun(r))
	writeResult(w, res, err)
}

// handleTestCrowdSecConnection probes candidate settings; nothing is saved.
func (s *Server) handleTestCrowdSecConnection(w http.ResponseWriter, r *http.Request) {
	var in crowdsec.ConnectionSettings
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	result, err := s.deps.CrowdSec.TestConnection(r.Context(), in)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}
