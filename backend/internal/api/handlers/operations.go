package handlers

import (
	"context"
	"net/http"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := pageParams(r)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	rows, err := s.deps.History.ListOperations(r.Context(), limit+1, cursor)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	out := page[models.OperationRow]{Items: rows}
	if len(rows) > limit {
		out.Items, out.NextCursor = rows[:limit], rows[limit-1].ID
	}
	if out.Items == nil {
		out.Items = []models.OperationRow{}
	}
	response.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	row, err := s.deps.History.GetOperation(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if row == nil {
		response.WriteError(w, engine.ErrOperationNotFound)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

func (s *Server) handleRecoverOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	row, err := s.deps.Operations.Recover(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

func (s *Server) handleAbandonOperation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	row, err := s.deps.Operations.Abandon(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

func (s *Server) handleConfigState(w http.ResponseWriter, r *http.Request) {
	state, err := s.deps.Operations.State(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	state.PluginUpdates = s.pluginUpdateCount(r.Context())
	response.WriteJSON(w, http.StatusOK, state)
}

// pluginUpdateCount is the number of installed plugins with an update in the synced catalog.
// It only reads the cached catalog (no network). The count is advisory, so a failure to read
// the catalog or the inventory reports 0 instead of failing the whole state request.
func (s *Server) pluginUpdateCount(ctx context.Context) int {
	if s.deps.Plugins == nil {
		return 0
	}
	updates, err := s.deps.Plugins.ReadPluginUpdates(ctx)
	if err != nil || updates == nil {
		return 0
	}
	n := 0
	for _, item := range updates.Items {
		if item.State == models.CatalogUpdateAvailable {
			n++
		}
	}
	return n
}

func (s *Server) handleAdoptConfig(w http.ResponseWriter, r *http.Request) {
	row, err := s.deps.Operations.Adopt(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

// Draft endpoints are gone; clients get a stable 410 for one release instead of a 404.
func handleDraftGone(w http.ResponseWriter, r *http.Request) {
	response.WriteCustomError(w, http.StatusGone, "ERR_DRAFTS_REMOVED", "Drafts were removed. Save resources directly and use snapshots to keep restore points.", nil, false)
}
