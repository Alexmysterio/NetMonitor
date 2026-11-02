package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) listDigestRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, interval_sec, channel_ids, is_enabled, last_sent_at
		FROM digest_rules ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var interval int
		var channels []string
		var enabled bool
		var last any
		if rows.Scan(&id, &name, &interval, &channels, &enabled, &last) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "interval_sec": interval,
				"channel_ids": channels, "is_enabled": enabled, "last_sent_at": last})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type digestRuleRequest struct {
	Name       string   `json:"name"`
	Interval   int      `json:"interval_sec"`
	ChannelIDs []string `json:"channel_ids"`
	IsEnabled  *bool    `json:"is_enabled"`
}

func (s *Server) createDigestRule(w http.ResponseWriter, r *http.Request) {
	var req digestRuleRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Interval < 60 {
		req.Interval = 3600
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []string{}
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO digest_rules (name, interval_sec, channel_ids, is_enabled)
		VALUES ($1,$2,$3,$4) RETURNING id
	`, strings.TrimSpace(req.Name), req.Interval, req.ChannelIDs, enabled).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateDigestRule(w http.ResponseWriter, r *http.Request) {
	var req digestRuleRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Interval < 60 {
		req.Interval = 3600
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []string{}
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE digest_rules SET name=$2, interval_sec=$3, channel_ids=$4,
		 is_enabled=COALESCE($5,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.Interval, req.ChannelIDs, req.IsEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "resumen no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDigestRule(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM digest_rules WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "resumen no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
