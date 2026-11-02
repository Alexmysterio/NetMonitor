package receivers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/correlation"
	"github.com/netmonitor/netmonitor/internal/state"
	"github.com/netmonitor/netmonitor/internal/ws"
)

type Receivers struct {
	pool   *pgxpool.Pool
	store  *state.Store
	hub    *ws.Hub
	notify func(context.Context, string, string)
	send   func(context.Context, string, string, []string)

	mibMu   sync.RWMutex
	mibAt   time.Time
	objects map[string]mibEntry
	notifs  map[string]mibEntry

	settingsMu sync.RWMutex
	community  string
}

type mibEntry struct {
	Name        string
	Module      string
	Syntax      string
	Description string
	Objects     []string
}

func New(pool *pgxpool.Pool, store *state.Store, hub *ws.Hub) *Receivers {
	return &Receivers{pool: pool, store: store, hub: hub, objects: map[string]mibEntry{}, notifs: map[string]mibEntry{}}
}

// SetNotifier lets a trap rule use the regular alert-rule pipeline or an
// explicit channel list owned by the rule.
func (r *Receivers) SetNotifier(fn func(context.Context, string, string), send func(context.Context, string, string, []string)) {
	r.notify = fn
	r.send = send
}

// Start launches the UDP syslog and SNMP trap listeners.
func (r *Receivers) Start(ctx context.Context, trapAddr, syslogAddr string, traps, syslog bool) {
	if traps {
		go r.startTraps(ctx, trapAddr)
	}
	if syslog {
		go r.startSyslog(ctx, syslogAddr)
	}
}

func (r *Receivers) startTraps(ctx context.Context, addr string) {
	tl := gosnmp.NewTrapListener()
	tl.Params = &gosnmp.GoSNMP{
		Transport: "udp",
		Port:      1620,
		Version:   gosnmp.Version2c,
		Community: r.trapCommunity(ctx),
		Logger:    gosnmp.NewLogger(log.New(log.Writer(), "", log.LstdFlags)),
	}
	tl.OnNewTrap = func(packet *gosnmp.SnmpPacket, remote *net.UDPAddr) {
		r.handleTrap(ctx, packet, remote)
	}
	log.Printf("SNMP trap receiver listening on %s/udp", addr)
	if err := tl.Listen(addr); err != nil {
		log.Printf("SNMP trap receiver: %v", err)
	}
}

func (r *Receivers) trapCommunity(ctx context.Context) string {
	var value string
	_ = r.pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='traps.community'`).Scan(&value)
	if strings.TrimSpace(value) == "" {
		return "public"
	}
	return value
}

// InjectTestTrap feeds a synthetic v2c trap through the same decoding, rule
// matching and event pipeline used by the UDP listener. The UI uses it to
// validate a trap rule without touching production devices.
func (r *Receivers) InjectTestTrap(ctx context.Context, sourceIP, trapOID string) {
	if strings.TrimSpace(trapOID) == "" {
		return
	}
	packet := &gosnmp.SnmpPacket{
		Version: gosnmp.Version2c,
		Variables: []gosnmp.SnmpPDU{
			{Name: "1.3.6.1.6.3.1.1.4.1.0", Type: gosnmp.ObjectIdentifier, Value: trapOID},
			{Name: "1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(123456)},
		},
	}
	addr := &net.UDPAddr{IP: net.ParseIP(sourceIP), Port: 162}
	if addr.IP == nil {
		addr.IP = net.ParseIP("127.0.0.1")
	}
	r.handleTrap(ctx, packet, addr)
}

type varbind struct {
	OID   string `json:"oid"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type trapRule struct {
	ID         string
	Priority   int
	MatchOID   string
	MatchName  string
	Severity   string
	EventType  string
	Message    string
	DeviceIDs  []string
	Tags       []string
	MapIDs     []string
	ChannelIDs []string
	Notify     bool
	Suppress   bool
}

// Standard RFC 1157/SNMPv2 traps are decoded even when the operator has not
// imported the base MIBs yet.
var standardTraps = map[string]string{
	"1.3.6.1.6.3.1.1.5.1": "coldStart",
	"1.3.6.1.6.3.1.1.5.2": "warmStart",
	"1.3.6.1.6.3.1.1.5.3": "linkDown",
	"1.3.6.1.6.3.1.1.5.4": "linkUp",
	"1.3.6.1.6.3.1.1.5.5": "authenticationFailure",
	"1.3.6.1.6.3.1.1.5.6": "egpNeighborLoss",
}

func (r *Receivers) handleTrap(ctx context.Context, packet *gosnmp.SnmpPacket, remote *net.UDPAddr) {
	now := time.Now().UTC()
	sourceIP := remote.IP.String()
	version := trapVersion(packet.Version)
	trapOID := extractTrapOID(packet)
	objects, notifs := r.mibIndex(ctx)
	trapName := trapOID
	if entry, ok := notifs[trapOID]; ok {
		trapName = entry.Name
	} else if entry, ok := lookupOID(objects, trapOID); ok {
		trapName = entry.Name
	} else if standard, ok := standardTraps[trapOID]; ok {
		trapName = standard
	}

	vars := make([]varbind, 0, len(packet.Variables))
	for _, pdu := range packet.Variables {
		oid := strings.TrimPrefix(pdu.Name, ".")
		name := oid
		syntax := ""
		if entry, ok := lookupOID(objects, oid); ok {
			name = entry.Name
			syntax = entry.Syntax
		}
		vars = append(vars, varbind{OID: oid, Name: name, Type: syntax, Value: snmpValue(pdu)})
	}
	raw, _ := json.Marshal(map[string]any{
		"enterprise": packet.Enterprise, "generic_trap": packet.GenericTrap,
		"specific_trap": packet.SpecificTrap, "variables": len(packet.Variables),
	})
	varsJSON, _ := json.Marshal(vars)

	var deviceID, deviceName, mapID string
	var tags, mapIDs []string
	_ = r.pool.QueryRow(ctx, `
		SELECT d.id::text, d.name,
		       COALESCE(ARRAY(SELECT me.map_id::text FROM map_elements me WHERE me.device_id=d.id ORDER BY me.map_id),'{}'),
		       COALESCE(d.tags,'{}')
		FROM devices d
		WHERE host(d.primary_ip)=$1 OR EXISTS (
			SELECT 1 FROM unnest(d.secondary_ips) sip WHERE host(sip)=$1
		)
		LIMIT 1
	`, sourceIP).Scan(&deviceID, &deviceName, &mapIDs, &tags)
	if len(mapIDs) > 0 {
		mapID = mapIDs[0]
	}

	rule := r.matchRule(ctx, trapOID, trapName, deviceID, mapIDs, tags)
	severity := "warning"
	eventType := "snmp_trap"
	message := defaultTrapMessage(trapName, trapOID, sourceIP, vars)
	notify := false
	suppress := false
	channels := []string{}
	if rule != nil {
		if rule.Severity != "" {
			severity = rule.Severity
		}
		if rule.EventType != "" {
			eventType = rule.EventType
		}
		if strings.TrimSpace(rule.Message) != "" {
			message = renderTrapMessage(rule.Message, trapName, trapOID, sourceIP, deviceName, vars)
		}
		notify = rule.Notify
		suppress = rule.Suppress
		channels = rule.ChannelIDs
	} else {
		severity = defaultTrapSeverity(trapName)
	}

	rootID := ""
	rootName := ""
	if deviceID != "" {
		if resolution, err := correlation.Resolve(ctx, r.pool, deviceID); err == nil && resolution.HasRoot {
			rootID, rootName = resolution.Root.ID, resolution.Root.Name
			if rootID != deviceID {
				suppress = true
			}
		}
	}

	eventID := int64(0)
	var ruleID any
	if rule != nil {
		ruleID = rule.ID
	}
	var rootArg any
	if rootID != "" {
		rootArg = rootID
	}
	if !suppress {
		meta, _ := json.Marshal(map[string]any{
			"source_ip": sourceIP, "trap_oid": trapOID, "trap_name": trapName,
			"version": version, "variables": json.RawMessage(varsJSON), "rule_id": ruleID,
			"root_cause_device_id": rootID,
		})
		if err := r.pool.QueryRow(ctx, `
			INSERT INTO events (at, device_id, map_id, severity, event_type, message, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb) RETURNING id
		`, now, nullable(deviceID), nullable(mapID), severity, eventType, message, string(meta)).Scan(&eventID); err != nil {
			log.Printf("trap event: %v", err)
		}
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO snmp_traps (at, source_ip, device_id, map_id, trap_oid, trap_name, severity,
		                        event_type, message, version, variables, matched_rule_id, event_id,
		                        root_cause_device_id, suppressed, raw)
		VALUES ($1,$2::inet,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16::jsonb)
	`, now, sourceIP, nullable(deviceID), nullable(mapID), trapOID, trapName, severity, eventType,
		message, version, string(varsJSON), ruleID, nullableEvent(eventID), rootArg, suppress, string(raw)); err != nil {
		log.Printf("store trap: %v", err)
	}

	if suppress {
		r.hub.Broadcast(map[string]any{
			"type": "TRAP_SUPPRESSED",
			"payload": map[string]any{
				"device_id": deviceID, "device_name": deviceName, "trap_name": trapName,
				"root_cause_device_id": rootID, "root_cause": rootName,
				"source_ip": sourceIP, "map_id": mapID, "message": message,
			},
		})
		return
	}

	r.hub.Broadcast(map[string]any{
		"type": "TRAP_RECEIVED",
		"payload": map[string]any{
			"id": time.Now().UnixNano(), "at": now, "device_id": deviceID, "device_name": deviceName,
			"map_id": mapID, "source_ip": sourceIP, "trap_oid": trapOID, "trap_name": trapName,
			"severity": severity, "event_type": eventType, "message": message, "variables": vars,
		},
	})
	r.hub.Broadcast(map[string]any{
		"type": "EVENT_RECEIVED",
		"payload": map[string]any{
			"severity": severity, "event_type": eventType, "message": message,
			"source_ip": sourceIP, "map_id": mapID, "device_id": deviceID,
		},
	})
	if notify && deviceID != "" {
		if len(channels) > 0 && r.send != nil {
			go r.send(context.Background(), deviceID, message, channels)
		} else if r.notify != nil {
			go r.notify(context.Background(), deviceID, message)
		}
	}
}

func (r *Receivers) mibIndex(ctx context.Context) (map[string]mibEntry, map[string]mibEntry) {
	r.mibMu.RLock()
	if time.Since(r.mibAt) < 2*time.Minute {
		objects, notifs := r.objects, r.notifs
		r.mibMu.RUnlock()
		return objects, notifs
	}
	r.mibMu.RUnlock()
	objects := map[string]mibEntry{}
	rows, err := r.pool.Query(ctx, `SELECT oid, name, module, syntax, COALESCE(description,'') FROM mib_objects`)
	if err == nil {
		for rows.Next() {
			var e mibEntry
			var oid string
			if rows.Scan(&oid, &e.Name, &e.Module, &e.Syntax, &e.Description) == nil {
				objects[oid] = e
			}
		}
		rows.Close()
	}
	notifs := map[string]mibEntry{}
	nrows, err := r.pool.Query(ctx, `SELECT oid, name, module, COALESCE(description,''), objects FROM mib_notifications`)
	if err == nil {
		for nrows.Next() {
			var e mibEntry
			var oid string
			if nrows.Scan(&oid, &e.Name, &e.Module, &e.Description, &e.Objects) == nil {
				notifs[oid] = e
			}
		}
		nrows.Close()
	}
	r.mibMu.Lock()
	r.objects, r.notifs, r.mibAt = objects, notifs, time.Now()
	r.mibMu.Unlock()
	return objects, notifs
}

func lookupOID(index map[string]mibEntry, oid string) (mibEntry, bool) {
	if e, ok := index[oid]; ok {
		return e, true
	}
	// Table columns use the definition OID plus an index. Walk up until a
	// known object is found, exactly like an SNMP MIB browser.
	for candidate := oid; strings.Contains(candidate, "."); {
		idx := strings.LastIndex(candidate, ".")
		if idx <= 0 {
			break
		}
		candidate = candidate[:idx]
		if e, ok := index[candidate]; ok {
			return e, true
		}
	}
	return mibEntry{}, false
}

func (r *Receivers) matchRule(ctx context.Context, trapOID, trapName, deviceID string, mapIDs, tags []string) *trapRule {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, priority, match_oid, match_name, severity, event_type,
		       COALESCE(message_template,''), filter_device_ids, filter_tags, filter_map_ids,
		       channel_ids, notify, suppress
		FROM trap_rules WHERE is_enabled=TRUE ORDER BY priority, name
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var rule trapRule
		if rows.Scan(&rule.ID, &rule.Priority, &rule.MatchOID, &rule.MatchName, &rule.Severity,
			&rule.EventType, &rule.Message, &rule.DeviceIDs, &rule.Tags, &rule.MapIDs,
			&rule.ChannelIDs, &rule.Notify, &rule.Suppress) != nil {
			continue
		}
		if !matchOIDPattern(rule.MatchOID, trapOID) {
			continue
		}
		if strings.TrimSpace(rule.MatchName) != "" && !strings.Contains(strings.ToLower(trapName), strings.ToLower(rule.MatchName)) {
			continue
		}
		if len(rule.DeviceIDs) > 0 && !contains(rule.DeviceIDs, deviceID) {
			continue
		}
		if len(rule.MapIDs) > 0 && !intersects(rule.MapIDs, mapIDs) {
			continue
		}
		if len(rule.Tags) > 0 && !intersects(rule.Tags, tags) {
			continue
		}
		return &rule
	}
	return nil
}

func matchOIDPattern(pattern, oid string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(oid, strings.TrimSuffix(pattern, "*"))
	}
	return oid == pattern
}

func extractTrapOID(packet *gosnmp.SnmpPacket) string {
	for _, pdu := range packet.Variables {
		if strings.TrimPrefix(pdu.Name, ".") == "1.3.6.1.6.3.1.1.4.1.0" {
			switch v := pdu.Value.(type) {
			case string:
				return strings.TrimPrefix(v, ".")
			case []byte:
				return strings.TrimPrefix(string(v), ".")
			}
		}
	}
	if packet.Enterprise != "" {
		if packet.GenericTrap == 6 {
			return fmt.Sprintf("%s.0.%d", strings.TrimSuffix(packet.Enterprise, "."), packet.SpecificTrap)
		}
		generic := map[int]string{
			0: "1.3.6.1.6.3.1.1.5.1", 1: "1.3.6.1.6.3.1.1.5.2",
			2: "1.3.6.1.6.3.1.1.5.3", 3: "1.3.6.1.6.3.1.1.5.4",
			4: "1.3.6.1.6.3.1.1.5.5", 5: "1.3.6.1.6.3.1.1.5.6",
		}
		if oid := generic[packet.GenericTrap]; oid != "" {
			return oid
		}
		return strings.TrimSuffix(packet.Enterprise, ".")
	}
	return ""
}

func trapVersion(v gosnmp.SnmpVersion) string {
	switch v {
	case gosnmp.Version1:
		return "v1"
	case gosnmp.Version3:
		return "v3"
	default:
		return "v2c"
	}
}

func defaultTrapSeverity(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "linkdown"), strings.Contains(lower, "coldstart"),
		strings.Contains(lower, "authenticationfailure"), strings.Contains(lower, "critical"),
		strings.Contains(lower, "failure"), strings.Contains(lower, "down"):
		return "critical"
	case strings.Contains(lower, "linkup"), strings.Contains(lower, "warmstart"), strings.Contains(lower, "up"):
		return "info"
	default:
		return "warning"
	}
}

func defaultTrapMessage(name, oid, source string, vars []varbind) string {
	parts := make([]string, 0, 4)
	for _, v := range vars {
		if v.OID == "1.3.6.1.6.3.1.1.4.1.0" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", v.Name, v.Value))
		if len(parts) >= 4 {
			break
		}
	}
	msg := fmt.Sprintf("Trap %s desde %s", name, source)
	if oid != "" && oid != name {
		msg += " (" + oid + ")"
	}
	if len(parts) > 0 {
		msg += ": " + strings.Join(parts, ", ")
	}
	return msg
}

func renderTrapMessage(template, name, oid, source, device string, vars []varbind) string {
	r := strings.NewReplacer(
		"{{trap_name}}", name, "{{trap.oid}}", oid, "{{trap_oid}}", oid,
		"{{source_ip}}", source, "{{device_name}}", device, "{{device.name}}", device,
	)
	out := r.Replace(template)
	for _, v := range vars {
		out = strings.ReplaceAll(out, "{{"+v.Name+"}}", v.Value)
		out = strings.ReplaceAll(out, "{{varbind."+v.Name+"}}", v.Value)
	}
	return out
}

func snmpValue(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case nil:
		return ""
	case []byte:
		if printable(v) {
			return string(v)
		}
		return "0x" + hex.EncodeToString(v)
	case string:
		return v
	case time.Duration:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func printable(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func intersects(a, b []string) bool {
	set := map[string]bool{}
	for _, v := range b {
		set[strings.ToLower(v)] = true
	}
	for _, v := range a {
		if set[strings.ToLower(v)] {
			return true
		}
	}
	return false
}

var syslogPrefix = regexp.MustCompile(`^<(\d+)>`)

func (r *Receivers) startSyslog(ctx context.Context, addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Printf("syslog receiver resolve: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Printf("syslog receiver listen: %v", err)
		return
	}
	defer conn.Close()
	log.Printf("syslog receiver listening on %s/udp", addr)
	buf := make([]byte, 64*1024)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		line := strings.TrimSpace(string(buf[:n]))
		severity := "info"
		priority := 6
		if m := syslogPrefix.FindStringSubmatch(line); len(m) == 2 {
			fmt.Sscanf(m[1], "%d", &priority)
			line = strings.TrimSpace(syslogPrefix.ReplaceAllString(line, ""))
		}
		switch {
		case priority <= 3:
			severity = "critical"
		case priority <= 4:
			severity = "warning"
		}
		if len(line) > 1000 {
			line = line[:1000]
		}
		r.record(ctx, remote.IP.String(), "syslog", severity, line, map[string]any{
			"source_ip": remote.IP.String(), "priority": priority,
		})
	}
}

func (r *Receivers) record(ctx context.Context, sourceIP, eventType, severity, message string, metadata map[string]any) {
	meta := "{}"
	if len(metadata) > 0 {
		if encoded, err := json.Marshal(metadata); err == nil {
			meta = string(encoded)
		}
	}
	var deviceID any
	var mapID string
	var deviceName string
	err := r.pool.QueryRow(ctx, `
		SELECT d.id::text,
		       COALESCE((SELECT me.map_id::text FROM map_elements me WHERE me.device_id=d.id LIMIT 1),'')
		FROM devices d WHERE host(d.primary_ip)=$1 LIMIT 1
	`, sourceIP).Scan(&deviceName, &mapID)
	if err == nil {
		deviceID = deviceName
	}
	_, _ = r.pool.Exec(ctx, `
		INSERT INTO events (device_id, map_id, severity, event_type, message, metadata)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb)
	`, deviceID, nullable(mapID), severity, eventType, message, meta)
	r.hub.Broadcast(map[string]any{
		"type": "EVENT_RECEIVED",
		"payload": map[string]any{
			"severity": severity, "event_type": eventType, "message": message,
			"source_ip": sourceIP, "map_id": mapID, "device_id": deviceID,
		},
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableEvent(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
