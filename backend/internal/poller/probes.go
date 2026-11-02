package poller

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/dsl"
	probing "github.com/prometheus-community/pro-bing"
)

func (p *Poller) probeICMP(ctx context.Context, j job) (probeResult, error) {
	host := cleanHost(j.IP)
	count := intCfg(j.Config, "count", 3)
	if count < 1 {
		count = 1
	}
	if count > 8 {
		count = 8
	}
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return probeResult{}, err
	}
	pinger.Count = count
	pinger.Timeout = time.Duration(j.Timeout) * time.Millisecond
	if pinger.Timeout < 500*time.Millisecond {
		pinger.Timeout = 500 * time.Millisecond
	}
	pinger.Interval = 100 * time.Millisecond
	pinger.SetPrivileged(true)

	done := make(chan struct{})
	go func() {
		_ = pinger.Run()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return probeResult{}, ctx.Err()
	case <-done:
	}
	stats := pinger.Statistics()
	loss := stats.PacketLoss
	if stats.PacketsRecv == 0 {
		return probeResult{OK: false, Loss: 100, Message: fmt.Sprintf("Sin respuesta ICMP (%d paquetes)", count)}, nil
	}
	rtt := float64(stats.AvgRtt.Microseconds()) / 1000.0
	warning := loss > 0 || rtt > floatCfg(j.Config, "warning_rtt_ms", 80)
	msg := fmt.Sprintf("%d/%d respuestas · %.1f ms", stats.PacketsRecv, count, rtt)
	if loss > 0 {
		msg = fmt.Sprintf("%d/%d respuestas · %.0f%% pérdida · %.1f ms", stats.PacketsRecv, count, loss, rtt)
	}
	return probeResult{OK: true, Warning: warning, HasValue: true, Value: rtt, RTT: rtt, Loss: loss, Message: msg}, nil
}

func (p *Poller) probeSNMP(ctx context.Context, j job) (probeResult, error) {
	if j.SNMPProfileID == nil {
		return probeResult{}, fmt.Errorf("el dispositivo no tiene perfil SNMP asignado")
	}
	host := cleanHost(j.IP)
	port := uint16(j.SNMPPort)
	if port == 0 {
		port = 161
	}
	g := &gosnmp.GoSNMP{
		Target:    host,
		Port:      port,
		Transport: "udp",
		Timeout:   time.Duration(j.Timeout) * time.Millisecond,
		Retries:   1,
		MaxOids:   40,
	}
	switch strings.ToLower(j.SNMPVersion) {
	case "v1":
		g.Version = gosnmp.Version1
		g.Community = j.SNMPCommunity
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		sec := gosnmp.NoAuthNoPriv
		switch strings.ToLower(j.SNMPv3SecLevel) {
		case "authnopriv":
			sec = gosnmp.AuthNoPriv
		case "authpriv":
			sec = gosnmp.AuthPriv
		}
		g.MsgFlags = sec
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 j.SNMPv3User,
			AuthenticationProtocol:   authProtocol(j.SNMPv3AuthProto),
			AuthenticationPassphrase: j.SNMPv3AuthPass,
			PrivacyProtocol:          privProtocol(j.SNMPv3PrivProto),
			PrivacyPassphrase:        j.SNMPv3PrivPass,
		}
	default:
		g.Version = gosnmp.Version2c
		g.Community = j.SNMPCommunity
	}
	if err := g.Connect(); err != nil {
		return probeResult{}, fmt.Errorf("SNMP connect: %w", err)
	}
	defer g.Conn.Close()

	oids := stringSliceCfg(j.Config, "oids")
	if len(oids) == 0 {
		return probeResult{}, fmt.Errorf("la sonda SNMP no define ningún OID")
	}
	oids = append(oids, "1.3.6.1.2.1.1.3.0")
	oids = uniqueStrings(oids)

	packet, err := g.Get(oids)
	if err != nil {
		return probeResult{}, fmt.Errorf("SNMP GET: %w", err)
	}
	values := map[string]any{}
	rates := map[string]float64{}
	firstNumeric := 0.0
	hasNumeric := false
	for _, pdu := range packet.Variables {
		oid := strings.TrimPrefix(pdu.Name, ".")
		val := normalizePDU(pdu)
		values[oid] = val
		p.store.SetOID(ctx, j.DeviceID, oid, val)
		if f, ok := val.(float64); ok {
			if !hasNumeric {
				firstNumeric = f
				hasNumeric = true
			}
			if rate := p.store.CounterRate(ctx, j.DeviceID, oid, f); rate != 0 {
				rates[oid] = rate
				p.store.SetRate(ctx, j.DeviceID, oid, rate)
			}
		}
	}

	// Optional table/column walks.
	columns := map[string][]float64{}
	for _, base := range stringSliceCfg(j.Config, "walks") {
		var vals []float64
		_ = g.BulkWalk(base, func(pdu gosnmp.SnmpPDU) error {
			oid := strings.TrimPrefix(pdu.Name, ".")
			val := normalizePDU(pdu)
			values[oid] = val
			p.store.SetOID(ctx, j.DeviceID, oid, val)
			if f, ok := val.(float64); ok {
				vals = append(vals, f)
			}
			return nil
		})
		if len(vals) > 0 {
			columns[base] = vals
			p.store.SetColumn(ctx, j.DeviceID, base, vals)
		}
	}

	dslCtx := &dsl.Context{OIDs: values, Columns: columns, Rates: rates}
	value := firstNumeric
	hasValue := hasNumeric
	if strings.TrimSpace(j.ValueExpr) != "" {
		if v, err := dsl.Parse(j.ValueExpr); err == nil {
			if out, err := v.Eval(dslCtx); err == nil {
				if f, ok := toNumber(out); ok {
					value = f
					hasValue = true
				}
			}
		}
	}
	if strings.TrimSpace(j.ErrorExpr) != "" {
		if expr, err := dsl.Parse(j.ErrorExpr); err == nil {
			if out, err := expr.Eval(dslCtx); err == nil {
				msg := strings.TrimSpace(dsl.Stringify(out))
				if msg != "" {
					return probeResult{OK: false, Message: msg, Value: value, HasValue: hasValue, OIDs: values, Columns: columns}, nil
				}
			}
		}
	}
	warning := false
	warningMsg := ""
	if strings.TrimSpace(j.WarningExpr) != "" {
		if expr, err := dsl.Parse(j.WarningExpr); err == nil {
			if out, err := expr.Eval(dslCtx); err == nil {
				warningMsg = strings.TrimSpace(dsl.Stringify(out))
				warning = warningMsg != ""
			}
		}
	}
	msg := "SNMP OK"
	if hasValue {
		msg = fmt.Sprintf("%s%s", dsl.Stringify(value), unitSuffix(j.Unit))
	}
	if warning {
		msg = warningMsg + " · " + msg
	}
	return probeResult{OK: true, Warning: warning, Value: value, HasValue: hasValue, Message: msg, OIDs: values, Columns: columns}, nil
}

func (p *Poller) probeTCP(ctx context.Context, j job) (probeResult, error) {
	port := intCfg(j.Config, "port", 0)
	if port == 0 {
		return probeResult{}, fmt.Errorf("puerto TCP no configurado")
	}
	host := cleanHost(j.IP)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	d := net.Dialer{Timeout: time.Duration(j.Timeout) * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return probeResult{}, fmt.Errorf("TCP %s: %w", addr, err)
	}
	defer conn.Close()
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	if send := strCfg(j.Config, "send"); send != "" {
		_ = conn.SetDeadline(time.Now().Add(time.Duration(j.Timeout) * time.Millisecond))
		_, _ = conn.Write([]byte(send))
		buf := make([]byte, 2048)
		n, _ := conn.Read(buf)
		banner := string(buf[:n])
		if re := strCfg(j.Config, "expect_regex"); re != "" {
			rx, err := regexp.Compile(re)
			if err != nil {
				return probeResult{}, fmt.Errorf("regex inválida: %w", err)
			}
			if !rx.MatchString(banner) {
				return probeResult{}, fmt.Errorf("banner no coincide con la expresión")
			}
		}
	}
	return probeResult{OK: true, Value: elapsed, RTT: elapsed, HasValue: true,
		Message: fmt.Sprintf("TCP %d abierto · %.1f ms", port, elapsed)}, nil
}

func (p *Poller) probeUDP(ctx context.Context, j job) (probeResult, error) {
	port := intCfg(j.Config, "port", 0)
	if port == 0 {
		return probeResult{}, fmt.Errorf("puerto UDP no configurado")
	}
	host := cleanHost(j.IP)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: time.Duration(j.Timeout) * time.Millisecond}
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return probeResult{}, fmt.Errorf("UDP %s: %w", addr, err)
	}
	defer conn.Close()
	start := time.Now()
	payload := strCfg(j.Config, "send")
	if payload == "" {
		payload = "netmonitor"
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return probeResult{}, fmt.Errorf("UDP %s: %w", addr, err)
	}
	if boolCfg(j.Config, "expect_response", false) {
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(j.Timeout) * time.Millisecond))
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		if err != nil {
			return probeResult{}, fmt.Errorf("UDP %s sin respuesta: %w", addr, err)
		}
		if re := strCfg(j.Config, "expect_regex"); re != "" {
			rx, err := regexp.Compile(re)
			if err != nil || !rx.Match(buf[:n]) {
				return probeResult{}, fmt.Errorf("respuesta UDP no coincide con la expresión")
			}
		}
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return probeResult{OK: true, Value: elapsed, RTT: elapsed, HasValue: true,
		Message: fmt.Sprintf("UDP %d · %.1f ms", port, elapsed)}, nil
}

func (p *Poller) probeBanner(ctx context.Context, j job, label string, defaultPort int) (probeResult, error) {
	port := intCfg(j.Config, "port", defaultPort)
	addr := net.JoinHostPort(cleanHost(j.IP), strconv.Itoa(port))
	d := net.Dialer{Timeout: time.Duration(j.Timeout) * time.Millisecond}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return probeResult{}, fmt.Errorf("%s %s: %w", label, addr, err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(j.Timeout) * time.Millisecond))
	buf := make([]byte, 2048)
	n, _ := conn.Read(buf)
	banner := strings.TrimSpace(string(buf[:n]))
	if expected := strCfg(j.Config, "expect_code"); expected != "" && !strings.HasPrefix(banner, expected) {
		return probeResult{}, fmt.Errorf("%s respondió %q", label, banner)
	}
	if re := strCfg(j.Config, "expect_regex"); re != "" {
		rx, err := regexp.Compile(re)
		if err != nil || !rx.MatchString(banner) {
			return probeResult{}, fmt.Errorf("%s banner no coincide: %q", label, banner)
		}
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return probeResult{OK: true, Value: elapsed, RTT: elapsed, HasValue: true,
		Message: fmt.Sprintf("%s %d · %s · %.1f ms", label, port, banner, elapsed)}, nil
}

func (p *Poller) probeHTTP(ctx context.Context, j job) (probeResult, error) {
	scheme := strCfg(j.Config, "scheme")
	if scheme == "" {
		scheme = "http"
	}
	rawURL := strCfg(j.Config, "url")
	if rawURL == "" {
		path := strCfg(j.Config, "path")
		if path == "" {
			path = "/"
		}
		port := intCfg(j.Config, "port", 0)
		host := cleanHost(j.IP)
		if port > 0 {
			rawURL = fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path)
		} else {
			rawURL = fmt.Sprintf("%s://%s%s", scheme, host, path)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return probeResult{}, err
	}
	client := &http.Client{
		Timeout: time.Duration(j.Timeout) * time.Millisecond,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec -- we explicitly report certificate health
			DialContext:     (&net.Dialer{Timeout: time.Duration(j.Timeout) * time.Millisecond}).DialContext,
		},
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return probeResult{}, fmt.Errorf("HTTP: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	minStatus := intCfg(j.Config, "expected_status_min", 200)
	maxStatus := intCfg(j.Config, "expected_status_max", 399)
	if resp.StatusCode < minStatus || resp.StatusCode > maxStatus {
		return probeResult{}, fmt.Errorf("HTTP %d fuera del rango esperado %d-%d", resp.StatusCode, minStatus, maxStatus)
	}
	warning := false
	note := ""
	if contains := strCfg(j.Config, "body_contains"); contains != "" && !strings.Contains(string(body), contains) {
		return probeResult{}, fmt.Errorf("el cuerpo HTTP no contiene %q", contains)
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		days := int(time.Until(cert.NotAfter).Hours() / 24)
		warnDays := intCfg(j.Config, "warn_tls_days", 14)
		failDays := intCfg(j.Config, "fail_tls_days", 3)
		if days <= failDays {
			return probeResult{}, fmt.Errorf("certificado TLS caduca en %d días", days)
		}
		if days <= warnDays {
			warning = true
			note = fmt.Sprintf(" · TLS caduca en %d días", days)
		}
	}
	return probeResult{OK: true, Warning: warning, Value: elapsed, RTT: elapsed, HasValue: true,
		Message: fmt.Sprintf("HTTP %d · %.1f ms%s", resp.StatusCode, elapsed, note)}, nil
}

func (p *Poller) probeDNS(ctx context.Context, j job) (probeResult, error) {
	name := strCfg(j.Config, "name")
	if name == "" {
		name = "example.com"
	}
	server := cleanHost(j.IP)
	port := intCfg(j.Config, "port", 53)
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Duration(j.Timeout) * time.Millisecond}
			return d.DialContext(ctx, "udp", net.JoinHostPort(server, strconv.Itoa(port)))
		},
	}
	start := time.Now()
	addrs, err := resolver.LookupHost(ctx, name)
	if err != nil {
		return probeResult{}, fmt.Errorf("DNS %s: %w", name, err)
	}
	if len(addrs) == 0 {
		return probeResult{}, fmt.Errorf("DNS sin registros para %s", name)
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	return probeResult{OK: true, Value: elapsed, RTT: elapsed, HasValue: true,
		Message: fmt.Sprintf("DNS %s → %s · %.1f ms", name, addrs[0], elapsed)}, nil
}

func (p *Poller) probeFunction(ctx context.Context, j job) (probeResult, error) {
	if strings.TrimSpace(j.ValueExpr) == "" {
		return probeResult{}, fmt.Errorf("la sonda Function no define value_expression")
	}
	oidMap := p.store.GetOIDs(ctx, j.DeviceID)
	cols := p.store.GetColumns(ctx, j.DeviceID)
	c := &dsl.Context{OIDs: oidMap, Columns: cols, Rates: map[string]float64{}}
	expr, err := dsl.Parse(j.ValueExpr)
	if err != nil {
		return probeResult{}, err
	}
	out, err := expr.Eval(c)
	if err != nil {
		return probeResult{}, err
	}
	msg := strings.TrimSpace(dsl.Stringify(out))
	if msg == "" {
		return probeResult{OK: false, Message: "la función devolvió un valor vacío"}, nil
	}
	f, _ := toNumber(out)
	return probeResult{OK: true, Value: f, HasValue: true, Message: msg}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func cleanHost(ip string) string {
	ip = strings.TrimSpace(ip)
	if strings.Contains(ip, "/") {
		ip = strings.Split(ip, "/")[0]
	}
	if ip == "" {
		return "127.0.0.1"
	}
	return ip
}

func normalizePDU(pdu gosnmp.SnmpPDU) any {
	switch pdu.Type {
	case gosnmp.OctetString:
		switch v := pdu.Value.(type) {
		case []byte:
			return strings.TrimSpace(string(v))
		case string:
			return strings.TrimSpace(v)
		}
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks, gosnmp.Uinteger32:
		switch v := pdu.Value.(type) {
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case uint:
			return float64(v)
		case uint32:
			return float64(v)
		case uint64:
			return float64(v)
		}
	case gosnmp.Counter64:
		switch v := pdu.Value.(type) {
		case uint64:
			return float64(v)
		case int64:
			return float64(v)
		}
	case gosnmp.IPAddress, gosnmp.ObjectIdentifier:
		return fmt.Sprint(pdu.Value)
	case gosnmp.Null, gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView:
		return ""
	}
	return pdu.Value
}

func authProtocol(s string) gosnmp.SnmpV3AuthProtocol {
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

func privProtocol(s string) gosnmp.SnmpV3PrivProtocol {
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

func toNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

func boolCfg(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func intCfg(m map[string]any, key string, def int) int {
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return def
}

func floatCfg(m map[string]any, key string, def float64) float64 {
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			return n
		}
	}
	return def
}

func strCfg(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func stringSliceCfg(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case []string:
		return t
	case string:
		if t == "" {
			return nil
		}
		return strings.Split(t, ",")
	}
	return nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func unitSuffix(u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "°") || u == "%" || strings.HasPrefix(u, "dB") {
		return u
	}
	return " " + u
}
