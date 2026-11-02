package api

import (
	"encoding/json"
	"net/http"
	"time"
)

func (s *Server) getGlobalHolidays(w http.ResponseWriter, r *http.Request) {
	var raw string
	_ = s.pool.QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key='global_holidays'`).Scan(&raw)
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) keyStatus(w http.ResponseWriter, r *http.Request) {
	var created string
	_ = s.pool.QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key='encryption_key_created_at'`).Scan(&created)
	age := 0
	if t, err := time.Parse("2006-01-02 15:04:05.999999-07", created); err == nil {
		age = int(time.Since(t).Hours() / 24)
	}
	needs := s.cfg.KeyRotationDays > 0 && age >= s.cfg.KeyRotationDays
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"created_at": created, "age_days": age, "rotation_days": s.cfg.KeyRotationDays,
		"needs_rotation": needs,
	}})
}

func (s *Server) setGlobalHolidays(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Holidays []string `json:"holidays"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	clean := []string{}
	for _, day := range req.Holidays {
		if _, err := time.Parse("2006-01-02", day); err != nil {
			writeError(w, http.StatusBadRequest, "fecha inválida: usa YYYY-MM-DD")
			return
		}
		clean = append(clean, day)
	}
	raw, _ := json.Marshal(clean)
	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO system_settings (key, value) VALUES ('global_holidays', $1)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()
	`, string(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": clean})
}
