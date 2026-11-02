package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listAgentReleases(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, version, channel, url, sha256, signature, created_at
		FROM agent_releases ORDER BY created_at DESC LIMIT 100
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version, channel, url, sha, sig string
		var created any
		if rows.Scan(&id, &version, &channel, &url, &sha, &sig, &created) == nil {
			out = append(out, map[string]any{"id": id, "version": version, "channel": channel,
				"url": url, "sha256": sha, "signature": sig, "created_at": created})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createAgentRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version   string `json:"version"`
		Channel   string `json:"channel"`
		URL       string `json:"url"`
		SHA256    string `json:"sha256"`
		Signature string `json:"signature"`
	}
	if err := decodeBody(r, &req); err != nil || req.Version == "" || req.URL == "" || len(req.SHA256) != 64 || req.Signature == "" {
		writeError(w, http.StatusBadRequest, "version, url, sha256 y signature son obligatorios")
		return
	}
	if req.Channel != "beta" {
		req.Channel = "stable"
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO agent_releases (version, channel, url, sha256, signature)
		VALUES ($1,$2,$3,$4,$5) RETURNING id
	`, req.Version, req.Channel, req.URL, strings.ToLower(req.SHA256), req.Signature).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "la release ya existe o no es válida")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) deleteAgentRelease(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM agent_releases WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "release no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) batchUpdateAgents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel   string   `json:"channel"`
		ReleaseID string   `json:"release_id"`
		AgentIDs  []string `json:"agent_ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.ReleaseID == "" && req.Channel == "" {
		req.Channel = "stable"
	}
	var version, url, sha, sig string
	err := s.pool.QueryRow(r.Context(), `
		SELECT version, url, sha256, signature FROM agent_releases
		WHERE ($1 <> '' AND id=$1::uuid) OR ($1 = '' AND channel=$2)
		ORDER BY created_at DESC LIMIT 1
	`, req.ReleaseID, req.Channel).Scan(&version, &url, &sha, &sig)
	if err != nil {
		writeError(w, http.StatusNotFound, "no hay releases para ese canal")
		return
	}
	if len(req.AgentIDs) == 0 {
		rows, err := s.pool.Query(r.Context(), `SELECT id FROM agents WHERE is_enabled=TRUE`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				req.AgentIDs = append(req.AgentIDs, id)
			}
		}
		rows.Close()
	}
	sent, failed := 0, 0
	for _, id := range req.AgentIDs {
		if err := s.agentMgr.Update(id, version, url, sha, sig); err != nil {
			failed++
		} else {
			sent++
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"success": true, "data": map[string]any{
		"version": version, "channel": req.Channel, "sent": sent, "failed": failed,
	}})
}
