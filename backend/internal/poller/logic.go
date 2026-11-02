package poller

import (
	"context"
	"fmt"
	"strings"
)

// probeLogic aggregates the state of child services. By default it combines
// every other enabled service on the same device with AND semantics; set
// {"mode":"any"} for OR semantics. An optional service_ids array narrows the
// children to specific device_service UUIDs.
func (p *Poller) probeLogic(ctx context.Context, j job) (probeResult, error) {
	ids := stringSliceCfg(j.Config, "service_ids")
	if ids == nil {
		ids = []string{}
	}
	scope := strings.ToLower(strCfg(j.Config, "scope"))
	query := `
		SELECT COALESCE(pr.name, 'servicio'), ds.status
		FROM device_services ds
		JOIN probes pr ON pr.id = ds.probe_id
		WHERE ds.id <> $2
		  AND ds.is_enabled = TRUE
		  AND ds.status <> 'disabled'
		  AND (cardinality($3::uuid[]) > 0 OR ds.device_id = $1)
		  AND (cardinality($3::uuid[]) = 0 OR ds.id = ANY($3::uuid[]))
	`
	args := []any{j.DeviceID, j.ServiceID, ids}
	if len(ids) == 0 && (scope == "map" || scope == "submap") {
		mapID := strCfg(j.Config, "map_id")
		if mapID == "" {
			_ = p.pool.QueryRow(ctx, `SELECT map_id::text FROM map_elements WHERE device_id=$1 LIMIT 1`, j.DeviceID).Scan(&mapID)
		}
		if mapID != "" {
			if scope == "submap" {
				query = `
					WITH RECURSIVE tree AS (
						SELECT id FROM maps WHERE id=$1
						UNION ALL
						SELECT m.id FROM maps m JOIN tree t ON m.parent_map_id=t.id
					)
					SELECT COALESCE(pr.name,'servicio'), ds.status
					FROM device_services ds
					JOIN probes pr ON pr.id=ds.probe_id
					JOIN devices d ON d.id=ds.device_id
					JOIN map_elements me ON me.device_id=d.id
					WHERE ds.id <> $2 AND ds.is_enabled=TRUE AND ds.status <> 'disabled'
					  AND me.map_id IN (SELECT id FROM tree)
				`
			} else {
				query = `
					SELECT COALESCE(pr.name,'servicio'), ds.status
					FROM device_services ds
					JOIN probes pr ON pr.id=ds.probe_id
					JOIN devices d ON d.id=ds.device_id
					JOIN map_elements me ON me.device_id=d.id
					WHERE ds.id <> $2 AND ds.is_enabled=TRUE AND ds.status <> 'disabled'
					  AND me.map_id=$1
				`
			}
			args = []any{mapID, j.ServiceID}
		}
	}
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return probeResult{}, err
	}
	defer rows.Close()
	var labels, statuses []string
	for rows.Next() {
		var label, status string
		if err := rows.Scan(&label, &status); err != nil {
			return probeResult{}, err
		}
		labels = append(labels, label)
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return probeResult{}, err
	}
	if len(statuses) == 0 {
		return probeResult{OK: false, Message: "Sin servicios hijos que evaluar"}, nil
	}
	mode := strings.ToLower(strCfg(j.Config, "mode"))
	if mode == "" {
		mode = "all"
	}
	return aggregateLogic(labels, statuses, mode), nil
}

func aggregateLogic(labels, statuses []string, mode string) probeResult {
	up, warning, down := 0, 0, 0
	var failed []string
	for i, status := range statuses {
		switch status {
		case "up":
			up++
		case "warning":
			warning++
			if i < len(labels) {
				failed = append(failed, labels[i]+" (advertencia)")
			}
		default:
			down++
			if i < len(labels) {
				failed = append(failed, labels[i]+" ("+status+")")
			}
		}
	}
	total := len(statuses)
	pct := float64(up) * 100 / float64(total)
	detail := ""
	if len(failed) > 0 {
		detail = " · " + strings.Join(failed, ", ")
	}
	if mode == "any" {
		if up > 0 {
			return probeResult{OK: true, HasValue: true, Value: pct,
				Message: fmt.Sprintf("%d/%d servicios hijos operativos%s", up, total, detail)}
		}
		if warning > 0 {
			return probeResult{OK: true, Warning: true, HasValue: true, Value: pct,
				Message: fmt.Sprintf("Ningún hijo operativo; %d con advertencia%s", warning, detail)}
		}
		return probeResult{OK: false, Message: fmt.Sprintf("Ningún servicio hijo operativo%s", detail)}
	}
	if down > 0 {
		return probeResult{OK: false, Message: fmt.Sprintf("%d/%d servicios hijos caídos%s", down, total, detail)}
	}
	if warning > 0 {
		return probeResult{OK: true, Warning: true, HasValue: true, Value: pct,
			Message: fmt.Sprintf("%d/%d servicios hijos operativos; %d con advertencia%s", up, total, warning, detail)}
	}
	return probeResult{OK: true, HasValue: true, Value: pct,
		Message: fmt.Sprintf("%d/%d servicios hijos operativos", up, total)}
}
