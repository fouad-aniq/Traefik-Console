package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/traefik-console/internal/models"
)

func TestEntryPointsAreCreatedEditedAndDeletedOverHTTP(t *testing.T) {
	h, _ := protocolStack(t, liveRuntime{})

	// A mistake comes back as 422 naming the field, so the dialog can point at it.
	w := call(h, "PUT", "/api/v1/entrypoints/app", `{"port":443}`, create...)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"entrypoint:app.port"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Without a precondition nothing is written.
	if w = call(h, "PUT", "/api/v1/entrypoints/app", `{"port":8443}`); w.Code != 428 {
		t.Fatal(w.Code, w.Body.String())
	}

	body := `{"port":8443,"as_default":true,"http3":{},"forwarded_headers":{"trusted_ips":["10.0.0.0/8"]},
		"http":{"tls":{"cert_resolver":"le","options":"modern@file"},"redirect":{"to":":9443","temporary":true},"denied_encoded_characters":["slash"]},
		"transport":{"read_timeout":"30s"},"observability":{"disable_metrics":true}}`
	w = call(h, "PUT", "/api/v1/entrypoints/app", body, create...)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved models.WriteResult[models.EntryPointConfig]
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Resource == nil {
		t.Fatal(err, w.Body.String())
	}
	cfg := saved.Resource.Config
	if cfg.Address != ":8443" || cfg.Protocol != "tcp" || cfg.HTTP.TLS.Options != "modern@file" || cfg.HTTP.TLS.CertResolver != "le" || !cfg.HTTP.Redirect.Temporary || saved.Resource.Version != 1 {
		t.Fatalf("%+v", cfg)
	}
	rendered, _ := json.Marshal(cfg.RenderedNode)
	for _, want := range []string{`"address":":8443"`, `"http3":{}`, `"permanent":false`, `"allowEncodedSlash":false`, `"certResolver":"le"`, `"metrics":false`} {
		if !strings.Contains(string(rendered), want) {
			t.Fatalf("missing %s in %s", want, rendered)
		}
	}

	if w = call(h, "GET", "/api/v1/entrypoints/app", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"origin":"managed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "PUT", "/api/v1/entrypoints/app", `{"port":8444}`, edit("1")...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Declared in traefik.yml: read-only until adopted explicitly.
	if w = call(h, "PUT", "/api/v1/entrypoints/web", `{"port":80}`, create...); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "PUT", "/api/v1/entrypoints/web", `{"port":80,"adopted":true,"http":{"redirect":{"to":"websecure"}}}`, create...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "DELETE", "/api/v1/entrypoints/app", "", edit("2")...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(h, "GET", "/api/v1/entrypoints/app", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
