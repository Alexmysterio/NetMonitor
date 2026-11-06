package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// selectiveTimeout keeps normal API calls at 30 seconds, gives long-running
// report/import/batch operations a bounded 5 minute budget, and never wraps
// hijacked WebSocket connections with an HTTP context deadline.
func selectiveTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/ws" || path == "/api/v1/agent/ws" {
			next.ServeHTTP(w, r)
			return
		}
		timeout := 30 * time.Second
		switch {
		case path == "/api/v1/discovery/scan":
			timeout = 10 * time.Minute
		case path == "/api/v1/reports/generate",
			path == "/api/v1/reports/storage/test",
			path == "/api/v1/routeros/inventory/refresh",
			path == "/api/v1/traps/test",
			path == "/api/v1/imports/dude/preview":
			timeout = 5 * time.Minute
		case strings.HasPrefix(path, "/api/v1/reports/schedules/") && strings.HasSuffix(path, "/run"):
			timeout = 5 * time.Minute
		case strings.HasPrefix(path, "/api/v1/imports/dude/") && strings.HasSuffix(path, "/apply"):
			timeout = 5 * time.Minute
		case strings.HasPrefix(path, "/api/v1/routeros/jobs/") && strings.HasSuffix(path, "/dry-run"):
			timeout = 5 * time.Minute
		}
		middleware.Timeout(timeout)(next).ServeHTTP(w, r)
	})
}
