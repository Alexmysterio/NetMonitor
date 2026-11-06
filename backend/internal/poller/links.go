package poller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/netmonitor/netmonitor/internal/secure"
	"github.com/netmonitor/netmonitor/internal/state"
)

func (p *Poller) linkLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.pollLinks(ctx)
		}
	}
}

type linkJob struct {
	ID            string
	MapID         string
	DeviceID      string
	IP            string
	IfIndex       int
	IfName        string
	MaxSpeedBps   int64
	SNMPVersion   string
	SNMPPort      int
	SNMPCommunity string
	V3User        string
	V3SecLevel    string
	V3AuthProto   string
	V3AuthPass    string
	V3PrivProto   string
	V3PrivPass    string
}

func (p *Poller) pollLinks(ctx context.Context) {
	rows, err := p.pool.Query(ctx, `
		SELECT l.id, l.map_id, d.id, host(d.primary_ip), COALESCE(l.snmp_if_index, 0), COALESCE(l.snmp_if_name,''),
		       l.max_speed_bps,
		       COALESCE(sp.version,'v2c'), COALESCE(sp.port,161), COALESCE(sp.community,'public'),
		       COALESCE(sp.v3_username,''), COALESCE(sp.v3_security_level,''), COALESCE(sp.v3_auth_protocol,''),
		       COALESCE(sp.v3_auth_pass,''), COALESCE(sp.v3_priv_protocol,''), COALESCE(sp.v3_priv_pass,'')
		FROM map_links l
		JOIN devices d ON d.id = l.master_device_id
		JOIN snmp_profiles sp ON sp.id = d.snmp_profile_id
		WHERE l.mastering_type='snmp' AND l.snmp_if_index IS NOT NULL AND d.is_monitoring_enabled=TRUE
	`)
	if err != nil {
		return
	}
	var jobs []linkJob
	for rows.Next() {
		var j linkJob
		if rows.Scan(&j.ID, &j.MapID, &j.DeviceID, &j.IP, &j.IfIndex, &j.IfName, &j.MaxSpeedBps,
			&j.SNMPVersion, &j.SNMPPort, &j.SNMPCommunity,
			&j.V3User, &j.V3SecLevel, &j.V3AuthProto, &j.V3AuthPass, &j.V3PrivProto, &j.V3PrivPass) == nil {
			j.SNMPCommunity, _ = secure.Decrypt(j.SNMPCommunity)
			j.V3AuthPass, _ = secure.Decrypt(j.V3AuthPass)
			j.V3PrivPass, _ = secure.Decrypt(j.V3PrivPass)
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		p.pollLink(ctx, j)
	}
}

func (p *Poller) pollLink(ctx context.Context, j linkJob) {
	if j.IfIndex <= 0 {
		return
	}
	g := &gosnmp.GoSNMP{Target: cleanHost(j.IP), Port: uint16(j.SNMPPort), Transport: "udp", Timeout: 2 * time.Second, Retries: 1}
	switch strings.ToLower(j.SNMPVersion) {
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		g.MsgFlags = gosnmp.AuthPriv
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 j.V3User,
			AuthenticationProtocol:   authProtocol(j.V3AuthProto),
			AuthenticationPassphrase: j.V3AuthPass,
			PrivacyProtocol:          privProtocol(j.V3PrivProto),
			PrivacyPassphrase:        j.V3PrivPass,
		}
	default:
		g.Version = gosnmp.Version2c
		g.Community = j.SNMPCommunity
	}
	if err := g.Connect(); err != nil {
		return
	}
	defer g.Conn.Close()
	base := "1.3.6.1.2.1.31.1.1.1"
	oids := []string{
		fmt.Sprintf("%s.6.%d", base, j.IfIndex),
		fmt.Sprintf("%s.10.%d", base, j.IfIndex),
		fmt.Sprintf("1.3.6.1.2.1.2.2.1.8.%d", j.IfIndex),
		fmt.Sprintf("%s.15.%d", base, j.IfIndex),
		fmt.Sprintf("1.3.6.1.2.1.2.2.1.14.%d", j.IfIndex),
		fmt.Sprintf("1.3.6.1.2.1.2.2.1.20.%d", j.IfIndex),
	}
	packet, err := g.Get(oids)
	if err != nil {
		return
	}
	var rxOctets, txOctets float64
	var oper, speedMbps, inErr, outErr float64
	for i, pdu := range packet.Variables {
		f, _ := toNumber(normalizePDU(pdu))
		switch i {
		case 0:
			rxOctets = f
		case 1:
			txOctets = f
		case 2:
			oper = f
		case 3:
			speedMbps = f
		case 4:
			inErr = f
		case 5:
			outErr = f
		}
	}
	rxRate := p.store.CounterRate(ctx, j.DeviceID, fmt.Sprintf("ifHCInOctets.%d", j.IfIndex), rxOctets) * 8
	txRate := p.store.CounterRate(ctx, j.DeviceID, fmt.Sprintf("ifHCOutOctets.%d", j.IfIndex), txOctets) * 8
	speed := j.MaxSpeedBps
	if speedMbps > 0 {
		speed = int64(speedMbps) * 1000 * 1000
	}
	util := 0.0
	if speed > 0 {
		util = float64(max64(int64(rxRate), int64(txRate))) / float64(speed) * 100
	}
	status := "up"
	if oper != 1 {
		status = "down"
	}
	_, _ = p.pool.Exec(ctx, `
		INSERT INTO link_metrics (time, link_id, device_id, if_index, rx_bps, tx_bps, in_errors, out_errors, oper_status)
		VALUES (NOW(),$1,$2,$3,$4,$5,$6,$7,$8)
	`, j.ID, j.DeviceID, j.IfIndex, int64(rxRate), int64(txRate), int(inErr), int(outErr), int(oper))
	_, _ = p.pool.Exec(ctx, `UPDATE map_links SET status=$2, updated_at=NOW() WHERE id=$1`, j.ID, status)

	label := fmt.Sprintf("⬆ %s · ⬇ %s", formatBps(txRate), formatBps(rxRate))
	p.store.SetLink(ctx, j.ID, map[string]any{
		"status": status, "rx_bps": int64(rxRate), "tx_bps": int64(txRate),
		"utilization_pct": util, "if_oper_status": int(oper), "label": label,
	})
	p.hub.BroadcastMap(j.MapID, map[string]any{
		"type": "LINK_METRICS_PATCH",
		"payload": []map[string]any{{
			"link_id": j.ID, "status": status, "tx_bps": int64(txRate), "rx_bps": int64(rxRate),
			"utilization_pct": util, "rendered_label": label,
		}},
	})
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func formatBps(bps float64) string {
	if bps < 0 {
		bps = 0
	}
	units := []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	i := 0
	for bps >= 1000 && i < len(units)-1 {
		bps /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(int64(bps), 10) + " bps"
	}
	if bps >= 100 {
		return fmt.Sprintf("%.0f %s", bps, units[i])
	}
	return fmt.Sprintf("%.1f %s", bps, units[i])
}

func getString(m map[string]string, key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}

var _ = state.Store{}
