package handlers

import (
	"context"
	"net/http"

	"github.com/traefik-console/internal/api/request"
	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/middlewares"
	"github.com/traefik-console/internal/models"
)

func (s *Server) handleListMiddlewares(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Middlewares.ListMiddlewares(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if routers, chains, ok := s.middlewareConsumers(r.Context(), nil); ok {
		for i := range items {
			items[i].Config.UsedBy = middlewares.UsageOf(items[i].Name, models.MiddlewareProtocol(items[i].Config), routers, chains)
		}
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleUpsertMiddleware(w http.ResponseWriter, r *http.Request) {
	var in models.MiddlewareConfig
	// Only nested values inside the opaque spec may be null.
	if !request.DecodeObject(w, r, &in, request.Options{NullPolicy: request.SpecNulls}) {
		return
	}
	res, err := s.deps.Middlewares.UpsertMiddleware(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteMiddleware(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Middlewares.DeleteMiddleware(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleListChains(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.Middlewares.ListChains(r.Context())
	if err != nil {
		response.WriteError(w, err)
		return
	}
	if routers, chains, ok := s.middlewareConsumers(r.Context(), items); ok {
		for i := range items {
			items[i].Config.UsedBy = middlewares.Usage(items[i].Name, routers, chains)
		}
	}
	response.WriteJSON(w, http.StatusOK, items)
}

func (s *Server) handleUpsertChain(w http.ResponseWriter, r *http.Request) {
	var in models.ChainConfig
	if !request.DecodeObject(w, r, &in, request.Options{}) {
		return
	}
	res, err := s.deps.Middlewares.UpsertChain(r.Context(), r.PathValue("name"), in, request.DryRun(r))
	writeResult(w, res, err)
}

func (s *Server) handleDeleteChain(w http.ResponseWriter, r *http.Request) {
	res, err := s.deps.Middlewares.DeleteChain(r.Context(), r.PathValue("name"), request.DryRun(r))
	writeResult(w, res, err)
}

// middlewareConsumers reads what can reference a middleware or chain: routers and chains. Usage is
// advisory: when either cannot be read, ok is false and the list is returned without it (deleting
// is still checked by the server). known is the chain list when the caller already has it.
func (s *Server) middlewareConsumers(ctx context.Context, known []models.UIResource[models.ChainConfig]) ([]models.UIResource[models.RouterConfig], []models.UIResource[models.ChainConfig], bool) {
	routers, err := s.deps.Routers.ListRouters(ctx)
	if err != nil {
		return nil, nil, false
	}
	chains := known
	if chains == nil {
		if chains, err = s.deps.Middlewares.ListChains(ctx); err != nil {
			return nil, nil, false
		}
	}
	return routers, chains, true
}
