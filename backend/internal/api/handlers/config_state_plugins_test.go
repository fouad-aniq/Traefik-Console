package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/middleware"
	"github.com/traefik-console/internal/models"
)

type stateOperations struct{ operationRoutes }

func (stateOperations) State(context.Context) (*models.ConfigState, error) {
	return &models.ConfigState{}, nil
}

type updatePlugins struct {
	pluginRoutes
	updates *models.PluginUpdates
	err     error
}

func (f updatePlugins) ReadPluginUpdates(context.Context) (*models.PluginUpdates, error) {
	return f.updates, f.err
}

func TestConfigStateCountsPluginUpdates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plugins updatePlugins
		want    string
	}{
		{"counts only available updates", updatePlugins{updates: &models.PluginUpdates{Items: []models.PluginUpdate{
			{Alias: "a", State: models.CatalogUpdateAvailable},
			{Alias: "b", State: "up_to_date"},
			{Alias: "c", State: models.CatalogUpdateAvailable},
			{Alias: "d", State: "not_in_catalog"},
			{Alias: "e", State: "unknown"},
		}}}, `"plugin_updates":2`},
		{"unreadable catalog is advisory", updatePlugins{err: errors.New("catalog unavailable")}, `"plugin_updates":0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &routeCalls{}
			d := routeDependencies(t, calls)
			d.Operations = stateOperations{operationRoutes{c: calls}}
			tc.plugins.pluginRoutes = pluginRoutes{c: calls}
			d.Plugins = tc.plugins
			h, err := NewServer(d, middleware.Policy{})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/v1/config/state", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
