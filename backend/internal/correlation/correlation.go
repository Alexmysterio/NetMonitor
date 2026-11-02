// Package correlation computes root-cause relationships from the explicit
// Parents graph and the L2 topology discovered from LLDP/CDP/MNDP/FDB. It is
// intentionally conservative: a device is only marked unreachable when there
// is a credible upstream failure, never merely because a neighbour is down.
package correlation

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Node is a device in a root-cause chain or impact list.
type Node struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	DeviceType   string   `json:"device_type"`
	Depth        int      `json:"depth"`
	Via          string   `json:"via"`
	RootCauseID  string   `json:"root_cause_id,omitempty"`
	RootCause    string   `json:"root_cause,omitempty"`
	MapIDs       []string `json:"map_ids,omitempty"`
	PrimaryIP    string   `json:"primary_ip,omitempty"`
	StatusReason string   `json:"status_reason,omitempty"`
}

// Resolution is the result of resolving one device against the topology.
type Resolution struct {
	Root      Node   `json:"root"`
	HasRoot   bool   `json:"has_root"`
	Via       string `json:"via"`
	Depth     int    `json:"depth"`
	RootCause string `json:"root_cause,omitempty"`
}

// Impact describes what a root failure affects on the maps.
type Impact struct {
	Root       Node     `json:"root"`
	Impacted   []Node   `json:"impacted"`
	Chain      []Node   `json:"chain"`
	MapIDs     []string `json:"map_ids"`
	ParentHops int      `json:"parent_hops"`
	L2Hops     int      `json:"l2_hops"`
}

type device struct {
	Node
	parentCount int
}

func loadDevice(ctx context.Context, pool *pgxpool.Pool, id string) (device, error) {
	var d device
	err := pool.QueryRow(ctx, `
		SELECT d.id::text, d.name, d.status, d.device_type, COALESCE(d.root_cause_device_id::text,''),
		       COALESCE(d.status_reason,''), host(d.primary_ip),
		       (SELECT count(*) FROM device_parents dp WHERE dp.device_id=d.id)
		FROM devices d WHERE d.id=$1
	`, id).Scan(&d.ID, &d.Name, &d.Status, &d.DeviceType, &d.RootCauseID, &d.StatusReason, &d.PrimaryIP, &d.parentCount)
	if err != nil {
		return d, fmt.Errorf("dispositivo no encontrado: %w", err)
	}
	d.MapIDs = deviceMaps(ctx, pool, id)
	return d, nil
}

func deviceMaps(ctx context.Context, pool *pgxpool.Pool, id string) []string {
	rows, err := pool.Query(ctx, `SELECT DISTINCT map_id::text FROM map_elements WHERE device_id=$1`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var mapID string
		if rows.Scan(&mapID) == nil {
			out = append(out, mapID)
		}
	}
	return out
}

func parentAncestors(ctx context.Context, pool *pgxpool.Pool, id string) ([]Node, error) {
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT dp.parent_device_id AS id, 1 AS depth, ARRAY[dp.device_id, dp.parent_device_id] AS path
			FROM device_parents dp WHERE dp.device_id=$1
			UNION ALL
			SELECT dp.parent_device_id, u.depth+1, u.path || dp.parent_device_id
			FROM up u JOIN device_parents dp ON dp.device_id=u.id
			WHERE u.depth < 32 AND NOT dp.parent_device_id = ANY(u.path)
		)
		SELECT u.id::text, d.name, d.status, d.device_type, u.depth,
		       COALESCE(d.root_cause_device_id::text,''), COALESCE(d.status_reason,''), host(d.primary_ip)
		FROM up u JOIN devices d ON d.id=u.id
		ORDER BY u.depth, d.name
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if rows.Scan(&n.ID, &n.Name, &n.Status, &n.DeviceType, &n.Depth, &n.RootCauseID, &n.StatusReason, &n.PrimaryIP) == nil {
			n.Via = "parents"
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

func l2Neighbours(ctx context.Context, pool *pgxpool.Pool, id string) ([]Node, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT d.id::text, d.name, d.status, d.device_type, host(d.primary_ip),
		       COALESCE(d.root_cause_device_id::text,''), COALESCE(d.status_reason,'')
		FROM map_links ml
		JOIN map_elements a ON a.id=ml.source_element_id
		JOIN map_elements b ON b.id=ml.target_element_id
		JOIN devices d ON d.id = CASE WHEN a.device_id=$1 THEN b.device_id ELSE a.device_id END
		WHERE (a.device_id=$1 OR b.device_id=$1) AND a.device_id IS NOT NULL AND b.device_id IS NOT NULL
		  AND lower(ml.link_type) NOT IN ('tunnel','vpn','logical','gre','ipsec')
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if rows.Scan(&n.ID, &n.Name, &n.Status, &n.DeviceType, &n.PrimaryIP, &n.RootCauseID, &n.StatusReason) == nil {
			n.Via = "l2"
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// Component returns the physical L2 component around a device, including the
// device itself. It walks map_links, so it works with links created by the LLDP
// and bridge-FDB discovery.
func Component(ctx context.Context, pool *pgxpool.Pool, id string) (map[string]device, error) {
	seen := map[string]device{}
	root, err := loadDevice(ctx, pool, id)
	if err != nil {
		return nil, err
	}
	seen[id] = root
	frontier := []string{id}
	for depth := 0; depth < 10 && len(frontier) > 0 && len(seen) < 512; depth++ {
		var next []string
		for _, current := range frontier {
			neighbours, err := l2Neighbours(ctx, pool, current)
			if err != nil {
				return nil, err
			}
			for _, n := range neighbours {
				if _, ok := seen[n.ID]; ok {
					continue
				}
				d := device{Node: n}
				d.MapIDs = deviceMaps(ctx, pool, n.ID)
				if c, err := loadDevice(ctx, pool, n.ID); err == nil {
					d.parentCount = c.parentCount
				}
				seen[n.ID] = d
				next = append(next, n.ID)
			}
		}
		frontier = next
	}
	return seen, nil
}

// Resolve finds the upstream root that explains a down device. Parents are
// authoritative. When there is no Parent relationship, the L2 component is
// used but only when a router/switch/firewall (or AP) has a credible
// downstream footprint and at least two affected neighbours.
func Resolve(ctx context.Context, pool *pgxpool.Pool, id string) (Resolution, error) {
	return ResolveWithStatus(ctx, pool, id, "")
}

// ResolveWithStatus is used by the poller before it persists the new device
// status. Without the override, the first transition to down would still see
// the old "up" row and miss the root cause for one polling cycle.
func ResolveWithStatus(ctx context.Context, pool *pgxpool.Pool, id, statusOverride string) (Resolution, error) {
	dev, err := loadDevice(ctx, pool, id)
	if err != nil {
		return Resolution{}, err
	}
	if statusOverride != "" {
		dev.Status = statusOverride
	}
	if dev.Status == "up" || dev.Status == "warning" || dev.Status == "disabled" || dev.Status == "unknown" {
		return Resolution{}, nil
	}
	ancestors, err := parentAncestors(ctx, pool, id)
	if err != nil {
		return Resolution{}, err
	}
	for _, a := range ancestors {
		if a.Status == "down" {
			return Resolution{Root: a, HasRoot: true, Via: "parents", Depth: a.Depth}, nil
		}
		if a.Status == "unreachable" && a.RootCauseID != "" {
			root, err := loadDevice(ctx, pool, a.RootCauseID)
			if err == nil && root.Status == "down" {
				root.Depth = a.Depth
				root.Via = "parents"
				return Resolution{Root: root.Node, HasRoot: true, Via: "parents", Depth: a.Depth}, nil
			}
		}
	}

	component, err := Component(ctx, pool, id)
	if err != nil || len(component) < 2 {
		return Resolution{}, nil
	}
	type candidate struct {
		d      device
		degree int
		down   int
	}
	var candidates []candidate
	for did, d := range component {
		if did == id || (d.Status != "down" && d.Status != "unreachable") {
			continue
		}
		neighbours, _ := l2Neighbours(ctx, pool, did)
		degree := 0
		downNeighbours := 0
		for _, n := range neighbours {
			if _, ok := component[n.ID]; !ok {
				continue
			}
			degree++
			status := component[n.ID].Status
			if status == "down" || status == "unreachable" {
				downNeighbours++
			}
		}
		if rank(d.DeviceType) > 2 || downNeighbours < 2 {
			continue
		}
		candidates = append(candidates, candidate{d: d, degree: degree, down: downNeighbours})
	}
	if len(candidates) == 0 {
		return Resolution{}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		ri, rj := rank(candidates[i].d.DeviceType), rank(candidates[j].d.DeviceType)
		if ri != rj {
			return ri < rj
		}
		if candidates[i].down != candidates[j].down {
			return candidates[i].down > candidates[j].down
		}
		if candidates[i].degree != candidates[j].degree {
			return candidates[i].degree > candidates[j].degree
		}
		return candidates[i].d.ID < candidates[j].d.ID
	})
	best := candidates[0]
	if best.d.Status == "unreachable" && best.d.RootCauseID != "" {
		if root, err := loadDevice(ctx, pool, best.d.RootCauseID); err == nil && root.Status == "down" {
			root.Via = "l2"
			return Resolution{Root: root.Node, HasRoot: true, Via: "l2", Depth: 1}, nil
		}
	}
	return Resolution{Root: best.d.Node, HasRoot: true, Via: "l2", Depth: 1}, nil
}

func rank(deviceType string) int {
	switch strings.ToLower(deviceType) {
	case "router", "firewall", "core":
		return 0
	case "switch":
		return 1
	case "access_point", "wifi", "wireless":
		return 2
	default:
		return 3
	}
}

// Apply records a root cause on the device and its active outage. It is called
// by the poller after a device becomes unreachable.
func Apply(ctx context.Context, pool *pgxpool.Pool, deviceID, rootID string, depth int) error {
	_, err := pool.Exec(ctx, `
		UPDATE devices SET root_cause_device_id=$2, correlation_checked_at=NOW(), updated_at=NOW()
		WHERE id=$1
	`, deviceID, nullable(rootID))
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE outages SET root_cause_device_id=$2, correlation_depth=$3, suppress_notifications=TRUE
		WHERE device_id=$1 AND ended_at IS NULL
	`, deviceID, nullable(rootID), depth)
	return err
}

// Clear removes a stale root cause when a device recovers or becomes a root.
func Clear(ctx context.Context, pool *pgxpool.Pool, deviceID string) error {
	_, err := pool.Exec(ctx, `
		UPDATE devices SET root_cause_device_id=NULL, correlation_checked_at=NOW(), updated_at=NOW()
		WHERE id=$1
	`, deviceID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE outages SET root_cause_device_id=NULL, correlation_depth=0, suppress_notifications=FALSE
		WHERE device_id=$1 AND ended_at IS NULL
	`, deviceID)
	return err
}

// Dependents returns devices that should be re-evaluated when rootID changes:
// explicit children (recursively) plus down devices in the same L2 component.
func Dependents(ctx context.Context, pool *pgxpool.Pool, rootID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE down AS (
			SELECT device_id FROM device_parents WHERE parent_device_id=$1
			UNION
			SELECT dp.device_id FROM device_parents dp JOIN down d ON dp.parent_device_id=d.device_id
		)
		SELECT DISTINCT device_id::text FROM down WHERE device_id <> $1
	`, rootID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{rootID: true}
	var out []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	rows.Close()
	component, err := Component(ctx, pool, rootID)
	if err != nil {
		return out, nil
	}
	for id, d := range component {
		if id == rootID || seen[id] {
			continue
		}
		if d.Status == "down" || d.Status == "unreachable" {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// BuildImpact returns the map-facing impact of a root failure.
func BuildImpact(ctx context.Context, pool *pgxpool.Pool, rootID string) (Impact, error) {
	root, err := loadDevice(ctx, pool, rootID)
	if err != nil {
		return Impact{}, err
	}
	impact := Impact{Root: root.Node, Impacted: []Node{}}
	seen := map[string]bool{rootID: true}

	ancestors, _ := parentAncestors(ctx, pool, rootID)
	_ = ancestors

	rows, err := pool.Query(ctx, `
		WITH RECURSIVE down AS (
			SELECT dp.device_id AS id, 1 AS depth, 'parents'::text AS via,
			       ARRAY[$1::uuid, dp.device_id] AS path
			FROM device_parents dp WHERE dp.parent_device_id=$1
			UNION ALL
			SELECT dp.device_id, d.depth+1, 'parents',
			       d.path || dp.device_id
			FROM down d JOIN device_parents dp ON dp.parent_device_id=d.id
			WHERE d.depth < 32 AND NOT dp.device_id = ANY(d.path)
		)
		SELECT d.id::text, dev.name, dev.status, dev.device_type, d.depth, d.via,
		       host(dev.primary_ip), COALESCE(dev.status_reason,'')
		FROM down d JOIN devices dev ON dev.id=d.id
		ORDER BY d.depth, dev.name
	`, rootID)
	if err != nil {
		return Impact{}, err
	}
	for rows.Next() {
		var n Node
		if rows.Scan(&n.ID, &n.Name, &n.Status, &n.DeviceType, &n.Depth, &n.Via, &n.PrimaryIP, &n.StatusReason) == nil {
			if !seen[n.ID] {
				seen[n.ID] = true
				n.MapIDs = deviceMaps(ctx, pool, n.ID)
				impact.Impacted = append(impact.Impacted, n)
				impact.ParentHops = maxInt(impact.ParentHops, n.Depth)
			}
		}
	}
	rows.Close()

	component, err := Component(ctx, pool, rootID)
	if err == nil {
		for id, d := range component {
			if seen[id] || (d.Status != "down" && d.Status != "unreachable") {
				continue
			}
			n := d.Node
			n.Depth = 1
			n.Via = "l2"
			n.MapIDs = d.MapIDs
			seen[id] = true
			impact.Impacted = append(impact.Impacted, n)
			impact.L2Hops = 1
		}
	}
	sort.Slice(impact.Impacted, func(i, j int) bool {
		if impact.Impacted[i].Depth == impact.Impacted[j].Depth {
			return impact.Impacted[i].Name < impact.Impacted[j].Name
		}
		return impact.Impacted[i].Depth < impact.Impacted[j].Depth
	})
	impact.MapIDs = root.MapIDs
	impact.Chain = append([]Node{root.Node}, ancestors...)
	return impact, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
