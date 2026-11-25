package handlers

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handlePluginCatalogPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query, module := q.Get("q"), q.Get("module")
	for _, text := range []string{query, module} {
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxCatalogQueryCharacters {
			response.WriteError(w, response.ErrValidation("Catalog search must not exceed 256 characters", nil))
			return
		}
	}

	limit, offset := 25, 0
	var err error
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			limit = 0
		}
	}
	if q.Has("offset") {
		offset, err = strconv.Atoi(q.Get("offset"))
		if err != nil {
			offset = -1
		}
	}

	order := q.Get("order")

	availability := q.Get("availability")
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 || (order != "" && order != "asc" && order != "desc") || (query != "" && module != "") || (availability != "" && availability != "available") || (availability != "" && module != "") {
		response.WriteError(w, response.ErrValidation("Invalid catalog pagination or search", nil))
		return
	}

	var page *models.CatalogPageView
	if availability == "available" {
		page, err = s.deps.Plugins.ReadAvailableCatalogPage(r.Context(), query, limit, offset, order == "desc")
	} else {
		page, err = s.deps.Plugins.ReadCatalogPage(r.Context(), query, module, limit, offset, order == "desc")
	}
	if err != nil {
		response.WriteError(w, err)
		return
	}
	response.WriteJSON(w, 200, page)
}
