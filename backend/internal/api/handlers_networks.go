package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/netmonitor/netmonitor/internal/auth"
)

type networkRequest struct {
	Name           string   `json:"name"`
	CIDR           string   `json:"cidr"`
	AddressListID  string   `json:"address_list_id"`
	MapID          string   `json:"map_id"`
	AgentID        string   `json:"agent_id"`
	SNMPProfileID  string   `json:"snmp_profile_id"`
	ProbeIDs       []string `json:"probe_ids"`
	IntervalSec    int      `json:"interval_sec"`
	Mode           string   `json:"mode"`
	NamePreference string   `json:"name_preference"`
	AutoCreate     bool     `json:"auto_create"`
	IsEnabled      bool     `json:"is_enabled"`
}

func normalizeNetworkRequest(req *networkRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	req.CIDR = strings.TrimSpace(req.CIDR)
	if req.CIDR != "" {
		_, ipnet, err := net.ParseCIDR(req.CIDR)
		if err != nil {
			return fmt.Errorf("CIDR inválido")
		}
		ones, bits := ipnet.Mask.Size()
		if bits != 32 {
			return fmt.Errorf("el descubrimiento programado solo admite IPv4")
		}
		if bits-ones > 10 {
			return fmt.Errorf("usa /22 o menor para evitar barridos masivos")
		}
		req.CIDR = ipnet.String()
	}
	if req.CIDR == "" && req.AddressListID == "" {
		return fmt.Errorf("indica un CIDR o una lista de direcciones")
	}
	if req.IntervalSec < 30 {
		req.IntervalSec = 3600
	}
	if req.Mode != "reliable" {
		req.Mode = "fast"
	}
	if req.NamePreference == "" {
		req.NamePreference = "dns_snmp_ip"
	}
	if req.ProbeIDs == nil {
		req.ProbeIDs = []string{}
	}
	return nil
}

func (s *Server) canAccessNetwork(ctx context.Context, user auth.User, networkID string) bool {
	if user.Role == "admin" {
		return true
	}
	var mapID string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(map_id::text,'') FROM networks WHERE id=$1`, networkID).Scan(&mapID); err != nil {
		return false
	}
	return mapID != "" && s.canAccessMap(ctx, user, mapID)
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	rows, err := s.pool.Query(r.Context(), `
		SELECT n.id, n.name, COALESCE(n.cidr::text,''), COALESCE(n.address_list_id::text,''),
		       COALESCE(n.map_id::text,''), COALESCE(n.agent_id::text,''), COALESCE(n.snmp_profile_id::text,''),
		       n.probe_ids, n.interval_sec, n.mode, n.name_preference, n.auto_create, n.is_enabled,
		       n.last_scan_at, n.next_scan_at, n.last_status, n.last_error,
		       COALESCE(m.name,''), COALESCE(a.name,''), COALESCE(l.name,''), COALESCE(p.name,''),
		       COALESCE(r.scanned_count,0), COALESCE(r.alive_count,0), COALESCE(r.created_count,0)
		FROM networks n
		LEFT JOIN maps m ON m.id=n.map_id
		LEFT JOIN agents a ON a.id=n.agent_id
		LEFT JOIN address_lists l ON l.id=n.address_list_id
		LEFT JOIN snmp_profiles p ON p.id=n.snmp_profile_id
		LEFT JOIN LATERAL (
			SELECT scanned_count, alive_count, created_count FROM network_scan_runs
			WHERE network_id=n.id ORDER BY started_at DESC LIMIT 1
		) r ON TRUE
		ORDER BY n.name
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, cidr, listID, mapID, agentID, profileID, mode, namePref, lastStatus, lastError string
		var mapName, agentName, listName, profileName string
		var probeIDs []string
		var interval, scanned, alive, created int
		var autoCreate, enabled bool
		var lastScan, nextScan *time.Time
		if rows.Scan(&id, &name, &cidr, &listID, &mapID, &agentID, &profileID, &probeIDs,
			&interval, &mode, &namePref, &autoCreate, &enabled, &lastScan, &nextScan,
			&lastStatus, &lastError, &mapName, &agentName, &listName, &profileName,
			&scanned, &alive, &created) == nil {
			if user.Role != "admin" && (mapID == "" || !s.canAccessMap(r.Context(), user, mapID)) {
				continue
			}
			out = append(out, map[string]any{
				"id": id, "name": name, "cidr": cidr, "address_list_id": listID,
				"map_id": mapID, "agent_id": agentID, "snmp_profile_id": profileID,
				"probe_ids": probeIDs, "interval_sec": interval, "mode": mode,
				"name_preference": namePref, "auto_create": autoCreate, "is_enabled": enabled,
				"last_scan_at": lastScan, "next_scan_at": nextScan, "last_status": lastStatus,
				"last_error": lastError, "map_name": mapName, "agent_name": agentName,
				"address_list_name": listName, "snmp_profile_name": profileName,
				"last_scanned": scanned, "last_alive": alive, "last_created": created,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) createNetwork(w http.ResponseWriter, r *http.Request) {
	var req networkRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := normalizeNetworkRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if user.Role != "admin" && (req.MapID == "" || !s.canAccessMap(r.Context(), user, req.MapID)) {
		writeError(w, http.StatusForbidden, "sin acceso al mapa de destino")
		return
	}
	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO networks (name, cidr, address_list_id, map_id, agent_id, snmp_profile_id,
		  probe_ids, interval_sec, mode, name_preference, auto_create, is_enabled, next_scan_at)
		VALUES ($1,NULLIF($2,'')::cidr,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,
		  NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,NOW())
		RETURNING id
	`, req.Name, req.CIDR, req.AddressListID, req.MapID, req.AgentID, req.SNMPProfileID,
		req.ProbeIDs, req.IntervalSec, req.Mode, req.NamePreference, req.AutoCreate, req.IsEnabled).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "data": map[string]any{"id": id}})
}

func (s *Server) updateNetwork(w http.ResponseWriter, r *http.Request) {
	var req networkRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := normalizeNetworkRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, _ := auth.UserFrom(r.Context())
	networkID := chi.URLParam(r, "id")
	if !s.canAccessNetwork(r.Context(), user, networkID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	if user.Role != "admin" && (req.MapID == "" || !s.canAccessMap(r.Context(), user, req.MapID)) {
		writeError(w, http.StatusForbidden, "sin acceso al mapa de destino")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE networks SET name=$2, cidr=NULLIF($3,'')::cidr, address_list_id=NULLIF($4,'')::uuid,
		  map_id=NULLIF($5,'')::uuid, agent_id=NULLIF($6,'')::uuid, snmp_profile_id=NULLIF($7,'')::uuid,
		  probe_ids=$8, interval_sec=$9, mode=$10, name_preference=$11, auto_create=$12,
		  is_enabled=$13, next_scan_at=NOW()+($9::int * interval '1 second'), updated_at=NOW()
		WHERE id=$1
	`, networkID, req.Name, req.CIDR, req.AddressListID, req.MapID, req.AgentID,
		req.SNMPProfileID, req.ProbeIDs, req.IntervalSec, req.Mode, req.NamePreference,
		req.AutoCreate, req.IsEnabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "red no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteNetwork(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	networkID := chi.URLParam(r, "id")
	if !s.canAccessNetwork(r.Context(), user, networkID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM networks WHERE id=$1`, networkID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "red no encontrada")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) scanNetworkNow(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessNetwork(r.Context(), user, chi.URLParam(r, "id")) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	run, err := s.runNetworkScan(r.Context(), chi.URLParam(r, "id"), "manual")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": run})
}

func (s *Server) listNetworkRuns(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	networkID := chi.URLParam(r, "id")
	if !s.canAccessNetwork(r.Context(), user, networkID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, network_id, started_at, finished_at, status, mode, COALESCE(agent_id::text,''),
		       scanned_count, alive_count, created_count, error
		FROM network_scan_runs WHERE network_id=$1 ORDER BY started_at DESC LIMIT 50
	`, networkID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, networkID, status, mode, agentID, errMsg string
		var started time.Time
		var finished *time.Time
		var scanned, alive, created int
		if rows.Scan(&id, &networkID, &started, &finished, &status, &mode, &agentID,
			&scanned, &alive, &created, &errMsg) == nil {
			out = append(out, map[string]any{
				"id": id, "network_id": networkID, "started_at": started, "finished_at": finished,
				"status": status, "mode": mode, "agent_id": agentID, "scanned_count": scanned,
				"alive_count": alive, "created_count": created, "error": errMsg,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func (s *Server) getNetworkRun(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	networkID := chi.URLParam(r, "id")
	if !s.canAccessNetwork(r.Context(), user, networkID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	var id, networkIDValue, status, mode, agentID, errMsg, results string
	var started time.Time
	var finished *time.Time
	var scanned, alive, created int
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, network_id, started_at, finished_at, status, mode, COALESCE(agent_id::text,''),
		       scanned_count, alive_count, created_count, results::text, error
		FROM network_scan_runs WHERE id=$1 AND network_id=$2
	`, chi.URLParam(r, "runID"), networkID).
		Scan(&id, &networkIDValue, &started, &finished, &status, &mode, &agentID,
			&scanned, &alive, &created, &results, &errMsg)
	if err != nil {
		writeError(w, http.StatusNotFound, "ejecución no encontrada")
		return
	}
	var hosts any
	_ = json.Unmarshal([]byte(results), &hosts)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"id": id, "network_id": networkIDValue, "started_at": started, "finished_at": finished,
		"status": status, "mode": mode, "agent_id": agentID, "scanned_count": scanned,
		"alive_count": alive, "created_count": created, "results": hosts, "error": errMsg,
	}})
}

func (s *Server) applyNetworkRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunID string   `json:"run_id"`
		IPs   []string `json:"ips"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	networkID := chi.URLParam(r, "id")
	if !s.canAccessNetwork(r.Context(), user, networkID) {
		writeError(w, http.StatusForbidden, "sin acceso a esta red")
		return
	}
	created, err := s.applyDiscoveredHosts(r.Context(), networkID, req.RunID, req.IPs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"created": created}})
}
