package handlers

import (
	"net/http"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

// writeResult answers every resource write. A dry run returns the preview (200); a saved change
// that still needs a manual Traefik restart returns 202 with ERR_MANUAL_RESTART_REQUIRED so the
// client can tell the user the change is kept but not loaded yet.
func writeResult[T any](w http.ResponseWriter, res *models.WriteResult[T], err error) {
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if res.RestartRequired && res.Preview == nil {
		response.WriteJSON(w, http.StatusAccepted, struct {
			*models.WriteResult[T]
			Error *response.Error `json:"error"`
		}{res, &response.Error{Code: "ERR_MANUAL_RESTART_REQUIRED", Message: engine.ErrManualRestartRequired.Error()}})
		return
	}
	response.WriteJSON(w, http.StatusOK, res)
}
