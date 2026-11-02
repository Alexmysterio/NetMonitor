package api

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/secure"
	probing "github.com/prometheus-community/pro-bing"
)

type discoveryResult struct {
	IP             string  `json:"ip"`
	Alive          bool    `json:"alive"`
	Name           string  `json:"name"`
	NameSource     string  `json:"name_source"`
	SysDescr       string  `json:"sys_descr"`
	SysObjectID    string  `json:"sys_object_id"`
	LatencyMS      float64 `json:"latency_ms"`
	ExistingDevice string  `json:"existing_device_id"`
}

// discoveryScan performs a bounded /24-style sweep. It intentionally stays
// synchronous with a hard timeout: operators expect a quick preview before
// committing discovered devices to a map.
func (s *Server) discoveryScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CIDR          string `json:"cidr"`
		SNMPProfileID string `json:"snmp_profile_id"`
	}
	if err := decodeBody(r, &req); err != nil || req.CIDR == "" {
		writeError(w, http.StatusBadRequest, "cidr is required")
		return
	}
	_, network, err := net.ParseCIDR(strings.TrimSpace(req.CIDR))
	if err != nil {
		writeError(w, http.StatusBadRequest, "CIDR inválido")
		return
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		writeError(w, http.StatusBadRequest, "por ahora el descubrimiento soporta IPv4")
		return
	}
	if ones < 24 {
		writeError(w, http.StatusBadRequest, "usa un rango /24 o menor para evitar barridos masivos")
		return
	}
	ips := enumerateIPv4(network)
	if len(ips) > 512 {
		writeError(w, http.StatusBadRequest, "demasiadas direcciones")
		return
	}

	var profile *snmpProfileRow
	if req.SNMPProfileID != "" {
		p, err := s.profileByID(r.Context(), req.SNMPProfileID)
		if err == nil {
			profile = &p
		}
	}

	type indexed struct {
		idx int
		res discoveryResult
	}
	results := make([]discoveryResult, len(ips))
	sem := make(chan struct{}, 40)
	var wg sync.WaitGroup
	done := make(chan indexed, len(ips))
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
spawnLoop:
	for i, ip := range ips {
		select {
		case <-ctx.Done():
			break spawnLoop
		default:
		}
		wg.Add(1)
		go func(idx int, ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := probeHost(ctx, ip, profile, "reliable")
			done <- indexed{idx: idx, res: res}
		}(i, ip)
	}
	go func() { wg.Wait(); close(done) }()
	for item := range done {
		results[item.idx] = item.res
	}

	// Enrich existing devices with their DB UUID so the UI can add references
	// instead of creating duplicate rows.
	for i := range results {
		if !results[i].Alive {
			continue
		}
		var id string
		if err := s.pool.QueryRow(r.Context(), `SELECT id::text FROM devices WHERE host(primary_ip)=$1 LIMIT 1`, results[i].IP).Scan(&id); err == nil {
			results[i].ExistingDevice = id
		}
	}
	out := make([]discoveryResult, 0, len(results))
	for _, res := range results {
		if res.Alive {
			out = append(out, res)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func probeHost(ctx context.Context, ip string, profile *snmpProfileRow, mode string) discoveryResult {
	res := discoveryResult{IP: ip, Name: ip, NameSource: "ip"}
	count, timeout := 1, 800*time.Millisecond
	if strings.EqualFold(mode, "reliable") {
		count, timeout = 3, 1200*time.Millisecond
	}
	pinger, err := probing.NewPinger(ip)
	if err != nil {
		return res
	}
	pinger.Count = count
	pinger.Timeout = timeout
	pinger.Interval = 50 * time.Millisecond
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return res
	}
	st := pinger.Statistics()
	if st.PacketsRecv == 0 {
		return res
	}
	res.Alive = true
	res.LatencyMS = float64(st.AvgRtt.Microseconds()) / 1000

	// Name preference: DNS (PTR) → SNMP sysName → IP.
	dnsCtx, cancel := context.WithTimeout(ctx, 650*time.Millisecond)
	if names, err := net.DefaultResolver.LookupAddr(dnsCtx, ip); err == nil && len(names) > 0 {
		res.Name = strings.TrimSuffix(names[0], ".")
		res.NameSource = "dns"
	}
	cancel()

	if profile == nil {
		return res
	}
	select {
	case <-ctx.Done():
		return res
	default:
	}
	retries := 0
	if strings.EqualFold(mode, "reliable") {
		retries = 1
	}
	g := &gosnmp.GoSNMP{Target: ip, Port: uint16(profile.Port), Transport: "udp", Timeout: timeout, Retries: retries}
	switch strings.ToLower(profile.Version) {
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
		return res
	}
	defer g.Conn.Close()
	packet, err := g.Get([]string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0"})
	if err != nil || len(packet.Variables) < 3 {
		return res
	}
	snmpName := strings.TrimSpace(snmpString(packet.Variables[0].Value))
	if snmpName != "" && res.NameSource != "dns" {
		res.Name = snmpName
		res.NameSource = "snmp"
	}
	res.SysDescr = snmpString(packet.Variables[1].Value)
	res.SysObjectID = snmpString(packet.Variables[2].Value)
	return res
}

func enumerateIPv4(network *net.IPNet) []string {
	start := binary.BigEndian.Uint32(network.IP.To4())
	ones, bits := network.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	var out []string
	for i := uint32(1); i < size-1; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, start+i)
		out = append(out, ip.String())
	}
	return out
}

func (s *Server) profileByID(ctx context.Context, id string) (snmpProfileRow, error) {
	var p snmpProfileRow
	err := s.pool.QueryRow(ctx, `
		SELECT version, port, community, COALESCE(v3_username,''), COALESCE(v3_security_level,''),
		       COALESCE(v3_auth_protocol,''), COALESCE(v3_auth_pass,''), COALESCE(v3_priv_protocol,''), COALESCE(v3_priv_pass,'')
		FROM snmp_profiles WHERE id=$1
	`, id).Scan(&p.Version, &p.Port, &p.Community, &p.V3User, &p.V3SecLevel,
		&p.V3AuthProto, &p.V3AuthPass, &p.V3PrivProto, &p.V3PrivPass)
	p.Community, _ = secure.Decrypt(p.Community)
	p.V3AuthPass, _ = secure.Decrypt(p.V3AuthPass)
	p.V3PrivPass, _ = secure.Decrypt(p.V3PrivPass)
	return p, err
}
