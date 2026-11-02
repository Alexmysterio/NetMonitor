package poller

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/config"
	"github.com/netmonitor/netmonitor/internal/correlation"
	"github.com/netmonitor/netmonitor/internal/dsl"
	"github.com/netmonitor/netmonitor/internal/secure"
	"github.com/netmonitor/netmonitor/internal/state"
	"github.com/netmonitor/netmonitor/internal/ws"
)

type Poller struct {
	pool        *pgxpool.Pool
	store       *state.Store
	hub         *ws.Hub
	agentMgr    *agents.Manager
	cfg         config.Config
	jobs        chan job
	wg          sync.WaitGroup
	stopCh      chan struct{}
	running     atomic.Bool
	alerts      alertLimiter
	groupMu     sync.Mutex
	groups      map[string]*groupedAlert
	ctx         context.Context
	holidayMu   sync.RWMutex
	holidays    map[string]bool
	recheckMu   sync.Mutex
	rechecking  map[string]bool
	deviceLocks sync.Map

	funcMu    sync.RWMutex
	functions map[string]string
	funcAt    time.Time
}

type job struct {
	ServiceID       string
	DeviceID        string
	DeviceName      string
	IP              string
	ProbeName       string
	ProbeType       string
	Config          map[string]any
	AvailableExpr   string
	WarningExpr     string
	ErrorExpr       string
	ValueExpr       string
	NotifyEnabled   bool
	Unit            string
	Interval        int
	Timeout         int
	DownRetries     int
	UpRetries       int
	Status          string
	RetryCount      int
	UpCount         int
	IsCritical      bool
	AffectsDevice   bool
	SNMPProfileID   *string
	AgentID         *string
	SNMPVersion     string
	SNMPPort        int
	SNMPCommunity   string
	SNMPv3User      string
	SNMPv3SecLevel  string
	SNMPv3AuthProto string
	SNMPv3AuthPass  string
	SNMPv3PrivProto string
	SNMPv3PrivPass  string
	IsAcknowledged  bool
}

type probeResult struct {
	OK        bool
	Warning   bool
	HasValue  bool
	Value     float64
	RTT       float64
	Loss      float64
	Message   string
	OIDs      map[string]any
	Columns   map[string][]float64
	Rates     map[string]float64
	StatusRaw string
}

func New(pool *pgxpool.Pool, store *state.Store, hub *ws.Hub, cfg config.Config) *Poller {
	p := &Poller{
		pool:       pool,
		store:      store,
		hub:        hub,
		cfg:        cfg,
		jobs:       make(chan job, 4096),
		stopCh:     make(chan struct{}),
		functions:  map[string]string{},
		alerts:     alertLimiter{limit: cfg.AlertMaxPerMin},
		groups:     map[string]*groupedAlert{},
		holidays:   map[string]bool{},
		rechecking: map[string]bool{},
	}
	return p
}

func (p *Poller) SetAgents(mgr *agents.Manager) { p.agentMgr = mgr }

func (p *Poller) Start(ctx context.Context) {
	p.ctx = ctx
	workers := p.cfg.PollerWorkers
	if workers < 4 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}
	go p.scheduleLoop(ctx)
	go p.linkLoop(ctx)
	go p.digestLoop(ctx)
	go p.holidayLoop(ctx)
	go p.ackExpiryLoop(ctx)
	log.Printf("poller started with %d workers", workers)
}

func (p *Poller) ackExpiryLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			_, _ = p.pool.Exec(ctx, `
				UPDATE outages SET acknowledged=FALSE, acknowledged_by=NULL, acknowledged_at=NULL
				WHERE ended_at IS NULL AND device_id IN (
					SELECT id FROM devices WHERE is_acknowledged=TRUE AND ack_expires_at IS NOT NULL AND ack_expires_at <= NOW()
				)
			`)
			_, _ = p.pool.Exec(ctx, `
				UPDATE devices SET is_acknowledged=FALSE, ack_by=NULL, ack_note=NULL, ack_expires_at=NULL, updated_at=NOW()
				WHERE is_acknowledged=TRUE AND ack_expires_at IS NOT NULL AND ack_expires_at <= NOW()
			`)
		}
	}
}

func (p *Poller) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

func (p *Poller) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			if !p.running.CompareAndSwap(false, true) {
				continue
			}
			go func() {
				defer p.running.Store(false)
				if err := p.dispatchDue(ctx); err != nil {
					log.Printf("poller dispatch: %v", err)
				}
			}()
		}
	}
}

func (p *Poller) dispatchDue(ctx context.Context) error {
	rows, err := p.pool.Query(ctx, `
		SELECT q.service_id, q.device_id, q.device_name, q.primary_ip, q.probe_name, q.probe_type, q.config,
		       q.available_expression, q.warning_expression, q.error_expression, q.value_expression, q.unit,
		       q.eff_interval, q.timeout_ms, q.eff_retries, q.up_retries,
		       q.status, q.current_retry_count, q.current_up_count, q.is_critical, q.affects_device_status,
		       q.snmp_profile_id, q.agent_id::text, q.is_acknowledged, q.overrides,
		       q.snmp_version, q.snmp_port, q.snmp_community,
		       q.snmp_v3_user, q.snmp_v3_sec_level, q.snmp_v3_auth_proto,
		       q.snmp_v3_auth_pass, q.snmp_v3_priv_proto, q.snmp_v3_priv_pass,
		       q.eff_notify
		FROM (
			SELECT ds.id AS service_id, d.id AS device_id, d.name AS device_name, host(d.primary_ip) AS primary_ip,
			       p.name AS probe_name, p.probe_type, p.config::text AS config,
			       p.available_expression, p.warning_expression, p.error_expression, p.value_expression, p.unit,
			       COALESCE(d.poll_interval_sec, gp.poll_interval_sec, mp.poll_interval_sec, ds.interval_sec) AS eff_interval,
			       ds.timeout_ms,
			       COALESCE(d.poll_down_retries, gp.poll_down_retries, mp.poll_down_retries, ds.down_retries) AS eff_retries,
			       ds.up_retries, ds.status, ds.current_retry_count, ds.current_up_count, ds.is_critical, ds.affects_device_status,
			       d.snmp_profile_id,
			       COALESCE(d.poll_agent_id, gp.poll_agent_id, mp.poll_agent_id, d.agent_id) AS agent_id,
			       d.is_acknowledged, ds.config_overrides::text AS overrides,
			       COALESCE(sp.version, 'v2c') AS snmp_version, COALESCE(sp.port, 161) AS snmp_port,
			       COALESCE(sp.community, 'public') AS snmp_community,
			       COALESCE(sp.v3_username, '') AS snmp_v3_user, COALESCE(sp.v3_security_level, '') AS snmp_v3_sec_level,
			       COALESCE(sp.v3_auth_protocol, '') AS snmp_v3_auth_proto, COALESCE(sp.v3_auth_pass, '') AS snmp_v3_auth_pass,
			       COALESCE(sp.v3_priv_protocol, '') AS snmp_v3_priv_proto, COALESCE(sp.v3_priv_pass, '') AS snmp_v3_priv_pass,
			       COALESCE(d.poll_notifications_enabled, gp.poll_notifications_enabled, mp.poll_notifications_enabled, TRUE) AS eff_notify,
			       ds.last_check_at
			FROM device_services ds
			JOIN devices d ON d.id = ds.device_id
			JOIN probes p ON p.id = ds.probe_id
			LEFT JOIN snmp_profiles sp ON sp.id = d.snmp_profile_id
			LEFT JOIN LATERAL (
				SELECT g.poll_interval_sec, g.poll_down_retries, g.poll_agent_id, g.poll_notifications_enabled
				FROM device_group_members gm JOIN device_groups g ON g.id=gm.group_id
				WHERE gm.device_id=d.id
				  AND (g.poll_interval_sec IS NOT NULL OR g.poll_down_retries IS NOT NULL
				       OR g.poll_agent_id IS NOT NULL OR g.poll_notifications_enabled IS NOT NULL)
				ORDER BY (g.poll_interval_sec IS NOT NULL) DESC, g.poll_interval_sec NULLS LAST, g.name
				LIMIT 1
			) gp ON TRUE
			LEFT JOIN LATERAL (
				SELECT m.poll_interval_sec, m.poll_down_retries, m.poll_agent_id, m.poll_notifications_enabled
				FROM map_elements me JOIN maps m ON m.id=me.map_id
				WHERE me.device_id=d.id
				  AND (m.poll_interval_sec IS NOT NULL OR m.poll_down_retries IS NOT NULL
				       OR m.poll_agent_id IS NOT NULL OR m.poll_notifications_enabled IS NOT NULL)
				ORDER BY (m.poll_interval_sec IS NOT NULL) DESC, m.poll_interval_sec ASC NULLS LAST, m.name
				LIMIT 1
			) mp ON TRUE
			WHERE ds.is_enabled = TRUE AND d.is_monitoring_enabled = TRUE
			  AND NOT EXISTS (
			      SELECT 1 FROM poll_in_flight f
			      WHERE f.service_id=ds.id AND f.expires_at > NOW()
			  )
		) q
		WHERE q.last_check_at IS NULL OR q.last_check_at + (q.eff_interval * interval '1 second') <= NOW()
		ORDER BY q.last_check_at NULLS FIRST
		LIMIT 400
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var jobs []job
	var ids []string
	for rows.Next() {
		var j job
		var cfgJSON, overrides string
		if err := rows.Scan(
			&j.ServiceID, &j.DeviceID, &j.DeviceName, &j.IP, &j.ProbeName, &j.ProbeType, &cfgJSON,
			&j.AvailableExpr, &j.WarningExpr, &j.ErrorExpr, &j.ValueExpr, &j.Unit,
			&j.Interval, &j.Timeout, &j.DownRetries, &j.UpRetries,
			&j.Status, &j.RetryCount, &j.UpCount, &j.IsCritical, &j.AffectsDevice,
			&j.SNMPProfileID, &j.AgentID, &j.IsAcknowledged, &overrides,
			&j.SNMPVersion, &j.SNMPPort, &j.SNMPCommunity,
			&j.SNMPv3User, &j.SNMPv3SecLevel, &j.SNMPv3AuthProto,
			&j.SNMPv3AuthPass, &j.SNMPv3PrivProto, &j.SNMPv3PrivPass,
			&j.NotifyEnabled,
		); err != nil {
			return err
		}
		j.SNMPCommunity, _ = secure.Decrypt(j.SNMPCommunity)
		j.SNMPv3AuthPass, _ = secure.Decrypt(j.SNMPv3AuthPass)
		j.SNMPv3PrivPass, _ = secure.Decrypt(j.SNMPv3PrivPass)
		j.Config = decodeMap(cfgJSON)
		for k, v := range decodeMap(overrides) {
			j.Config[k] = v
		}
		jobs = append(jobs, j)
		ids = append(ids, j.ServiceID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(jobs) == 0 {
		return nil
	}

	// Claim each due service atomically. The in-flight table prevents a slow
	// probe (or another core) from running the same service concurrently.
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	claimed := make([]job, 0, len(jobs))
	for _, j := range jobs {
		var claimedID string
		err := tx.QueryRow(ctx, `
			INSERT INTO poll_in_flight (service_id, claimed_at, expires_at)
			VALUES ($1, NOW(), NOW() + interval '5 minutes')
			ON CONFLICT (service_id) DO UPDATE
			SET claimed_at=NOW(), expires_at=NOW() + interval '5 minutes'
			WHERE poll_in_flight.expires_at < NOW()
			RETURNING service_id::text
		`, j.ServiceID).Scan(&claimedID)
		if err != nil {
			continue // already claimed by another worker/instance
		}
		if _, err := tx.Exec(ctx, `UPDATE device_services SET last_check_at=NOW() WHERE id=$1`, j.ServiceID); err != nil {
			return err
		}
		claimed = append(claimed, j)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, j := range claimed {
		select {
		case p.jobs <- j:
		case <-ctx.Done():
			p.clearInFlight(j.ServiceID)
			return nil
		}
	}
	return nil
}

func (p *Poller) clearInFlight(serviceID string) {
	_, _ = p.pool.Exec(context.Background(), `DELETE FROM poll_in_flight WHERE service_id=$1`, serviceID)
}

func (p *Poller) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case j := <-p.jobs:
			p.runJob(ctx, j)
		}
	}
}

func (p *Poller) runJob(ctx context.Context, j job) {
	defer p.clearInFlight(j.ServiceID)
	timeout := time.Duration(j.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout+2*time.Second)
	defer cancel()

	var res probeResult
	var err error
	if j.AgentID != nil && *j.AgentID != "" && j.ProbeType != "function" && j.ProbeType != "logic" {
		if p.agentMgr == nil {
			log.Printf("agent %s unavailable for %s", *j.AgentID, j.DeviceName)
			return
		}
		res, err = p.probeViaAgent(probeCtx, j)
		if err != nil {
			// Do not turn an agent connectivity outage into a device outage.
			log.Printf("agent probe %s / %s: %v", j.DeviceName, j.ProbeName, err)
			return
		}
	} else {
		switch j.ProbeType {
		case "icmp":
			res, err = p.probeICMP(probeCtx, j)
		case "snmp":
			res, err = p.probeSNMP(probeCtx, j)
		case "tcp":
			res, err = p.probeTCP(probeCtx, j)
		case "http":
			res, err = p.probeHTTP(probeCtx, j)
		case "dns":
			res, err = p.probeDNS(probeCtx, j)
		case "udp":
			res, err = p.probeUDP(probeCtx, j)
		case "ssh":
			res, err = p.probeBanner(probeCtx, j, "SSH", 22)
		case "smtp":
			res, err = p.probeBanner(probeCtx, j, "SMTP", 25)
		case "ftp":
			res, err = p.probeBanner(probeCtx, j, "FTP", 21)
		case "function":
			res, err = p.probeFunction(probeCtx, j)
		case "logic":
			res, err = p.probeLogic(probeCtx, j)
		default:
			err = fmt.Errorf("probe type %q is not implemented yet", j.ProbeType)
		}
	}
	if err != nil {
		res = probeResult{OK: false, Message: err.Error()}
	}
	if res.OK && strings.TrimSpace(j.AvailableExpr) != "" {
		available, evalErr := p.evaluateAvailability(ctx, j, res)
		if evalErr != nil {
			res = probeResult{OK: false, Message: "available_expression: " + evalErr.Error()}
		} else if !available {
			res = probeResult{OK: false, Message: "No disponible según available_expression", Value: res.Value, HasValue: res.HasValue}
		}
	}
	p.applyResult(ctx, j, res)
}

func (p *Poller) evaluateAvailability(ctx context.Context, j job, res probeResult) (bool, error) {
	oids := p.store.GetOIDs(ctx, j.DeviceID)
	cols := p.store.GetColumns(ctx, j.DeviceID)
	rates := p.store.GetRates(ctx, j.DeviceID)
	for k, v := range res.OIDs {
		oids[k] = v
	}
	for k, v := range res.Columns {
		cols[k] = v
	}
	expr, err := dsl.Parse(j.AvailableExpr)
	if err != nil {
		return false, err
	}
	out, err := expr.Eval(&dsl.Context{OIDs: oids, Columns: cols, Rates: rates, Vars: map[string]string{}})
	if err != nil {
		return false, err
	}
	switch v := out.(type) {
	case bool:
		return v, nil
	case nil:
		return false, nil
	default:
		return strings.TrimSpace(dsl.Stringify(v)) != "", nil
	}
}

func (p *Poller) lockDevice(deviceID string) func() {
	value, _ := p.deviceLocks.LoadOrStore(deviceID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (p *Poller) applyResult(ctx context.Context, j job, res probeResult) {
	unlock := p.lockDevice(j.DeviceID)
	defer unlock()
	now := time.Now().UTC()
	newStatus := j.Status
	newRetry := 0
	upCount := j.UpCount
	changed := false

	if res.OK && !res.Warning {
		maxUp := j.UpRetries
		if maxUp < 1 {
			maxUp = 1
		}
		if j.Status == "down" && maxUp > 1 {
			upCount++
			if upCount < maxUp {
				newStatus = "down"
				res.Message = fmt.Sprintf("Recuperando (%d/%d): %s", upCount, maxUp, res.Message)
			} else {
				newStatus = "up"
				upCount = 0
			}
		} else {
			newStatus = "up"
			upCount = 0
		}
		newRetry = 0
	} else if res.OK && res.Warning {
		newStatus = "warning"
		newRetry = 0
		upCount = 0
	} else {
		upCount = 0
		newRetry = j.RetryCount + 1
		maxRetries := j.DownRetries
		if maxRetries < 1 {
			maxRetries = 1
		}
		if newRetry >= maxRetries {
			newStatus = "down"
			if res.Message != "" {
				res.Message = fmt.Sprintf("%s (%d/%d)", res.Message, maxRetries, maxRetries)
			}
		} else {
			newStatus = "warning"
			res.Message = fmt.Sprintf("Reintentando (%d/%d): %s", newRetry, maxRetries, res.Message)
		}
	}
	if j.Status == "unknown" && newStatus == "warning" && !res.OK {
		// Keep the visual retry state even on first discovery.
		changed = true
	}
	if newStatus != j.Status {
		changed = true
	}

	_, err := p.pool.Exec(ctx, `
		UPDATE device_services
		SET status=$2::text, current_retry_count=$3, current_up_count=$4, last_value=$5, last_output_message=$6,
		    last_status_change_at = CASE WHEN status <> $2::text THEN $7 ELSE last_status_change_at END,
		    updated_at=NOW()
		WHERE id=$1
	`, j.ServiceID, newStatus, newRetry, upCount, nullableFloat(res.Value, res.HasValue), res.Message, now)
	if err != nil {
		log.Printf("update service %s: %v", j.ServiceID, err)
	}

	if res.HasValue || res.RTT > 0 {
		_, _ = p.pool.Exec(ctx, `
			INSERT INTO service_metrics (time, device_id, service_id, value, rtt_ms, packet_loss_pct)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, now, j.DeviceID, j.ServiceID, res.Value, res.RTT, res.Loss)
	}

	if changed {
		if newStatus == "down" && j.AffectsDevice {
			p.openOutage(ctx, j, res.Message, now)
		}
		p.insertEvent(ctx, j.DeviceID, severityFor(newStatus), "service_state",
			fmt.Sprintf("%s · %s: %s", j.DeviceName, j.ProbeName, res.Message))
	}

	p.consolidateLocked(ctx, j.DeviceID, j.NotifyEnabled)
	stateFields := map[string]any{
		"service_status": newStatus,
		"message":        res.Message,
		"last_probe":     j.ProbeName,
	}
	// Do not let an SNMP probe overwrite the ICMP latency with zero.
	if res.RTT > 0 {
		stateFields["rtt_ms"] = res.RTT
		stateFields["packet_loss"] = res.Loss
	}
	p.store.SetDevice(ctx, j.DeviceID, stateFields)

	if changed || newStatus == "up" {
		p.broadcastDevice(ctx, j.DeviceID, newStatus, res)
	}
}

func nullableFloat(f float64, ok bool) any {
	if !ok {
		return nil
	}
	return f
}

func severityFor(status string) string {
	switch status {
	case "down":
		return "critical"
	case "warning":
		return "warning"
	case "up":
		return "ok"
	default:
		return "info"
	}
}

func (p *Poller) consolidate(ctx context.Context, deviceID string, notifyEnabled bool) {
	unlock := p.lockDevice(deviceID)
	defer unlock()
	p.consolidateLocked(ctx, deviceID, notifyEnabled)
}

func (p *Poller) consolidateLocked(ctx context.Context, deviceID string, notifyEnabled bool) {
	type svc struct {
		status    string
		critical  bool
		affects   bool
		retry     int
		output    string
		probeName string
	}
	rows, err := p.pool.Query(ctx, `
		SELECT ds.status, ds.is_critical, ds.affects_device_status, ds.current_retry_count,
		       ds.last_output_message, p.name
		FROM device_services ds JOIN probes p ON p.id = ds.probe_id
		WHERE ds.device_id=$1 AND ds.is_enabled=TRUE
		ORDER BY ds.is_critical DESC, p.probe_type='icmp' DESC
	`, deviceID)
	if err != nil {
		return
	}
	defer rows.Close()
	var services []svc
	for rows.Next() {
		var s svc
		if err := rows.Scan(&s.status, &s.critical, &s.affects, &s.retry, &s.output, &s.probeName); err != nil {
			return
		}
		services = append(services, s)
	}

	var currentStatus, reason string
	var lastChange time.Time
	var maintenance *time.Time
	err = p.pool.QueryRow(ctx, `
		SELECT status, status_reason, COALESCE(last_status_change_at, NOW()), maintenance_until
		FROM devices WHERE id=$1
	`, deviceID).Scan(&currentStatus, &reason, &lastChange, &maintenance)
	if err != nil {
		return
	}

	newStatus := "unknown"
	newReason := "Sin servicios de monitorización"
	up, warn, down, unknown := 0, 0, 0, 0
	criticalDown := false
	for _, s := range services {
		switch s.status {
		case "up":
			up++
		case "warning":
			if s.affects {
				warn++
				if s.critical {
					newStatus = "warning"
					newReason = fmt.Sprintf("%s: %s", s.probeName, s.output)
				}
			}
		case "down":
			if s.affects {
				down++
				if s.critical {
					criticalDown = true
					newStatus = "down"
					newReason = fmt.Sprintf("%s: %s", s.probeName, s.output)
				} else {
					newStatus = "warning"
					newReason = fmt.Sprintf("%s: %s", s.probeName, s.output)
				}
			}
		default:
			unknown++
		}
	}
	if criticalDown {
		newStatus = "down"
	} else if warn > 0 || down > 0 {
		if newStatus == "unknown" || newStatus == "up" {
			newStatus = "warning"
		}
	} else if up > 0 && unknown == 0 {
		newStatus = "up"
		newReason = "Todos los servicios operativos"
	} else if up > 0 && down == 0 && warn == 0 {
		newStatus = "up"
		newReason = "Servicios operativos"
	}

	var rootCause correlation.Resolution
	if newStatus == "down" {
		// Correlation is authoritative over the simple direct-parent check:
		// it follows the whole Parents chain and, when no explicit parent is
		// available, conservatively uses the L2 topology.
		if resolution, err := correlation.ResolveWithStatus(ctx, p.pool, deviceID, newStatus); err == nil && resolution.HasRoot {
			rootCause = resolution
			newStatus = "unreachable"
			newReason = fmt.Sprintf("Equipo padre inalcanzable (%s): alerta suprimida por correlación", resolution.Root.Name)
			if resolution.Via == "l2" {
				newReason = fmt.Sprintf("Causa raíz en topología L2 (%s): alerta suprimida", resolution.Root.Name)
			}
		}
	}

	if maintenance != nil && time.Now().Before(*maintenance) {
		newStatus = "disabled"
		newReason = "Mantenimiento programado hasta " + maintenance.Local().Format("02/01/2006 15:04")
	}

	// Close any stale incident as soon as the device is no longer fully down.
	if newStatus != "down" && newStatus != "unreachable" {
		p.closeOutage(ctx, deviceID, time.Now().UTC())
	}

	if newStatus == currentStatus {
		return
	}
	_, err = p.pool.Exec(ctx, `
		UPDATE devices SET status=$2::text, status_reason=$3, last_status_change_at=NOW(),
		       last_seen_up_at=CASE WHEN $2::text='up' THEN NOW() ELSE last_seen_up_at END,
		       updated_at=NOW()
		WHERE id=$1
	`, deviceID, newStatus, newReason)
	if err != nil {
		log.Printf("consolidate device %s: %v", deviceID, err)
		return
	}

	if newStatus == "unreachable" && rootCause.HasRoot {
		if err := correlation.Apply(ctx, p.pool, deviceID, rootCause.Root.ID, rootCause.Depth); err != nil {
			log.Printf("correlation apply %s: %v", deviceID, err)
		}
	} else if newStatus != "unreachable" {
		_ = correlation.Clear(ctx, p.pool, deviceID)
	}

	p.store.SetDevice(ctx, deviceID, map[string]any{"status": newStatus, "message": newReason})
	p.insertEvent(ctx, deviceID, severityFor(newStatus), "device_state", newReason)
	// A device that returns to UP or degrades to WARNING is no longer a full
	// outage; close the incident so MTTR/active-outage counters stay truthful.
	if newStatus == "up" || newStatus == "warning" || newStatus == "acknowledged" {
		p.closeOutage(ctx, deviceID, time.Now().UTC())
	}
	if notifyEnabled && (newStatus == "up" || newStatus == "down" || newStatus == "warning") {
		if transitions := p.store.RecordFlap(ctx, deviceID); transitions >= 4 && p.store.FlapAlertOnce(ctx, deviceID) {
			flapReason := fmt.Sprintf("Dispositivo oscilando: %d cambios de estado en 15 minutos", transitions)
			p.insertEvent(ctx, deviceID, "warning", "flapping", flapReason)
			p.hub.Broadcast(map[string]any{"type": "NOC_ALERT_EVENT", "payload": map[string]any{
				"transition": "flapping", "severity": "warning", "device_id": deviceID,
				"device_name": p.deviceName(ctx, deviceID), "message": flapReason,
				"timestamp": time.Now().UTC(),
			}})
			p.notifyFlap(ctx, deviceID, flapReason)
		}
	}
	if notifyEnabled {
		p.alert(ctx, deviceID, newStatus, newReason)
	}
	p.broadcastDevice(ctx, deviceID, newStatus, probeResult{Message: newReason})
	p.recalcMapSummaries(ctx, deviceID)
	if (currentStatus == "down" || currentStatus == "unreachable") && newStatus != "down" && newStatus != "unreachable" {
		go p.recheckDependents(deviceID)
	}
}

func (p *Poller) recheckDependents(rootID string) {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	p.recheckMu.Lock()
	if p.rechecking[rootID] {
		p.recheckMu.Unlock()
		return
	}
	p.rechecking[rootID] = true
	p.recheckMu.Unlock()
	defer func() {
		p.recheckMu.Lock()
		delete(p.rechecking, rootID)
		p.recheckMu.Unlock()
	}()
	deps, err := correlation.Dependents(ctx, p.pool, rootID)
	if err != nil || len(deps) == 0 {
		return
	}
	for _, id := range deps {
		select {
		case <-ctx.Done():
			return
		default:
		}
		p.consolidate(ctx, id, p.deviceNotifyEnabled(ctx, id))
	}
}

func (p *Poller) deviceNotifyEnabled(ctx context.Context, deviceID string) bool {
	var enabled bool
	_ = p.pool.QueryRow(ctx, `
		SELECT COALESCE(d.poll_notifications_enabled, gp.poll_notifications_enabled,
		                mp.poll_notifications_enabled, TRUE)
		FROM devices d
		LEFT JOIN LATERAL (
			SELECT g.poll_notifications_enabled FROM device_group_members gm
			JOIN device_groups g ON g.id=gm.group_id
			WHERE gm.device_id=d.id AND g.poll_notifications_enabled IS NOT NULL LIMIT 1
		) gp ON TRUE
		LEFT JOIN LATERAL (
			SELECT m.poll_notifications_enabled FROM map_elements me
			JOIN maps m ON m.id=me.map_id
			WHERE me.device_id=d.id AND m.poll_notifications_enabled IS NOT NULL LIMIT 1
		) mp ON TRUE
		WHERE d.id=$1
	`, deviceID).Scan(&enabled)
	return enabled
}

func (p *Poller) openOutage(ctx context.Context, j job, message string, at time.Time) {
	// The partial unique index on (device_id) WHERE ended_at IS NULL makes this
	// idempotent even if two services of the same device fail at once.
	_, err := p.pool.Exec(ctx, `
		INSERT INTO outages (device_id, device_service_id, severity, started_at, error_message)
		VALUES ($1,$2,'down',$3,$4)
		ON CONFLICT (device_id) WHERE ended_at IS NULL DO NOTHING
	`, j.DeviceID, j.ServiceID, at, message)
	if err != nil {
		log.Printf("open outage: %v", err)
	}
}

func (p *Poller) closeOutage(ctx context.Context, deviceID string, at time.Time) {
	_, _ = p.pool.Exec(ctx, `
		UPDATE outages SET ended_at=$2
		WHERE device_id=$1 AND ended_at IS NULL
	`, deviceID, at)
}

func (p *Poller) insertEvent(ctx context.Context, deviceID, severity, eventType, message string) {
	_, _ = p.pool.Exec(ctx, `
		INSERT INTO events (device_id, severity, event_type, message)
		VALUES ($1,$2,$3,$4)
	`, deviceID, severity, eventType, message)
}

func (p *Poller) recalcMapSummaries(ctx context.Context, deviceID string) {
	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT me.map_id FROM map_elements me WHERE me.device_id=$1
	`, deviceID)
	if err != nil {
		return
	}
	var mapIDs []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		mapIDs = append(mapIDs, id)
	}
	rows.Close()

	for _, mapID := range mapIDs {
		var up, warn, down, unreachable, disabled int
		_ = p.pool.QueryRow(ctx, `
			SELECT
			  count(*) FILTER (WHERE d.status='up'),
			  count(*) FILTER (WHERE d.status='warning'),
			  count(*) FILTER (WHERE d.status IN ('down','acknowledged')),
			  count(*) FILTER (WHERE d.status='unreachable'),
			  count(*) FILTER (WHERE d.status IN ('disabled','unknown'))
			FROM map_elements me JOIN devices d ON d.id=me.device_id
			WHERE me.map_id=$1
		`, mapID).Scan(&up, &warn, &down, &unreachable, &disabled)
		status := "up"
		if down > 0 {
			status = "down"
		} else if unreachable > 0 {
			status = "unreachable"
		} else if warn > 0 {
			status = "warning"
		} else if up == 0 {
			status = "unknown"
		}
		p.store.SetMapSummary(ctx, mapID, map[string]any{
			"status": status, "up": up, "warning": warn, "down": down,
			"unreachable": unreachable, "disabled": disabled,
		})
		p.hub.BroadcastMap(mapID, map[string]any{
			"type": "MAP_SUMMARY_PATCH",
			"payload": map[string]any{
				"map_id": mapID, "status": status, "up": up, "warning": warn,
				"down": down, "unreachable": unreachable, "disabled": disabled,
			},
		})
		// Propagate to maps that show this map as a submap node.
		parents, _ := p.pool.Query(ctx, `
			SELECT pm.id FROM maps pm JOIN map_elements me ON me.map_id=pm.id
			WHERE me.element_type='submap' AND me.target_submap_id=$1
		`, mapID)
		for parents.Next() {
			var parentID string
			_ = parents.Scan(&parentID)
			p.hub.BroadcastMap(parentID, map[string]any{
				"type": "SUBMAP_SUMMARY_PATCH",
				"payload": map[string]any{
					"submap_id": mapID, "status": status, "counts": map[string]int{
						"up": up, "warning": warn, "down": down, "unreachable": unreachable,
					},
				},
			})
		}
		parents.Close()
	}
}

func (p *Poller) broadcastDevice(ctx context.Context, deviceID, status string, res probeResult) {
	label, rtt, loss, ack, name, ip := p.deviceSnapshot(ctx, deviceID)
	var rootID, rootName string
	_ = p.pool.QueryRow(ctx, `
		SELECT COALESCE(d.root_cause_device_id::text,''), COALESCE(rc.name,'')
		FROM devices d LEFT JOIN devices rc ON rc.id=d.root_cause_device_id WHERE d.id=$1
	`, deviceID).Scan(&rootID, &rootName)
	payload := map[string]any{
		"type": "DEVICE_STATE_PATCH",
		"payload": map[string]any{
			"device_id":            deviceID,
			"status":               status,
			"rtt_ms":               rtt,
			"packet_loss_pct":      loss,
			"rendered_label":       label,
			"message":              res.Message,
			"is_acknowledged":      ack,
			"name":                 name,
			"ip":                   ip,
			"root_cause_device_id": rootID,
			"root_cause":           rootName,
			"services_summary":     p.serviceSummary(ctx, deviceID),
		},
	}
	maps := p.mapsForDevice(ctx, deviceID)
	if len(maps) == 0 {
		p.hub.Broadcast(payload)
		return
	}
	for _, m := range maps {
		p.hub.BroadcastMap(m, payload)
	}
}

func (p *Poller) mapsForDevice(ctx context.Context, deviceID string) []string {
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT map_id FROM map_elements WHERE device_id=$1`, deviceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

func (p *Poller) deviceName(ctx context.Context, deviceID string) string {
	var name string
	_ = p.pool.QueryRow(ctx, `SELECT name FROM devices WHERE id=$1`, deviceID).Scan(&name)
	return name
}

func (p *Poller) serviceSummary(ctx context.Context, deviceID string) map[string]int {
	out := map[string]int{"up": 0, "warning": 0, "down": 0}
	rows, err := p.pool.Query(ctx, `SELECT status, count(*) FROM device_services WHERE device_id=$1 AND is_enabled=TRUE GROUP BY status`, deviceID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	return out
}

func (p *Poller) deviceSnapshot(ctx context.Context, deviceID string) (label string, rtt, loss float64, ack bool, name, ip string) {
	var cidr string
	err := p.pool.QueryRow(ctx, `
		SELECT d.name, host(d.primary_ip), d.is_acknowledged, COALESCE(me.label_template, d.name)
		FROM devices d
		LEFT JOIN LATERAL (SELECT label_template FROM map_elements WHERE device_id=d.id ORDER BY created_at LIMIT 1) me ON TRUE
		WHERE d.id=$1
	`, deviceID).Scan(&name, &cidr, &ack, &label)
	if err != nil {
		return "", 0, 0, false, "", ""
	}
	ip = cidr
	oidMap := p.store.GetOIDs(ctx, deviceID)
	cols := p.store.GetColumns(ctx, deviceID)
	rates := p.store.GetRates(ctx, deviceID)
	ctxDSL := dsl.Context{OIDs: oidMap, Columns: cols, Rates: rates, Vars: map[string]string{
		"Device.Name": name, "Device.FirstAddress": ip, "Device.RTT": strconv.FormatFloat(rtt, 'f', 1, 64),
	}}
	if v := p.store.GetDevice(ctx, deviceID); v["rtt_ms"] != "" {
		fmt.Sscanf(v["rtt_ms"], "%f", &rtt)
		ctxDSL.Vars["Device.RTT"] = strconv.FormatFloat(rtt, 'f', 1, 64)
		if v["packet_loss"] != "" {
			fmt.Sscanf(v["packet_loss"], "%f", &loss)
			ctxDSL.Vars["Device.PacketLoss"] = fmt.Sprintf("%.0f%%", loss)
		}
	}
	ctxDSL.Vars["Device.StatusText"] = strings.ToUpper(statusFromSnapshot(ctx, p, deviceID))
	ctxDSL.Vars["Device.Status"] = statusFromSnapshot(ctx, p, deviceID)
	p.loadFunctions(ctx, false)
	p.funcMu.RLock()
	fn := make(map[string]string, len(p.functions))
	for k, v := range p.functions {
		fn[k] = v
	}
	p.funcMu.RUnlock()
	label = dsl.RenderLabel(label, &ctxDSL, fn)
	if label == "" {
		label = name + "\n" + ip
	}
	return label, rtt, loss, ack, name, ip
}

func (p *Poller) loadFunctions(ctx context.Context, force bool) {
	p.funcMu.RLock()
	fresh := time.Since(p.funcAt) < 60*time.Second
	p.funcMu.RUnlock()
	if fresh && !force {
		return
	}
	rows, err := p.pool.Query(ctx, `SELECT name, expression_code FROM functions`)
	if err != nil {
		return
	}
	defer rows.Close()
	fns := map[string]string{}
	for rows.Next() {
		var n, c string
		if rows.Scan(&n, &c) == nil {
			fns[n] = c
		}
	}
	p.funcMu.Lock()
	p.functions = fns
	p.funcAt = time.Now()
	p.funcMu.Unlock()
}

func statusFromSnapshot(ctx context.Context, p *Poller, deviceID string) string {
	s := p.store.GetDevice(ctx, deviceID)
	if s["status"] != "" {
		return s["status"]
	}
	var status string
	_ = p.pool.QueryRow(ctx, `SELECT status FROM devices WHERE id=$1`, deviceID).Scan(&status)
	return status
}

func (p *Poller) alert(ctx context.Context, deviceID, status, reason string) {
	if status == "unknown" || status == "disabled" || p.deviceBlocked(ctx, deviceID) {
		return
	}
	var name, ip, mapID, mapName string
	var ack bool
	_ = p.pool.QueryRow(ctx, `
		SELECT d.name, host(d.primary_ip), COALESCE(me.map_id::text,''), COALESCE(m.name,''), d.is_acknowledged
		FROM devices d
		LEFT JOIN LATERAL (SELECT map_id FROM map_elements WHERE device_id=d.id LIMIT 1) me ON TRUE
		LEFT JOIN maps m ON m.id=me.map_id
		WHERE d.id=$1
	`, deviceID).Scan(&name, &ip, &mapID, &mapName, &ack)
	eventType := status
	if status == "up" {
		eventType = "recovered"
	}
	payload := map[string]any{
		"type": "NOC_ALERT_EVENT",
		"payload": map[string]any{
			"transition":   eventType,
			"severity":     severityFor(status),
			"device_id":    deviceID,
			"device_name":  name,
			"ip":           ip,
			"map_id":       mapID,
			"map_name":     mapName,
			"message":      reason,
			"acknowledged": ack,
			"timestamp":    time.Now().UTC(),
		},
	}
	p.hub.Broadcast(payload)
	p.notifyExternal(ctx, deviceID, mapID, name, ip, mapName, eventType, reason)
}

func decodeMap(raw string) map[string]any {
	out := map[string]any{}
	if raw != "" {
		_ = jsonUnmarshal(raw, &out)
	}
	return out
}
