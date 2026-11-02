package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

func (s *Server) listMaintenanceWindows(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.pool.Query(r.Context(), `
		SELECT w.id, w.name, w.scope_type, COALESCE(w.scope_id::text,''), w.scope_tag,
		       w.starts_at, w.ends_at, w.reason, w.created_by, w.created_at,
		       COALESCE(m.name, d.name, l.name, '')
		FROM maintenance_windows w
		LEFT JOIN maps m ON w.scope_type='map' AND m.id=w.scope_id
		LEFT JOIN devices d ON w.scope_type='device' AND d.id=w.scope_id
		LEFT JOIN address_lists l ON w.scope_type='address_list' AND l.id=w.scope_id
		ORDER BY w.starts_at DESC
		LIMIT 500
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, scopeType, scopeID, scopeTag, reason, createdBy, scopeName string
		var starts, ends, created time.Time
		if rows.Scan(&id, &name, &scopeType, &scopeID, &scopeTag, &starts, &ends, &reason, &createdBy, &created, &scopeName) == nil {
			if user.Role != "admin" {
				switch scopeType {
				case "device":
					if !s.canAccessDevice(r.Context(), user, scopeID) {
						continue
					}
				case "map":
					if !s.canAccessMap(r.Context(), user, scopeID) {
						continue
					}
				default:
					// Tag/address-list windows are global; only admins see them.
					continue
				}
			}
			out = append(out, map[string]any{
				"id": id, "name": name, "scope_type": scopeType, "scope_id": scopeID,
				"scope_tag": scopeTag, "scope_name": scopeName, "starts_at": starts, "ends_at": ends,
				"reason": reason, "created_by": createdBy, "created_at": created,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
		ScopeTag  string `json:"scope_tag"`
		StartsAt  string `json:"starts_at"`
		EndsAt    string `json:"ends_at"`
		Reason    string `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.ScopeType != "device" && req.ScopeType != "map" && req.ScopeType != "tag" && req.ScopeType != "address_list" {
		writeError(w, http.StatusBadRequest, "scope_type debe ser device, map, tag o address_list")
		return
	}
	req.ScopeTag = strings.TrimSpace(req.ScopeTag)
	if req.ScopeType == "tag" && req.ScopeTag == "" {
		writeError(w, http.StatusBadRequest, "scope_tag is required")
		return
	}
	var scope any
	if req.ScopeType != "tag" {
		if req.ScopeID == "" {
			writeError(w, http.StatusBadRequest, "scope_id is required")
			return
		}
		scope = req.ScopeID
	}
	starts, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "starts_at debe ser RFC3339")
		return
	}
	ends, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ends_at debe ser RFC3339")
		return
	}
	if !ends.After(starts) {
		writeError(w, http.StatusBadRequest, "ends_at debe ser posterior a starts_at")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if user.Role != "admin" {
		switch req.ScopeType {
		case "device":
			if !s.canAccessDevice(r.Context(), user, req.ScopeID) {
				writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
				return
			}
		case "map":
			if !s.canAccessMap(r.Context(), user, req.ScopeID) {
				writeError(w, http.StatusForbidden, "sin acceso a este mapa")
				return
			}
		default:
			writeError(w, http.StatusForbidden, "solo un administrador puede crear ventanas globales")
			return
		}
	}
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO maintenance_windows (name, scope_type, scope_id, scope_tag, starts_at, ends_at, reason, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id
	`, strings.TrimSpace(req.Name), req.ScopeType, scope, req.ScopeTag, starts, ends, req.Reason, user.Username).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) deleteMaintenanceWindow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var scopeType, scopeID string
	if err := s.pool.QueryRow(r.Context(), `SELECT scope_type, COALESCE(scope_id::text,'') FROM maintenance_windows WHERE id=$1`, id).Scan(&scopeType, &scopeID); err != nil {
		writeError(w, http.StatusNotFound, "ventana no encontrada")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if user.Role != "admin" {
		switch scopeType {
		case "device":
			if !s.canAccessDevice(r.Context(), user, scopeID) {
				writeError(w, http.StatusForbidden, "sin acceso a esta ventana")
				return
			}
		case "map":
			if !s.canAccessMap(r.Context(), user, scopeID) {
				writeError(w, http.StatusForbidden, "sin acceso a esta ventana")
				return
			}
		default:
			writeError(w, http.StatusForbidden, "solo un administrador puede borrar ventanas globales")
			return
		}
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM maintenance_windows WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "ventana no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
