package request

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/traefik-console/internal/api/response"
	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

// NameFunc resolves the resource name a write targets (usually the {name} path value).
type NameFunc func(*http.Request) string

// PathName reads the {name} path value.
func PathName(r *http.Request) string { return r.PathValue("name") }

// Singleton names a fixed resource, e.g. the CrowdSec integration.
func Singleton(name string) NameFunc { return func(*http.Request) string { return name } }

// WithResourceVersion guards a single-resource write. It requires If-Match "<version>"
// (update/delete) or If-None-Match: * (create, expected version 0) and carries the
// precondition to the operation boundary, where it is re-checked under the lock.
// A dry run (?dry_run=true) previews against the same precondition.
func WithResourceVersion(kind models.ItemKind, name NameFunc, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := models.ItemRef{Kind: kind, Name: name(r)}
		ifMatch := r.Header.Values("If-Match")
		ifNone := strings.TrimSpace(r.Header.Get("If-None-Match"))
		switch {
		case len(ifMatch) > 1 || ifNone != "" && ifNone != "*" || len(ifMatch) == 1 && ifNone != "":
			response.WriteCustomError(w, http.StatusBadRequest, "BAD_REQUEST", "request.invalid_precondition", nil, false)
			return
		case ifNone == "*":
			next(w, r.WithContext(engine.WithExpectedVersion(r.Context(), ref, 0, true)))
			return
		case len(ifMatch) == 0 || strings.TrimSpace(ifMatch[0]) == "":
			response.WriteError(w, engine.ErrPreconditionRequired)
			return
		}
		raw := strings.Trim(strings.TrimSpace(ifMatch[0]), `"`)
		version, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || version < 1 {
			response.WriteCustomError(w, http.StatusBadRequest, "BAD_REQUEST", "request.invalid_version", nil, false)
			return
		}
		next(w, r.WithContext(engine.WithExpectedVersion(r.Context(), ref, version, false)))
	}
}

// WithIdempotencyKey carries an optional Idempotency-Key header to the operation boundary.
func WithIdempotencyKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
			r = r.WithContext(engine.WithRequestKey(r.Context(), key))
		}
		next(w, r)
	}
}

// DryRun reports whether the request asks for a preview only.
func DryRun(r *http.Request) models.WriteOptions {
	v := r.URL.Query().Get("dry_run")
	return models.WriteOptions{DryRun: v == "true" || v == "1"}
}
