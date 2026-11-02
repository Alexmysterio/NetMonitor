package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

func hashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Server) apiKeyUser(ctx context.Context, raw string) (auth.User, bool) {
	if raw == "" {
		return auth.User{}, false
	}
	var id, name, role string
	var enabled bool
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, role, is_enabled FROM api_keys WHERE key_hash=$1
	`, hashAPIKey(raw)).Scan(&id, &name, &role, &enabled)
	if err != nil || !enabled {
		return auth.User{}, false
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at=NOW()
		WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < NOW() - interval '1 minute')
	`, id)
	return auth.User{Username: "api:" + name, Role: role}, true
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, role, is_enabled, last_used_at, created_at
		FROM api_keys ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, role string
		var enabled bool
		var last *time.Time
		var created time.Time
		if rows.Scan(&id, &name, &role, &enabled, &last, &created) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "role": role,
				"is_enabled": enabled, "last_used_at": last, "created_at": created})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if req.Role != "admin" && req.Role != "operator" && req.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "role debe ser admin, operator o viewer")
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := "nm_" + base64.RawURLEncoding.EncodeToString(buf)
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO api_keys (name, key_hash, role) VALUES ($1,$2,$3) RETURNING id
	`, strings.TrimSpace(req.Name), hashAPIKey(token), req.Role).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id, "token": token}})
}

func (s *Server) updateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role    *string `json:"role"`
		Enabled *bool   `json:"is_enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Role != nil && *req.Role != "admin" && *req.Role != "operator" && *req.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "role debe ser admin, operator o viewer")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE api_keys SET role=COALESCE($2,role), is_enabled=COALESCE($3,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Role, req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "API key no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM api_keys WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "API key no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
