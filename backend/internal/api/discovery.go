package api

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/secure"
	probing "github.com/prometheus-community/pro-bing"
)

const maxDiscoveryHosts = 65536

type discoveryResult struct {
	IP                 string  `json:"ip"`
	Alive              bool    `json:"alive"`
	Name               string  `json:"name"`
	NameSource         string  `json:"name_source"`
	SysDescr           string  `json:"sys_descr"`
	SysObjectID        string  `json:"sys_object_id"`
	LatencyMS          float64 `json:"latency_ms"`
	ExistingDevice     string  `json:"existing_device_id"`
	ExistingDeviceName string  `json:"existing_device_name"`
	ExistingDeviceIP   string  `json:"existing_device_ip"`
	IPMismatch         bool    `json:"ip_mismatch"`
}

// discoveryScan performs a bounded sweep over one or more IPv4 CIDRs. Small
// scans stay synchronous so operators get a quick preview; larger scans use a
// faster probe mode and more workers, up to maxDiscoveryHosts.
func (s *Server) discoveryScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CIDR           string   `json:"cidr"`
		SNMPProfileID  string   `json:"snmp_profile_id"`
		SNMPProfileIDs []string `json:"snmp_profile_ids"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.CIDR) == "" {
		writeError(w, http.StatusBadRequest, "cidr is required")
		return
	}
	networks, total, err := parseDiscoveryCIDRs(req.CIDR)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if total > maxDiscoveryHosts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("el conjunto de rangos supera %d direcciones; divide la red o usa varios descubrimientos", maxDiscoveryHosts))
		return
	}
	ips := []string{}
	seen := map[string]bool{}
	for _, network := range networks {
		for _, ip := range enumerateIPv4(network) {
			if !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		writeError(w, http.StatusBadRequest, "los rangos no contienen direcciones utilizables")
		return
	}

	profileIDs := req.SNMPProfileIDs
	if len(profileIDs) == 0 && req.SNMPProfileID != "" {
		profileIDs = []string{req.SNMPProfileID}
	}
	if len(profileIDs) > 3 {
		writeError(w, http.StatusBadRequest, "máximo 3 perfiles SNMP por descubrimiento")
		return
	}
	profiles := []snmpProfileRow{}
	seenProfiles := map[string]bool{}
	for _, id := range profileIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenProfiles[id] {
			continue
		}
		seenProfiles[id] = true
		p, err := s.profileByID(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "perfil SNMP no encontrado")
			return
		}
		profiles = append(profiles, p)
	}

	mode := "reliable"
	workers := 40
	scanTimeout := 30 * time.Second
	if len(ips) > 512 {
		mode = "fast"
		workers = 256
		scanTimeout = 8 * time.Minute
	}
	type indexed struct {
		idx int
		res discoveryResult
	}
	type scanJob struct {
		idx int
		ip  string
	}
	results := make([]discoveryResult, len(ips))
	ctx, cancel := context.WithTimeout(r.Context(), scanTimeout)
	defer cancel()

	jobs := make(chan scanJob, workers*2)
	done := make(chan indexed, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				done <- indexed{idx: item.idx, res: probeHost(ctx, item.ip, profiles, mode)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i, ip := range ips {
			select {
			case <-ctx.Done():
				return
			case jobs <- scanJob{idx: i, ip: ip}:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	for item := range done {
		results[item.idx] = item.res
	}

	// Enrich existing devices with their DB UUID so the UI can add references
	// instead of creating duplicate rows. If the same name exists under a
	// different IP, flag it so the UI can offer an address update.
	user, _ := auth.UserFrom(r.Context())
	for i := range results {
		if !results[i].Alive {
			continue
		}
		var id, name, existingIP string
		err := s.pool.QueryRow(r.Context(), `
			SELECT id::text, name, host(primary_ip)
			FROM devices WHERE host(primary_ip)=$1 LIMIT 1
		`, results[i].IP).Scan(&id, &name, &existingIP)
		if err == nil {
			if !s.canAccessDevice(r.Context(), user, id) {
				continue
			}
			results[i].ExistingDevice = id
			results[i].ExistingDeviceName = name
			results[i].ExistingDeviceIP = existingIP
			continue
		}
		if results[i].Name == "" || results[i].Name == results[i].IP {
			continue
		}
		err = s.pool.QueryRow(r.Context(), `
			SELECT id::text, name, host(primary_ip)
			FROM devices
			WHERE lower(name)=lower($1)
			   OR lower(COALESCE(sys_name,''))=lower($1)
			   OR lower(COALESCE(dns_name,''))=lower($1)
			ORDER BY (lower(name)=lower($1) OR lower(COALESCE(dns_name,''))=lower($1)) DESC, name
			LIMIT 1
		`, results[i].Name).Scan(&id, &name, &existingIP)
		if err == nil {
			if !s.canAccessDevice(r.Context(), user, id) {
				continue
			}
			results[i].ExistingDevice = id
			results[i].ExistingDeviceName = name
			results[i].ExistingDeviceIP = existingIP
			results[i].IPMismatch = existingIP != results[i].IP
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

func probeHost(ctx context.Context, ip string, profiles []snmpProfileRow, mode string) discoveryResult {
	res := discoveryResult{IP: ip, Name: ip, NameSource: "ip"}
	count, timeout := 1, 800*time.Millisecond
	if strings.EqualFold(mode, "reliable") {
		count, timeout = 3, 1200*time.Millisecond
	} else if strings.EqualFold(mode, "fast") {
		count, timeout = 1, 700*time.Millisecond
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

	if len(profiles) == 0 {
		return res
	}
	retries := 0
	if strings.EqualFold(mode, "reliable") {
		retries = 1
	}
	snmpTimeout := timeout
	if snmpTimeout < 1500*time.Millisecond {
		snmpTimeout = 1500 * time.Millisecond
	}
	for i := range profiles {
		select {
		case <-ctx.Done():
			return res
		default:
		}
		if probeSNMP(ctx, ip, &profiles[i], retries, snmpTimeout, &res) {
			break
		}
	}
	return res
}

func probeSNMP(ctx context.Context, ip string, profile *snmpProfileRow, retries int, timeout time.Duration, res *discoveryResult) bool {
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
		return false
	}
	defer g.Conn.Close()
	packet, err := g.Get([]string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0"})
	if err != nil || len(packet.Variables) < 3 {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	default:
	}
	snmpName := strings.TrimSpace(snmpString(packet.Variables[0].Value))
	if snmpName != "" && res.NameSource != "dns" {
		res.Name = snmpName
		res.NameSource = "snmp"
	}
	res.SysDescr = snmpString(packet.Variables[1].Value)
	res.SysObjectID = snmpString(packet.Variables[2].Value)
	return true
}

func parseDiscoveryCIDRs(raw string) ([]*net.IPNet, uint64, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	if len(parts) == 0 {
		return nil, 0, fmt.Errorf("indica al menos un rango CIDR")
	}
	networks := make([]*net.IPNet, 0, len(parts))
	seen := map[string]bool{}
	var total uint64
	for _, part := range parts {
		_, network, err := net.ParseCIDR(strings.TrimSpace(part))
		if err != nil {
			return nil, 0, fmt.Errorf("CIDR inválido: %s", part)
		}
		if network.IP.To4() == nil {
			return nil, 0, fmt.Errorf("por ahora el descubrimiento solo soporta IPv4")
		}
		key := network.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		ones, bits := network.Mask.Size()
		size := uint64(1) << uint(bits-ones)
		total += size
		if total > maxDiscoveryHosts {
			return nil, total, fmt.Errorf("el conjunto de rangos supera %d direcciones", maxDiscoveryHosts)
		}
		networks = append(networks, network)
	}
	return networks, total, nil
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
