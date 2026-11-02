package poller

import (
	"context"
	"fmt"

	"github.com/netmonitor/netmonitor/internal/agents"
)

func (p *Poller) probeViaAgent(ctx context.Context, j job) (probeResult, error) {
	agentID := ""
	if j.AgentID != nil {
		agentID = *j.AgentID
	}
	if agentID == "" {
		return probeResult{}, fmt.Errorf("device has no agent")
	}
	job := agents.Job{
		ID:            j.ServiceID,
		DeviceID:      j.DeviceID,
		ProbeType:     j.ProbeType,
		IP:            cleanHost(j.IP),
		TimeoutMS:     j.Timeout,
		Config:        j.Config,
		AvailableExpr: j.AvailableExpr,
		WarningExpr:   j.WarningExpr,
		ErrorExpr:     j.ErrorExpr,
		ValueExpr:     j.ValueExpr,
		Unit:          j.Unit,
		SNMP: agents.SNMPConfig{
			Version: j.SNMPVersion, Port: j.SNMPPort, Community: j.SNMPCommunity,
			User: j.SNMPv3User, Security: j.SNMPv3SecLevel, AuthProto: j.SNMPv3AuthProto,
			AuthPass: j.SNMPv3AuthPass, PrivProto: j.SNMPv3PrivProto, PrivPass: j.SNMPv3PrivPass,
		},
	}
	if p.agentMgr == nil {
		return probeResult{}, fmt.Errorf("agent manager not configured")
	}
	result, err := p.agentMgr.Execute(ctx, agentID, job)
	if err != nil {
		return probeResult{}, err
	}
	oids := result.OIDs
	if oids == nil {
		oids = map[string]any{}
	}
	for oid, val := range oids {
		p.store.SetOID(ctx, j.DeviceID, oid, val)
		if f, ok := val.(float64); ok {
			if rate := p.store.CounterRate(ctx, j.DeviceID, oid, f); rate != 0 {
				p.store.SetRate(ctx, j.DeviceID, oid, rate)
			}
		}
	}
	for base, vals := range result.Columns {
		p.store.SetColumn(ctx, j.DeviceID, base, vals)
	}
	message := result.Message
	if message == "" {
		message = "agent probe completed"
	}
	return probeResult{
		OK: result.OK, Warning: result.Warning, HasValue: result.HasValue,
		Value: result.Value, RTT: result.RTT, Loss: result.Loss,
		Message: message, OIDs: oids, Columns: result.Columns,
	}, nil
}
