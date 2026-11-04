package api

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/dsl"
	"github.com/netmonitor/netmonitor/internal/secure"
	probing "github.com/prometheus-community/pro-bing"
	"golang.org/x/net/ipv4"
)

type snmpProfileRow struct {
	ID          string
	Version     string
	Port        int
	Community   string
	V3User      string
	V3SecLevel  string
	V3AuthProto string
	V3AuthPass  string
	V3PrivProto string
	V3PrivPass  string
}

func (s *Server) loadSNMPProfile(ctx context.Context, deviceID string) (snmpProfileRow, string, error) {
	var p snmpProfileRow
	var ip string
	err := s.pool.QueryRow(ctx, `
		SELECT host(d.primary_ip), COALESCE(sp.version,'v2c'), COALESCE(sp.port,161), COALESCE(sp.community,'public'),
		       COALESCE(sp.v3_username,''), COALESCE(sp.v3_security_level,''), COALESCE(sp.v3_auth_protocol,''),
		       COALESCE(sp.v3_auth_pass,''), COALESCE(sp.v3_priv_protocol,''), COALESCE(sp.v3_priv_pass,'')
		FROM devices d LEFT JOIN snmp_profiles sp ON sp.id=d.snmp_profile_id
		WHERE d.id=$1
	`, deviceID).Scan(&ip, &p.Version, &p.Port, &p.Community, &p.V3User, &p.V3SecLevel,
		&p.V3AuthProto, &p.V3AuthPass, &p.V3PrivProto, &p.V3PrivPass)
	p.Community, _ = secure.Decrypt(p.Community)
	p.V3AuthPass, _ = secure.Decrypt(p.V3AuthPass)
	p.V3PrivPass, _ = secure.Decrypt(p.V3PrivPass)
	return p, ip, err
}

func (s *Server) snmpWalk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		OID      string `json:"oid"`
	}
	if err := decodeBody(r, &req); err != nil || req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id is required")
		return
	}
	if req.OID == "" {
		req.OID = "1.3.6.1.2.1.1"
	}
	if u, ok := auth.UserFrom(r.Context()); ok && !s.canAccessDevice(r.Context(), u, req.DeviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}
	profile, ip, err := s.loadSNMPProfile(r.Context(), req.DeviceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "device has no SNMP profile")
		return
	}
	g := &gosnmp.GoSNMP{Target: ip, Port: uint16(profile.Port), Timeout: 4 * time.Second, Retries: 1, MaxOids: 40}
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
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer g.Conn.Close()
	results := []map[string]any{}
	err = g.BulkWalk(req.OID, func(pdu gosnmp.SnmpPDU) error {
		if len(results) >= 500 {
			return fmt.Errorf("limit reached")
		}
		oid := strings.TrimPrefix(pdu.Name, ".")
		results = append(results, map[string]any{
			"oid": oid, "type": pdu.Type.String(), "value": fmt.Sprint(pdu.Value),
			"name": s.mibName(r.Context(), oid),
		})
		return nil
	})
	if err != nil && len(results) == 0 {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": results})
}

func (s *Server) evaluateDSL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID   string `json:"device_id"`
		Expression string `json:"expression"`
	}
	if err := decodeBody(r, &req); err != nil || req.Expression == "" {
		writeError(w, http.StatusBadRequest, "expression is required")
		return
	}
	if req.DeviceID != "" {
		if u, ok := auth.UserFrom(r.Context()); ok && !s.canAccessDevice(r.Context(), u, req.DeviceID) {
			writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
			return
		}
	}
	ctx := &dsl.Context{
		OIDs:    map[string]any{},
		Columns: map[string][]float64{},
		Rates:   map[string]float64{},
		Vars:    map[string]string{},
	}
	if req.DeviceID != "" {
		ctx.OIDs = s.store.GetOIDs(r.Context(), req.DeviceID)
		ctx.Columns = s.store.GetColumns(r.Context(), req.DeviceID)
	}
	expr, err := dsl.Parse(req.Expression)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := expr.Eval(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"result": dsl.Stringify(out), "type": fmt.Sprintf("%T", out),
	}})
}

func (s *Server) pingTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  string `json:"host"`
		Count int    `json:"count"`
	}
	if err := decodeBody(r, &req); err != nil || req.Host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 3
	}
	host := strings.TrimSpace(req.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	pinger, err := probing.NewPinger(host)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pinger.Count = req.Count
	pinger.Timeout = 3 * time.Second
	pinger.Interval = 150 * time.Millisecond
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	st := pinger.Statistics()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"host": host, "sent": st.PacketsSent, "received": st.PacketsRecv,
		"loss_pct": st.PacketLoss, "min_ms": float64(st.MinRtt.Microseconds()) / 1000,
		"avg_ms": float64(st.AvgRtt.Microseconds()) / 1000, "max_ms": float64(st.MaxRtt.Microseconds()) / 1000,
		"stddev_ms": float64(st.StdDevRtt.Microseconds()) / 1000,
	}})
}

func (s *Server) dnsTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Host) == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	host := strings.TrimSpace(req.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	answers := []map[string]any{}
	add := func(kind, value string) {
		if value != "" {
			answers = append(answers, map[string]any{"type": kind, "value": value})
		}
	}
	if ips, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
		for _, ip := range ips {
			add("A/AAAA", ip)
		}
	} else {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if cname, err := net.DefaultResolver.LookupCNAME(ctx, host); err == nil {
		cname = strings.TrimSuffix(cname, ".")
		if !strings.EqualFold(cname, strings.TrimSuffix(host, ".")) {
			add("CNAME", cname)
		}
	}
	if mx, err := net.DefaultResolver.LookupMX(ctx, host); err == nil {
		for _, m := range mx {
			add("MX", fmt.Sprintf("%d %s", m.Pref, strings.TrimSuffix(m.Host, ".")))
		}
	}
	if ns, err := net.DefaultResolver.LookupNS(ctx, host); err == nil {
		for _, n := range ns {
			add("NS", strings.TrimSuffix(n.Host, "."))
		}
	}
	if txt, err := net.DefaultResolver.LookupTXT(ctx, host); err == nil {
		for _, t := range txt {
			add("TXT", t)
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if names, err := net.DefaultResolver.LookupAddr(ctx, host); err == nil {
			for _, n := range names {
				add("PTR", strings.TrimSuffix(n, "."))
			}
		}
	} else {
		if ips, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			for _, raw := range ips {
				if ip := net.ParseIP(raw); ip != nil {
					if names, err := net.DefaultResolver.LookupAddr(ctx, raw); err == nil {
						for _, n := range names {
							add("PTR", strings.TrimSuffix(n, "."))
						}
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"host": host, "answers": answers,
	}})
}

func (s *Server) tcpTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host      string `json:"host"`
		Port      int    `json:"port"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Host) == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port debe estar entre 1 y 65535")
		return
	}
	if req.TimeoutMS <= 0 || req.TimeoutMS > 15000 {
		req.TimeoutMS = 4000
	}
	host := strings.TrimSpace(req.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	address := net.JoinHostPort(host, strconv.Itoa(req.Port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, time.Duration(req.TimeoutMS)*time.Millisecond)
	latency := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"host": host, "port": req.Port, "open": false, "latency_ms": latency, "error": err.Error(),
		}})
		return
	}
	defer conn.Close()
	data := map[string]any{"host": host, "port": req.Port, "open": true, "latency_ms": latency}
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		data["remote_ip"] = addr.IP.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func (s *Server) httpTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL       string `json:"url"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	raw := strings.TrimSpace(req.URL)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	if req.TimeoutMS <= 0 || req.TimeoutMS > 20000 {
		req.TimeoutMS = 8000
	}
	client := &http.Client{
		Timeout: time.Duration(req.TimeoutMS) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("demasiadas redirecciones")
			}
			return nil
		},
	}
	start := time.Now()
	resp, err := client.Head(raw)
	if err == nil && (resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented) {
		resp.Body.Close()
		getReq, reqErr := http.NewRequest(http.MethodGet, raw, nil)
		if reqErr != nil {
			err = reqErr
		} else {
			getReq.Header.Set("Range", "bytes=0-0")
			resp, err = client.Do(getReq)
		}
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
			"url": raw, "ok": false, "error": err.Error(),
		}})
		return
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)
	data := map[string]any{
		"url": raw, "ok": resp.StatusCode < 400, "status_code": resp.StatusCode,
		"status": resp.Status, "latency_ms": float64(time.Since(start).Microseconds()) / 1000,
		"server": resp.Header.Get("Server"), "content_type": resp.Header.Get("Content-Type"),
		"redirect": resp.Request.URL.String(),
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		data["tls_subject"] = cert.Subject.CommonName
		data["tls_issuer"] = cert.Issuer.CommonName
		data["tls_expires_at"] = cert.NotAfter
		data["tls_days_left"] = int(time.Until(cert.NotAfter).Hours() / 24)
		data["tls_version"] = tls.VersionName(resp.TLS.Version)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

var traceMu sync.Mutex

func (s *Server) tracerouteTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host      string `json:"host"`
		MaxHops   int    `json:"max_hops"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Host) == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	host := strings.TrimSpace(req.Host)
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ipAddr, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ip := ipAddr.IP.To4()
	if ip == nil {
		writeError(w, http.StatusBadRequest, "solo se admite IPv4")
		return
	}
	if req.MaxHops <= 0 || req.MaxHops > 40 {
		req.MaxHops = 30
	}
	if req.TimeoutMS <= 0 || req.TimeoutMS > 5000 {
		req.TimeoutMS = 1500
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	hops, err := traceIPv4(ip, req.MaxHops, time.Duration(req.TimeoutMS)*time.Millisecond)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"host": host, "target": ip.String(), "hops": hops,
	}})
}

func traceIPv4(target net.IP, maxHops int, timeout time.Duration) ([]map[string]any, error) {
	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir ICMP: %w", err)
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc, true)
	hops := make([]map[string]any, 0, maxHops)
	buf := make([]byte, 1500)
	for ttl := 1; ttl <= maxHops; ttl++ {
		port := 33434 + ttl
		udp, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: target, Port: port})
		if err != nil {
			return hops, err
		}
		uc := ipv4.NewConn(udp)
		if err := uc.SetTTL(ttl); err != nil {
			udp.Close()
			return hops, err
		}
		start := time.Now()
		_, _ = udp.Write([]byte("netmonitor-trace"))
		_ = pc.SetReadDeadline(time.Now().Add(timeout))
		hop := map[string]any{"hop": ttl, "ip": "*", "rtt_ms": nil, "final": false}
		for {
			n, _, src, err := pc.ReadFrom(buf)
			if err != nil {
				break
			}
			off := 0
			if n > 20 && buf[0]>>4 == 4 {
				off = int(buf[0]&0x0f) * 4
			}
			if off+8 > n {
				continue
			}
			icmpType := buf[off]
			if icmpType != 11 && icmpType != 3 {
				continue
			}
			inner := off + 8
			if inner+20 > n || buf[inner]>>4 != 4 {
				continue
			}
			ihl := int(buf[inner]&0x0f) * 4
			udpOff := inner + ihl
			if udpOff+4 > n {
				continue
			}
			if int(binary.BigEndian.Uint16(buf[udpOff+2:udpOff+4])) != port {
				continue
			}
			hop["ip"] = src.String()
			hop["rtt_ms"] = float64(time.Since(start).Microseconds()) / 1000
			if icmpType == 3 {
				hop["final"] = true
			}
			break
		}
		udp.Close()
		hops = append(hops, hop)
		if final, _ := hop["final"].(bool); final {
			break
		}
	}
	return hops, nil
}

func authProto(s string) gosnmp.SnmpV3AuthProtocol {
	switch strings.ToUpper(s) {
	case "MD5":
		return gosnmp.MD5
	case "SHA224":
		return gosnmp.SHA224
	case "SHA256":
		return gosnmp.SHA256
	case "SHA384":
		return gosnmp.SHA384
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.SHA
	}
}

func privProto(s string) gosnmp.SnmpV3PrivProtocol {
	switch strings.ToUpper(s) {
	case "DES":
		return gosnmp.DES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.AES
	}
}
