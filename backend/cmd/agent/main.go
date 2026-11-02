package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gosnmp/gosnmp"
	probing "github.com/prometheus-community/pro-bing"

	"github.com/netmonitor/netmonitor/internal/agents"
	"github.com/netmonitor/netmonitor/internal/dsl"
)

const version = "0.1.0"

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	url := env("CORE_WS_URL", "ws://netmonitor:8080/api/v1/agent/ws")
	token := os.Getenv("AGENT_TOKEN")
	if token == "" {
		log.Fatal("AGENT_TOKEN is required")
	}
	dialer, err := makeDialer()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; cancel() }()

	for {
		if err := run(ctx, url, token, dialer); err != nil {
			log.Printf("agent connection: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func makeDialer() (*websocket.Dialer, error) {
	insecure := envBool("AGENT_INSECURE_TLS", false)
	certFile, keyFile, caFile := os.Getenv("AGENT_TLS_CERT"), os.Getenv("AGENT_TLS_KEY"), os.Getenv("AGENT_TLS_CA")
	if certFile == "" && keyFile == "" && caFile == "" && !insecure {
		return websocket.DefaultDialer, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("AGENT_TLS_CERT and AGENT_TLS_KEY must be provided together")
	}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("agent client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("agent CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("agent CA is invalid")
		}
		cfg.RootCAs = pool
	}
	if name := os.Getenv("AGENT_TLS_SERVER_NAME"); name != "" {
		cfg.ServerName = name
	}
	d := *websocket.DefaultDialer
	d.TLSClientConfig = cfg
	return &d, nil
}

func run(ctx context.Context, baseURL, token string, dialer *websocket.Dialer) error {
	dialURL := strings.TrimRight(baseURL, "/")
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, _, err := dialer.DialContext(ctx, dialURL, headers)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("connected to %s", baseURL)
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	if err := write(map[string]any{"type": "HELLO", "version": version}); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var msg struct {
				Type   string      `json:"type"`
				ID     string      `json:"id"`
				Job    agents.Job  `json:"job"`
				Update *updateSpec `json:"update"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg.Type {
			case "JOB":
				log.Printf("job %s · %s · %s", msg.Job.DeviceID, msg.Job.ProbeType, msg.Job.IP)
				go func(id string, job agents.Job) {
					result := execute(job)
					_ = write(map[string]any{"type": "RESULT", "id": id, "result": result})
				}(msg.ID, msg.Job)
			case "UPDATE":
				if msg.Update != nil {
					go func(u updateSpec) {
						log.Printf("update requested: %s", u.Version)
						if err := applyUpdate(u); err != nil {
							log.Printf("update failed: %v", err)
						}
					}(*msg.Update)
				}
			case "PING":
				_ = write(map[string]any{"type": "PONG"})
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-done:
		return fmt.Errorf("connection closed")
	}
}

func execute(job agents.Job) agents.Result {
	switch job.ProbeType {
	case "icmp":
		return probeICMP(job)
	case "tcp":
		return probeTCP(job)
	case "http":
		return probeHTTP(job)
	case "dns":
		return probeDNS(job)
	case "snmp":
		return probeSNMP(job)
	case "udp":
		return probeUDP(job)
	case "ssh":
		return probeBanner(job, "SSH", 22)
	case "smtp":
		return probeBanner(job, "SMTP", 25)
	case "ftp":
		return probeBanner(job, "FTP", 21)
	case "discovery":
		return probeDiscovery(job)
	default:
		return agents.Result{OK: false, Message: "probe type not supported by agent: " + job.ProbeType}
	}
}

func probeUDP(job agents.Job) agents.Result {
	port := intCfg(job.Config, "port", 0)
	if port == 0 {
		return agents.Result{Message: "puerto UDP no configurado"}
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(job.IP, strconv.Itoa(port)), time.Duration(timeoutMS(job))*time.Millisecond)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	defer conn.Close()
	start := time.Now()
	payload := strCfg(job.Config, "send")
	if payload == "" {
		payload = "netmonitor"
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return agents.Result{Message: err.Error()}
	}
	if boolCfg(job.Config, "expect_response", false) {
		_ = conn.SetReadDeadline(time.Now().Add(time.Duration(timeoutMS(job)) * time.Millisecond))
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		if err != nil {
			return agents.Result{Message: err.Error()}
		}
		if re := strCfg(job.Config, "expect_regex"); re != "" {
			rx, err := regexp.Compile(re)
			if err != nil || !rx.Match(buf[:n]) {
				return agents.Result{Message: "respuesta UDP no coincide"}
			}
		}
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return agents.Result{OK: true, HasValue: true, Value: elapsed, RTT: elapsed,
		Message: fmt.Sprintf("UDP %d · %.1f ms", port, elapsed)}
}

func probeBanner(job agents.Job, label string, defaultPort int) agents.Result {
	port := intCfg(job.Config, "port", defaultPort)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(job.IP, strconv.Itoa(port)), time.Duration(timeoutMS(job))*time.Millisecond)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(timeoutMS(job)) * time.Millisecond))
	buf := make([]byte, 2048)
	n, _ := conn.Read(buf)
	banner := strings.TrimSpace(string(buf[:n]))
	expected := strCfg(job.Config, "expect_code")
	if expected != "" && !strings.HasPrefix(banner, expected) {
		return agents.Result{Message: fmt.Sprintf("%s inesperado: %q", label, banner)}
	}
	if re := strCfg(job.Config, "expect_regex"); re != "" {
		rx, err := regexp.Compile(re)
		if err != nil || !rx.MatchString(banner) {
			return agents.Result{Message: fmt.Sprintf("%s banner no coincide: %q", label, banner)}
		}
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return agents.Result{OK: true, HasValue: true, Value: elapsed, RTT: elapsed,
		Message: fmt.Sprintf("%s %d · %s · %.1f ms", label, port, banner, elapsed)}
}

func probeICMP(job agents.Job) agents.Result {
	count := intCfg(job.Config, "count", 3)
	if count < 1 {
		count = 1
	}
	pinger, err := probing.NewPinger(job.IP)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	pinger.Count = count
	pinger.Timeout = time.Duration(timeoutMS(job)) * time.Millisecond
	pinger.Interval = 100 * time.Millisecond
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return agents.Result{Message: err.Error()}
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return agents.Result{OK: false, Loss: 100, Message: fmt.Sprintf("Sin respuesta ICMP (%d paquetes)", count)}
	}
	rtt := float64(stats.AvgRtt.Microseconds()) / 1000
	loss := stats.PacketLoss
	return agents.Result{
		OK: true, HasValue: true, Value: rtt, RTT: rtt, Loss: loss,
		Warning: loss > 0 || rtt > floatCfg(job.Config, "warning_rtt_ms", 80),
		Message: fmt.Sprintf("%d/%d respuestas · %.1f ms · %.0f%% pérdida", stats.PacketsRecv, count, rtt, loss),
	}
}

func probeTCP(job agents.Job) agents.Result {
	port := intCfg(job.Config, "port", 0)
	if port == 0 {
		return agents.Result{Message: "puerto TCP no configurado"}
	}
	start := time.Now()
	d := net.Dialer{Timeout: time.Duration(timeoutMS(job)) * time.Millisecond}
	conn, err := d.Dial("tcp", net.JoinHostPort(job.IP, strconv.Itoa(port)))
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	defer conn.Close()
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return agents.Result{OK: true, HasValue: true, Value: elapsed, RTT: elapsed,
		Message: fmt.Sprintf("TCP %d abierto · %.1f ms", port, elapsed)}
}

func probeHTTP(job agents.Job) agents.Result {
	scheme := strCfg(job.Config, "scheme")
	if scheme == "" {
		scheme = "http"
	}
	rawURL := strCfg(job.Config, "url")
	if rawURL == "" {
		path := strCfg(job.Config, "path")
		if path == "" {
			path = "/"
		}
		port := intCfg(job.Config, "port", 0)
		host := job.IP
		if port > 0 {
			rawURL = fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path)
		} else {
			rawURL = fmt.Sprintf("%s://%s%s", scheme, host, path)
		}
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	client := &http.Client{Timeout: time.Duration(timeoutMS(job)) * time.Millisecond, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	min := intCfg(job.Config, "expected_status_min", 200)
	max := intCfg(job.Config, "expected_status_max", 399)
	if resp.StatusCode < min || resp.StatusCode > max {
		return agents.Result{Message: fmt.Sprintf("HTTP %d fuera de rango %d-%d", resp.StatusCode, min, max)}
	}
	if contains := strCfg(job.Config, "body_contains"); contains != "" && !strings.Contains(string(body), contains) {
		return agents.Result{Message: "el cuerpo HTTP no contiene el texto esperado"}
	}
	warning := false
	note := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		days := int(time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24)
		if days <= intCfg(job.Config, "fail_tls_days", 3) {
			return agents.Result{Message: fmt.Sprintf("certificado TLS caduca en %d días", days)}
		}
		if days <= intCfg(job.Config, "warn_tls_days", 14) {
			warning = true
			note = fmt.Sprintf(" · TLS caduca en %d días", days)
		}
	}
	return agents.Result{OK: true, HasValue: true, Value: elapsed, RTT: elapsed, Warning: warning,
		Message: fmt.Sprintf("HTTP %d · %.1f ms%s", resp.StatusCode, elapsed, note)}
}

func probeDNS(job agents.Job) agents.Result {
	name := strCfg(job.Config, "name")
	if name == "" {
		name = "example.com"
	}
	port := intCfg(job.Config, "port", 53)
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: time.Duration(timeoutMS(job)) * time.Millisecond}
		return d.DialContext(ctx, "udp", net.JoinHostPort(job.IP, strconv.Itoa(port)))
	}}
	start := time.Now()
	addrs, err := resolver.LookupHost(context.Background(), name)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	return agents.Result{OK: true, HasValue: true, Value: elapsed, RTT: elapsed,
		Message: fmt.Sprintf("DNS %s → %d registros · %.1f ms", name, len(addrs), elapsed)}
}

func probeSNMP(job agents.Job) agents.Result {
	profile := job.SNMP
	g := &gosnmp.GoSNMP{Target: job.IP, Port: uint16(profile.Port), Transport: "udp",
		Timeout: time.Duration(timeoutMS(job)) * time.Millisecond, Retries: 1, MaxOids: 40}
	switch strings.ToLower(profile.Version) {
	case "v1":
		g.Version = gosnmp.Version1
		g.Community = profile.Community
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		sec := gosnmp.NoAuthNoPriv
		switch strings.ToLower(profile.Security) {
		case "authnopriv":
			sec = gosnmp.AuthNoPriv
		case "authpriv":
			sec = gosnmp.AuthPriv
		}
		g.MsgFlags = sec
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 profile.User,
			AuthenticationProtocol:   authProtocol(profile.AuthProto),
			AuthenticationPassphrase: profile.AuthPass,
			PrivacyProtocol:          privProtocol(profile.PrivProto),
			PrivacyPassphrase:        profile.PrivPass,
		}
	default:
		g.Version = gosnmp.Version2c
		g.Community = profile.Community
	}
	if err := g.Connect(); err != nil {
		return agents.Result{Message: err.Error()}
	}
	defer g.Conn.Close()
	oids := stringList(job.Config["oids"])
	if len(oids) == 0 {
		return agents.Result{Message: "la sonda SNMP no define OIDs"}
	}
	packet, err := g.Get(oids)
	if err != nil {
		return agents.Result{Message: err.Error()}
	}
	values := map[string]any{}
	first, has := 0.0, false
	for _, pdu := range packet.Variables {
		oid := strings.TrimPrefix(pdu.Name, ".")
		val := normalize(pdu)
		values[oid] = val
		if f, ok := val.(float64); ok && !has {
			first, has = f, true
		}
	}
	ctx := &dsl.Context{OIDs: values, Columns: map[string][]float64{}, Rates: map[string]float64{}}
	for _, base := range stringList(job.Config["walks"]) {
		var vals []float64
		_ = g.BulkWalk(base, func(pdu gosnmp.SnmpPDU) error {
			val := normalize(pdu)
			values[strings.TrimPrefix(pdu.Name, ".")] = val
			if f, ok := val.(float64); ok {
				vals = append(vals, f)
			}
			return nil
		})
		if len(vals) > 0 {
			ctx.Columns[base] = vals
		}
	}
	value, hasValue := first, has
	if job.ValueExpr != "" {
		if expr, err := dsl.Parse(job.ValueExpr); err == nil {
			if out, err := expr.Eval(ctx); err == nil {
				if f, ok := toNumber(out); ok {
					value, hasValue = f, true
				}
			}
		}
	}
	if job.ErrorExpr != "" {
		if expr, err := dsl.Parse(job.ErrorExpr); err == nil {
			if out, err := expr.Eval(ctx); err == nil && strings.TrimSpace(dsl.Stringify(out)) != "" {
				return agents.Result{OK: false, Message: dsl.Stringify(out), Value: value, HasValue: hasValue, OIDs: values}
			}
		}
	}
	warning, msg := false, "SNMP OK"
	if hasValue {
		msg = fmt.Sprintf("%s%s", dsl.Stringify(value), unitSuffix(job.Unit))
	}
	if job.WarningExpr != "" {
		if expr, err := dsl.Parse(job.WarningExpr); err == nil {
			if out, err := expr.Eval(ctx); err == nil && strings.TrimSpace(dsl.Stringify(out)) != "" {
				warning = true
				msg = dsl.Stringify(out) + " · " + msg
			}
		}
	}
	return agents.Result{OK: true, Warning: warning, Value: value, HasValue: hasValue, Message: msg, OIDs: values}
}

func normalize(pdu gosnmp.SnmpPDU) any {
	switch pdu.Type {
	case gosnmp.OctetString:
		if b, ok := pdu.Value.([]byte); ok {
			return strings.TrimSpace(string(b))
		}
		return fmt.Sprint(pdu.Value)
	case gosnmp.Counter64:
		if v, ok := pdu.Value.(uint64); ok {
			return float64(v)
		}
	case gosnmp.IPAddress, gosnmp.ObjectIdentifier:
		return fmt.Sprint(pdu.Value)
	}
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
	return pdu.Value
}

func timeoutMS(job agents.Job) int {
	if job.TimeoutMS > 0 {
		return job.TimeoutMS
	}
	return 2000
}

func probeDiscovery(job agents.Job) agents.Result {
	mode := strCfg(job.Config, "mode")
	if mode == "" {
		mode = "fast"
	}
	ips := []string{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(strCfg(job.Config, "cidr"), ",") {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil || network.IP.To4() == nil {
			continue
		}
		for _, ip := range enumerateScanCIDR(network) {
			if !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		return agents.Result{Message: "sin direcciones para descubrir"}
	}
	if len(ips) > 1024 {
		return agents.Result{Message: "el rango supera 1024 direcciones"}
	}
	count, timeout := 1, 800*time.Millisecond
	if mode == "reliable" {
		count, timeout = 3, 1200*time.Millisecond
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 48)
	hosts := []map[string]any{}
	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pinger, err := probing.NewPinger(ip)
			if err != nil {
				return
			}
			pinger.Count = count
			pinger.Timeout = timeout
			pinger.Interval = 50 * time.Millisecond
			pinger.SetPrivileged(true)
			if err := pinger.Run(); err != nil {
				return
			}
			st := pinger.Statistics()
			if st.PacketsRecv == 0 {
				return
			}
			host := map[string]any{
				"ip": ip, "alive": true, "name": ip, "name_source": "ip",
				"latency_ms": float64(st.AvgRtt.Microseconds()) / 1000,
			}
			dnsCtx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
			if names, err := net.DefaultResolver.LookupAddr(dnsCtx, ip); err == nil && len(names) > 0 {
				host["name"] = strings.TrimSuffix(names[0], ".")
				host["name_source"] = "dns"
			}
			cancel()
			if job.SNMP.Port > 0 {
				if name, descr, objectID := discoverySNMP(job, ip); name != "" || descr != "" {
					host["sys_descr"] = descr
					host["sys_object_id"] = objectID
					if host["name_source"] != "dns" && name != "" {
						host["name"] = name
						host["name_source"] = "snmp"
					}
				}
			}
			mu.Lock()
			hosts = append(hosts, host)
			mu.Unlock()
		}(ip)
	}
	wg.Wait()
	sort.Slice(hosts, func(i, j int) bool { return fmt.Sprint(hosts[i]["ip"]) < fmt.Sprint(hosts[j]["ip"]) })
	return agents.Result{OK: true, Data: map[string]any{"hosts": hosts, "scanned": len(ips)}}
}

func discoverySNMP(job agents.Job, ip string) (string, string, string) {
	profile := job.SNMP
	g := &gosnmp.GoSNMP{Target: ip, Port: uint16(profile.Port), Transport: "udp", Timeout: 900 * time.Millisecond, Retries: 0}
	switch strings.ToLower(profile.Version) {
	case "v1":
		g.Version = gosnmp.Version1
		g.Community = profile.Community
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName: profile.User, AuthenticationProtocol: authProtocol(profile.AuthProto),
			AuthenticationPassphrase: profile.AuthPass, PrivacyProtocol: privProtocol(profile.PrivProto),
			PrivacyPassphrase: profile.PrivPass,
		}
	default:
		g.Version = gosnmp.Version2c
		g.Community = profile.Community
	}
	if err := g.Connect(); err != nil {
		return "", "", ""
	}
	defer g.Conn.Close()
	packet, err := g.Get([]string{"1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.1.0", "1.3.6.1.2.1.1.2.0"})
	if err != nil || len(packet.Variables) < 3 {
		return "", "", ""
	}
	return snmpText(packet.Variables[0].Value), snmpText(packet.Variables[1].Value), snmpText(packet.Variables[2].Value)
}

func snmpText(v any) string {
	switch t := v.(type) {
	case []byte:
		return strings.TrimSpace(string(t))
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
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

func boolCfg(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func intCfg(m map[string]any, key string, def int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(string); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func floatCfg(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

func strCfg(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func stringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
	case []string:
		out = t
	case string:
		if t != "" {
			out = strings.Split(t, ",")
		}
	}
	return out
}

func toNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
