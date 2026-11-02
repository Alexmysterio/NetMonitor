package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/netmonitor/netmonitor/internal/auth"
)

type metricWindow struct {
	Interval string
	From     time.Time
	To       time.Time
}

func resolveMetricWindow(r *http.Request) (metricWindow, error) {
	now := time.Now().UTC()
	fromRaw, toRaw := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	var from, to time.Time
	if fromRaw != "" || toRaw != "" {
		var err error
		from, err = time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return metricWindow{}, fmt.Errorf("from debe ser RFC3339")
		}
		to, err = time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return metricWindow{}, fmt.Errorf("to debe ser RFC3339")
		}
		if !to.After(from) {
			return metricWindow{}, fmt.Errorf("to debe ser posterior a from")
		}
	} else {
		to = now
		switch r.URL.Query().Get("range") {
		case "1h":
			from = now.Add(-time.Hour)
		case "6h":
			from = now.Add(-6 * time.Hour)
		case "7d":
			from = now.Add(-7 * 24 * time.Hour)
		case "30d":
			from = now.Add(-30 * 24 * time.Hour)
		default:
			from = now.Add(-24 * time.Hour)
		}
	}
	if to.Sub(from) > 180*24*time.Hour {
		return metricWindow{}, fmt.Errorf("el rango máximo es 180 días")
	}
	d := to.Sub(from)
	interval := "15 minutes"
	switch {
	case d <= 2*time.Hour:
		interval = "1 minute"
	case d <= 8*time.Hour:
		interval = "5 minutes"
	case d <= 36*time.Hour:
		interval = "15 minutes"
	case d <= 8*24*time.Hour:
		interval = "1 hour"
	case d <= 35*24*time.Hour:
		interval = "6 hours"
	default:
		interval = "1 day"
	}
	return metricWindow{Interval: interval, From: from, To: to}, nil
}

func (s *Server) metricsCatalog(w http.ResponseWriter, r *http.Request) {
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
	series := []map[string]any{}
	rows, err := s.pool.Query(r.Context(), `
		SELECT d.id, d.name, host(d.primary_ip), ds.id, COALESCE(NULLIF(ds.custom_name,''),p.name),
		       p.probe_type, COALESCE(p.unit,'')
		FROM devices d
		JOIN device_services ds ON ds.device_id=d.id AND ds.is_enabled=TRUE
		JOIN probes p ON p.id=ds.probe_id
		WHERE ($1::uuid[] IS NULL OR d.id = ANY($1::uuid[]))
		  AND ($2::uuid[] IS NULL OR EXISTS (
		      SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id = ANY($2::uuid[])))
		ORDER BY d.name, p.name
	`, deviceArg, mapArg)
	if err == nil {
		for rows.Next() {
			var deviceID, deviceName, ip, serviceID, name, probeType, unit string
			if rows.Scan(&deviceID, &deviceName, &ip, &serviceID, &name, &probeType, &unit) == nil {
				series = append(series, map[string]any{
					"id": "service:" + serviceID, "kind": "service", "label": fmt.Sprintf("%s · %s", deviceName, name),
					"device_id": deviceID, "device_name": deviceName, "ip": ip, "unit": unit,
					"probe_type": probeType,
				})
			}
		}
		rows.Close()
	}
	deviceRows, err := s.pool.Query(r.Context(), `
		SELECT id, name, host(primary_ip) FROM devices
		WHERE ($1::uuid[] IS NULL OR id = ANY($1::uuid[]))
		  AND ($2::uuid[] IS NULL OR EXISTS (
		      SELECT 1 FROM map_elements me WHERE me.device_id=devices.id AND me.map_id = ANY($2::uuid[])))
		ORDER BY name
	`, deviceArg, mapArg)
	if err == nil {
		for deviceRows.Next() {
			var id, name, ip string
			if deviceRows.Scan(&id, &name, &ip) == nil {
				series = append(series, map[string]any{
					"id": "device:" + id, "kind": "device",
					"label": fmt.Sprintf("%s · todas las sondas", name),
					"unit":  "", "device_id": id, "device_name": name, "ip": ip,
				})
			}
		}
		deviceRows.Close()
	}
	linkRows, err := s.pool.Query(r.Context(), `
		SELECT l.id, m.name, COALESCE(sd.name, se.label_template,'Origen'), COALESCE(td.name, te.label_template,'Destino'),
		       COALESCE(l.snmp_if_name,''), m.id
		FROM map_links l
		JOIN maps m ON m.id=l.map_id
		LEFT JOIN map_elements se ON se.id=l.source_element_id
		LEFT JOIN devices sd ON sd.id=se.device_id
		LEFT JOIN map_elements te ON te.id=l.target_element_id
		LEFT JOIN devices td ON td.id=te.device_id
		WHERE ($1::uuid[] IS NULL OR m.id = ANY($1::uuid[]))
		  AND ($2::uuid[] IS NULL OR (
		      EXISTS (
		          SELECT 1 FROM map_elements lime
		          WHERE lime.id IN (l.source_element_id, l.target_element_id)
		            AND lime.device_id = ANY($2::uuid[]))
		      AND NOT EXISTS (
		          SELECT 1 FROM map_elements lime2
		          WHERE lime2.id IN (l.source_element_id, l.target_element_id)
		            AND lime2.device_id IS NOT NULL
		            AND NOT (lime2.device_id = ANY($2::uuid[])))))
		ORDER BY m.name, l.id
	`, mapArg, deviceArg)
	if err == nil {
		for linkRows.Next() {
			var id, mapName, source, target, ifName, mapID string
			if linkRows.Scan(&id, &mapName, &source, &target, &ifName, &mapID) == nil {
				suffix := ""
				if ifName != "" {
					suffix = " · " + ifName
				}
				series = append(series, map[string]any{
					"id": "link:" + id + ":rx", "kind": "link", "direction": "rx",
					"label": fmt.Sprintf("%s · %s → %s RX%s", mapName, source, target, suffix),
					"unit":  "bps", "map_id": mapID,
				})
				series = append(series, map[string]any{
					"id": "link:" + id + ":tx", "kind": "link", "direction": "tx",
					"label": fmt.Sprintf("%s · %s → %s TX%s", mapName, source, target, suffix),
					"unit":  "bps", "map_id": mapID,
				})
			}
		}
		linkRows.Close()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": series})
}

func (s *Server) metricsCompare(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFrom(r.Context())
	window, err := resolveMetricWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	requested := []string{}
	for _, raw := range strings.Split(r.URL.Query().Get("series"), ",") {
		if strings.TrimSpace(raw) != "" {
			requested = append(requested, strings.TrimSpace(raw))
		}
	}
	if len(requested) == 0 {
		writeError(w, http.StatusBadRequest, "series is required")
		return
	}
	if len(requested) > 12 {
		writeError(w, http.StatusBadRequest, "máximo 12 series por consulta")
		return
	}
	out := []map[string]any{}
	for _, spec := range requested {
		parts := strings.Split(spec, ":")
		if len(parts) < 2 {
			continue
		}
		kind, id := parts[0], parts[1]
		switch kind {
		case "service":
			var deviceID string
			if err := s.pool.QueryRow(r.Context(), `SELECT device_id::text FROM device_services WHERE id=$1`, id).Scan(&deviceID); err != nil || !s.canAccessDevice(r.Context(), user, deviceID) {
				continue
			}
			points, label, unit, qerr := s.queryServiceSeries(r, id, window)
			if qerr == nil {
				out = append(out, map[string]any{"id": spec, "kind": kind, "label": label, "unit": unit, "points": points})
			}
		case "link":
			var mapID string
			if err := s.pool.QueryRow(r.Context(), `SELECT map_id::text FROM map_links WHERE id=$1`, id).Scan(&mapID); err != nil || !s.canAccessMap(r.Context(), user, mapID) {
				continue
			}
			dir := "rx"
			if len(parts) > 2 {
				dir = parts[2]
			}
			points, label, qerr := s.queryLinkSeries(r, id, dir, window)
			if qerr == nil {
				out = append(out, map[string]any{"id": spec, "kind": kind, "label": label, "unit": "bps", "points": points})
			}
		case "device":
			if !s.canAccessDevice(r.Context(), user, id) {
				continue
			}
			points, label, qerr := s.queryDeviceSeries(r, id, window)
			if qerr == nil {
				out = append(out, map[string]any{"id": spec, "kind": kind, "label": label, "unit": "", "points": points})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"from": window.From, "to": window.To, "interval": window.Interval, "series": out,
	}})
}

func (s *Server) queryServiceSeries(r *http.Request, id string, window metricWindow) ([]map[string]any, string, string, error) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT time_bucket($2::interval, time) AS bucket,
		       COALESCE(avg(value), avg(rtt_ms), 0)::float8
		FROM service_metrics
		WHERE service_id=$1 AND time >= $3 AND time <= $4
		GROUP BY 1 ORDER BY 1
	`, id, window.Interval, window.From, window.To)
	if err != nil {
		return nil, "", "", err
	}
	defer rows.Close()
	points := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var value float64
		if rows.Scan(&at, &value) == nil {
			points = append(points, map[string]any{"time": at, "value": value})
		}
	}
	var label, unit string
	_ = s.pool.QueryRow(r.Context(), `
		SELECT d.name || ' · ' || COALESCE(NULLIF(ds.custom_name,''),p.name), COALESCE(p.unit,'')
		FROM device_services ds JOIN devices d ON d.id=ds.device_id JOIN probes p ON p.id=ds.probe_id
		WHERE ds.id=$1
	`, id).Scan(&label, &unit)
	if label == "" {
		label = "Servicio " + id
	}
	return points, label, unit, nil
}

func (s *Server) queryDeviceSeries(r *http.Request, id string, window metricWindow) ([]map[string]any, string, error) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT time_bucket($2::interval, time) AS bucket,
		       COALESCE(avg(value), avg(rtt_ms), 0)::float8
		FROM service_metrics
		WHERE device_id=$1 AND time >= $3 AND time <= $4
		GROUP BY 1 ORDER BY 1
	`, id, window.Interval, window.From, window.To)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	points := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var value float64
		if rows.Scan(&at, &value) == nil {
			points = append(points, map[string]any{"time": at, "value": value})
		}
	}
	var label string
	_ = s.pool.QueryRow(r.Context(), `SELECT name FROM devices WHERE id=$1`, id).Scan(&label)
	if label == "" {
		label = "Dispositivo " + id
	}
	return points, label + " · todas las sondas", nil
}

func (s *Server) queryLinkSeries(r *http.Request, id, direction string, window metricWindow) ([]map[string]any, string, error) {
	column := "rx_bps"
	if direction == "tx" {
		column = "tx_bps"
	}
	rows, err := s.pool.Query(r.Context(), fmt.Sprintf(`
		SELECT time_bucket($2::interval, time) AS bucket, avg(%s)::float8
		FROM link_metrics
		WHERE link_id=$1 AND time >= $3 AND time <= $4
		GROUP BY 1 ORDER BY 1
	`, column), id, window.Interval, window.From, window.To)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	points := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var value float64
		if rows.Scan(&at, &value) == nil {
			points = append(points, map[string]any{"time": at, "value": value})
		}
	}
	var label string
	_ = s.pool.QueryRow(r.Context(), `
		SELECT m.name || ' · ' || COALESCE(sd.name, se.label_template,'Origen') || ' → ' ||
		       COALESCE(td.name, te.label_template,'Destino')
		FROM map_links l JOIN maps m ON m.id=l.map_id
		LEFT JOIN map_elements se ON se.id=l.source_element_id LEFT JOIN devices sd ON sd.id=se.device_id
		LEFT JOIN map_elements te ON te.id=l.target_element_id LEFT JOIN devices td ON td.id=te.device_id
		WHERE l.id=$1
	`, id).Scan(&label)
	if label == "" {
		label = "Enlace " + id
	}
	label += " " + strings.ToUpper(direction)
	return points, label, nil
}
