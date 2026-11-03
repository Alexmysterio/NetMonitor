package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/netmonitor/netmonitor/internal/agents"
)

type scheduledNetwork struct {
	ID             string
	Name           string
	CIDR           string
	AddressListID  string
	MapID          string
	AgentID        string
	SNMPProfileID  string
	ProbeIDs       []string
	IntervalSec    int
	Mode           string
	NamePreference string
	AutoCreate     bool
}

func (s *Server) StartBackground(ctx context.Context) {
	s.startReportScheduler(ctx)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDueNetworks(ctx)
			}
		}
	}()
}

func (s *Server) runDueNetworks(ctx context.Context) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(982451653)`).Scan(&locked); err != nil || !locked {
		return
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM networks
		WHERE is_enabled=TRUE AND next_scan_at <= NOW()
		ORDER BY next_scan_at LIMIT 5
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE networks SET next_scan_at=NOW()+interval '1 minute' WHERE id=$1`, id); err != nil {
			continue
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return
	}
	for _, id := range ids {
		scanCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, err := s.runNetworkScan(scanCtx, id, "scheduler")
		cancel()
		if err != nil {
			log.Printf("scheduled discovery %s: %v", id, err)
		}
	}
}

func (s *Server) loadScheduledNetwork(ctx context.Context, id string) (scheduledNetwork, error) {
	var n scheduledNetwork
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(cidr::text,''), COALESCE(address_list_id::text,''),
		       COALESCE(map_id::text,''), COALESCE(agent_id::text,''), COALESCE(snmp_profile_id::text,''),
		       probe_ids, interval_sec, mode, name_preference, auto_create
		FROM networks WHERE id=$1
	`, id).Scan(&n.ID, &n.Name, &n.CIDR, &n.AddressListID, &n.MapID, &n.AgentID,
		&n.SNMPProfileID, &n.ProbeIDs, &n.IntervalSec, &n.Mode, &n.NamePreference, &n.AutoCreate)
	return n, err
}

func (s *Server) networkCIDRs(ctx context.Context, n scheduledNetwork) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(raw string) {
		_, ipnet, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return
		}
		if ipnet.IP.To4() == nil {
			return
		}
		value := ipnet.String()
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if n.CIDR != "" {
		add(n.CIDR)
	}
	if n.AddressListID != "" {
		rows, err := s.pool.Query(ctx, `SELECT value::text FROM address_list_entries WHERE list_id=$1`, n.AddressListID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) == nil {
				add(raw)
			}
		}
		rows.Close()
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("la red no tiene CIDR ni entradas de lista utilizables")
	}
	return out, nil
}

func (s *Server) runNetworkScan(ctx context.Context, id, trigger string) (map[string]any, error) {
	n, err := s.loadScheduledNetwork(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("red no encontrada")
	}
	cidrs, err := s.networkCIDRs(ctx, n)
	if err != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE networks SET last_status='error', last_error=$2, last_scan_at=NOW(),
			next_scan_at=NOW()+($3::int * interval '1 second') WHERE id=$1`, id, err.Error(), n.IntervalSec)
		return nil, err
	}
	ips := []string{}
	seen := map[string]bool{}
	total := uint64(0)
	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		ones, bits := network.Mask.Size()
		if bits != 32 {
			continue
		}
		size := uint64(1) << uint(bits-ones)
		if total+size > 1024 {
			return nil, fmt.Errorf("el rango supera 1024 direcciones; divide la red o usa listas")
		}
		total += size
		for _, ip := range enumerateScanCIDR(network) {
			if !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("el rango no contiene direcciones utilizables")
	}
	if len(ips) > 1024 {
		return nil, fmt.Errorf("el rango supera 1024 direcciones; divide la red o usa listas")
	}
	sort.Strings(ips)

	var agentArg any
	if n.AgentID != "" {
		agentArg = n.AgentID
	}
	var runID string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO network_scan_runs (network_id, status, mode, agent_id)
		VALUES ($1,'running',$2,$3) RETURNING id
	`, id, n.Mode, agentArg).Scan(&runID)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE networks SET last_status='running', last_error='', last_scan_at=NOW() WHERE id=$1`, id)

	var profile *snmpProfileRow
	if n.SNMPProfileID != "" {
		if p, err := s.profileByID(ctx, n.SNMPProfileID); err == nil {
			profile = &p
		}
	}

	started := time.Now()
	var results []discoveryResult
	if n.AgentID != "" {
		results, err = s.scanViaAgent(ctx, n, profile, cidrs, len(ips))
	} else {
		results, err = scanCIDRsCore(ctx, ips, profile, n.Mode)
	}
	if err != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE network_scan_runs SET status='error', finished_at=NOW(), error=$2 WHERE id=$1`, runID, err.Error())
		_, _ = s.pool.Exec(ctx, `UPDATE networks SET last_status='error', last_error=$2,
			next_scan_at=NOW()+($3::int * interval '1 second') WHERE id=$1`, id, err.Error(), n.IntervalSec)
		return map[string]any{"id": runID, "status": "error", "error": err.Error()}, err
	}
	results = s.enrichDiscoveryResults(ctx, results)
	raw, _ := json.Marshal(results)
	scanned := len(ips)
	alive := len(results)
	_, err = s.pool.Exec(ctx, `
		UPDATE network_scan_runs SET status='done', finished_at=NOW(), scanned_count=$2,
		       alive_count=$3, results=$4::jsonb WHERE id=$1
	`, runID, scanned, alive, string(raw))
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE networks SET last_status='ok', last_error='',
		next_scan_at=NOW()+($2::int * interval '1 second') WHERE id=$1`, id, n.IntervalSec)

	created := 0
	if n.AutoCreate {
		created, _ = s.applyDiscoveredHosts(ctx, id, runID, nil)
	}
	log.Printf("discovery %s (%s): %d/%d vivos, %d creados en %s", n.Name, n.Mode, alive, scanned, created, time.Since(started).Round(time.Millisecond))
	return map[string]any{
		"id": runID, "network_id": id, "status": "done", "mode": n.Mode, "trigger": trigger,
		"scanned_count": scanned, "alive_count": alive, "created_count": created,
		"results": results, "started_at": started, "finished_at": time.Now(),
	}, nil
}

func scanCIDRsCore(ctx context.Context, ips []string, profile *snmpProfileRow, mode string) ([]discoveryResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	results := make([]discoveryResult, 0, len(ips))
	var profiles []snmpProfileRow
	if profile != nil {
		profiles = []snmpProfileRow{*profile}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 48)
spawn:
	for _, ip := range ips {
		select {
		case <-scanCtx.Done():
			break spawn
		default:
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := probeHost(scanCtx, ip, profiles, mode)
			if res.Alive {
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		return ipLess(results[i].IP, results[j].IP)
	})
	return results, nil
}

func enumerateScanCIDR(network *net.IPNet) []string {
	start := binary.BigEndian.Uint32(network.IP.To4())
	ones, bits := network.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	out := []string{}
	first, last := uint32(1), size-1
	if ones >= 31 {
		first, last = 0, size
	}
	for i := first; i < last; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, start+i)
		out = append(out, ip.String())
	}
	return out
}

func ipLess(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia == nil || ib == nil {
		return a < b
	}
	return strings.Compare(string(ia.To16()), string(ib.To16())) < 0
}

func (s *Server) scanViaAgent(ctx context.Context, n scheduledNetwork, profile *snmpProfileRow, cidrs []string, expected int) ([]discoveryResult, error) {
	if s.agentMgr == nil || !s.agentMgr.Online(n.AgentID) {
		return nil, fmt.Errorf("el agente de descubrimiento está offline")
	}
	snmpCfg := agents.SNMPConfig{}
	if profile != nil {
		snmpCfg = agents.SNMPConfig{
			Version: profile.Version, Port: profile.Port, Community: profile.Community,
			User: profile.V3User, Security: profile.V3SecLevel, AuthProto: profile.V3AuthProto,
			AuthPass: profile.V3AuthPass, PrivProto: profile.V3PrivProto, PrivPass: profile.V3PrivPass,
		}
	}
	job := agents.Job{
		ID: n.ID, DeviceID: "discovery", ProbeType: "discovery",
		IP: strings.Join(cidrs, ","), TimeoutMS: 22000,
		Config: map[string]any{
			"cidr": strings.Join(cidrs, ","), "mode": n.Mode,
			"name_preference": n.NamePreference, "max_hosts": expected,
		},
		SNMP: snmpCfg,
	}
	res, err := s.agentMgr.Execute(ctx, n.AgentID, job)
	if err != nil {
		return nil, fmt.Errorf("agente: %w", err)
	}
	if !res.OK {
		return nil, fmt.Errorf("agente: %s", res.Message)
	}
	raw, _ := json.Marshal(res.Data["hosts"])
	var hosts []discoveryResult
	if err := json.Unmarshal(raw, &hosts); err != nil {
		return nil, fmt.Errorf("respuesta del agente inválida")
	}
	out := hosts[:0]
	for _, h := range hosts {
		if h.Alive && h.IP != "" {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return ipLess(out[i].IP, out[j].IP) })
	return out, nil
}

func (s *Server) enrichDiscoveryResults(ctx context.Context, results []discoveryResult) []discoveryResult {
	if len(results) == 0 {
		return results
	}
	ips := make([]string, 0, len(results))
	for _, r := range results {
		ips = append(ips, r.IP)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT host(primary_ip), id::text FROM devices WHERE primary_ip = ANY($1::inet[])
	`, ips)
	if err != nil {
		return results
	}
	defer rows.Close()
	byIP := map[string]string{}
	for rows.Next() {
		var ip, id string
		if rows.Scan(&ip, &id) == nil {
			byIP[ip] = id
		}
	}
	for i := range results {
		results[i].ExistingDevice = byIP[results[i].IP]
	}
	return results
}

func (s *Server) applyDiscoveredHosts(ctx context.Context, networkID, runID string, selected []string) (int, error) {
	n, err := s.loadScheduledNetwork(ctx, networkID)
	if err != nil {
		return 0, err
	}
	query := `SELECT results::text FROM network_scan_runs WHERE id=$1 AND network_id=$2`
	var raw string
	if err := s.pool.QueryRow(ctx, query, runID, networkID).Scan(&raw); err != nil {
		return 0, fmt.Errorf("ejecución no encontrada")
	}
	var hosts []discoveryResult
	_ = json.Unmarshal([]byte(raw), &hosts)
	wanted := map[string]bool{}
	for _, ip := range selected {
		wanted[strings.TrimSpace(ip)] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	created := 0
	for _, host := range hosts {
		if !host.Alive || (len(wanted) > 0 && !wanted[host.IP]) {
			continue
		}
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE host(primary_ip)=$1 LIMIT 1`, host.IP).Scan(&existing); err == nil && existing != "" {
			continue
		}
		name := strings.TrimSpace(host.Name)
		if name == "" {
			name = host.IP
		}
		var profile, agent any
		if n.SNMPProfileID != "" {
			profile = n.SNMPProfileID
		}
		if n.AgentID != "" {
			agent = n.AgentID
		}
		var deviceID string
		err := tx.QueryRow(ctx, `
			INSERT INTO devices (name, primary_ip, device_type, snmp_profile_id, agent_id, tags, notes, status)
			VALUES ($1,$2::inet,'generic',$3,$4,ARRAY['descubierto'],$5,'unknown') RETURNING id
		`, name, host.IP, profile, agent, "Descubierto por "+n.Name).Scan(&deviceID)
		if err != nil {
			continue
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO device_services (device_id, probe_id, custom_name, interval_sec, timeout_ms, down_retries, up_retries)
			SELECT $1, id, 'Ping', default_interval_sec, default_timeout_ms, default_down_retries, default_up_retries
			FROM probes WHERE name='Ping' LIMIT 1 ON CONFLICT DO NOTHING
		`, deviceID)
		if len(n.ProbeIDs) > 0 {
			_, _ = tx.Exec(ctx, `
				INSERT INTO device_services (device_id, probe_id, custom_name, interval_sec, timeout_ms, down_retries, up_retries)
				SELECT $1, p.id, p.name, p.default_interval_sec, p.default_timeout_ms, p.default_down_retries, p.default_up_retries
				FROM probes p WHERE p.id = ANY($2::uuid[]) ON CONFLICT DO NOTHING
			`, deviceID, n.ProbeIDs)
		}
		if n.MapID != "" {
			var count int
			var label, shape string
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM map_elements WHERE map_id=$1`, n.MapID).Scan(&count)
			_ = tx.QueryRow(ctx, `SELECT default_device_label, default_device_shape FROM maps WHERE id=$1`, n.MapID).Scan(&label, &shape)
			posX := 80 + (count%4)*240
			posY := 80 + (count/4)*150
			if shape == "" {
				shape = "circle"
			}
			if label == "" {
				label = "[Device.Name]\n[Device.FirstAddress]"
			}
			_, _ = tx.Exec(ctx, `
				INSERT INTO map_elements (map_id, element_type, device_id, pos_x, pos_y, width, height,
				  shape, icon_name, show_icon, label_template, z_index, custom_style)
				SELECT $1,'device',$2,$3,$4,190,96,$5,
				       COALESCE((SELECT icon FROM device_types WHERE name='generic'),'server'),
				       TRUE,$6,10,'{}'
				WHERE NOT EXISTS (SELECT 1 FROM map_elements WHERE map_id=$1 AND device_id=$2)
			`, n.MapID, deviceID, posX, posY, shape, label)
		}
		created++
	}
	if err := tx.Commit(ctx); err != nil {
		return created, err
	}
	if created > 0 {
		_, _ = s.pool.Exec(ctx, `UPDATE network_scan_runs SET created_count=created_count+$2 WHERE id=$1`, runID, created)
	}
	return created, nil
}
