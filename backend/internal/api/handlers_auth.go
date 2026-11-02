package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/netmonitor/netmonitor/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("netmonitor-dummy-password"), bcrypt.DefaultCost)

func (s *Server) auditLogin(r *http.Request, username, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	ip = strings.Split(ip, ",")[0]
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO audit_logs (username, ip, method, path, status, resource_type, details)
		VALUES ($1,$2,'LOGIN','/api/v1/auth/login',$3,'auth',$4::jsonb)
	`, username, strings.TrimSpace(ip), status, `{"action":"login"}`)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var id, hash, display, role string
	var restrictMaps, restrictGroups, restrictDevices bool
	var allowedProbeTypes []string
	var tokenVersion int
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, password_hash, COALESCE(display_name,''), role, restrict_maps, restrict_groups,
		       restrict_devices, COALESCE(allowed_probe_types,'{}'), token_version
		FROM users WHERE username=$1
	`, req.Username).Scan(&id, &hash, &display, &role, &restrictMaps, &restrictGroups, &restrictDevices, &allowedProbeTypes, &tokenVersion)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		s.auditLogin(r, req.Username, "401")
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		s.auditLogin(r, req.Username, "401")
		writeError(w, http.StatusUnauthorized, "usuario o contraseña incorrectos")
		return
	}
	token, err := auth.Issue(s.cfg.JWTSecret, id, req.Username, role, tokenVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	_, _ = s.pool.Exec(r.Context(), `UPDATE users SET last_login_at=NOW() WHERE id=$1`, id)
	s.auditLogin(r, req.Username, "200")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"token":   token,
		"user": map[string]any{
			"id": id, "username": req.Username, "display_name": display, "role": role,
			"restrict_maps": restrictMaps, "restrict_groups": restrictGroups,
			"restrict_devices": restrictDevices, "allowed_probe_types": allowedProbeTypes,
		},
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "user": u})
}

func (s *Server) dashboardSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}
	user, _ := auth.UserFrom(ctx)
	allowedSet, restrictedGroups := s.allowedDeviceIDs(ctx, user)
	var allowedDevices []string
	if restrictedGroups {
		for id := range allowedSet {
			allowedDevices = append(allowedDevices, id)
		}
	}
	allowedMaps, restrictedMaps := s.allowedMapIDs(ctx, user)
	var allowedMapList []string
	if restrictedMaps {
		for id := range allowedMaps {
			allowedMapList = append(allowedMapList, id)
		}
	}
	var up, warning, down, unreachable, disabled, unknown, devices, maps, probes, activeOutages int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*),
		 count(*) FILTER (WHERE status='up'),
		 count(*) FILTER (WHERE status='warning'),
		 count(*) FILTER (WHERE status IN ('down','acknowledged')),
		 count(*) FILTER (WHERE status='unreachable'),
		 count(*) FILTER (WHERE status='disabled'),
		 count(*) FILTER (WHERE status='unknown')
		FROM devices
		WHERE (NOT $1 OR id = ANY($2::uuid[]))
		  AND (NOT $3 OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=devices.id AND me.map_id = ANY($4::uuid[])))
	`, restrictedGroups, allowedDevices, restrictedMaps, allowedMapList).Scan(&devices, &up, &warning, &down, &unreachable, &disabled, &unknown)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM maps WHERE (NOT $1 OR id = ANY($2::uuid[]))`, restrictedMaps, allowedMapList).Scan(&maps)
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM probes`).Scan(&probes)
	_ = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM outages
		WHERE ended_at IS NULL AND (NOT $1 OR device_id = ANY($2::uuid[]))
		  AND (NOT $3 OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=outages.device_id AND me.map_id = ANY($4::uuid[])))
	`, restrictedGroups, allowedDevices, restrictedMaps, allowedMapList).Scan(&activeOutages)
	out["devices"] = map[string]int{"total": devices, "up": up, "warning": warning, "down": down,
		"unreachable": unreachable, "disabled": disabled, "unknown": unknown}
	out["maps"] = maps
	out["probes"] = probes
	out["active_outages"] = activeOutages
	out["server_time"] = time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}
