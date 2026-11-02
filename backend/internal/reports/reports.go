// Package reports builds availability, SLA and interface reports and writes
// them as PDF/CSV. It shares the same SQL definitions between the interactive
// API and the scheduled generator so a downloaded report and an emailed one
// never disagree.
package reports

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Pool  *pgxpool.Pool
	Scope Scope
}

// Scope narrows every report query to the devices/maps a non-admin user is
// allowed to see. Nil slices mean "not restricted"; non-nil empty slices mean
// "restricted to nothing".
type Scope struct {
	DeviceIDs []string
	MapIDs    []string
}

func (s Service) scopeArgs() (any, any) {
	var devices, maps any
	if s.Scope.DeviceIDs != nil {
		devices = s.Scope.DeviceIDs
	}
	if s.Scope.MapIDs != nil {
		maps = s.Scope.MapIDs
	}
	return devices, maps
}

type Summary struct {
	From            time.Time   `json:"from"`
	To              time.Time   `json:"to"`
	Devices         int         `json:"devices"`
	Up              int         `json:"up"`
	Warning         int         `json:"warning"`
	Down            int         `json:"down"`
	Unreachable     int         `json:"unreachable"`
	Disabled        int         `json:"disabled"`
	Unknown         int         `json:"unknown"`
	Incidents       int         `json:"incidents"`
	ActiveOutages   int         `json:"active_outages"`
	DowntimeSec     float64     `json:"downtime_sec"`
	AvailabilityPct float64     `json:"availability_pct"`
	MTTRSec         float64     `json:"mttr_sec"`
	Maxtime         float64     `json:"maxtime_sec"`
	TopDown         []DeviceRow `json:"top_down"`
}

type DeviceRow struct {
	DeviceID        string   `json:"device_id"`
	DeviceName      string   `json:"device_name"`
	PrimaryIP       string   `json:"primary_ip"`
	Status          string   `json:"status"`
	DeviceType      string   `json:"device_type"`
	Tags            []string `json:"tags"`
	Incidents       int      `json:"incidents"`
	DowntimeSec     float64  `json:"downtime_sec"`
	AvailabilityPct float64  `json:"availability_pct"`
	RootCauseID     string   `json:"root_cause_device_id"`
	RootCause       string   `json:"root_cause"`
}

type ClientRow struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	TargetPct       float64 `json:"target_pct"`
	Devices         int     `json:"devices"`
	Incidents       int     `json:"incidents"`
	DowntimeSec     float64 `json:"downtime_sec"`
	AvailabilityPct float64 `json:"availability_pct"`
	Compliant       bool    `json:"compliant"`
}

type InterfaceRow struct {
	LinkID          string  `json:"link_id"`
	MapID           string  `json:"map_id"`
	MapName         string  `json:"map_name"`
	DeviceName      string  `json:"device_name"`
	InterfaceName   string  `json:"interface_name"`
	LinkType        string  `json:"link_type"`
	LinkStatus      string  `json:"link_status"`
	MaxSpeedBps     int64   `json:"max_speed_bps"`
	Samples         int64   `json:"samples"`
	UpSamples       int64   `json:"up_samples"`
	AvailabilityPct float64 `json:"availability_pct"`
	AvgRxBps        float64 `json:"avg_rx_bps"`
	AvgTxBps        float64 `json:"avg_tx_bps"`
	MaxErrors       int64   `json:"max_errors"`
}

type MonthlyRow struct {
	Month           string  `json:"month"`
	From            string  `json:"from"`
	To              string  `json:"to"`
	Devices         int     `json:"devices"`
	Incidents       int     `json:"incidents"`
	DowntimeSec     float64 `json:"downtime_sec"`
	AvailabilityPct float64 `json:"availability_pct"`
	DeltaPct        float64 `json:"delta_pct"`
	DeltaIncidents  int     `json:"delta_incidents"`
}

func normalizeRange(from, to time.Time) (time.Time, time.Time, error) {
	if from.IsZero() {
		from = time.Now().UTC().Add(-30 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if !to.After(from) {
		return from, to, fmt.Errorf("to debe ser posterior a from")
	}
	return from, to, nil
}

func mapFilterSQL(mapID string, arg int) string {
	return fmt.Sprintf("($%d::uuid IS NULL OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id=$%d::uuid))", arg, arg)
}

func nullableMapID(mapID string) any {
	if strings.TrimSpace(mapID) == "" {
		return nil
	}
	return mapID
}

func (s Service) Summary(ctx context.Context, from, to time.Time, mapID string) (Summary, error) {
	from, to, err := normalizeRange(from, to)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{From: from, To: to}
	mapArg := nullableMapID(mapID)
	scopeDevices, scopeMaps := s.scopeArgs()
	deviceFilter := `
		($1::uuid IS NULL OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id=$1::uuid))
		AND ($2::uuid[] IS NULL OR d.id = ANY($2::uuid[]))
		AND ($3::uuid[] IS NULL OR EXISTS (SELECT 1 FROM map_elements me2 WHERE me2.device_id=d.id AND me2.map_id = ANY($3::uuid[])))`
	query := `
		SELECT count(*),
		       count(*) FILTER (WHERE d.status='up'),
		       count(*) FILTER (WHERE d.status='warning'),
		       count(*) FILTER (WHERE d.status='down'),
		       count(*) FILTER (WHERE d.status='unreachable'),
		       count(*) FILTER (WHERE d.status='disabled'),
		       count(*) FILTER (WHERE d.status='unknown')
		FROM devices d
		WHERE ` + deviceFilter
	if err := s.Pool.QueryRow(ctx, query, mapArg, scopeDevices, scopeMaps).Scan(&out.Devices, &out.Up, &out.Warning,
		&out.Down, &out.Unreachable, &out.Disabled, &out.Unknown); err != nil {
		return out, err
	}
	query = `
		SELECT COALESCE(sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(o.ended_at,$2),$2) - GREATEST(o.started_at,$1)))),0),
		       count(*) FILTER (WHERE o.started_at >= $1),
		       count(*) FILTER (WHERE o.ended_at IS NULL)
		FROM outages o JOIN devices d ON d.id=o.device_id
		WHERE o.started_at < $2 AND (o.ended_at IS NULL OR o.ended_at > $1)
		  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id=$3::uuid))
		  AND ($4::uuid[] IS NULL OR d.id = ANY($4::uuid[]))
		  AND ($5::uuid[] IS NULL OR EXISTS (SELECT 1 FROM map_elements me2 WHERE me2.device_id=d.id AND me2.map_id = ANY($5::uuid[])))`
	if err := s.Pool.QueryRow(ctx, query, from, to, mapArg, scopeDevices, scopeMaps).Scan(&out.DowntimeSec, &out.Incidents, &out.ActiveOutages); err != nil {
		return out, err
	}
	query = `
		SELECT COALESCE(avg(EXTRACT(EPOCH FROM (LEAST(COALESCE(o.ended_at,$2),$2) - GREATEST(o.started_at,$1)))),0),
		       COALESCE(max(EXTRACT(EPOCH FROM (LEAST(COALESCE(o.ended_at,$2),$2) - GREATEST(o.started_at,$1)))),0)
		FROM outages o JOIN devices d ON d.id=o.device_id
		WHERE o.ended_at IS NOT NULL AND o.started_at < $2 AND o.ended_at > $1
		  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id=$3::uuid))
		  AND ($4::uuid[] IS NULL OR d.id = ANY($4::uuid[]))
		  AND ($5::uuid[] IS NULL OR EXISTS (SELECT 1 FROM map_elements me2 WHERE me2.device_id=d.id AND me2.map_id = ANY($5::uuid[])))`
	_ = s.Pool.QueryRow(ctx, query, from, to, mapArg, scopeDevices, scopeMaps).Scan(&out.MTTRSec, &out.Maxtime)
	window := to.Sub(from).Seconds() * float64(out.Devices)
	if window > 0 {
		out.AvailabilityPct = clampPct((window - out.DowntimeSec) / window * 100)
	} else {
		out.AvailabilityPct = 100
	}
	rows, err := s.Devices(ctx, from, to, mapID)
	if err == nil {
		filtered := make([]DeviceRow, 0, len(rows))
		for _, row := range rows {
			if row.DowntimeSec > 0 || row.Incidents > 0 {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].DowntimeSec == rows[j].DowntimeSec {
				return rows[i].Incidents > rows[j].Incidents
			}
			return rows[i].DowntimeSec > rows[j].DowntimeSec
		})
		if len(rows) > 8 {
			rows = rows[:8]
		}
		out.TopDown = rows
	}
	return out, nil
}

func (s Service) Devices(ctx context.Context, from, to time.Time, mapID string) ([]DeviceRow, error) {
	from, to, err := normalizeRange(from, to)
	if err != nil {
		return nil, err
	}
	mapArg := nullableMapID(mapID)
	scopeDevices, scopeMaps := s.scopeArgs()
	query := `
		SELECT d.id::text, d.name, host(d.primary_ip), d.status, d.device_type, COALESCE(d.tags,'{}'),
		       COALESCE(o.incidents,0), COALESCE(o.downtime,0),
		       COALESCE(d.root_cause_device_id::text,''), COALESCE(rc.name,'')
		FROM devices d
		LEFT JOIN devices rc ON rc.id=d.root_cause_device_id
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE o.started_at >= $1) AS incidents,
			       sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(o.ended_at,$2),$2) - GREATEST(o.started_at,$1)))) AS downtime
			FROM outages o
			WHERE o.device_id=d.id AND o.started_at < $2 AND (o.ended_at IS NULL OR o.ended_at > $1)
		) o ON TRUE
		WHERE ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id=$3::uuid))
		  AND ($4::uuid[] IS NULL OR d.id = ANY($4::uuid[]))
		  AND ($5::uuid[] IS NULL OR EXISTS (SELECT 1 FROM map_elements me2 WHERE me2.device_id=d.id AND me2.map_id = ANY($5::uuid[])))
		ORDER BY d.name
	`
	rows, err := s.Pool.Query(ctx, query, from, to, mapArg, scopeDevices, scopeMaps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	window := to.Sub(from).Seconds()
	var out []DeviceRow
	for rows.Next() {
		var row DeviceRow
		if rows.Scan(&row.DeviceID, &row.DeviceName, &row.PrimaryIP, &row.Status, &row.DeviceType,
			&row.Tags, &row.Incidents, &row.DowntimeSec, &row.RootCauseID, &row.RootCause) == nil {
			if window > 0 {
				row.AvailabilityPct = clampPct((window - row.DowntimeSec) / window * 100)
			} else {
				row.AvailabilityPct = 100
			}
			out = append(out, row)
		}
	}
	return out, rows.Err()
}

func (s Service) Clients(ctx context.Context, from, to time.Time, clientID string) ([]ClientRow, error) {
	from, to, err := normalizeRange(from, to)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, name, description, target_pct, device_ids, map_ids, tags
		FROM sla_clients WHERE ($1::text='' OR id = NULLIF($1,'')::uuid) ORDER BY name
	`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type client struct {
		id, name, description string
		target                float64
		devices, maps         []string
		tags                  []string
	}
	var clients []client
	for rows.Next() {
		var c client
		if rows.Scan(&c.id, &c.name, &c.description, &c.target, &c.devices, &c.maps, &c.tags) == nil {
			clients = append(clients, c)
		}
	}
	rows.Close()
	window := to.Sub(from).Seconds()
	out := make([]ClientRow, 0, len(clients))
	for _, c := range clients {
		deviceIDs, err := s.clientDevices(ctx, c.devices, c.maps, c.tags)
		if err != nil {
			return nil, err
		}
		devRows, err := s.deviceRowsByIDs(ctx, from, to, deviceIDs)
		if err != nil {
			return nil, err
		}
		row := ClientRow{ID: c.id, Name: c.name, Description: c.description, TargetPct: c.target,
			Devices: len(devRows)}
		for _, d := range devRows {
			row.Incidents += d.Incidents
			row.DowntimeSec += d.DowntimeSec
		}
		capacity := window * float64(len(devRows))
		if capacity > 0 {
			row.AvailabilityPct = clampPct((capacity - row.DowntimeSec) / capacity * 100)
		} else {
			row.AvailabilityPct = 100
		}
		row.Compliant = row.AvailabilityPct >= c.target
		out = append(out, row)
	}
	return out, nil
}

func (s Service) clientDevices(ctx context.Context, deviceIDs, mapIDs, tags []string) ([]string, error) {
	scopeDevices, scopeMaps := s.scopeArgs()
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT d.id::text
		FROM devices d
		WHERE (cardinality($1::uuid[])=0 OR d.id = ANY($1::uuid[]))
		  AND (cardinality($2::uuid[])=0 OR EXISTS (
		      SELECT 1 FROM map_elements me WHERE me.device_id=d.id AND me.map_id = ANY($2::uuid[])))
		  AND (cardinality($3::text[])=0 OR d.tags && $3::text[])
		  AND ($4::uuid[] IS NULL OR d.id = ANY($4::uuid[]))
		  AND ($5::uuid[] IS NULL OR EXISTS (
		      SELECT 1 FROM map_elements scoped_me WHERE scoped_me.device_id=d.id AND scoped_me.map_id = ANY($5::uuid[])))
		ORDER BY 1
	`, deviceIDs, mapIDs, tags, scopeDevices, scopeMaps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

func (s Service) deviceRowsByIDs(ctx context.Context, from, to time.Time, ids []string) ([]DeviceRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.id::text, d.name, host(d.primary_ip), d.status, d.device_type, COALESCE(d.tags,'{}'),
		       COALESCE(o.incidents,0), COALESCE(o.downtime,0),
		       COALESCE(d.root_cause_device_id::text,''), COALESCE(rc.name,'')
		FROM devices d
		LEFT JOIN devices rc ON rc.id=d.root_cause_device_id
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE o.started_at >= $1) AS incidents,
			       sum(EXTRACT(EPOCH FROM (LEAST(COALESCE(o.ended_at,$2),$2) - GREATEST(o.started_at,$1)))) AS downtime
			FROM outages o
			WHERE o.device_id=d.id AND o.started_at < $2 AND (o.ended_at IS NULL OR o.ended_at > $1)
		) o ON TRUE
		WHERE d.id = ANY($3::uuid[])
		ORDER BY d.name
	`, from, to, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	window := to.Sub(from).Seconds()
	var out []DeviceRow
	for rows.Next() {
		var row DeviceRow
		if rows.Scan(&row.DeviceID, &row.DeviceName, &row.PrimaryIP, &row.Status, &row.DeviceType,
			&row.Tags, &row.Incidents, &row.DowntimeSec, &row.RootCauseID, &row.RootCause) == nil {
			if window > 0 {
				row.AvailabilityPct = clampPct((window - row.DowntimeSec) / window * 100)
			}
			out = append(out, row)
		}
	}
	return out, rows.Err()
}

func (s Service) Interfaces(ctx context.Context, from, to time.Time, mapID string) ([]InterfaceRow, error) {
	from, to, err := normalizeRange(from, to)
	if err != nil {
		return nil, err
	}
	scopeDevices, scopeMaps := s.scopeArgs()
	rows, err := s.Pool.Query(ctx, `
		SELECT ml.id::text, ml.map_id::text, m.name, COALESCE(d.name,''),
		       COALESCE(NULLIF(ml.snmp_if_name,''), NULLIF(ml.label_template,''), 'Enlace ' || left(ml.id::text,8)),
		       ml.link_type, ml.status, COALESCE(ml.max_speed_bps,0),
		       count(lm.time), count(lm.time) FILTER (WHERE lm.oper_status=1),
		       COALESCE(avg(lm.rx_bps),0), COALESCE(avg(lm.tx_bps),0),
		       COALESCE(max(lm.in_errors + lm.out_errors),0)
		FROM map_links ml
		JOIN maps m ON m.id=ml.map_id
		LEFT JOIN devices d ON d.id=ml.master_device_id
		LEFT JOIN link_metrics lm ON lm.link_id=ml.id AND lm.time >= $1 AND lm.time < $2
		WHERE ($3::uuid IS NULL OR ml.map_id=$3::uuid)
		  AND ($4::uuid[] IS NULL OR ml.map_id = ANY($4::uuid[]))
		  AND ($5::uuid[] IS NULL OR (
		      EXISTS (
		          SELECT 1 FROM map_elements lime
		          WHERE lime.id IN (ml.source_element_id, ml.target_element_id)
		            AND lime.device_id = ANY($5::uuid[]))
		      AND NOT EXISTS (
		          SELECT 1 FROM map_elements lime2
		          WHERE lime2.id IN (ml.source_element_id, ml.target_element_id)
		            AND lime2.device_id IS NOT NULL
		            AND NOT (lime2.device_id = ANY($5::uuid[])))))
		GROUP BY ml.id, ml.map_id, m.name, d.name, ml.snmp_if_name, ml.label_template, ml.link_type,
		         ml.status, ml.max_speed_bps
		ORDER BY m.name, ml.link_type, ml.id
	`, from, to, nullableMapID(mapID), scopeMaps, scopeDevices)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InterfaceRow{}
	for rows.Next() {
		var row InterfaceRow
		if rows.Scan(&row.LinkID, &row.MapID, &row.MapName, &row.DeviceName, &row.InterfaceName,
			&row.LinkType, &row.LinkStatus, &row.MaxSpeedBps, &row.Samples, &row.UpSamples,
			&row.AvgRxBps, &row.AvgTxBps, &row.MaxErrors) == nil {
			if row.Samples > 0 {
				row.AvailabilityPct = clampPct(float64(row.UpSamples) / float64(row.Samples) * 100)
			} else {
				row.AvailabilityPct = -1
			}
			out = append(out, row)
		}
	}
	return out, rows.Err()
}

func (s Service) Monthly(ctx context.Context, months int, mapID string) ([]MonthlyRow, error) {
	if months <= 0 || months > 24 {
		months = 6
	}
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := make([]MonthlyRow, 0, months)
	var previous *MonthlyRow
	for i := months - 1; i >= 0; i-- {
		start := first.AddDate(0, -i, 0)
		end := start.AddDate(0, 1, 0)
		if end.After(now) {
			end = now
		}
		summary, err := s.Summary(ctx, start, end, mapID)
		if err != nil {
			return nil, err
		}
		row := MonthlyRow{
			Month: start.Format("2006-01"), From: start.Format(time.RFC3339),
			To: end.Format(time.RFC3339), Devices: summary.Devices, Incidents: summary.Incidents,
			DowntimeSec: summary.DowntimeSec, AvailabilityPct: summary.AvailabilityPct,
		}
		if previous != nil {
			row.DeltaPct = row.AvailabilityPct - previous.AvailabilityPct
			row.DeltaIncidents = row.Incidents - previous.Incidents
		}
		out = append(out, row)
		previous = &out[len(out)-1]
	}
	return out, nil
}

func clampPct(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s Service) Client(ctx context.Context, id string) (ClientRow, error) {
	rows, err := s.Clients(ctx, time.Time{}, time.Time{}, id)
	if err != nil {
		return ClientRow{}, err
	}
	if len(rows) == 0 {
		return ClientRow{}, fmt.Errorf("cliente no encontrado")
	}
	return rows[0], nil
}

// ParseRange parses optional RFC3339 query values.
func ParseRange(fromRaw, toRaw string) (time.Time, time.Time, error) {
	var from, to time.Time
	if strings.TrimSpace(fromRaw) != "" {
		t, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return from, to, fmt.Errorf("from inválido")
		}
		from = t
	}
	if strings.TrimSpace(toRaw) != "" {
		t, err := time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return from, to, fmt.Errorf("to inválido")
		}
		to = t
	}
	return normalizeRange(from, to)
}
