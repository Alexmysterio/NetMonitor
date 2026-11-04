package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func (s *Server) writeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		u, ok := auth.UserFrom(r.Context())
		if !ok || u.Role == "viewer" {
			writeError(w, http.StatusForbidden, "el rol viewer es de solo lectura")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok || u.Role != "admin" {
			writeError(w, http.StatusForbidden, "se requiere rol administrador")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, username, COALESCE(display_name,''), role, restrict_maps, restrict_groups,
		       restrict_devices, COALESCE(allowed_probe_types,'{}'), created_at, last_login_at
		FROM users ORDER BY username
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, username, display, role string
		var restrictMaps, restrictGroups, restrictDevices bool
		var allowedProbeTypes []string
		var created time.Time
		var last *time.Time
		if rows.Scan(&id, &username, &display, &role, &restrictMaps, &restrictGroups,
			&restrictDevices, &allowedProbeTypes, &created, &last) == nil {
			out = append(out, map[string]any{"id": id, "username": username, "display_name": display,
				"role": role, "restrict_maps": restrictMaps, "restrict_groups": restrictGroups,
				"restrict_devices": restrictDevices, "allowed_probe_types": allowedProbeTypes,
				"created_at": created, "last_login_at": last})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type userRequest struct {
	Username          string   `json:"username"`
	Password          string   `json:"password"`
	DisplayName       string   `json:"display_name"`
	Role              string   `json:"role"`
	RestrictMaps      bool     `json:"restrict_maps"`
	RestrictGroups    bool     `json:"restrict_groups"`
	RestrictDevices   bool     `json:"restrict_devices"`
	AllowedProbeTypes []string `json:"allowed_probe_types"`
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req userRequest
	if err := decodeBody(r, &req); err != nil || req.Username == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "username y password (mínimo 8 caracteres) son obligatorios")
		return
	}
	if req.Role == "" {
		req.Role = "operator"
	}
	if req.Role != "admin" && req.Role != "operator" && req.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "rol inválido")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var id string
	if req.AllowedProbeTypes == nil {
		req.AllowedProbeTypes = []string{}
	}
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO users (username, password_hash, display_name, role, restrict_maps, restrict_groups,
		 restrict_devices, allowed_probe_types)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id
	`, req.Username, string(hash), req.DisplayName, req.Role, req.RestrictMaps, req.RestrictGroups,
		req.RestrictDevices, req.AllowedProbeTypes).Scan(&id)
	if err != nil {
		writeError(w, http.StatusConflict, "el usuario ya existe o no es válido")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DisplayName       *string   `json:"display_name"`
		Role              *string   `json:"role"`
		Password          *string   `json:"password"`
		RestrictMaps      *bool     `json:"restrict_maps"`
		RestrictGroups    *bool     `json:"restrict_groups"`
		RestrictDevices   *bool     `json:"restrict_devices"`
		AllowedProbeTypes *[]string `json:"allowed_probe_types"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Role != nil && *req.Role != "admin" && *req.Role != "operator" && *req.Role != "viewer" {
		writeError(w, http.StatusBadRequest, "rol inválido")
		return
	}
	var hash any
	bumpVersion := false
	if req.Password != nil && len(*req.Password) >= 8 {
		h, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hash = string(h)
		bumpVersion = true
	}
	if req.Role != nil {
		bumpVersion = true
	}
	var username, currentRole string
	_ = s.pool.QueryRow(r.Context(), `SELECT username, role FROM users WHERE id=$1`, chi.URLParam(r, "id")).Scan(&username, &currentRole)
	if req.Role != nil && currentRole == "admin" && *req.Role != "admin" {
		var admins int
		_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE role='admin'`).Scan(&admins)
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "no puedes quitar el último administrador")
			return
		}
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE users SET
		 display_name=COALESCE($2,display_name), role=COALESCE($3,role),
		 password_hash=COALESCE($4,password_hash),
		 restrict_maps=COALESCE($5,restrict_maps),
		 restrict_groups=COALESCE($6,restrict_groups),
		 restrict_devices=COALESCE($7,restrict_devices),
		 allowed_probe_types=COALESCE($8,allowed_probe_types),
		 token_version=token_version + CASE WHEN $9 THEN 1 ELSE 0 END
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.DisplayName, req.Role, hash, req.RestrictMaps,
		req.RestrictGroups, req.RestrictDevices, req.AllowedProbeTypes, bumpVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}
	if username != "" {
		s.hub.DisconnectUser(username)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) getUserMaps(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT map_id::text FROM map_permissions WHERE user_id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": ids})
}

func (s *Server) setUserMaps(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MapIDs []string `json:"map_ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE users SET restrict_maps=TRUE WHERE id=$1`, userID)
	_, _ = tx.Exec(ctx, `DELETE FROM map_permissions WHERE user_id=$1`, userID)
	for _, mapID := range req.MapIDs {
		_, _ = tx.Exec(ctx, `INSERT INTO map_permissions (user_id,map_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, mapID)
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var username string
	_ = s.pool.QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, userID).Scan(&username)
	s.hub.DisconnectUser(username)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) allowedMapIDs(ctx context.Context, u auth.User) (map[string]bool, bool) {
	if u.Role == "admin" || !u.RestrictMaps {
		return nil, false
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE allowed AS (
			SELECT map_id FROM map_permissions WHERE user_id=$1
			UNION
			SELECT m.id FROM maps m JOIN allowed a ON m.parent_map_id=a.map_id
		)
		SELECT map_id::text FROM allowed
	`, u.ID)
	if err != nil {
		return map[string]bool{}, true
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			allowed[id] = true
		}
	}
	return allowed, true
}

func (s *Server) canAccessMap(ctx context.Context, u auth.User, mapID string) bool {
	if u.Role == "admin" || !u.RestrictMaps {
		return true
	}
	var ok bool
	_ = s.pool.QueryRow(ctx, `
		WITH RECURSIVE allowed AS (
			SELECT map_id FROM map_permissions WHERE user_id=$1
			UNION
			SELECT m.id FROM maps m JOIN allowed a ON m.parent_map_id=a.map_id
		)
		SELECT EXISTS(SELECT 1 FROM allowed WHERE map_id=$2)
	`, u.ID, mapID).Scan(&ok)
	return ok
}

func (s *Server) allowedDeviceIDs(ctx context.Context, u auth.User) (map[string]bool, bool) {
	if u.Role == "admin" || (!u.RestrictGroups && !u.RestrictDevices) {
		return nil, false
	}
	rows, err := s.pool.Query(ctx, `
		SELECT device_id::text FROM user_device_permissions WHERE user_id=$1
		UNION
		SELECT m.device_id::text
		FROM user_device_groups ug JOIN device_group_members m ON m.group_id=ug.group_id
		WHERE ug.user_id=$1
	`, u.ID)
	if err != nil {
		return map[string]bool{}, true
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			allowed[id] = true
		}
	}
	return allowed, true
}

func (s *Server) canAccessDevice(ctx context.Context, u auth.User, deviceID string) bool {
	if u.Role == "admin" {
		return true
	}
	if u.RestrictMaps {
		var ok bool
		_ = s.pool.QueryRow(ctx, `
			WITH RECURSIVE allowed AS (
				SELECT map_id FROM map_permissions WHERE user_id=$2
				UNION
				SELECT m.id FROM maps m JOIN allowed a ON m.parent_map_id=a.map_id
			)
			SELECT EXISTS(
				SELECT 1 FROM map_elements me JOIN allowed a ON a.map_id=me.map_id
				WHERE me.device_id=$1
			)
		`, deviceID, u.ID).Scan(&ok)
		if !ok {
			return false
		}
	}
	if u.RestrictGroups || u.RestrictDevices {
		var ok bool
		_ = s.pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM user_device_permissions WHERE user_id=$1 AND device_id=$2
				UNION
				SELECT 1 FROM user_device_groups ug
				JOIN device_group_members m ON m.group_id=ug.group_id
				WHERE ug.user_id=$1 AND m.device_id=$2
			)
		`, u.ID, deviceID).Scan(&ok)
		return ok
	}
	return true
}

func probeTypeAllowed(u auth.User, probeType string) bool {
	if len(u.AllowedProbeTypes) == 0 {
		return true
	}
	for _, allowed := range u.AllowedProbeTypes {
		if strings.EqualFold(allowed, probeType) {
			return true
		}
	}
	return false
}

func (s *Server) accessGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok || u.Role == "admin" || (!u.RestrictMaps && !u.RestrictGroups && !u.RestrictDevices) {
			next.ServeHTTP(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 2 {
			switch parts[0] {
			case "maps":
				if parts[1] != "import" && !s.canAccessMap(r.Context(), u, parts[1]) {
					writeError(w, http.StatusForbidden, "sin acceso a este mapa")
					return
				}
			case "devices":
				// /devices/bulk validates every device id inside the handler.
				if parts[1] != "" && parts[1] != "bulk" && !s.canAccessDevice(r.Context(), u, parts[1]) {
					writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if u.ID == chi.URLParam(r, "id") {
		writeError(w, http.StatusBadRequest, "no puedes eliminar tu propio usuario")
		return
	}
	var username, role string
	_ = s.pool.QueryRow(r.Context(), `SELECT username, role FROM users WHERE id=$1`, chi.URLParam(r, "id")).Scan(&username, &role)
	if role == "admin" {
		var admins int
		_ = s.pool.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE role='admin'`).Scan(&admins)
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "no puedes eliminar el último administrador")
			return
		}
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "usuario no encontrado")
		return
	}
	s.hub.DisconnectUser(username)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
