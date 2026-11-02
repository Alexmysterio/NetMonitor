package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/auth"
)

type snmpSystemInfo struct {
	Name          string `json:"name"`
	Descr         string `json:"descr"`
	ObjectID      string `json:"object_id"`
	Contact       string `json:"contact"`
	Location      string `json:"location"`
	Uptime        string `json:"uptime"`
	UptimeSeconds uint64 `json:"uptime_seconds"`
	Services      int    `json:"services"`
}

type snmpIPInfo struct {
	Address string `json:"address"`
	Mask    string `json:"mask"`
	IfIndex int    `json:"if_index"`
	IfName  string `json:"if_name"`
	Bcast   int    `json:"bcast"`
}

type snmpInterfaceInfo struct {
	IfIndex     int     `json:"if_index"`
	Name        string  `json:"name"`
	Descr       string  `json:"descr"`
	Alias       string  `json:"alias"`
	Type        int     `json:"type"`
	Mtu         int     `json:"mtu"`
	Mac         string  `json:"mac"`
	AdminStatus int     `json:"admin_status"`
	OperStatus  int     `json:"oper_status"`
	SpeedMbps   float64 `json:"speed_mbps"`
	InOctets    uint64  `json:"in_octets"`
	OutOctets   uint64  `json:"out_octets"`
	InErrors    uint64  `json:"in_errors"`
	OutErrors   uint64  `json:"out_errors"`
	InDiscards  uint64  `json:"in_discards"`
	OutDiscards uint64  `json:"out_discards"`
}

type snmpARPInfo struct {
	IP      string `json:"ip"`
	MAC     string `json:"mac"`
	IfIndex int    `json:"if_index"`
	IfName  string `json:"if_name"`
}

type snmpRouteInfo struct {
	Destination string `json:"destination"`
	Mask        string `json:"mask"`
	NextHop     string `json:"next_hop"`
	IfIndex     int    `json:"if_index"`
	IfName      string `json:"if_name"`
	Type        int    `json:"type"`
	Metric      int    `json:"metric"`
}

type snmpDeviceInfo struct {
	System     snmpSystemInfo      `json:"system"`
	IPs        []snmpIPInfo        `json:"ips"`
	Interfaces []snmpInterfaceInfo `json:"interfaces"`
	ARP        []snmpARPInfo       `json:"arp"`
	Routes     []snmpRouteInfo     `json:"routes"`
	Warnings   []string            `json:"warnings"`
}

func (s *Server) deviceSNMPInfo(w http.ResponseWriter, r *http.Request) {
	deviceID := chi.URLParam(r, "id")
	if u, ok := auth.UserFrom(r.Context()); ok && !s.canAccessDevice(r.Context(), u, deviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	profile, ip, err := s.loadSNMPProfile(r.Context(), deviceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "el dispositivo no tiene un perfil SNMP válido")
		return
	}
	g, err := openInfoSNMP(profile, ip)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer g.Conn.Close()

	info := snmpDeviceInfo{
		IPs:        []snmpIPInfo{},
		Interfaces: []snmpInterfaceInfo{},
		ARP:        []snmpARPInfo{},
		Routes:     []snmpRouteInfo{},
		Warnings:   []string{},
	}
	section := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("section")))
	switch section {
	case "", "system", "interfaces", "ips", "arp", "routes":
	default:
		writeError(w, http.StatusBadRequest, "sección SNMP inválida")
		return
	}
	all := section == ""
	var ifNames map[int]string
	if all || section == "system" {
		info.System = readSNMPSystem(g, &info.Warnings)
	}
	if all || section == "interfaces" {
		ifNames = readSNMPInterfaces(g, &info)
	} else if section == "ips" || section == "arp" || section == "routes" {
		ifNames = readSNMPIfNames(g)
	}
	if all || section == "ips" {
		readSNMPIPs(g, &info, ifNames)
	}
	if all || section == "arp" {
		readSNMPARP(g, &info, ifNames)
	}
	if all || section == "routes" {
		readSNMPRoutes(g, &info, ifNames)
	}

	if all && info.System.Name == "" && info.System.Descr == "" && len(info.Interfaces) == 0 {
		info.Warnings = append(info.Warnings, "El equipo no devolvió datos SNMP legibles")
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": info})
}

func openInfoSNMP(profile snmpProfileRow, target string) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{Target: target, Port: uint16(profile.Port), Transport: "udp", Timeout: 3 * time.Second, Retries: 1, MaxOids: 10}
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

func walkSNMP(g *gosnmp.GoSNMP, base string, fn func(index string, pdu gosnmp.SnmpPDU)) {
	prefix := strings.TrimSuffix(base, ".")
	_ = g.BulkWalk(base, func(pdu gosnmp.SnmpPDU) error {
		name := strings.TrimPrefix(pdu.Name, ".")
		index := strings.TrimPrefix(strings.TrimPrefix(name, prefix), ".")
		fn(index, pdu)
		return nil
	})
}

func readSNMPSystem(g *gosnmp.GoSNMP, warnings *[]string) snmpSystemInfo {
	oids := []string{
		"1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0", "1.3.6.1.2.1.1.3.0",
		"1.3.6.1.2.1.1.4.0", "1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.6.0",
		"1.3.6.1.2.1.1.7.0",
	}
	var out snmpSystemInfo
	packet, err := g.Get(oids)
	if err != nil {
		*warnings = append(*warnings, "No se pudo leer el grupo system: "+err.Error())
		return out
	}
	for _, v := range packet.Variables {
		switch strings.TrimPrefix(v.Name, ".") {
		case "1.3.6.1.2.1.1.1.0":
			out.Descr = snmpString(v.Value)
		case "1.3.6.1.2.1.1.2.0":
			out.ObjectID = snmpString(v.Value)
		case "1.3.6.1.2.1.1.3.0":
			if n, err := numeric(v.Value); err == nil && n >= 0 {
				out.UptimeSeconds = uint64(n / 100)
				out.Uptime = formatSNMPUptime(out.UptimeSeconds)
			}
		case "1.3.6.1.2.1.1.4.0":
			out.Contact = snmpString(v.Value)
		case "1.3.6.1.2.1.1.5.0":
			out.Name = snmpString(v.Value)
		case "1.3.6.1.2.1.1.6.0":
			out.Location = snmpString(v.Value)
		case "1.3.6.1.2.1.1.7.0":
			if n, err := numeric(v.Value); err == nil {
				out.Services = int(n)
			}
		}
	}
	return out
}

func readSNMPInterfaces(g *gosnmp.GoSNMP, info *snmpDeviceInfo) map[int]string {
	byIndex := map[int]*snmpInterfaceInfo{}
	get := func(idx int) *snmpInterfaceInfo {
		if byIndex[idx] == nil {
			byIndex[idx] = &snmpInterfaceInfo{IfIndex: idx}
		}
		return byIndex[idx]
	}
	walkIndex := func(base string, fn func(idx int, pdu gosnmp.SnmpPDU)) {
		walkSNMP(g, base, func(index string, pdu gosnmp.SnmpPDU) {
			idx, err := strconv.Atoi(index)
			if err != nil || idx <= 0 {
				return
			}
			fn(idx, pdu)
		})
	}
	walkIndex("1.3.6.1.2.1.31.1.1.1.1", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).Name = snmpString(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.2", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).Descr = snmpString(pdu.Value) })
	walkIndex("1.3.6.1.2.1.31.1.1.1.18", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).Alias = snmpString(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.3", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(idx).Type = int(n)
		}
	})
	walkIndex("1.3.6.1.2.1.2.2.1.4", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(idx).Mtu = int(n)
		}
	})
	walkIndex("1.3.6.1.2.1.2.2.1.6", func(idx int, pdu gosnmp.SnmpPDU) {
		if b, ok := pdu.Value.([]byte); ok && len(b) == 6 {
			get(idx).Mac = netMAC(b)
		}
	})
	walkIndex("1.3.6.1.2.1.2.2.1.7", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(idx).AdminStatus = int(n)
		}
	})
	walkIndex("1.3.6.1.2.1.2.2.1.8", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(idx).OperStatus = int(n)
		}
	})
	walkIndex("1.3.6.1.2.1.2.2.1.5", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(idx).SpeedMbps = n / 1_000_000
		}
	})
	walkIndex("1.3.6.1.2.1.31.1.1.1.15", func(idx int, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil && n > 0 {
			get(idx).SpeedMbps = n
		}
	})
	walkIndex("1.3.6.1.2.1.31.1.1.1.6", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).InOctets = asUint64(pdu.Value) })
	walkIndex("1.3.6.1.2.1.31.1.1.1.10", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).OutOctets = asUint64(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.14", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).InErrors = asUint64(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.20", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).OutErrors = asUint64(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.13", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).InDiscards = asUint64(pdu.Value) })
	walkIndex("1.3.6.1.2.1.2.2.1.19", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).OutDiscards = asUint64(pdu.Value) })

	out := make([]snmpInterfaceInfo, 0, len(byIndex))
	ifNames := map[int]string{}
	for _, v := range byIndex {
		if v.Name == "" {
			v.Name = v.Descr
		}
		if v.Name == "" {
			v.Name = fmt.Sprintf("ifIndex %d", v.IfIndex)
		}
		ifNames[v.IfIndex] = v.Name
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfIndex < out[j].IfIndex })
	info.Interfaces = out
	if len(out) == 0 {
		info.Warnings = append(info.Warnings, "No se pudo leer la tabla de interfaces (ifTable/ifXTable)")
	}
	return ifNames
}

func readSNMPIfNames(g *gosnmp.GoSNMP) map[int]string {
	names := walkStrings(g, "1.3.6.1.2.1.31.1.1.1.1")
	descr := walkStrings(g, "1.3.6.1.2.1.2.2.1.2")
	out := map[int]string{}
	for idx, name := range names {
		n, err := strconv.Atoi(idx)
		if err == nil {
			out[n] = name
		}
	}
	for idx, name := range descr {
		n, err := strconv.Atoi(idx)
		if err == nil && out[n] == "" {
			out[n] = name
		}
	}
	return out
}

func readSNMPIPs(g *gosnmp.GoSNMP, info *snmpDeviceInfo, ifNames map[int]string) {
	byIP := map[string]*snmpIPInfo{}
	get := func(ip string) *snmpIPInfo {
		if byIP[ip] == nil {
			byIP[ip] = &snmpIPInfo{Address: ip}
		}
		return byIP[ip]
	}
	walkSNMP(g, "1.3.6.1.2.1.4.20.1.1", func(index string, pdu gosnmp.SnmpPDU) {
		ip := snmpString(pdu.Value)
		if ip == "" {
			ip = index
		}
		get(ip)
	})
	walkSNMP(g, "1.3.6.1.2.1.4.20.1.2", func(index string, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(index).IfIndex = int(n)
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.20.1.3", func(index string, pdu gosnmp.SnmpPDU) {
		get(index).Mask = snmpString(pdu.Value)
	})
	walkSNMP(g, "1.3.6.1.2.1.4.20.1.4", func(index string, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(index).Bcast = int(n)
		}
	})
	out := make([]snmpIPInfo, 0, len(byIP))
	for _, v := range byIP {
		v.IfName = ifNames[v.IfIndex]
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	info.IPs = out
	if len(out) == 0 {
		info.Warnings = append(info.Warnings, "No se pudo leer la tabla de direcciones IP (ipAddrTable)")
	}
}

func readSNMPARP(g *gosnmp.GoSNMP, info *snmpDeviceInfo, ifNames map[int]string) {
	walkSNMP(g, "1.3.6.1.2.1.4.22.1.2", func(index string, pdu gosnmp.SnmpPDU) {
		parts := strings.Split(index, ".")
		if len(parts) < 5 {
			return
		}
		ifIndex, _ := strconv.Atoi(parts[0])
		ip := strings.Join(parts[1:5], ".")
		b, ok := pdu.Value.([]byte)
		if !ok || len(b) != 6 || ifIndex == 0 {
			return
		}
		info.ARP = append(info.ARP, snmpARPInfo{IP: ip, MAC: netMAC(b), IfIndex: ifIndex, IfName: ifNames[ifIndex]})
	})
	sort.Slice(info.ARP, func(i, j int) bool { return info.ARP[i].IP < info.ARP[j].IP })
}

func readSNMPRoutes(g *gosnmp.GoSNMP, info *snmpDeviceInfo, ifNames map[int]string) {
	// RouterOS and modern stacks expose ipCidrRouteTable; ipRouteTable is
	// legacy and often empty. The index is dest(4)+mask(4)+tos(1)+nextHop(4).
	type route struct {
		destination string
		mask        string
		nextHop     string
		ifIndex     int
		routeType   int
		metric      int
	}
	byKey := map[string]*route{}
	get := func(index string) *route {
		if byKey[index] == nil {
			r := &route{}
			parts := strings.Split(index, ".")
			if len(parts) >= 13 {
				r.destination = strings.Join(parts[0:4], ".")
				r.mask = strings.Join(parts[4:8], ".")
				r.nextHop = strings.Join(parts[9:13], ".")
			}
			byKey[index] = r
		}
		return byKey[index]
	}
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.1", func(index string, pdu gosnmp.SnmpPDU) {
		if v := snmpString(pdu.Value); v != "" {
			get(index).destination = v
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.2", func(index string, pdu gosnmp.SnmpPDU) {
		if v := snmpString(pdu.Value); v != "" {
			get(index).mask = v
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.4", func(index string, pdu gosnmp.SnmpPDU) {
		if v := snmpString(pdu.Value); v != "" {
			get(index).nextHop = v
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.5", func(index string, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(index).ifIndex = int(n)
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.6", func(index string, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(index).routeType = int(n)
		}
	})
	walkSNMP(g, "1.3.6.1.2.1.4.24.4.1.11", func(index string, pdu gosnmp.SnmpPDU) {
		if n, err := numeric(pdu.Value); err == nil {
			get(index).metric = int(n)
		}
	})
	out := make([]snmpRouteInfo, 0, len(byKey))
	for _, v := range byKey {
		if v.destination == "" {
			continue
		}
		out = append(out, snmpRouteInfo{
			Destination: v.destination,
			Mask:        v.mask,
			NextHop:     v.nextHop,
			IfIndex:     v.ifIndex,
			IfName:      ifNames[v.ifIndex],
			Type:        v.routeType,
			Metric:      v.metric,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Destination == out[j].Destination {
			return out[i].Mask < out[j].Mask
		}
		return out[i].Destination < out[j].Destination
	})
	info.Routes = out
}

func asUint64(v any) uint64 {
	switch t := v.(type) {
	case uint64:
		return t
	case uint32:
		return uint64(t)
	case int:
		if t >= 0 {
			return uint64(t)
		}
	case int64:
		if t >= 0 {
			return uint64(t)
		}
	case float64:
		if t >= 0 {
			return uint64(t)
		}
	case string:
		n, _ := strconv.ParseUint(t, 10, 64)
		return n
	}
	return 0
}

func formatSNMPUptime(seconds uint64) string {
	d := time.Duration(seconds) * time.Second
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute
	secs := d / time.Second
	if days > 0 {
		return fmt.Sprintf("%dd %02dh %02dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", hours, mins, secs)
	}
	return fmt.Sprintf("%dm %02ds", mins, secs)
}
