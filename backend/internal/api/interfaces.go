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
)

type ifaceInfo struct {
	IfIndex   int     `json:"if_index"`
	Name      string  `json:"name"`
	Descr     string  `json:"descr"`
	Oper      int     `json:"oper_status"`
	SpeedMbps float64 `json:"speed_mbps"`
	Mac       string  `json:"mac"`
}

func (s *Server) deviceInterfaces(w http.ResponseWriter, r *http.Request) {
	profile, ip, err := s.loadSNMPProfile(r.Context(), chi.URLParam(r, "id"))
	if err != nil || profile.Community == "" && profile.Version != "v3" {
		writeError(w, http.StatusBadRequest, "el dispositivo no tiene un perfil SNMP válido")
		return
	}
	g := &gosnmp.GoSNMP{Target: ip, Port: uint16(profile.Port), Transport: "udp", Timeout: 4 * time.Second, Retries: 1, MaxOids: 40}
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

	byIndex := map[int]*ifaceInfo{}
	get := func(idx int) *ifaceInfo {
		if byIndex[idx] == nil {
			byIndex[idx] = &ifaceInfo{IfIndex: idx}
		}
		return byIndex[idx]
	}
	walk := func(base string, fn func(idx int, pdu gosnmp.SnmpPDU)) {
		_ = g.BulkWalk(base, func(pdu gosnmp.SnmpPDU) error {
			parts := strings.Split(strings.TrimPrefix(pdu.Name, "."), ".")
			if len(parts) == 0 {
				return nil
			}
			idx, err := strconv.Atoi(parts[len(parts)-1])
			if err != nil || idx <= 0 {
				return nil
			}
			fn(idx, pdu)
			return nil
		})
	}
	walk("1.3.6.1.2.1.31.1.1.1.1", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).Name = snmpString(pdu.Value) })
	walk("1.3.6.1.2.1.31.1.1.1.15", func(idx int, pdu gosnmp.SnmpPDU) {
		if f, err := numeric(pdu.Value); err == nil {
			get(idx).SpeedMbps = f
		}
	})
	walk("1.3.6.1.2.1.2.2.1.2", func(idx int, pdu gosnmp.SnmpPDU) { get(idx).Descr = snmpString(pdu.Value) })
	walk("1.3.6.1.2.1.2.2.1.8", func(idx int, pdu gosnmp.SnmpPDU) {
		if f, err := numeric(pdu.Value); err == nil {
			get(idx).Oper = int(f)
		}
	})
	walk("1.3.6.1.2.1.2.2.1.6", func(idx int, pdu gosnmp.SnmpPDU) {
		if b, ok := pdu.Value.([]byte); ok && len(b) == 6 {
			get(idx).Mac = netMAC(b)
		}
	})

	out := make([]ifaceInfo, 0, len(byIndex))
	for _, v := range byIndex {
		if v.Name == "" {
			v.Name = v.Descr
		}
		if v.Name == "" {
			v.Name = fmt.Sprintf("ifIndex %d", v.IfIndex)
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IfIndex < out[j].IfIndex })
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func numeric(v any) (float64, error) {
	switch t := v.(type) {
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case uint:
		return float64(t), nil
	case uint32:
		return float64(t), nil
	case uint64:
		return float64(t), nil
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	}
	return 0, fmt.Errorf("not numeric")
}

func netMAC(b []byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}
