package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/crowdsec"
)

func crowdSecQueryInt(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, response.ErrValidation("Invalid CrowdSec query", nil)
	}
	return value, nil
}

func (s *Server) handleListCrowdSecAlerts(w http.ResponseWriter, r *http.Request) {
	page, err := crowdSecQueryInt(r, "page")
	if err != nil {
		response.WriteError(w, err)
		return
	}
	limit, err := crowdSecQueryInt(r, "limit")
	if err != nil {
		response.WriteError(w, err)
		return
	}
	since, err := crowdSecQueryInt(r, "since_hours")
	if err != nil {
		response.WriteError(w, err)
		return
	}
	result, err := s.deps.CrowdSec.ListAlerts(r.Context(), crowdsec.AlertQuery{Page: page, Limit: limit, SinceHours: since, Scenario: strings.TrimSpace(r.URL.Query().Get("scenario")), Source: strings.TrimSpace(r.URL.Query().Get("source")), Origin: strings.TrimSpace(r.URL.Query().Get("origin"))})
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetCrowdSecAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.WriteError(w, crowdsec.ErrCrowdSecNotFound)
		return
	}
	result, err := s.deps.CrowdSec.GetAlert(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteCrowdSecDecisionByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		response.WriteError(w, crowdsec.ErrCrowdSecNotFound)
		return
	}
	if err := s.deps.CrowdSec.DeleteDecisionByID(r.Context(), id); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

type crowdSecAllowlistRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type crowdSecAllowlistEntryRequest struct {
	Value       string `json:"value"`
	Description string `json:"description"`
	Expiration  string `json:"expiration"`
}

func (s *Server) handleListCrowdSecAllowlists(w http.ResponseWriter, r *http.Request) {
	result, err := s.deps.CrowdSec.ListAllowlists(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

func (s *Server) handleCreateCrowdSecAllowlist(w http.ResponseWriter, r *http.Request) {
	var input crowdSecAllowlistRequest
	if !request.DecodeObject(w, r, &input, request.Options{}) {
		return
	}
	if err := s.deps.CrowdSec.CreateAllowlist(r.Context(), strings.TrimSpace(input.Name), input.Description); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, map[string]bool{"created": true})
}

func (s *Server) handleAddCrowdSecAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	var input crowdSecAllowlistEntryRequest
	if !request.DecodeObject(w, r, &input, request.Options{}) {
		return
	}
	if err := s.deps.CrowdSec.AddAllowlistEntry(r.Context(), r.PathValue("name"), input.Value, input.Description, input.Expiration); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, map[string]bool{"added": true})
}

func (s *Server) handleRemoveCrowdSecAllowlistEntry(w http.ResponseWriter, r *http.Request) {
	var input crowdSecAllowlistEntryRequest
	if !request.DecodeObject(w, r, &input, request.Options{}) {
		return
	}
	if err := s.deps.CrowdSec.RemoveAllowlistEntry(r.Context(), r.PathValue("name"), input.Value); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

func (s *Server) handleDeleteCrowdSecAllowlist(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.CrowdSec.DeleteAllowlist(r.Context(), r.PathValue("name")); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (s *Server) handleCheckCrowdSecAllowlist(w http.ResponseWriter, r *http.Request) {
	var input crowdSecAllowlistEntryRequest
	if !request.DecodeObject(w, r, &input, request.Options{}) {
		return
	}
	allowed, reason, err := s.deps.CrowdSec.CheckAllowlist(r.Context(), input.Value)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]any{"allowlisted": allowed, "reason": reason})
}

func (s *Server) handleCrowdSecMetrics(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, s.deps.CrowdSec.Metrics(r.Context()))
}

func (s *Server) handleListCrowdSecHubItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.CrowdSec.ListHubItems(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}
