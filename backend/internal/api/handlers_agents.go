package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/agents"
)

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token = strings.TrimSpace(authHeader[7:])
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing agent token")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		SELECT id FROM agents WHERE token_hash=$1 AND is_enabled=TRUE
	`, agents.TokenHash(token)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid agent token")
		return
	}
	s.agentMgr.ServeWS(w, r, id)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, version, last_seen_at, max_devices, is_enabled, created_at,
		       (SELECT count(*) FROM devices d WHERE d.agent_id=a.id)
		FROM agents a ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, version string
		var last *time.Time
		var maxDevices, devices int
		var enabled bool
		var created time.Time
		if rows.Scan(&id, &name, &version, &last, &maxDevices, &enabled, &created, &devices) == nil {
			out = append(out, map[string]any{
				"id": id, "name": name, "version": version, "last_seen_at": last,
				"max_devices": maxDevices, "is_enabled": enabled, "created_at": created, "device_count": devices,
				"online": s.agentMgr.Online(id),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		MaxDevices int    `json:"max_devices"`
	}
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.MaxDevices < 0 {
		req.MaxDevices = 0
	}
	token := agents.RandomToken()
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO agents (name, token_hash, max_devices) VALUES ($1,$2,$3) RETURNING id
	`, req.Name, agents.TokenHash(token), req.MaxDevices).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "ya existe un agente con ese nombre")
		return
	}
	connectURL := s.cfg.PublicURL + "/api/v1/agent/ws"
	connectURL = strings.Replace(connectURL, "https://", "wss://", 1)
	connectURL = strings.Replace(connectURL, "http://", "ws://", 1)
	// The plaintext token is returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": req.Name, "token": token,
		"connect_url": connectURL,
	}})
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxDevices *int  `json:"max_devices"`
		Enabled    *bool `json:"is_enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE agents SET max_devices=COALESCE($2,max_devices), is_enabled=COALESCE($3,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.MaxDevices, req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) agentCapacity(ctx context.Context, agentID, excludeDevice string) error {
	var maxDevices, count int
	err := s.pool.QueryRow(ctx, `
		SELECT a.max_devices,
		       (SELECT count(*) FROM devices d WHERE d.agent_id=a.id AND d.id <> $2)
		FROM agents a WHERE a.id=$1 AND a.is_enabled=TRUE
	`, agentID, nullableDeviceID(excludeDevice)).Scan(&maxDevices, &count)
	if err != nil {
		return fmt.Errorf("agente no encontrado o deshabilitado")
	}
	if maxDevices > 0 && count >= maxDevices {
		return fmt.Errorf("el agente alcanzó su cuota de %d dispositivos", maxDevices)
	}
	return nil
}

func nullableDeviceID(id string) string {
	if id == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return id
}

func (s *Server) updateAgentBinary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version   string `json:"version"`
		URL       string `json:"url"`
		SHA256    string `json:"sha256"`
		Signature string `json:"signature"`
	}
	if err := decodeBody(r, &req); err != nil || req.URL == "" || req.SHA256 == "" || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "url, sha256 y signature son obligatorios")
		return
	}
	if len(req.SHA256) != 64 {
		writeError(w, http.StatusBadRequest, "sha256 debe ser hexadecimal de 64 caracteres")
		return
	}
	agentID := chi.URLParam(r, "id")
	if err := s.agentMgr.Update(agentID, req.Version, req.URL, strings.ToLower(req.SHA256), req.Signature); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"success": true})
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM agents WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
