package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/netmonitor/netmonitor/internal/auth"
)

// auditMiddleware records mutating API calls without capturing request bodies
// (which may contain SNMP communities, tokens or passwords).
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		u, _ := auth.UserFrom(r.Context())
		ip := r.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = r.RemoteAddr
		}
		ip = strings.Split(ip, ",")[0]
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
		resourceType, resourceID := "", ""
		if len(parts) > 0 {
			resourceType = parts[0]
		}
		if len(parts) > 1 {
			resourceID = parts[1]
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var userID any
			if u.ID != "" {
				userID = u.ID
			}
			_, _ = s.pool.Exec(ctx, `
				INSERT INTO audit_logs (user_id, username, ip, method, path, status, resource_type, resource_id, details)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}')
			`, userID, u.Username, strings.TrimSpace(ip), r.Method, r.URL.Path, rec.status, resourceType, resourceID)
		}()
	})
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, at, username, ip, method, path, status, resource_type, resource_id
		FROM audit_logs ORDER BY at DESC LIMIT 500
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var at time.Time
		var username, ip, method, path, resourceType, resourceID string
		var status int
		if rows.Scan(&id, &at, &username, &ip, &method, &path, &status, &resourceType, &resourceID) == nil {
			out = append(out, map[string]any{"id": id, "at": at, "username": username, "ip": ip,
				"method": method, "path": path, "status": status, "resource_type": resourceType, "resource_id": resourceID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}
