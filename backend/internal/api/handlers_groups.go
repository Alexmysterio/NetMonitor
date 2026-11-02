package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Server) listDeviceGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT g.id, g.name, g.description, g.color,
		       COALESCE(ARRAY(SELECT m.device_id::text FROM device_group_members m WHERE m.group_id=g.id),'{}'),
		       COALESCE(g.poll_interval_sec,0), COALESCE(g.poll_down_retries,0),
		       COALESCE(g.poll_agent_id::text,''), COALESCE(g.poll_notifications_enabled::int,-1)
		FROM device_groups g ORDER BY g.name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, description, color, pollAgent string
		var deviceIDs []string
		var pollInterval, pollRetries, pollNotify int
		if rows.Scan(&id, &name, &description, &color, &deviceIDs, &pollInterval, &pollRetries, &pollAgent, &pollNotify) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "description": description,
				"color": color, "device_ids": deviceIDs, "device_count": len(deviceIDs),
				"poll_interval_sec": pollInterval, "poll_down_retries": pollRetries,
				"poll_agent_id": pollAgent, "poll_notifications": pollNotify})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type deviceGroupRequest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Color             string   `json:"color"`
	DeviceIDs         []string `json:"device_ids"`
	PollInterval      int      `json:"poll_interval_sec"`
	PollRetries       int      `json:"poll_down_retries"`
	PollAgentID       string   `json:"poll_agent_id"`
	PollNotifications int      `json:"poll_notifications"`
}

func (s *Server) createDeviceGroup(w http.ResponseWriter, r *http.Request) {
	var req deviceGroupRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Color == "" {
		req.Color = "#2563eb"
	}
	if req.DeviceIDs == nil {
		req.DeviceIDs = []string{}
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var pollInterval, pollRetries, pollAgent, pollNotify any
	if req.PollInterval > 0 {
		pollInterval = req.PollInterval
	}
	if req.PollRetries > 0 {
		pollRetries = req.PollRetries
	}
	if strings.TrimSpace(req.PollAgentID) != "" {
		pollAgent = strings.TrimSpace(req.PollAgentID)
	}
	if req.PollNotifications >= 0 {
		pollNotify = req.PollNotifications == 1
	}
	var id string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO device_groups (name, description, color, poll_interval_sec, poll_down_retries,
		  poll_agent_id, poll_notifications_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id
	`, strings.TrimSpace(req.Name), req.Description, req.Color, pollInterval, pollRetries, pollAgent, pollNotify).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "el grupo ya existe o no es válido")
		return
	}
	if err := replaceGroupDevices(r.Context(), tx, id, req.DeviceIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateDeviceGroup(w http.ResponseWriter, r *http.Request) {
	var req deviceGroupRequest
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Color == "" {
		req.Color = "#2563eb"
	}
	if req.DeviceIDs == nil {
		req.DeviceIDs = []string{}
	}
	var pollInterval, pollRetries, pollAgent, pollNotify any
	if req.PollInterval > 0 {
		pollInterval = req.PollInterval
	}
	if req.PollRetries > 0 {
		pollRetries = req.PollRetries
	}
	if strings.TrimSpace(req.PollAgentID) != "" {
		pollAgent = strings.TrimSpace(req.PollAgentID)
	}
	if req.PollNotifications >= 0 {
		pollNotify = req.PollNotifications == 1
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	tag, err := tx.Exec(r.Context(), `
		UPDATE device_groups SET name=$2, description=$3, color=$4,
		  poll_interval_sec=$5, poll_down_retries=$6, poll_agent_id=$7, poll_notifications_enabled=$8
		WHERE id=$1
	`, chi.URLParam(r, "id"), strings.TrimSpace(req.Name), req.Description, req.Color,
		pollInterval, pollRetries, pollAgent, pollNotify)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "grupo no encontrado")
		return
	}
	if err := replaceGroupDevices(r.Context(), tx, chi.URLParam(r, "id"), req.DeviceIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDeviceGroup(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM device_groups WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "grupo no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) getUserGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT group_id::text FROM user_device_groups WHERE user_id=$1
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) setUserGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.GroupIDs == nil {
		req.GroupIDs = []string{}
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), `DELETE FROM user_device_groups WHERE user_id=$1`, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE users SET restrict_groups=TRUE WHERE id=$1`, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(req.GroupIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO user_device_groups (user_id, group_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING
		`, chi.URLParam(r, "id"), req.GroupIDs); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var username string
	_ = s.pool.QueryRow(r.Context(), `SELECT username FROM users WHERE id=$1`, chi.URLParam(r, "id")).Scan(&username)
	s.hub.DisconnectUser(username)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) getUserDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT device_id::text FROM user_device_permissions WHERE user_id=$1
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) setUserDevices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceIDs []string `json:"device_ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DeviceIDs == nil {
		req.DeviceIDs = []string{}
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), `DELETE FROM user_device_permissions WHERE user_id=$1`, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE users SET restrict_devices=TRUE WHERE id=$1`, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(req.DeviceIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO user_device_permissions (user_id, device_id)
			SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING
		`, chi.URLParam(r, "id"), req.DeviceIDs); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var username string
	_ = s.pool.QueryRow(r.Context(), `SELECT username FROM users WHERE id=$1`, chi.URLParam(r, "id")).Scan(&username)
	s.hub.DisconnectUser(username)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func replaceGroupDevices(ctx context.Context, tx pgx.Tx, groupID string, deviceIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM device_group_members WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	if len(deviceIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO device_group_members (group_id, device_id)
		SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING
	`, groupID, deviceIDs)
	return err
}
