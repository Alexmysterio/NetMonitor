package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/receivers"
)

func (s *Server) listTraps(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	allowedMaps, mapRestricted := s.allowedMapIDs(r.Context(), user)
	allowedDevices, deviceRestricted := s.allowedDeviceIDs(r.Context(), user)
	var mapArg, deviceArg any
	if mapRestricted {
		mapArg = stringSetKeys(allowedMaps)
	}
	if deviceRestricted {
		deviceArg = stringSetKeys(allowedDevices)
	}
	severity := strings.TrimSpace(r.URL.Query().Get("severity"))
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT t.id, t.at, COALESCE(host(t.source_ip),''), COALESCE(t.device_id::text,''),
		       COALESCE(d.name,''), COALESCE(t.map_id::text,''), COALESCE(m.name,''),
		       t.trap_oid, t.trap_name, t.severity, t.event_type, t.message, t.version,
		       t.variables::text, t.suppressed, COALESCE(t.root_cause_device_id::text,''),
		       COALESCE(rc.name,''), COALESCE(t.matched_rule_id::text,''), COALESCE(tr.name,'')
		FROM snmp_traps t
		LEFT JOIN devices d ON d.id=t.device_id
		LEFT JOIN maps m ON m.id=t.map_id
		LEFT JOIN devices rc ON rc.id=t.root_cause_device_id
		LEFT JOIN trap_rules tr ON tr.id=t.matched_rule_id
		WHERE ($1='' OR t.severity=$1)
		  AND ($2='' OR t.device_id=$2::uuid)
		  AND ($3::uuid[] IS NULL OR t.map_id = ANY($3::uuid[]))
		  AND ($4::uuid[] IS NULL OR t.device_id = ANY($4::uuid[]))
		ORDER BY t.at DESC LIMIT $5
	`, severity, deviceID, mapArg, deviceArg, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var at time.Time
		var sourceIP, deviceID, deviceName, mapID, mapName, oid, name, sev, eventType, message, version, variables, rootID, rootName, ruleID, ruleName string
		var suppressed bool
		if rows.Scan(&id, &at, &sourceIP, &deviceID, &deviceName, &mapID, &mapName, &oid, &name,
			&sev, &eventType, &message, &version, &variables, &suppressed, &rootID, &rootName,
			&ruleID, &ruleName) == nil {
			var vars any
			_ = json.Unmarshal([]byte(variables), &vars)
			out = append(out, map[string]any{"id": id, "at": at, "source_ip": sourceIP,
				"device_id": deviceID, "device_name": deviceName, "map_id": mapID, "map_name": mapName,
				"trap_oid": oid, "trap_name": name, "severity": sev, "event_type": eventType,
				"message": message, "version": version, "variables": vars, "suppressed": suppressed,
				"root_cause_device_id": rootID, "root_cause": rootName,
				"matched_rule_id": ruleID, "matched_rule": ruleName})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) getTrap(w http.ResponseWriter, r *http.Request) {
	var id int64
	var at time.Time
	var sourceIP, deviceID, deviceName, mapID, mapName, oid, name, sev, eventType, message, version, variables, raw, rootID, rootName string
	var suppressed bool
	err := s.pool.QueryRow(r.Context(), `
		SELECT t.id, t.at, COALESCE(host(t.source_ip),''), COALESCE(t.device_id::text,''), COALESCE(d.name,''),
		       COALESCE(t.map_id::text,''), COALESCE(m.name,''), t.trap_oid, t.trap_name, t.severity,
		       t.event_type, t.message, t.version, t.variables::text, t.raw::text, t.suppressed,
		       COALESCE(t.root_cause_device_id::text,''), COALESCE(rc.name,'')
		FROM snmp_traps t
		LEFT JOIN devices d ON d.id=t.device_id
		LEFT JOIN maps m ON m.id=t.map_id
		LEFT JOIN devices rc ON rc.id=t.root_cause_device_id
		WHERE t.id=$1
	`, chi.URLParam(r, "id")).Scan(&id, &at, &sourceIP, &deviceID, &deviceName, &mapID, &mapName,
		&oid, &name, &sev, &eventType, &message, &version, &variables, &raw, &suppressed, &rootID, &rootName)
	if err != nil {
		writeError(w, http.StatusNotFound, "trap no encontrado")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if deviceID != "" && !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este trap")
		return
	}
	if deviceID == "" && mapID != "" && !s.canAccessMap(r.Context(), user, mapID) {
		writeError(w, http.StatusForbidden, "sin acceso a este trap")
		return
	}
	if deviceID == "" && mapID == "" && user.Role != "admin" && (user.RestrictMaps || user.RestrictDevices || user.RestrictGroups) {
		writeError(w, http.StatusForbidden, "sin acceso a este trap")
		return
	}
	var vars, rawObj any
	_ = json.Unmarshal([]byte(variables), &vars)
	_ = json.Unmarshal([]byte(raw), &rawObj)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "at": at, "source_ip": sourceIP, "device_id": deviceID, "device_name": deviceName,
		"map_id": mapID, "map_name": mapName, "trap_oid": oid, "trap_name": name,
		"severity": sev, "event_type": eventType, "message": message, "version": version,
		"variables": vars, "raw": rawObj, "suppressed": suppressed,
		"root_cause_device_id": rootID, "root_cause": rootName,
	}})
}

type trapRuleRequest struct {
	Name       string   `json:"name"`
	Priority   int      `json:"priority"`
	MatchOID   string   `json:"match_oid"`
	MatchName  string   `json:"match_name"`
	Severity   string   `json:"severity"`
	EventType  string   `json:"event_type"`
	Message    string   `json:"message_template"`
	DeviceIDs  []string `json:"filter_device_ids"`
	Tags       []string `json:"filter_tags"`
	MapIDs     []string `json:"filter_map_ids"`
	ChannelIDs []string `json:"channel_ids"`
	Notify     *bool    `json:"notify"`
	Suppress   bool     `json:"suppress"`
	IsEnabled  *bool    `json:"is_enabled"`
}

func normalizeTrapRule(req *trapRuleRequest) bool {
	if strings.TrimSpace(req.Name) == "" {
		return false
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	switch req.Severity {
	case "info", "ok", "warning", "critical":
	default:
		req.Severity = "warning"
	}
	if req.EventType == "" {
		req.EventType = "snmp_trap"
	}
	if req.DeviceIDs == nil {
		req.DeviceIDs = []string{}
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	if req.MapIDs == nil {
		req.MapIDs = []string{}
	}
	if req.ChannelIDs == nil {
		req.ChannelIDs = []string{}
	}
	return true
}

func (s *Server) listTrapRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, priority, match_oid, match_name, severity, event_type,
		       COALESCE(message_template,''), filter_device_ids, filter_tags, filter_map_ids,
		       channel_ids, notify, suppress, is_enabled
		FROM trap_rules ORDER BY priority, name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, oid, nameMatch, sev, eventType, message string
		var priority int
		var deviceIDs, tags, mapIDs, channels []string
		var notify, suppress, enabled bool
		if rows.Scan(&id, &name, &priority, &oid, &nameMatch, &sev, &eventType, &message,
			&deviceIDs, &tags, &mapIDs, &channels, &notify, &suppress, &enabled) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "priority": priority,
				"match_oid": oid, "match_name": nameMatch, "severity": sev, "event_type": eventType,
				"message_template": message, "filter_device_ids": deviceIDs, "filter_tags": tags,
				"filter_map_ids": mapIDs, "channel_ids": channels, "notify": notify,
				"suppress": suppress, "is_enabled": enabled})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createTrapRule(w http.ResponseWriter, r *http.Request) {
	var req trapRuleRequest
	if err := decodeBody(r, &req); err != nil || !normalizeTrapRule(&req) {
		writeError(w, http.StatusBadRequest, "name es obligatorio")
		return
	}
	notify, enabled := true, true
	if req.Notify != nil {
		notify = *req.Notify
	}
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO trap_rules (name, priority, match_oid, match_name, severity, event_type,
		 message_template, filter_device_ids, filter_tags, filter_map_ids, channel_ids,
		 notify, suppress, is_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id
	`, req.Name, req.Priority, req.MatchOID, req.MatchName, req.Severity, req.EventType, req.Message,
		req.DeviceIDs, req.Tags, req.MapIDs, req.ChannelIDs, notify, req.Suppress, enabled).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateTrapRule(w http.ResponseWriter, r *http.Request) {
	var req trapRuleRequest
	if err := decodeBody(r, &req); err != nil || !normalizeTrapRule(&req) {
		writeError(w, http.StatusBadRequest, "name es obligatorio")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE trap_rules SET name=$2, priority=$3, match_oid=$4, match_name=$5, severity=$6,
		 event_type=$7, message_template=$8, filter_device_ids=$9, filter_tags=$10,
		 filter_map_ids=$11, channel_ids=$12, notify=COALESCE($13,notify), suppress=$14,
		 is_enabled=COALESCE($15,is_enabled)
		WHERE id=$1
	`, chi.URLParam(r, "id"), req.Name, req.Priority, req.MatchOID, req.MatchName, req.Severity,
		req.EventType, req.Message, req.DeviceIDs, req.Tags, req.MapIDs, req.ChannelIDs,
		req.Notify, req.Suppress, req.IsEnabled)
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

func (s *Server) deleteTrapRule(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM trap_rules WHERE id=$1`, chi.URLParam(r, "id"))
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

func (s *Server) getTrapSettings(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var community string
	_ = s.pool.QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key='traps.community'`).Scan(&community)
	if community == "" {
		community = "public"
	}
	hasCommunity := community != ""
	if u.Role != "admin" {
		community = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"community": community, "has_community": hasCommunity, "requires_restart": true,
	}})
}

func (s *Server) setTrapSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Community string `json:"community"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Community) == "" {
		writeError(w, http.StatusBadRequest, "community es obligatorio")
		return
	}
	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO system_settings (key, value, updated_at) VALUES ('traps.community',$1,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()
	`, strings.TrimSpace(req.Community))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"requires_restart": true}})
}

func (s *Server) testTrap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		SourceIP string `json:"source_ip"`
		TrapOID  string `json:"trap_oid"`
		TrapName string `json:"trap_name"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	source := strings.TrimSpace(req.SourceIP)
	if source == "" && req.DeviceID != "" {
		_ = s.pool.QueryRow(r.Context(), `SELECT host(primary_ip) FROM devices WHERE id=$1`, req.DeviceID).Scan(&source)
	}
	if source == "" {
		source = "127.0.0.1"
	}
	oid := strings.TrimSpace(req.TrapOID)
	if oid == "" && req.TrapName != "" {
		_ = s.pool.QueryRow(r.Context(), `SELECT oid FROM mib_notifications WHERE name=$1 LIMIT 1`, req.TrapName).Scan(&oid)
	}
	if oid == "" {
		oid = "1.3.6.1.6.3.1.1.5.3"
	}
	recv := receivers.New(s.pool, nil, s.hub)
	recv.InjectTestTrap(r.Context(), source, oid)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"source_ip": source, "trap_oid": oid,
	}})
}
