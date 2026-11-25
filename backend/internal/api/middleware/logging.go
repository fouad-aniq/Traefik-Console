package middleware

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/traefik-console/internal/api/response"
)

type traceContextKey struct{}
type requestTrace struct{ route, actor string }

// One wrapper owns final response state, recovery and a completion log for every request.
func requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		trace := &requestTrace{actor: "anonymous"}
		recorder := response.NewRecorder(w)
		defer func() {
			if recover() != nil {
				if recorder.Status() == 0 {
					response.WriteError(recorder, response.ErrInternal("Unhandled request failure", nil))
				} else {
					log.Printf("ERROR operation_id=%s code=ERR_PANIC_AFTER_COMMIT", response.OperationID(recorder))
				}
			}
			status := recorder.Status()
			if status == 0 {
				status = http.StatusOK
			}
			log.Printf("operation_id=%s actor=%q method=%q route=%q status=%d duration_ms=%d", response.OperationID(recorder), trace.actor, r.Method, trace.route, status, time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), traceContextKey{}, trace)))
	})
}

// SetRoute records the registered pattern without exporting request trace internals.
func SetRoute(ctx context.Context, pattern string) {
	if trace, ok := ctx.Value(traceContextKey{}).(*requestTrace); ok {
		trace.route = pattern
	}
}

// SetActor records who made the request for the completion log line.
func SetActor(ctx context.Context, actor string) {
	if trace, ok := ctx.Value(traceContextKey{}).(*requestTrace); ok {
		trace.actor = actor
	}
}
