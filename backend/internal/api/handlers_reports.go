package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type slaRow struct {
	DeviceID     string  `json:"device_id"`
	DeviceName   string  `json:"device_name"`
	Incidents    int     `json:"incidents"`
	Downtime     float64 `json:"downtime_sec"`
	Availability float64 `json:"availability_pct"`
}

func (s *Server) slaReport(w http.ResponseWriter, r *http.Request) {
	to := time.Now().UTC()
	from := to.Add(-30 * 24 * time.Hour)
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	if !to.After(from) {
		writeError(w, http.StatusBadRequest, "to debe ser posterior a from")
		return
	}
	mapID := r.URL.Query().Get("map_id")
	rows, err := s.pool.Query(r.Context(), `
		SELECT DISTINCT d.id::text, d.name
		FROM devices d
		LEFT JOIN map_elements me ON me.device_id=d.id
		WHERE ($1 = '' OR me.map_id=$1::uuid)
		ORDER BY d.name
	`, mapID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type dev struct{ id, name string }
	var devices []dev
	for rows.Next() {
		var d dev
		if rows.Scan(&d.id, &d.name) == nil {
			devices = append(devices, d)
		}
	}
	rows.Close()
	window := to.Sub(from).Seconds()
	out := make([]slaRow, 0, len(devices))
	for _, d := range devices {
		var downtime float64
		var incidents int
		oRows, err := s.pool.Query(r.Context(), `
			SELECT started_at, ended_at FROM outages
			WHERE device_id=$1 AND started_at < $3 AND (ended_at IS NULL OR ended_at > $2)
		`, d.id, from, to)
		if err == nil {
			for oRows.Next() {
				var start time.Time
				var end *time.Time
				if oRows.Scan(&start, &end) == nil {
					s := start
					if s.Before(from) {
						s = from
					}
					e := to
					if end != nil && end.Before(to) {
						e = *end
					}
					if e.After(s) {
						downtime += e.Sub(s).Seconds()
						incidents++
					}
				}
			}
			oRows.Close()
		}
		avail := 100.0
		if window > 0 {
			avail = (window - downtime) / window * 100
			if avail < 0 {
				avail = 0
			}
		}
		out = append(out, slaRow{d.id, d.name, incidents, downtime, avail})
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=sla.csv")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"device_id", "device", "incidents", "downtime_sec", "availability_pct", "from", "to"})
		for _, row := range out {
			_ = cw.Write([]string{row.DeviceID, row.DeviceName, strconv.Itoa(row.Incidents),
				fmt.Sprintf("%.0f", row.Downtime), fmt.Sprintf("%.3f", row.Availability),
				from.Format(time.RFC3339), to.Format(time.RFC3339)})
		}
		cw.Flush()
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"from": from, "to": to, "devices": out,
	}})
}
