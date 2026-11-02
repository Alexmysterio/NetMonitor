package api

import (
	"encoding/json"
	"net/http"
)

type snmpRefreshConfig struct {
	SystemSec     int `json:"system_sec"`
	InterfacesSec int `json:"interfaces_sec"`
	IPsSec        int `json:"ips_sec"`
	ARPSec        int `json:"arp_sec"`
	RoutesSec     int `json:"routes_sec"`
}

func defaultSNMPRefresh() snmpRefreshConfig {
	return snmpRefreshConfig{SystemSec: 300, InterfacesSec: 60, IPsSec: 300, ARPSec: 300, RoutesSec: 600}
}

func (s *Server) snmpRefreshDefaults(r *http.Request) snmpRefreshConfig {
	out := defaultSNMPRefresh()
	var raw string
	if err := s.pool.QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key='snmp_refresh_defaults'`).Scan(&raw); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) effectiveSNMPRefresh(r *http.Request, sys, ifs, ips, arp, routes int) snmpRefreshConfig {
	out := s.snmpRefreshDefaults(r)
	if sys >= 0 {
		out.SystemSec = sys
	}
	if ifs >= 0 {
		out.InterfacesSec = ifs
	}
	if ips >= 0 {
		out.IPsSec = ips
	}
	if arp >= 0 {
		out.ARPSec = arp
	}
	if routes >= 0 {
		out.RoutesSec = routes
	}
	return out
}

func (s *Server) getSNMPRefreshSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": s.snmpRefreshDefaults(r)})
}

func (s *Server) setSNMPRefreshSettings(w http.ResponseWriter, r *http.Request) {
	var req snmpRefreshConfig
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	for _, v := range []int{req.SystemSec, req.InterfacesSec, req.IPsSec, req.ARPSec, req.RoutesSec} {
		if v < 0 || v > 86400 {
			writeError(w, http.StatusBadRequest, "los intervalos deben estar entre 0 y 86400 segundos")
			return
		}
	}
	raw, _ := json.Marshal(req)
	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO system_settings (key, value) VALUES ('snmp_refresh_defaults', $1)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()
	`, string(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": req})
}
