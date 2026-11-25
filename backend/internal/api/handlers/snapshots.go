package handlers

import (
	"net/http"
	"strconv"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
	"github.com/traefik-console/internal/redaction"
)

const (
	defaultPageLimit = 25
	maxPageLimit     = 100
)

type page[T any] struct {
	Items      []T   `json:"items"`
	NextCursor int64 `json:"next_cursor,omitempty"`
}

func pageParams(r *http.Request) (limit int, cursor int64, err error) {
	limit = defaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxPageLimit {
			return 0, 0, response.ErrValidation("Invalid limit", map[string]string{"limit": "Must be between 1 and 100"})
		}
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		if cursor, err = strconv.ParseInt(v, 10, 64); err != nil || cursor < 0 {
			return 0, 0, response.ErrValidation("Invalid cursor", map[string]string{"cursor": "Must be a positive integer"})
		}
	}
	return limit, cursor, nil
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		response.WriteError(w, response.ErrValidation("Invalid ID", nil))
		return 0, false
	}
	return id, true
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := pageParams(r)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	rows, err := s.deps.History.ListSnapshots(r.Context(), limit+1, cursor)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	out := page[models.SnapshotRow]{Items: rows}
	if len(rows) > limit {
		out.Items, out.NextCursor = rows[:limit], rows[limit-1].ID
	}
	if out.Items == nil {
		out.Items = []models.SnapshotRow{}
	}
	response.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var in models.SnapshotCreateRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	row, err := s.deps.Operations.CreateSnapshot(r.Context(), in.Name, in.Description)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, row)
}

func (s *Server) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	row, err := s.deps.History.GetSnapshotMeta(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if row == nil {
		response.WriteError(w, engine.ErrSnapshotNotFound)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

func (s *Server) handlePatchSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in models.SnapshotPatch
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	row, err := s.deps.Operations.UpdateSnapshot(r.Context(), id, in)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, row)
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := s.deps.Operations.DeleteSnapshot(r.Context(), id, force); err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleSnapshotConfig returns the saved files, redacted. Secrets never leave this handler.
func (s *Server) handleSnapshotConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	cfg, err := s.deps.Operations.SnapshotConfig(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	cfg.StaticYAML = redaction.YAML([]byte(cfg.StaticYAML))
	cfg.DynamicYAML = redaction.YAML([]byte(cfg.DynamicYAML))
	cfg.DomainsYAML = redaction.YAML([]byte(cfg.DomainsYAML))
	response.WriteJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSnapshotResources(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	items, err := s.deps.Operations.SnapshotResources(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleRestorePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	preview, err := s.deps.Operations.PreviewRestore(r.Context(), id)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, preview)
}

func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in models.RestoreRequest
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Operations.RestoreSnapshot(r.Context(), id, in)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if res.Operation != nil && res.Operation.Status == models.OpStatusRestartRequired {
		response.WriteJSON(w, http.StatusAccepted, struct {
			*models.RestoreResult
			Error *response.Error `json:"error"`
		}{res, &response.Error{Code: "ERR_MANUAL_RESTART_REQUIRED", Message: engine.ErrManualRestartRequired.Error()}})
		return
	}
	response.WriteJSON(w, http.StatusOK, res)
}

func (s *Server) handleCompareSnapshots(w http.ResponseWriter, r *http.Request) {
	from, ok := pathID(w, r, "from")
	if !ok {
		return
	}
	to, ok := pathID(w, r, "to")
	if !ok {
		return
	}
	preview, err := s.deps.Operations.CompareSnapshots(r.Context(), from, to)
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, preview)
}
