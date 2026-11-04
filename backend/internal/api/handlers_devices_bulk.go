package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/netmonitor/netmonitor/internal/auth"
)

type bulkDeviceRequest struct {
	DeviceIDs     []string `json:"device_ids"`
	Action        string   `json:"action"`
	SNMPProfileID string   `json:"snmp_profile_id"`
	Tags          []string `json:"tags"`
	Enabled       *bool    `json:"enabled"`
	Note          string   `json:"note"`
	Duration      int      `json:"duration_minutes"`
}

// bulkDevices applies one operation to a set of devices. Every device is
// checked against the caller's permissions before any mutation is made.
func (s *Server) bulkDevices(w http.ResponseWriter, r *http.Request) {
	var req bulkDeviceRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ids := uniqueDeviceIDs(req.DeviceIDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "selecciona al menos un dispositivo")
		return
	}
	if len(ids) > 1000 {
		writeError(w, http.StatusBadRequest, "máximo 1000 dispositivos por operación")
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	user, _ := auth.UserFrom(r.Context())
	for _, id := range ids {
		if !s.canAccessDevice(r.Context(), user, id) {
			writeError(w, http.StatusForbidden, "sin acceso a uno de los dispositivos")
			return
		}
	}

	rows, err := s.pool.Query(r.Context(), `SELECT id::text FROM devices WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		writeError(w, http.StatusBadRequest, "identificadores de dispositivo no válidos")
		return
	}
	existing := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			existing[id] = true
		}
	}
	rows.Close()
	if rows.Err() != nil || len(existing) != len(ids) {
		writeError(w, http.StatusNotFound, "uno o más dispositivos ya no existen")
		return
	}

	affected := int64(0)
	switch action {
	case "snmp_profile":
		profileID := strings.TrimSpace(req.SNMPProfileID)
		var profile any
		if profileID != "" {
			var exists bool
			if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM snmp_profiles WHERE id=$1)`, profileID).Scan(&exists); err != nil || !exists {
				writeError(w, http.StatusBadRequest, "perfil SNMP no encontrado")
				return
			}
			profile = profileID
		}
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET snmp_profile_id=$2, updated_at=NOW() WHERE id = ANY($1::uuid[])
		`, ids, profile)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
	case "tags_add":
		tags := cleanBulkTags(req.Tags)
		if len(tags) == 0 {
			writeError(w, http.StatusBadRequest, "indica al menos una etiqueta")
			return
		}
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET tags = ARRAY(SELECT DISTINCT unnest(tags || $2::text[])), updated_at=NOW()
			WHERE id = ANY($1::uuid[])
		`, ids, tags)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
	case "tags_remove":
		tags := cleanBulkTags(req.Tags)
		if len(tags) == 0 {
			writeError(w, http.StatusBadRequest, "indica al menos una etiqueta")
			return
		}
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET tags = ARRAY(SELECT t FROM unnest(tags) AS t WHERE t <> ALL($2::text[])), updated_at=NOW()
			WHERE id = ANY($1::uuid[])
		`, ids, tags)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
	case "monitoring":
		if req.Enabled == nil {
			writeError(w, http.StatusBadRequest, "indica si activar o pausar la monitorización")
			return
		}
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET is_monitoring_enabled=$2, updated_at=NOW() WHERE id = ANY($1::uuid[])
		`, ids, *req.Enabled)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
	case "acknowledge":
		note := strings.TrimSpace(req.Note)
		if note == "" {
			note = "Ack masivo desde inventario"
		}
		var expires any
		if req.Duration > 0 {
			expires = time.Now().Add(time.Duration(req.Duration) * time.Minute)
		}
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET is_acknowledged=TRUE, ack_by=$2, ack_note=$3, ack_expires_at=$4, updated_at=NOW()
			WHERE id = ANY($1::uuid[])
		`, ids, user.Username, note, expires)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE outages SET acknowledged=TRUE, acknowledged_by=$2, acknowledged_at=NOW()
			WHERE device_id = ANY($1::uuid[]) AND ended_at IS NULL
		`, ids, user.Username)
	case "unacknowledge":
		ct, err := s.pool.Exec(r.Context(), `
			UPDATE devices SET is_acknowledged=FALSE, ack_by=NULL, ack_note=NULL, ack_expires_at=NULL, updated_at=NOW()
			WHERE id = ANY($1::uuid[])
		`, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE outages SET acknowledged=FALSE, acknowledged_by=NULL, acknowledged_at=NULL
			WHERE device_id = ANY($1::uuid[]) AND ended_at IS NULL
		`, ids)
	case "poll_now":
		if _, err := s.pool.Exec(r.Context(), `
			UPDATE device_services SET last_check_at=NULL WHERE device_id = ANY($1::uuid[])
		`, ids); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = int64(len(ids))
	case "delete":
		ct, err := s.pool.Exec(r.Context(), `DELETE FROM devices WHERE id = ANY($1::uuid[])`, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		affected = ct.RowsAffected()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"deleted": affected, "action": action,
		}})
		return
	default:
		writeError(w, http.StatusBadRequest, "acción masiva no soportada")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"updated": affected, "action": action,
	}})
}

func uniqueDeviceIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func cleanBulkTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 64 {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
	}
	return out
}
