package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/schedule"
	"github.com/netmonitor/netmonitor/internal/secure"
	probing "github.com/prometheus-community/pro-bing"
)

func (s *Server) listProbes(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, probe_type, description, config::text, available_expression, warning_expression, error_expression,
		       value_expression, unit, default_interval_sec, default_timeout_ms, default_down_retries, is_builtin
		FROM probes ORDER BY is_builtin DESC, name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, typ, desc, cfg, available, warn, errExpr, valueExpr, unit string
		var interval, timeout, retries int
		var builtin bool
		if rows.Scan(&id, &name, &typ, &desc, &cfg, &available, &warn, &errExpr, &valueExpr, &unit,
			&interval, &timeout, &retries, &builtin) == nil {
			if !probeTypeAllowed(user, typ) {
				continue
			}
			var cfgObj any
			_ = json.Unmarshal([]byte(cfg), &cfgObj)
			out = append(out, map[string]any{"id": id, "name": name, "probe_type": typ, "description": desc,
				"config": cfgObj, "available_expression": available, "warning_expression": warn,
				"error_expression": errExpr, "value_expression": valueExpr, "unit": unit,
				"default_interval_sec": interval, "default_timeout_ms": timeout,
				"default_down_retries": retries, "is_builtin": builtin})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type probeRequest struct {
	Name                string `json:"name"`
	ProbeType           string `json:"probe_type"`
	Description         string `json:"description"`
	Config              any    `json:"config"`
	AvailableExpression string `json:"available_expression"`
	WarningExpression   string `json:"warning_expression"`
	ErrorExpression     string `json:"error_expression"`
	ValueExpression     string `json:"value_expression"`
	Unit                string `json:"unit"`
	Interval            int    `json:"default_interval_sec"`
	Timeout             int    `json:"default_timeout_ms"`
	DownRetries         int    `json:"default_down_retries"`
}

func (s *Server) createProbe(w http.ResponseWriter, r *http.Request) {
	var req probeRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" || req.ProbeType == "" {
		writeError(w, http.StatusBadRequest, "name and probe_type are required")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if !probeTypeAllowed(user, req.ProbeType) {
		writeError(w, http.StatusForbidden, "sin permiso para este tipo de sonda")
		return
	}
	if req.Interval <= 0 {
		req.Interval = 30
	}
	if req.Timeout <= 0 {
		req.Timeout = 2000
	}
	if req.DownRetries <= 0 {
		req.DownRetries = 3
	}
	cfg, _ := json.Marshal(req.Config)
	if req.Config == nil {
		cfg = []byte("{}")
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO probes (name, probe_type, description, config, available_expression, warning_expression, error_expression,
		 value_expression, unit, default_interval_sec, default_timeout_ms, default_down_retries)
		VALUES ($1,$2,$3,$4::jsonb,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id
	`, req.Name, req.ProbeType, req.Description, string(cfg), req.AvailableExpression, req.WarningExpression, req.ErrorExpression,
		req.ValueExpression, req.Unit, req.Interval, req.Timeout, req.DownRetries).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateProbe(w http.ResponseWriter, r *http.Request) {
	var req probeRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" || req.ProbeType == "" {
		writeError(w, http.StatusBadRequest, "name and probe_type are required")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if !probeTypeAllowed(user, req.ProbeType) {
		writeError(w, http.StatusForbidden, "sin permiso para este tipo de sonda")
		return
	}
	if req.Interval <= 0 {
		req.Interval = 30
	}
	if req.Timeout <= 0 {
		req.Timeout = 2000
	}
	if req.DownRetries <= 0 {
		req.DownRetries = 3
	}
	cfg, _ := json.Marshal(req.Config)
	if req.Config == nil {
		cfg = []byte("{}")
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE probes SET name=$2, probe_type=$3, description=$4, config=$5::jsonb,
		 available_expression=$6, warning_expression=$7, error_expression=$8, value_expression=$9, unit=$10,
		 default_interval_sec=$11, default_timeout_ms=$12, default_down_retries=$13, updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.ProbeType, req.Description, string(cfg),
		req.AvailableExpression, req.WarningExpression, req.ErrorExpression, req.ValueExpression, req.Unit,
		req.Interval, req.Timeout, req.DownRetries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "sonda no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteProbe(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var builtin bool
	var used int
	err := s.pool.QueryRow(r.Context(), `
		SELECT p.is_builtin, (SELECT count(*) FROM device_services ds WHERE ds.probe_id=p.id)
		FROM probes p WHERE p.id=$1
	`, id).Scan(&builtin, &used)
	if err != nil {
		writeError(w, http.StatusNotFound, "sonda no encontrada")
		return
	}
	if builtin {
		writeError(w, http.StatusConflict, "las sondas built-in no se pueden eliminar")
		return
	}
	if used > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("la sonda está asignada a %d servicios; quítala antes de eliminarla", used))
		return
	}
	if _, err := s.pool.Exec(r.Context(), `DELETE FROM probes WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listFunctions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `SELECT id, name, description, expression_code, unit, is_builtin FROM functions ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, desc, code, unit string
		var builtin bool
		if rows.Scan(&id, &name, &desc, &code, &unit, &builtin) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "description": desc,
				"expression_code": code, "unit": unit, "is_builtin": builtin})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type functionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Expression  string `json:"expression_code"`
	Unit        string `json:"unit"`
}

func (s *Server) createFunction(w http.ResponseWriter, r *http.Request) {
	var req functionRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" || req.Expression == "" {
		writeError(w, http.StatusBadRequest, "name and expression_code are required")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO functions (name, description, expression_code, unit)
		VALUES ($1,$2,$3,$4) RETURNING id
	`, req.Name, req.Description, req.Expression, req.Unit).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) listSNMPProfiles(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	admin := u.Role == "admin"
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, version, port, timeout_ms, retries, community, is_default,
		       COALESCE(v3_username,''), COALESCE(v3_security_level,'')
		FROM snmp_profiles ORDER BY is_default DESC, name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, version, community, v3user, v3level string
		var port, timeout, retries int
		var def bool
		if rows.Scan(&id, &name, &version, &port, &timeout, &retries, &community, &def, &v3user, &v3level) == nil {
			if plain, err := secure.Decrypt(community); err == nil {
				community = plain
			}
			hasCommunity := community != ""
			if !admin {
				community = ""
			}
			out = append(out, map[string]any{"id": id, "name": name, "version": version, "port": port,
				"timeout_ms": timeout, "retries": retries, "community": community, "is_default": def,
				"v3_username": v3user, "v3_security_level": v3level, "has_community": hasCommunity})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type snmpProfileRequest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Community   string `json:"community"`
	V3Username  string `json:"v3_username"`
	V3Security  string `json:"v3_security_level"`
	V3AuthProto string `json:"v3_auth_protocol"`
	V3AuthPass  string `json:"v3_auth_pass"`
	V3PrivProto string `json:"v3_priv_protocol"`
	V3PrivPass  string `json:"v3_priv_pass"`
}

func (s *Server) createSNMPProfile(w http.ResponseWriter, r *http.Request) {
	var req snmpProfileRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Version == "" {
		req.Version = "v2c"
	}
	if req.Port <= 0 {
		req.Port = 161
	}
	community, _ := secure.Encrypt(req.Community)
	authPass, _ := secure.Encrypt(req.V3AuthPass)
	privPass, _ := secure.Encrypt(req.V3PrivPass)
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO snmp_profiles (name, version, port, community, v3_username, v3_security_level,
		 v3_auth_protocol, v3_auth_pass, v3_priv_protocol, v3_priv_pass)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,'')) RETURNING id
	`, req.Name, req.Version, req.Port, community, req.V3Username, req.V3Security,
		req.V3AuthProto, authPass, req.V3PrivProto, privPass).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateSNMPProfile(w http.ResponseWriter, r *http.Request) {
	var req snmpProfileRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Version == "" {
		req.Version = "v2c"
	}
	if req.Port <= 0 {
		req.Port = 161
	}
	var community, authPass, privPass any
	if req.Community != "" {
		community, _ = secure.Encrypt(req.Community)
	}
	if req.V3AuthPass != "" {
		authPass, _ = secure.Encrypt(req.V3AuthPass)
	}
	if req.V3PrivPass != "" {
		privPass, _ = secure.Encrypt(req.V3PrivPass)
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE snmp_profiles SET
		 name=$2, version=$3, port=$4,
		 community=COALESCE($5,community),
		 v3_username=COALESCE(NULLIF($6,''),v3_username),
		 v3_security_level=COALESCE(NULLIF($7,''),v3_security_level),
		 v3_auth_protocol=COALESCE(NULLIF($8,''),v3_auth_protocol),
		 v3_auth_pass=COALESCE($9,v3_auth_pass),
		 v3_priv_protocol=COALESCE(NULLIF($10,''),v3_priv_protocol),
		 v3_priv_pass=COALESCE($11,v3_priv_pass)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.Version, req.Port, community, req.V3Username,
		req.V3Security, req.V3AuthProto, authPass, req.V3PrivProto, privPass)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "perfil SNMP no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteSNMPProfile(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM snmp_profiles WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "perfil SNMP no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listOutages(w http.ResponseWriter, r *http.Request) {
	limit := 200
	user, _ := auth.UserFrom(r.Context())
	allowed, restricted := s.allowedMapIDs(r.Context(), user)
	allowedDevices, restrictedGroups := s.allowedDeviceIDs(r.Context(), user)
	rows, err := s.pool.Query(r.Context(), `
		SELECT o.id, o.device_id, d.name, host(d.primary_ip), COALESCE(o.device_service_id::text,''),
		       o.severity, o.started_at, o.ended_at, COALESCE(o.error_message,''), o.acknowledged,
		       COALESCE(o.acknowledged_by,''), COALESCE(m.name,''), COALESCE(me.map_id::text,''),
		       COALESCE(o.root_cause_device_id::text,''), COALESCE(rc.name,''),
		       o.suppress_notifications, o.correlation_depth
		FROM outages o
		JOIN devices d ON d.id=o.device_id
		LEFT JOIN devices rc ON rc.id=o.root_cause_device_id
		LEFT JOIN LATERAL (SELECT map_id FROM map_elements WHERE device_id=d.id LIMIT 1) me ON TRUE
		LEFT JOIN maps m ON m.id=me.map_id
		ORDER BY o.started_at DESC LIMIT $1
	`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, deviceID, name, ip, serviceID, severity, msg, ackBy, mapName, mapID string
		var rootID, rootName string
		var suppress bool
		var depth int
		var start time.Time
		var end *time.Time
		var ack bool
		if rows.Scan(&id, &deviceID, &name, &ip, &serviceID, &severity, &start, &end, &msg, &ack,
			&ackBy, &mapName, &mapID, &rootID, &rootName, &suppress, &depth) == nil {
			if restricted && !allowed[mapID] {
				continue
			}
			if restrictedGroups && deviceID != "" && !allowedDevices[deviceID] {
				continue
			}
			dur := int(time.Since(start).Seconds())
			if end != nil {
				dur = int(end.Sub(start).Seconds())
			}
			out = append(out, map[string]any{"id": id, "device_id": deviceID, "device_name": name,
				"primary_ip": ip, "device_service_id": serviceID, "severity": severity,
				"started_at": start, "ended_at": end, "duration_sec": dur, "error_message": msg,
				"acknowledged": ack, "acknowledged_by": ackBy, "map_name": mapName, "map_id": mapID,
				"root_cause_device_id": rootID, "root_cause": rootName,
				"suppress_notifications": suppress, "correlation_depth": depth,
				"active": end == nil})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) listActiveOutages(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	allowed, restricted := s.allowedMapIDs(r.Context(), user)
	allowedDevices, restrictedGroups := s.allowedDeviceIDs(r.Context(), user)
	rows, err := s.pool.Query(r.Context(), `
		SELECT o.id, o.device_id, d.name, host(d.primary_ip), o.severity, o.started_at,
		       COALESCE(o.error_message,''), o.acknowledged, COALESCE(m.name,''), COALESCE(me.map_id::text,''),
		       COALESCE(o.root_cause_device_id::text,''), COALESCE(rc.name,''),
		       o.suppress_notifications, o.correlation_depth
		FROM outages o
		JOIN devices d ON d.id=o.device_id
		LEFT JOIN devices rc ON rc.id=o.root_cause_device_id
		LEFT JOIN LATERAL (SELECT map_id FROM map_elements WHERE device_id=d.id LIMIT 1) me ON TRUE
		LEFT JOIN maps m ON m.id=me.map_id
		WHERE o.ended_at IS NULL ORDER BY o.started_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, deviceID, name, ip, severity, msg, mapName, mapID, rootID, rootName string
		var suppress bool
		var depth int
		var start time.Time
		var ack bool
		if rows.Scan(&id, &deviceID, &name, &ip, &severity, &start, &msg, &ack, &mapName, &mapID,
			&rootID, &rootName, &suppress, &depth) == nil {
			if restricted && !allowed[mapID] {
				continue
			}
			if restrictedGroups && deviceID != "" && !allowedDevices[deviceID] {
				continue
			}
			out = append(out, map[string]any{"id": id, "device_id": deviceID, "device_name": name,
				"primary_ip": ip, "severity": severity, "started_at": start, "duration_sec": int(time.Since(start).Seconds()),
				"error_message": msg, "acknowledged": ack, "map_name": mapName, "map_id": mapID,
				"root_cause_device_id": rootID, "root_cause": rootName,
				"suppress_notifications": suppress, "correlation_depth": depth})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) acknowledgeOutage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	_ = decodeBody(r, &req)
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var deviceID string
	if err := s.pool.QueryRow(ctx, `SELECT device_id::text FROM outages WHERE id=$1`, id).Scan(&deviceID); err != nil {
		writeError(w, http.StatusNotFound, "incidencia no encontrada")
		return
	}
	user, _ := auth.UserFrom(ctx)
	if !s.canAccessDevice(ctx, user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta incidencia")
		return
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE outages SET acknowledged=TRUE, acknowledged_at=NOW(), resolution_note=NULLIF($2,'')
		WHERE id=$1
	`, id, req.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "incidencia no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	user, _ := auth.UserFrom(r.Context())
	allowed, restricted := s.allowedMapIDs(r.Context(), user)
	allowedDevices, restrictedGroups := s.allowedDeviceIDs(r.Context(), user)
	rows, err := s.pool.Query(r.Context(), `
		SELECT e.id, e.at, COALESCE(e.device_id::text,''), COALESCE(d.name,''), e.severity,
		       e.event_type, e.message, COALESCE(m.name,''), COALESCE(me.map_id::text,'')
		FROM events e
		LEFT JOIN devices d ON d.id=e.device_id
		LEFT JOIN LATERAL (SELECT map_id FROM map_elements WHERE device_id=e.device_id LIMIT 1) me ON TRUE
		LEFT JOIN maps m ON m.id=me.map_id
		ORDER BY e.at DESC LIMIT $1
	`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var at time.Time
		var deviceID, deviceName, severity, typ, msg, mapName, mapID string
		if rows.Scan(&id, &at, &deviceID, &deviceName, &severity, &typ, &msg, &mapName, &mapID) == nil {
			if restricted && !allowed[mapID] {
				continue
			}
			if restrictedGroups && deviceID != "" && !allowedDevices[deviceID] {
				continue
			}
			out = append(out, map[string]any{"id": id, "at": at, "device_id": deviceID,
				"device_name": deviceName, "severity": severity, "event_type": typ,
				"message": msg, "map_name": mapName})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func bucketForRange(rng string) (interval string, since string, err error) {
	switch rng {
	case "1h":
		return "1 minute", "1 hour", nil
	case "6h":
		return "5 minutes", "6 hours", nil
	case "24h", "":
		return "15 minutes", "24 hours", nil
	case "7d":
		return "1 hour", "7 days", nil
	case "30d":
		return "6 hours", "30 days", nil
	default:
		return "", "", fmt.Errorf("invalid range")
	}
}

func (s *Server) serviceMetrics(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	var deviceID string
	if err := s.pool.QueryRow(r.Context(), `SELECT device_id::text FROM device_services WHERE id=$1`, chi.URLParam(r, "id")).Scan(&deviceID); err != nil || !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este servicio")
		return
	}
	interval, since, err := bucketForRange(r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT time_bucket($2::interval, time) AS bucket,
		       avg(value)::float8, avg(rtt_ms)::float8, max(packet_loss_pct)::float8
		FROM service_metrics WHERE service_id=$1 AND time > NOW()-$3::interval
		GROUP BY 1 ORDER BY 1
	`, chi.URLParam(r, "id"), interval, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var t time.Time
		var value, rtt, loss *float64
		if rows.Scan(&t, &value, &rtt, &loss) == nil {
			out = append(out, map[string]any{"time": t, "value": value, "rtt_ms": rtt, "packet_loss_pct": loss})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) linkMetrics(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	var mapID string
	if err := s.pool.QueryRow(r.Context(), `SELECT map_id::text FROM map_links WHERE id=$1`, chi.URLParam(r, "id")).Scan(&mapID); err != nil || !s.canAccessMap(r.Context(), user, mapID) {
		writeError(w, http.StatusForbidden, "sin acceso a este enlace")
		return
	}
	interval, since, err := bucketForRange(r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT time_bucket($2::interval, time) AS bucket,
		       avg(rx_bps)::float8, avg(tx_bps)::float8,
		       max(in_errors)::float8, max(out_errors)::float8
		FROM link_metrics WHERE link_id=$1 AND time > NOW()-$3::interval
		GROUP BY 1 ORDER BY 1
	`, chi.URLParam(r, "id"), interval, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var t time.Time
		var rx, tx, inErr, outErr *float64
		if rows.Scan(&t, &rx, &tx, &inErr, &outErr) == nil {
			out = append(out, map[string]any{"time": t, "rx_bps": rx, "tx_bps": tx, "in_errors": inErr, "out_errors": outErr})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) deviceMetrics(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, chi.URLParam(r, "id")) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	interval, since, err := bucketForRange(r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT time_bucket($2::interval, time) AS bucket,
		       avg(value)::float8, avg(rtt_ms)::float8, max(packet_loss_pct)::float8
		FROM service_metrics WHERE device_id=$1 AND time > NOW()-$3::interval
		GROUP BY 1 ORDER BY 1
	`, chi.URLParam(r, "id"), interval, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var t time.Time
		var value, rtt, loss *float64
		if rows.Scan(&t, &value, &rtt, &loss) == nil {
			out = append(out, map[string]any{"time": t, "value": value, "rtt_ms": rtt, "packet_loss_pct": loss})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) listAlertRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, priority, trigger_transitions, delay_sec, repeat_interval_sec, max_repeats,
		       channel_ids, active_schedule::text, group_by, group_window_sec, is_enabled,
		       filter_address_list_ids
		FROM alert_rules ORDER BY priority
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, scheduleRaw, groupBy string
		var triggers, channels, addressLists []string
		var priority, delay, repeat, max, groupWindow int
		var enabled bool
		if rows.Scan(&id, &name, &priority, &triggers, &delay, &repeat, &max, &channels, &scheduleRaw, &groupBy, &groupWindow, &enabled, &addressLists) == nil {
			var activeSchedule any
			_ = json.Unmarshal([]byte(scheduleRaw), &activeSchedule)
			out = append(out, map[string]any{"id": id, "name": name, "priority": priority,
				"trigger_transitions": triggers, "delay_sec": delay, "repeat_interval_sec": repeat,
				"max_repeats": max, "channel_ids": channels, "active_schedule": activeSchedule,
				"group_by": groupBy, "group_window_sec": groupWindow, "is_enabled": enabled,
				"filter_address_list_ids": addressLists})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type alertRuleRequest struct {
	Name           string          `json:"name"`
	Priority       int             `json:"priority"`
	Transitions    []string        `json:"trigger_transitions"`
	DelaySec       int             `json:"delay_sec"`
	RepeatInterval int             `json:"repeat_interval_sec"`
	MaxRepeats     int             `json:"max_repeats"`
	ChannelIDs     []string        `json:"channel_ids"`
	ActiveSchedule json.RawMessage `json:"active_schedule"`
	GroupBy        string          `json:"group_by"`
	GroupWindowSec int             `json:"group_window_sec"`
	AddressListIDs []string        `json:"filter_address_list_ids"`
	IsEnabled      *bool           `json:"is_enabled"`
}

func normalizeGrouping(by string, window int) (string, int, error) {
	by = strings.ToLower(strings.TrimSpace(by))
	if by == "" || by == "none" {
		return "none", 0, nil
	}
	if by != "map" && by != "tag" && by != "submap" && by != "zone" {
		return "", 0, fmt.Errorf("group_by debe ser none, map, tag, submap o zone")
	}
	if window <= 0 {
		window = 60
	}
	if window < 5 {
		window = 5
	}
	if window > 3600 {
		window = 3600
	}
	return by, window, nil
}

func normalizeSchedule(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return `{"always":true}`, nil
	}
	window, err := schedule.Parse(string(raw))
	if err != nil {
		return "", err
	}
	normalized, err := json.Marshal(window)
	if err != nil {
		return "", fmt.Errorf("horario inválido")
	}
	return string(normalized), nil
}

func (s *Server) createAlertRule(w http.ResponseWriter, r *http.Request) {
	var req alertRuleRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Transitions) == 0 {
		req.Transitions = []string{"down", "recovered"}
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []string{}
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	scheduleRaw, err := normalizeSchedule(req.ActiveSchedule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	groupBy, groupWindow, err := normalizeGrouping(req.GroupBy, req.GroupWindowSec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	var id string
	if req.AddressListIDs == nil {
		req.AddressListIDs = []string{}
	}
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO alert_rules (name, priority, trigger_transitions, delay_sec, repeat_interval_sec, max_repeats, channel_ids, active_schedule, group_by, group_window_sec, filter_address_list_ids, is_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12) RETURNING id
	`, req.Name, req.Priority, req.Transitions, req.DelaySec, req.RepeatInterval, req.MaxRepeats, req.ChannelIDs, scheduleRaw, groupBy, groupWindow, req.AddressListIDs, enabled).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	var req alertRuleRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Transitions) == 0 {
		req.Transitions = []string{"down", "recovered"}
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []string{}
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	scheduleRaw, err := normalizeSchedule(req.ActiveSchedule)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	groupBy, groupWindow, err := normalizeGrouping(req.GroupBy, req.GroupWindowSec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.AddressListIDs == nil {
		req.AddressListIDs = []string{}
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE alert_rules
		SET name=$2, priority=$3, trigger_transitions=$4, delay_sec=$5, repeat_interval_sec=$6,
		    max_repeats=$7, channel_ids=$8, active_schedule=$9::jsonb,
		    group_by=$10, group_window_sec=$11, filter_address_list_ids=$12,
		    is_enabled=COALESCE($13,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.Priority, req.Transitions, req.DelaySec, req.RepeatInterval,
		req.MaxRepeats, req.ChannelIDs, scheduleRaw, groupBy, groupWindow, req.AddressListIDs, req.IsEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "regla no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM alert_rules WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "regla no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	admin := u.Role == "admin"
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, channel_type, config::text, is_enabled FROM notification_channels ORDER BY name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, typ, cfg string
		var enabled bool
		if rows.Scan(&id, &name, &typ, &cfg, &enabled) == nil {
			var cfgObj map[string]any
			_ = json.Unmarshal([]byte(secure.UnwrapJSON(cfg)), &cfgObj)
			hasSecrets := false
			if !admin {
				cfgObj, hasSecrets = redactConfig(cfgObj)
			}
			out = append(out, map[string]any{"id": id, "name": name, "channel_type": typ,
				"config": cfgObj, "is_enabled": enabled, "has_secrets": hasSecrets})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type channelRequest struct {
	Name    string `json:"name"`
	Type    string `json:"channel_type"`
	Config  any    `json:"config"`
	Enabled *bool  `json:"is_enabled"`
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" || req.Type == "" {
		writeError(w, http.StatusBadRequest, "name and channel_type are required")
		return
	}
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	cfg, _ := json.Marshal(req.Config)
	wrapped, err := secure.WrapJSON(string(cfg))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var id string
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO notification_channels (name, channel_type, config, is_enabled)
		VALUES ($1,$2,$3::jsonb,$4) RETURNING id
	`, req.Name, req.Type, wrapped, enabled).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	var req channelRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var wrapped any
	if req.Config != nil {
		cfg, _ := json.Marshal(req.Config)
		encrypted, err := secure.WrapJSON(string(cfg))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		wrapped = encrypted
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE notification_channels
		SET name=COALESCE(NULLIF($2,''),name),
		    channel_type=COALESCE(NULLIF($3,''),channel_type),
		    config=COALESCE($4::jsonb,config),
		    is_enabled=COALESCE($5,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.Type, wrapped, req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "canal no encontrado")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	if _, err := tx.Exec(r.Context(), `
		UPDATE alert_rules SET channel_ids=array_remove(channel_ids,$1) WHERE $1 = ANY(channel_ids)
	`, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tag, err := tx.Exec(r.Context(), `DELETE FROM notification_channels WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "canal no encontrado")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

var _ = strconv.Itoa
var _ = strings.TrimSpace
var _ = gosnmp.Version2c
var _ = probing.NewPinger
