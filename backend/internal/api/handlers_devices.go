package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	user, _ := auth.UserFrom(ctx)
	allowedMapIDs, restricted := s.allowedMapIDs(ctx, user)
	var allowedList []string
	if restricted {
		for id := range allowedMapIDs {
			allowedList = append(allowedList, id)
		}
	}
	var mapFilter any
	if restricted {
		mapFilter = allowedList
	}
	allowedDevices, restrictedGroups := s.allowedDeviceIDs(ctx, user)
	var deviceFilter any
	if restrictedGroups {
		ids := make([]string, 0, len(allowedDevices))
		for id := range allowedDevices {
			ids = append(ids, id)
		}
		deviceFilter = ids
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.name, host(d.primary_ip), COALESCE(ARRAY(SELECT host(x) FROM unnest(d.secondary_ips) x),'{}'),
		       COALESCE(d.dns_name,''), d.device_type,
		       d.status, COALESCE(d.status_reason,''), d.is_acknowledged,
		       COALESCE(d.tags,'{}'), COALESCE(d.custom_fields::text,'{}'),
		       COALESCE(d.snmp_profile_id::text,''), COALESCE(d.notes,''),
		       COALESCE(d.last_seen_up_at, d.created_at), d.is_monitoring_enabled,
		       COALESCE(c.up,0), COALESCE(c.warning,0), COALESCE(c.down,0),
		       COALESCE(ARRAY(SELECT parent_device_id::text FROM device_parents WHERE device_id=d.id),'{}')
		FROM devices d
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE status='up') AS up,
			       count(*) FILTER (WHERE status='warning') AS warning,
			       count(*) FILTER (WHERE status='down') AS down
			FROM device_services WHERE device_id=d.id AND is_enabled=TRUE
		) c ON TRUE
		WHERE ($1='' OR d.status=$1)
		  AND ($2='' OR d.name ILIKE '%'||$2||'%' OR host(d.primary_ip) ILIKE '%'||$2||'%'
		       OR EXISTS (SELECT 1 FROM unnest(d.secondary_ips) sip WHERE host(sip) ILIKE '%'||$2||'%'))
		  AND ($3::uuid[] IS NULL OR EXISTS (
		      SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id = ANY($3::uuid[])
		  ))
		  AND ($4::uuid[] IS NULL OR d.id = ANY($4::uuid[]))
		ORDER BY d.name
		LIMIT 1000
	`, status, search, mapFilter, deviceFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, ip, dns, typ, st, reason, customJSON, profileID, notes string
		var ack, monitoring bool
		var tags, parents, secondary []string
		var up, warning, down int
		var lastSeen time.Time
		if err := rows.Scan(&id, &name, &ip, &secondary, &dns, &typ, &st, &reason, &ack, &tags, &customJSON,
			&profileID, &notes, &lastSeen, &monitoring, &up, &warning, &down, &parents); err != nil {
			continue
		}
		var custom map[string]any
		_ = json.Unmarshal([]byte(customJSON), &custom)
		out = append(out, map[string]any{
			"id": id, "name": name, "primary_ip": ip, "secondary_ips": secondary, "dns_name": dns, "device_type": typ,
			"status": st, "status_text": statusText(st), "status_reason": reason,
			"is_acknowledged": ack, "tags": tags, "custom_fields": custom,
			"snmp_profile_id": profileID, "notes": notes, "last_seen_up_at": lastSeen,
			"is_monitoring_enabled": monitoring,
			"services_summary":      map[string]int{"up": up, "warning": warning, "down": down},
			"parent_ids":            parents,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type deviceRequest struct {
	Name          string         `json:"name"`
	PrimaryIP     string         `json:"primary_ip"`
	SecondaryIPs  []string       `json:"secondary_ips"`
	DNSName       string         `json:"dns_name"`
	MACAddress    string         `json:"mac_address"`
	DeviceType    string         `json:"device_type"`
	SNMPProfileID string         `json:"snmp_profile_id"`
	AgentID       string         `json:"agent_id"`
	Tags          []string       `json:"tags"`
	CustomFields  map[string]any `json:"custom_fields"`
	Notes         string         `json:"notes"`
	ParentIDs     []string       `json:"parent_ids"`
	Monitoring    *bool          `json:"is_monitoring_enabled"`
	CreatePing    *bool          `json:"create_default_ping"`
	SysRefresh    *int           `json:"snmp_system_refresh_sec"`
	IfRefresh     *int           `json:"snmp_interfaces_refresh_sec"`
	IPRefresh     *int           `json:"snmp_ips_refresh_sec"`
	ARPRefresh    *int           `json:"snmp_arp_refresh_sec"`
	RouteRefresh  *int           `json:"snmp_routes_refresh_sec"`
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := decodeBody(r, &req); err != nil || req.Name == "" || req.PrimaryIP == "" {
		writeError(w, http.StatusBadRequest, "name and primary_ip are required")
		return
	}
	if req.DeviceType == "" {
		req.DeviceType = "generic"
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	custom, _ := json.Marshal(req.CustomFields)
	if req.CustomFields == nil {
		custom = []byte("{}")
	}
	var profile any
	if req.SNMPProfileID != "" {
		profile = req.SNMPProfileID
	}
	var agent any
	if req.AgentID != "" {
		if err := s.agentCapacity(r.Context(), req.AgentID, ""); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		agent = req.AgentID
	}
	monitoring := true
	if req.Monitoring != nil {
		monitoring = *req.Monitoring
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO devices (name, primary_ip, dns_name, device_type, snmp_profile_id, agent_id, tags, custom_fields, notes, is_monitoring_enabled, snmp_system_refresh_sec, snmp_interfaces_refresh_sec, snmp_ips_refresh_sec, snmp_arp_refresh_sec, snmp_routes_refresh_sec, status)
		VALUES ($1,$2::inet,NULLIF($3,''),$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15,'unknown') RETURNING id
	`, req.Name, req.PrimaryIP, req.DNSName, req.DeviceType, profile, agent, req.Tags, string(custom), req.Notes, monitoring,
		refreshArg(req.SysRefresh), refreshArg(req.IfRefresh), refreshArg(req.IPRefresh), refreshArg(req.ARPRefresh), refreshArg(req.RouteRefresh)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.MACAddress != "" {
		_, _ = s.pool.Exec(r.Context(), `UPDATE devices SET mac_address=$2 WHERE id=$1`, id, strings.ToLower(strings.TrimSpace(req.MACAddress)))
	}
	if len(req.SecondaryIPs) > 0 {
		_, _ = s.pool.Exec(r.Context(), `UPDATE devices SET secondary_ips=$2::inet[] WHERE id=$1`, id, req.SecondaryIPs)
	}
	_, _ = s.pool.Exec(r.Context(), `
		INSERT INTO device_services (device_id, probe_id, custom_name, interval_sec, timeout_ms, down_retries, up_retries)
		SELECT $1, p.id, p.name, p.default_interval_sec, p.default_timeout_ms, p.default_down_retries, p.default_up_retries
		FROM probes p JOIN device_types dt ON p.id = ANY(dt.probe_ids)
		WHERE dt.name=$2
		ON CONFLICT DO NOTHING
	`, id, req.DeviceType)
	for _, parent := range req.ParentIDs {
		_, _ = s.pool.Exec(r.Context(), `INSERT INTO device_parents (device_id,parent_device_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, parent)
	}
	createPing := true
	if req.CreatePing != nil {
		createPing = *req.CreatePing
	}
	if createPing {
		_, _ = s.pool.Exec(r.Context(), `
			INSERT INTO device_services (device_id, probe_id, custom_name, interval_sec, timeout_ms, down_retries, up_retries)
			SELECT $1, id, 'Ping', default_interval_sec, default_timeout_ms, default_down_retries, default_up_retries
			FROM probes WHERE name='Ping' LIMIT 1
			ON CONFLICT DO NOTHING
		`, id)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deviceID := chi.URLParam(r, "id")
	var id, name, ip, dns, mac, vendor, typ, status, reason, profileID, agentID, notes, customJSON, maintenanceUntil, maintenanceNote string
	var ack, monitoring bool
	var sysRefresh, ifRefresh, ipRefresh, arpRefresh, routeRefresh int
	var pollInterval, pollRetries, pollNotify int
	var pollAgent string
	var tags, secondary []string
	var lastChange, lastSeen time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, host(primary_ip), COALESCE(ARRAY(SELECT host(x) FROM unnest(secondary_ips) x),'{}'), COALESCE(dns_name,''), COALESCE(mac_address,''), COALESCE(vendor,''),
		       device_type, status, status_reason, COALESCE(is_acknowledged,false),
		       COALESCE(tags,'{}'), COALESCE(custom_fields::text,'{}'), COALESCE(snmp_profile_id::text,''),
		       COALESCE(agent_id::text,''), COALESCE(notes,''), is_monitoring_enabled,
		       COALESCE(last_status_change_at, created_at), COALESCE(last_seen_up_at, created_at),
		       COALESCE(maintenance_until::text,''), COALESCE(maintenance_note,''),
		       COALESCE(snmp_system_refresh_sec,-1), COALESCE(snmp_interfaces_refresh_sec,-1), COALESCE(snmp_ips_refresh_sec,-1), COALESCE(snmp_arp_refresh_sec,-1), COALESCE(snmp_routes_refresh_sec,-1),
		       COALESCE(poll_interval_sec,0), COALESCE(poll_down_retries,0), COALESCE(poll_agent_id::text,''),
		       COALESCE(poll_notifications_enabled::int,-1)
		FROM devices WHERE id=$1
	`, deviceID).Scan(&id, &name, &ip, &secondary, &dns, &mac, &vendor, &typ, &status, &reason, &ack, &tags,
		&customJSON, &profileID, &agentID, &notes, &monitoring, &lastChange, &lastSeen, &maintenanceUntil, &maintenanceNote,
		&sysRefresh, &ifRefresh, &ipRefresh, &arpRefresh, &routeRefresh, &pollInterval, &pollRetries, &pollAgent, &pollNotify)
	if err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	var custom map[string]any
	_ = json.Unmarshal([]byte(customJSON), &custom)
	parents := []string{}
	rows, _ := s.pool.Query(ctx, `SELECT parent_device_id::text FROM device_parents WHERE device_id=$1`, deviceID)
	if rows != nil {
		for rows.Next() {
			var p string
			_ = rows.Scan(&p)
			parents = append(parents, p)
		}
		rows.Close()
	}
	services := []map[string]any{}
	srows, err := s.pool.Query(ctx, `
		SELECT ds.id, ds.probe_id, p.name, p.probe_type, COALESCE(ds.custom_name,''), ds.status,
		       ds.interval_sec, ds.timeout_ms, ds.down_retries, ds.is_critical, ds.is_enabled,
		       COALESCE(ds.last_value,0), COALESCE(ds.last_output_message,''), COALESCE(p.unit,''),
		       ds.last_check_at, COALESCE(ds.config_overrides::text,'{}')
		FROM device_services ds JOIN probes p ON p.id=ds.probe_id
		WHERE ds.device_id=$1 ORDER BY p.name, ds.custom_name
	`, deviceID)
	if err == nil {
		defer srows.Close()
		for srows.Next() {
			var sid, probeID, pname, ptype, customName, st, unit, msg, overrides string
			var interval, timeout, retries int
			var critical, enabled bool
			var value float64
			var lastCheck *time.Time
			if srows.Scan(&sid, &probeID, &pname, &ptype, &customName, &st, &interval, &timeout, &retries,
				&critical, &enabled, &value, &msg, &unit, &lastCheck, &overrides) == nil {
				services = append(services, map[string]any{
					"id": sid, "probe_id": probeID, "probe_name": pname, "probe_type": ptype,
					"name": customName, "status": st, "status_text": statusText(st),
					"interval_sec": interval, "timeout_ms": timeout, "down_retries": retries,
					"is_critical": critical, "is_enabled": enabled, "last_value": value,
					"last_output_message": msg, "unit": unit, "last_check_at": lastCheck,
				})
			}
		}
	}
	refresh := s.effectiveSNMPRefresh(r, sysRefresh, ifRefresh, ipRefresh, arpRefresh, routeRefresh)
	overrides := map[string]any{}
	if sysRefresh >= 0 {
		overrides["system_sec"] = sysRefresh
	}
	if ifRefresh >= 0 {
		overrides["interfaces_sec"] = ifRefresh
	}
	if ipRefresh >= 0 {
		overrides["ips_sec"] = ipRefresh
	}
	if arpRefresh >= 0 {
		overrides["arp_sec"] = arpRefresh
	}
	if routeRefresh >= 0 {
		overrides["routes_sec"] = routeRefresh
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "name": name, "primary_ip": ip, "secondary_ips": secondary, "dns_name": dns, "mac_address": mac, "vendor": vendor,
		"device_type": typ, "status": status, "status_text": statusText(status), "status_reason": reason,
		"is_acknowledged": ack, "tags": tags, "custom_fields": custom, "snmp_profile_id": profileID,
		"agent_id": agentID, "notes": notes, "is_monitoring_enabled": monitoring, "last_status_change_at": lastChange,
		"last_seen_up_at": lastSeen, "parent_ids": parents, "services": services,
		"maintenance_until": maintenanceUntil, "maintenance_note": maintenanceNote,
		"poll_interval_sec": pollInterval, "poll_down_retries": pollRetries,
		"poll_agent_id": pollAgent, "poll_notifications": pollNotify,
		"snmp_refresh": map[string]any{
			"system_sec": refresh.SystemSec, "interfaces_sec": refresh.InterfacesSec,
			"ips_sec": refresh.IPsSec, "arp_sec": refresh.ARPSec, "routes_sec": refresh.RoutesSec,
			"overrides": overrides,
		},
	}})
}

type updateDeviceRequest struct {
	Name          *string        `json:"name"`
	PrimaryIP     *string        `json:"primary_ip"`
	SecondaryIPs  []string       `json:"secondary_ips"`
	DNSName       *string        `json:"dns_name"`
	MACAddress    *string        `json:"mac_address"`
	DeviceType    *string        `json:"device_type"`
	SNMPProfileID *string        `json:"snmp_profile_id"`
	AgentID       *string        `json:"agent_id"`
	Tags          []string       `json:"tags"`
	CustomFields  map[string]any `json:"custom_fields"`
	Notes         *string        `json:"notes"`
	ParentIDs     []string       `json:"parent_ids"`
	Monitoring    *bool          `json:"is_monitoring_enabled"`
	SysRefresh    *int           `json:"snmp_system_refresh_sec"`
	IfRefresh     *int           `json:"snmp_interfaces_refresh_sec"`
	IPRefresh     *int           `json:"snmp_ips_refresh_sec"`
	ARPRefresh    *int           `json:"snmp_arp_refresh_sec"`
	RouteRefresh  *int           `json:"snmp_routes_refresh_sec"`
	PollInterval  *int           `json:"poll_interval_sec"`
	PollRetries   *int           `json:"poll_down_retries"`
	PollAgentID   *string        `json:"poll_agent_id"`
	PollNotify    *int           `json:"poll_notifications"`
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var req updateDeviceRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	deviceID := chi.URLParam(r, "id")
	profileArg := "__KEEP__"
	if req.SNMPProfileID != nil {
		profileArg = *req.SNMPProfileID
	}
	agentArg := "__KEEP__"
	if req.AgentID != nil {
		agentArg = *req.AgentID
		if agentArg != "" {
			if err := s.agentCapacity(r.Context(), agentArg, deviceID); err != nil {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
		}
	}
	var customJSON any
	if req.CustomFields != nil {
		b, _ := json.Marshal(req.CustomFields)
		customJSON = string(b)
	}
	req.SysRefresh = normalizeRefresh(req.SysRefresh)
	req.IfRefresh = normalizeRefresh(req.IfRefresh)
	req.IPRefresh = normalizeRefresh(req.IPRefresh)
	req.ARPRefresh = normalizeRefresh(req.ARPRefresh)
	req.RouteRefresh = normalizeRefresh(req.RouteRefresh)
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET
		 name=COALESCE($2,name), primary_ip=COALESCE(NULLIF($3,''),host(primary_ip))::inet,
		 dns_name=COALESCE($4,dns_name), device_type=COALESCE($5,device_type),
		 snmp_profile_id=CASE WHEN $6::text='__KEEP__' THEN snmp_profile_id WHEN $6::text='' THEN NULL ELSE $6::uuid END,
		 agent_id=CASE WHEN $11::text='__KEEP__' THEN agent_id WHEN $11::text='' THEN NULL ELSE $11::uuid END,
		 tags=COALESCE($7,tags),
		 custom_fields=CASE WHEN $8::text IS NULL THEN custom_fields ELSE $8::jsonb END,
		 notes=COALESCE($9,notes), is_monitoring_enabled=COALESCE($10,is_monitoring_enabled),
		 mac_address=COALESCE($12,mac_address),
		 snmp_system_refresh_sec=CASE WHEN $13::int = -1 THEN NULL ELSE COALESCE($13,snmp_system_refresh_sec) END,
		 snmp_interfaces_refresh_sec=CASE WHEN $14::int = -1 THEN NULL ELSE COALESCE($14,snmp_interfaces_refresh_sec) END,
		 snmp_ips_refresh_sec=CASE WHEN $15::int = -1 THEN NULL ELSE COALESCE($15,snmp_ips_refresh_sec) END,
		 snmp_arp_refresh_sec=CASE WHEN $16::int = -1 THEN NULL ELSE COALESCE($16,snmp_arp_refresh_sec) END,
		 snmp_routes_refresh_sec=CASE WHEN $17::int = -1 THEN NULL ELSE COALESCE($17,snmp_routes_refresh_sec) END,
		 secondary_ips=CASE WHEN $18::inet[] IS NULL THEN secondary_ips ELSE $18::inet[] END,
		 poll_interval_sec=CASE WHEN $19::int = -1 THEN NULL WHEN $19::int > 0 THEN $19 ELSE poll_interval_sec END,
		 poll_down_retries=CASE WHEN $20::int = -1 THEN NULL WHEN $20::int > 0 THEN $20 ELSE poll_down_retries END,
		 poll_agent_id=CASE WHEN $21::text IS NULL THEN poll_agent_id WHEN $21::text='' THEN NULL ELSE $21::uuid END,
		 poll_notifications_enabled=CASE WHEN $22::int = -1 THEN NULL WHEN $22::int = 0 THEN FALSE WHEN $22::int = 1 THEN TRUE ELSE poll_notifications_enabled END,
		 updated_at=NOW()
		WHERE id=$1
	`, deviceID, req.Name, req.PrimaryIP, req.DNSName, req.DeviceType, profileArg, req.Tags, customJSON, req.Notes, req.Monitoring, agentArg, req.MACAddress,
		req.SysRefresh, req.IfRefresh, req.IPRefresh, req.ARPRefresh, req.RouteRefresh, req.SecondaryIPs,
		req.PollInterval, req.PollRetries, req.PollAgentID, req.PollNotify)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ParentIDs != nil {
		_, _ = s.pool.Exec(ctx, `DELETE FROM device_parents WHERE device_id=$1`, deviceID)
		for _, p := range req.ParentIDs {
			if p != deviceID {
				_, _ = s.pool.Exec(ctx, `INSERT INTO device_parents (device_id,parent_device_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, deviceID, p)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM devices WHERE id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) acknowledgeDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note     string `json:"note"`
		Duration int    `json:"duration_minutes"`
	}
	_ = decodeBody(r, &req)
	u, _ := auth.UserFrom(r.Context())
	var expires any
	if req.Duration > 0 {
		expires = time.Now().Add(time.Duration(req.Duration) * time.Minute)
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE devices SET is_acknowledged=TRUE, ack_by=$2, ack_note=$3, ack_expires_at=$4, updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"), u.Username, req.Note, expires)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.pool.Exec(r.Context(), `
		UPDATE outages SET acknowledged=TRUE, acknowledged_by=$2, acknowledged_at=NOW()
		WHERE device_id=$1 AND ended_at IS NULL
	`, chi.URLParam(r, "id"), u.Username)
	if s.notifyAck != nil {
		s.notifyAck(r.Context(), chi.URLParam(r, "id"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) unacknowledgeDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := s.pool.Exec(ctx, `
		UPDATE devices SET is_acknowledged=FALSE, ack_by=NULL, ack_note=NULL, ack_expires_at=NULL, updated_at=NOW()
		WHERE id=$1
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE outages SET acknowledged=FALSE, acknowledged_by=NULL, acknowledged_at=NULL
		WHERE device_id=$1 AND ended_at IS NULL
	`, chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) setMaintenance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int    `json:"minutes"`
		Note    string `json:"note"`
	}
	_ = decodeBody(r, &req)
	var until any
	if req.Minutes > 0 {
		until = time.Now().Add(time.Duration(req.Minutes) * time.Minute)
	}
	_, err := s.pool.Exec(r.Context(), `
		UPDATE devices SET maintenance_until=$2, maintenance_note=NULLIF($3,''), updated_at=NOW() WHERE id=$1
	`, chi.URLParam(r, "id"), until, req.Note)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) pollNow(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `UPDATE device_services SET last_check_at=NULL WHERE device_id=$1`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.pool.Query(r.Context(), `
		SELECT ds.id, p.name, p.probe_type, ds.status, ds.interval_sec, COALESCE(ds.last_output_message,''),
		       ds.last_check_at, COALESCE(ds.last_value,0), COALESCE(p.unit,'')
		FROM device_services ds JOIN probes p ON p.id=ds.probe_id
		WHERE ds.device_id=$1 ORDER BY p.name
	`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, pname, ptype, st, msg, unit string
		var interval int
		var value float64
		var last *time.Time
		if rows.Scan(&id, &pname, &ptype, &st, &interval, &msg, &last, &value, &unit) == nil {
			if !probeTypeAllowed(user, ptype) {
				continue
			}
			out = append(out, map[string]any{"id": id, "probe_name": pname, "probe_type": ptype,
				"status": st, "status_text": statusText(st), "interval_sec": interval,
				"last_output_message": msg, "last_check_at": last, "last_value": value, "unit": unit})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

type addServiceRequest struct {
	ProbeID       string `json:"probe_id"`
	CustomName    string `json:"custom_name"`
	IntervalSec   int    `json:"interval_sec"`
	TimeoutMs     int    `json:"timeout_ms"`
	DownRetries   int    `json:"down_retries"`
	IsCritical    *bool  `json:"is_critical"`
	AffectsDevice *bool  `json:"affects_device_status"`
	Config        any    `json:"config_overrides"`
}

func (s *Server) addService(w http.ResponseWriter, r *http.Request) {
	var req addServiceRequest
	if err := decodeBody(r, &req); err != nil || req.ProbeID == "" {
		writeError(w, http.StatusBadRequest, "probe_id is required")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	var probeType string
	_ = s.pool.QueryRow(r.Context(), `SELECT probe_type FROM probes WHERE id=$1`, req.ProbeID).Scan(&probeType)
	if !probeTypeAllowed(user, probeType) {
		writeError(w, http.StatusForbidden, "sin permiso para este tipo de sonda")
		return
	}
	if req.IntervalSec <= 0 {
		req.IntervalSec = 30
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 2000
	}
	if req.DownRetries <= 0 {
		req.DownRetries = 3
	}
	critical := true
	if req.IsCritical != nil {
		critical = *req.IsCritical
	}
	affects := true
	if req.AffectsDevice != nil {
		affects = *req.AffectsDevice
	}
	overrides, _ := json.Marshal(req.Config)
	if req.Config == nil {
		overrides = []byte("{}")
	}
	name := req.CustomName
	if name == "" {
		_ = s.pool.QueryRow(r.Context(), `SELECT name FROM probes WHERE id=$1`, req.ProbeID).Scan(&name)
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO device_services (device_id, probe_id, custom_name, interval_sec, timeout_ms, down_retries,
		 is_critical, affects_device_status, config_overrides)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb) RETURNING id
	`, chi.URLParam(r, "id"), req.ProbeID, name, req.IntervalSec, req.TimeoutMs, req.DownRetries,
		critical, affects, string(overrides)).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

type ensureProbeRequest struct {
	Method        string `json:"method"`
	TCPPort       int    `json:"tcp_port"`
	SNMPProfileID string `json:"snmp_profile_id"`
}

// ensureDeviceProbe adds a reachability probe that matches how the host was
// discovered. Devices discovered through SNMP/TCP usually block ICMP, so a
// default Ping probe would mark them down forever.
func (s *Server) ensureDeviceProbe(w http.ResponseWriter, r *http.Request) {
	var req ensureProbeRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	deviceID := chi.URLParam(r, "id")
	method := normalizeProbeMethod(req.Method)
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	if !probeTypeAllowed(user, method) {
		writeError(w, http.StatusForbidden, "sin permiso para este tipo de sonda")
		return
	}
	var currentProfile string
	if err := s.pool.QueryRow(r.Context(), `SELECT COALESCE(snmp_profile_id::text,'') FROM devices WHERE id=$1`, deviceID).Scan(&currentProfile); err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT p.probe_type, COALESCE(ds.custom_name,'')
		FROM device_services ds JOIN probes p ON p.id=ds.probe_id
		WHERE ds.device_id=$1 AND ds.is_enabled=TRUE
	`, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var types, names []string
	for rows.Next() {
		var typ, name string
		if rows.Scan(&typ, &name) == nil {
			types = append(types, typ)
			names = append(names, name)
		}
	}
	rows.Close()
	onlyDefaultPing := len(types) == 1 && types[0] == "icmp" && names[0] == "Ping"
	if len(types) > 0 && !(onlyDefaultPing && method != "icmp") {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"created": false, "reason": "el dispositivo ya tiene sondas activas",
		}})
		return
	}

	if method == "snmp" {
		profile := strings.TrimSpace(req.SNMPProfileID)
		if profile == "" {
			profile = currentProfile
		}
		if profile == "" {
			writeError(w, http.StatusBadRequest, "snmp_profile_id is required for an SNMP probe")
			return
		}
		if _, err := s.pool.Exec(r.Context(), `UPDATE devices SET snmp_profile_id=$2 WHERE id=$1`, deviceID, profile); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if onlyDefaultPing && method != "icmp" {
		// Keep the ICMP history but let the discovered protocol decide the
		// device status.
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE device_services ds
			SET is_critical=false, affects_device_status=false
			FROM probes p
			WHERE ds.probe_id=p.id AND ds.device_id=$1 AND p.probe_type='icmp'
		`, deviceID)
	}

	created, err := ensureDefaultProbe(r.Context(), s.pool, deviceID, method, req.TCPPort)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"created": created, "method": method,
	}})
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	_, err := s.pool.Exec(r.Context(), `DELETE FROM device_services WHERE id=$1 AND device_id=$2`,
		chi.URLParam(r, "serviceID"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

var _ = strconv.Itoa

func refreshArg(v *int) any {
	if v == nil || *v < 0 {
		return nil
	}
	n := *v
	if n > 86400 {
		n = 86400
	}
	return n
}

func normalizeRefresh(v *int) *int {
	if v == nil {
		return nil
	}
	if *v == -1 {
		n := -1
		return &n
	}
	n := *v
	if n < 0 {
		n = 0
	}
	if n > 86400 {
		n = 86400
	}
	return &n
}
