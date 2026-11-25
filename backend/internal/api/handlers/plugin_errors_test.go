package handlers

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/core/plugins"
	"github.com/traefik-console/internal/engine"
)

func TestPluginInstallAndUpdateErrorsAreSpecificConflicts(t *testing.T) {
	for _, tc := range []struct {
		err      error
		code     string
		contains string
	}{
		{fmt.Errorf("stage: %w", &engine.PluginModuleError{Module: "github.com/org/p", Alias: "my-plugin"}), "ERR_PLUGIN_ALREADY_INSTALLED", `"my-plugin"`},
		{engine.ErrPluginModuleInstalled, "ERR_PLUGIN_ALREADY_INSTALLED", "another alias"},
		{plugins.ErrPluginNoUpdate, "ERR_PLUGIN_NO_UPDATE", "no newer version"},
		{plugins.ErrPluginNotInCatalog, "ERR_PLUGIN_NOT_IN_CATALOG", "refresh the catalog"},
	} {
		w := httptest.NewRecorder()
		response.WriteError(w, tc.err)
		var body struct{ Code, Message string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 409 || body.Code != tc.code || !strings.Contains(body.Message, tc.contains) {
			t.Errorf("%v -> %d %s", tc.err, w.Code, w.Body.String())
		}
	}
}
