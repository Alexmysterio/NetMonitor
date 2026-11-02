package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

type neighborSuggestion struct {
	SourceDeviceID string `json:"source_device_id"`
	TargetDeviceID string `json:"target_device_id"`
	SourceName     string `json:"source_name"`
	TargetName     string `json:"target_name"`
	LocalPort      string `json:"local_port"`
	RemotePort     string `json:"remote_port"`
	LocalIfIndex   int    `json:"local_if_index,omitempty"`
	RemoteIfIndex  int    `json:"remote_if_index,omitempty"`
	STPState       string `json:"stp_state,omitempty"`
	VLAN           int    `json:"vlan,omitempty"`
	Protocol       string `json:"protocol"`
}

type neighborDevice struct {
	ID      string
	Name    string
	IP      string
	MAC     string
	HasSNMP bool
}

// discoverLLDP reads standard LLDP-MIB remote tables. Matching by remote
// sysName is deliberately conservative: it creates a suggestion only when the
// neighbor is already known to NetMonitor, never a duplicate placeholder.
func (s *Server) discoverLinks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MapID string `json:"map_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.MapID == "" {
		writeError(w, http.StatusBadRequest, "map_id is required")
		return
	}
	// Every map device can be a link target via its MAC, but only devices with
	// an SNMP profile can be polled. Filtering the whole result set by profile
	// hid SNMP-less hosts from FDB/Q-BRIDGE correlation.
	rows, err := s.pool.Query(r.Context(), `
		SELECT DISTINCT d.id::text, d.name, host(d.primary_ip), COALESCE(d.mac_address,''), d.snmp_profile_id IS NOT NULL
		FROM map_elements me JOIN devices d ON d.id=me.device_id
		WHERE me.map_id=$1
		LIMIT 60
	`, req.MapID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var devices []neighborDevice
	for rows.Next() {
		var d neighborDevice
		if rows.Scan(&d.ID, &d.Name, &d.IP, &d.MAC, &d.HasSNMP) == nil {
			devices = append(devices, d)
		}
	}
	rows.Close()
	if len(devices) < 2 {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": []neighborSuggestion{}})
		return
	}
	byName := map[string]neighborDevice{}
	byMAC := map[string]neighborDevice{}
	byIP := map[string]neighborDevice{}
	for _, d := range devices {
		byName[strings.ToLower(strings.TrimSpace(d.Name))] = d
		byIP[strings.TrimSpace(d.IP)] = d
		if mac := normalizeMAC(d.MAC); mac != "" {
			byMAC[mac] = d
		}
	}

	type result struct {
		list []neighborSuggestion
	}
	resCh := make(chan result, len(devices))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	for _, d := range devices {
		if !d.HasSNMP {
			continue
		}
		wg.Add(1)
		go func(d neighborDevice) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			profile, _, err := s.loadSNMPProfile(ctx, d.ID)
			if err != nil {
				resCh <- result{}
				return
			}
			list := readLLDP(ctx, profile, d, byName)
			list = append(list, readCDP(ctx, profile, d, byName)...)
			list = append(list, readMikroTik(ctx, profile, d, byName, byMAC, byIP)...)
			list = append(list, readFDB(ctx, profile, d, byMAC)...)
			list = append(list, readQBRIDGE(ctx, profile, d, byMAC)...)
			list = append(list, readARP(ctx, profile, d, byIP)...)
			resCh <- result{list: list}
		}(d)
	}
	go func() { wg.Wait(); close(resCh) }()
	seen := map[string]bool{}
	out := []neighborSuggestion{}
	for res := range resCh {
		for _, n := range res.list {
			key := n.SourceDeviceID + "|" + n.TargetDeviceID
			rev := n.TargetDeviceID + "|" + n.SourceDeviceID
			if seen[key] || seen[rev] {
				continue
			}
			seen[key] = true
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceName < out[j].SourceName })
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func openSNMP(profile snmpProfileRow, target string) (*gosnmp.GoSNMP, error) {
	// RouterOS can time out on large GETBULK requests for the bridge FDB.
	// 10 OIDs per request is fast and reliable on the RB5009; retries cover
	// transient UDP loss without making discovery exceed its budget.
	g := &gosnmp.GoSNMP{Target: target, Port: uint16(profile.Port), Transport: "udp", Timeout: 2 * time.Second, Retries: 1, MaxOids: 10}
	switch strings.ToLower(profile.Version) {
	case "v1":
		g.Version = gosnmp.Version1
		g.Community = profile.Community
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 profile.V3User,
			AuthenticationProtocol:   authProto(profile.V3AuthProto),
			AuthenticationPassphrase: profile.V3AuthPass,
			PrivacyProtocol:          privProto(profile.V3PrivProto),
			PrivacyPassphrase:        profile.V3PrivPass,
		}
	default:
		g.Version = gosnmp.Version2c
		g.Community = profile.Community
	}
	if err := g.Connect(); err != nil {
		return nil, err
	}
	return g, nil
}

func snmpString(v any) string {
	switch t := v.(type) {
	case []byte:
		return strings.TrimSpace(string(t))
	case string:
		return strings.Trim(strings.TrimSpace(t), `"`)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func walkStrings(g *gosnmp.GoSNMP, base string) map[string]string {
	values := map[string]string{}
	_ = g.BulkWalk(base, func(pdu gosnmp.SnmpPDU) error {
		parts := strings.Split(strings.TrimPrefix(pdu.Name, "."), ".")
		if len(parts) == 0 {
			return nil
		}
		values[parts[len(parts)-1]] = snmpString(pdu.Value)
		return nil
	})
	return values
}

func matchDevice(remoteName string, byName map[string]neighborDevice) (neighborDevice, bool) {
	name := strings.Trim(strings.TrimSpace(remoteName), `"`)
	if d, ok := byName[strings.ToLower(name)]; ok {
		return d, true
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		if d, ok := byName[strings.ToLower(name[:i])]; ok {
			return d, true
		}
	}
	return neighborDevice{}, false
}

func readLLDP(ctx context.Context, profile snmpProfileRow, local neighborDevice, byName map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()

	readColumn := func(base string) map[string]string { return walkStrings(g, base) }
	names := readColumn("1.0.8802.1.1.2.1.4.1.1.9")
	ports := readColumn("1.0.8802.1.1.2.1.4.1.1.7")
	portDescs := readColumn("1.0.8802.1.1.2.1.4.1.1.8")
	ifNames := readColumn("1.3.6.1.2.1.31.1.1.1.1")
	ifDescr := readColumn("1.3.6.1.2.1.2.2.1.2")
	stpByIf := stpStateByIf(g)
	var out []neighborSuggestion
	for idx, remoteName := range names {
		remote, ok := matchDevice(remoteName, byName)
		if !ok || remote.ID == local.ID {
			continue
		}
		localPort := portDescs[idx]
		if localPort == "" {
			localPort = ports[idx]
		}
		ifIndex := resolveIfIndex(localPort, ifNames, ifDescr)
		out = append(out, neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: localPort, RemotePort: ports[idx],
			LocalIfIndex: ifIndex, RemoteIfIndex: remoteIfIndexFromPort(ports[idx]),
			STPState: stpByIf[ifIndex], Protocol: "LLDP",
		})
	}
	return out
}

func stpStateByIf(g *gosnmp.GoSNMP) map[int]string {
	basePortIf := walkStrings(g, "1.3.6.1.2.1.17.1.4.1.2")
	stpPorts := walkStrings(g, "1.3.6.1.2.1.17.2.15.1.3")
	out := map[int]string{}
	for port, state := range stpPorts {
		ifIndex, _ := strconv.Atoi(basePortIf[port])
		if ifIndex > 0 {
			out[ifIndex] = stpStateName(state)
		}
	}
	return out
}

func stpStateName(v string) string {
	switch strings.TrimSpace(v) {
	case "1":
		return "disabled"
	case "2":
		return "blocking"
	case "3":
		return "listening"
	case "4":
		return "learning"
	case "5":
		return "forwarding"
	case "6":
		return "broken"
	default:
		return ""
	}
}

// remoteIfIndexFromPort extracts an ifIndex when the neighbor advertises a
// numeric port ID. This works without SNMP access to the neighboring device.
func remoteIfIndexFromPort(port string) int {
	port = strings.TrimSpace(port)
	if port == "" {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func resolveIfIndex(port string, ifNames, ifDescr map[string]string) int {
	port = strings.TrimSpace(port)
	if port == "" {
		return 0
	}
	for idx, name := range ifNames {
		if strings.EqualFold(name, port) {
			n, _ := strconv.Atoi(idx)
			return n
		}
	}
	for idx, desc := range ifDescr {
		if strings.EqualFold(desc, port) {
			n, _ := strconv.Atoi(idx)
			return n
		}
	}
	if n, err := strconv.Atoi(port); err == nil {
		return n
	}
	return 0
}

func normalizeMAC(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	if b.Len() != 12 {
		return ""
	}
	return b.String()
}

func macFromOID(oid string) string {
	parts := strings.Split(strings.TrimPrefix(oid, "."), ".")
	if len(parts) < 6 {
		return ""
	}
	var b strings.Builder
	for _, p := range parts[len(parts)-6:] {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return ""
		}
		b.WriteString(fmt.Sprintf("%02x", n))
	}
	return b.String()
}

// readQBRIDGE reads the VLAN-aware forwarding database (Q-BRIDGE-MIB) and maps
// known device MACs to their VLAN and exact local ifIndex.
func readQBRIDGE(ctx context.Context, profile snmpProfileRow, local neighborDevice, byMAC map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()
	type entry struct {
		vlan int
		mac  string
		port string
	}
	var entries []entry
	_ = g.BulkWalk("1.3.6.1.2.1.17.7.1.2.2.1.2", func(pdu gosnmp.SnmpPDU) error {
		parts := strings.Split(strings.TrimPrefix(pdu.Name, "."), ".")
		if len(parts) >= 7 {
			vlan, _ := strconv.Atoi(parts[len(parts)-7])
			if mac := macFromOID(pdu.Name); mac != "" {
				// VLAN 0 is valid on bridges without VLAN filtering.
				entries = append(entries, entry{vlan: vlan, mac: mac, port: strings.TrimSpace(fmt.Sprint(pdu.Value))})
			}
		}
		return nil
	})
	if len(entries) == 0 {
		return nil
	}
	basePortIf := walkStrings(g, "1.3.6.1.2.1.17.1.4.1.2")
	ifNames := walkStrings(g, "1.3.6.1.2.1.31.1.1.1.1")
	ifDescr := walkStrings(g, "1.3.6.1.2.1.2.2.1.2")
	var out []neighborSuggestion
	seen := map[string]bool{}
	for _, e := range entries {
		remote, ok := byMAC[e.mac]
		if !ok || remote.ID == local.ID {
			continue
		}
		ifIndex, _ := strconv.Atoi(basePortIf[e.port])
		if ifIndex == 0 {
			continue
		}
		key := remote.ID + "|" + strconv.Itoa(e.vlan)
		if seen[key] {
			continue
		}
		seen[key] = true
		portName := ifNames[strconv.Itoa(ifIndex)]
		if portName == "" {
			portName = ifDescr[strconv.Itoa(ifIndex)]
		}
		out = append(out, neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: portName, LocalIfIndex: ifIndex, VLAN: e.vlan, Protocol: "Q-BRIDGE",
		})
	}
	return out
}

// readFDB reads the bridge forwarding table and maps known device MACs to the
// exact local bridge port and ifIndex. It also uses STP state to ignore ports
// that are administratively or topologically down.
func readFDB(ctx context.Context, profile snmpProfileRow, local neighborDevice, byMAC map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()
	fdbPort := map[string]string{}
	_ = g.BulkWalk("1.3.6.1.2.1.17.4.3.1.2", func(pdu gosnmp.SnmpPDU) error {
		if mac := macFromOID(pdu.Name); mac != "" {
			fdbPort[mac] = strings.TrimSpace(fmt.Sprint(pdu.Value))
		}
		return nil
	})
	if len(fdbPort) == 0 {
		return nil
	}
	basePortIf := walkStrings(g, "1.3.6.1.2.1.17.1.4.1.2")
	stpPorts := walkStrings(g, "1.3.6.1.2.1.17.2.15.1.3")
	ifNames := walkStrings(g, "1.3.6.1.2.1.31.1.1.1.1")
	ifDescr := walkStrings(g, "1.3.6.1.2.1.2.2.1.2")
	var out []neighborSuggestion
	for mac, port := range fdbPort {
		remote, ok := byMAC[mac]
		if !ok || remote.ID == local.ID {
			continue
		}
		ifIndex, _ := strconv.Atoi(basePortIf[port])
		if ifIndex == 0 {
			continue
		}
		state := stpStateName(stpPorts[port])
		if state == "disabled" || state == "blocking" || state == "broken" {
			continue
		}
		portName := ifNames[strconv.Itoa(ifIndex)]
		if portName == "" {
			portName = ifDescr[strconv.Itoa(ifIndex)]
		}
		out = append(out, neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: portName, LocalIfIndex: ifIndex, STPState: state, Protocol: "FDB",
		})
	}
	return out
}

// readARP correlates the ARP table with known device MACs. Routed subnets do
// not appear in the bridge FDB, so this fallback still surfaces router-to-host
// adjacencies on L3 boundaries.
func readARP(ctx context.Context, profile snmpProfileRow, local neighborDevice, byIP map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()
	type entry struct {
		mac     string
		ip      string
		ifIndex int
	}
	var entries []entry
	_ = g.BulkWalk("1.3.6.1.2.1.4.22.1.2", func(pdu gosnmp.SnmpPDU) error {
		parts := strings.Split(strings.TrimPrefix(pdu.Name, "."), ".")
		if len(parts) < 8 {
			return nil
		}
		ifIndex, _ := strconv.Atoi(parts[len(parts)-5])
		ip := strings.Join(parts[len(parts)-4:], ".")
		raw, ok := pdu.Value.([]byte)
		if !ok || len(raw) != 6 || ifIndex == 0 {
			return nil
		}
		mac := normalizeMAC(fmt.Sprintf("%02x%02x%02x%02x%02x%02x", raw[0], raw[1], raw[2], raw[3], raw[4], raw[5]))
		if mac == "" {
			return nil
		}
		entries = append(entries, entry{mac: mac, ip: ip, ifIndex: ifIndex})
		return nil
	})
	if len(entries) == 0 {
		return nil
	}
	basePortIf := walkStrings(g, "1.3.6.1.2.1.17.1.4.1.2")
	stpPorts := walkStrings(g, "1.3.6.1.2.1.17.2.15.1.3")
	ifNames := walkStrings(g, "1.3.6.1.2.1.31.1.1.1.1")
	ifDescr := walkStrings(g, "1.3.6.1.2.1.2.2.1.2")
	stpByIf := map[int]string{}
	for port, state := range stpPorts {
		if idx, _ := strconv.Atoi(basePortIf[port]); idx > 0 {
			stpByIf[idx] = stpStateName(state)
		}
	}
	var out []neighborSuggestion
	seen := map[string]bool{}
	for _, e := range entries {
		remote, ok := byIP[e.ip]
		if !ok || remote.ID == local.ID {
			continue
		}
		if seen[remote.ID] {
			continue
		}
		seen[remote.ID] = true
		portName := ifNames[strconv.Itoa(e.ifIndex)]
		if portName == "" {
			portName = ifDescr[strconv.Itoa(e.ifIndex)]
		}
		out = append(out, neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: portName, LocalIfIndex: e.ifIndex, STPState: stpByIf[e.ifIndex], Protocol: "ARP",
		})
	}
	return out
}

// readCDP reads the Cisco Discovery Protocol cache. It is commonly available
// on Cisco, Aruba and some HP/ProCurve switches even when LLDP is disabled.
func readCDP(ctx context.Context, profile snmpProfileRow, local neighborDevice, byName map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()
	const base = "1.3.6.1.4.1.9.9.23.1.2.1.1"
	names := walkStrings(g, base+".6")
	ports := walkStrings(g, base+".7")
	platforms := walkStrings(g, base+".8")
	var out []neighborSuggestion
	for idx, remoteName := range names {
		remote, ok := matchDevice(remoteName, byName)
		if !ok || remote.ID == local.ID {
			continue
		}
		_ = platforms[idx]
		out = append(out, neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: "", RemotePort: ports[idx], RemoteIfIndex: remoteIfIndexFromPort(ports[idx]), Protocol: "CDP",
		})
	}
	return out
}

// readMikroTik reads the RouterOS neighbor discovery table (MNDP). Rows are
// matched by IP first, then MAC, then identity, so links do not depend on the
// operator having named devices exactly like the neighbor identity.
func readMikroTik(ctx context.Context, profile snmpProfileRow, local neighborDevice, byName, byMAC, byIP map[string]neighborDevice) []neighborSuggestion {
	g, err := openSNMP(profile, local.IP)
	if err != nil {
		return nil
	}
	defer g.Conn.Close()
	const base = "1.3.6.1.4.1.14988.1.1.11.1.1"
	ips := walkStrings(g, base+".2")
	identities := walkStrings(g, base+".6")
	ifIndexes := walkStrings(g, base+".8")
	macs := map[string]string{}
	_ = g.BulkWalk(base+".3", func(pdu gosnmp.SnmpPDU) error {
		parts := strings.Split(strings.TrimPrefix(pdu.Name, "."), ".")
		if len(parts) == 0 {
			return nil
		}
		if raw, ok := pdu.Value.([]byte); ok && len(raw) == 6 {
			macs[parts[len(parts)-1]] = normalizeMAC(fmt.Sprintf("%02x%02x%02x%02x%02x%02x", raw[0], raw[1], raw[2], raw[3], raw[4], raw[5]))
		}
		return nil
	})
	ifNames := walkStrings(g, "1.3.6.1.2.1.31.1.1.1.1")
	ifDescr := walkStrings(g, "1.3.6.1.2.1.2.2.1.2")
	seen := map[string]int{}
	candidates := map[string]neighborSuggestion{}
	for idx, ip := range ips {
		ip = strings.TrimSpace(ip)
		score := 0
		remote, ok := byIP[ip]
		if ok {
			score = 2
		} else if mac := macs[idx]; mac != "" {
			remote, ok = byMAC[mac]
			if ok {
				score = 1
			}
		}
		if !ok {
			if remote, ok = matchDevice(identities[idx], byName); ok {
				score = 0
			}
		}
		if !ok || remote.ID == local.ID {
			continue
		}
		if prev, exists := seen[remote.ID]; exists && prev >= score {
			continue
		}
		seen[remote.ID] = score
		ifIndex, _ := strconv.Atoi(ifIndexes[idx])
		portName := ifNames[strconv.Itoa(ifIndex)]
		if portName == "" {
			portName = ifDescr[strconv.Itoa(ifIndex)]
		}
		candidates[remote.ID] = neighborSuggestion{
			SourceDeviceID: local.ID, TargetDeviceID: remote.ID,
			SourceName: local.Name, TargetName: remote.Name,
			LocalPort: portName, LocalIfIndex: ifIndex, Protocol: "MNDP",
		}
	}
	var out []neighborSuggestion
	for _, n := range candidates {
		out = append(out, n)
	}
	return out
}
