package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type probeExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func normalizeProbeMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "tcp":
		return "tcp"
	case "snmp":
		return "snmp"
	default:
		return "icmp"
	}
}

func defaultProbeSpec(method string, tcpPort int) (probeName, customName, config string, err error) {
	switch normalizeProbeMethod(method) {
	case "tcp":
		if tcpPort < 1 || tcpPort > 65535 {
			return "", "", "", fmt.Errorf("puerto TCP inválido")
		}
		return "TCP Port", fmt.Sprintf("TCP %d", tcpPort), fmt.Sprintf(`{"port":%d}`, tcpPort), nil
	case "snmp":
		return "SNMP Uptime", "SNMP Uptime", "{}", nil
	default:
		return "Ping", "Ping", "{}", nil
	}
}

// ensureDefaultProbe adds the built-in reachability probe that matches how a
// host was discovered. It is idempotent and safe to call for existing devices.
func ensureDefaultProbe(ctx context.Context, q probeExecer, deviceID, method string, tcpPort int) (bool, error) {
	probeName, customName, config, err := defaultProbeSpec(method, tcpPort)
	if err != nil {
		return false, err
	}
	var probeID string
	if err := q.QueryRow(ctx, `SELECT id::text FROM probes WHERE name=$1 LIMIT 1`, probeName).Scan(&probeID); err != nil {
		return false, fmt.Errorf("sonda %q no disponible", probeName)
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO device_services (device_id, probe_id, custom_name, config_overrides,
			interval_sec, timeout_ms, down_retries, up_retries)
		SELECT $1, id, $3, $4::jsonb, default_interval_sec, default_timeout_ms, default_down_retries, default_up_retries
		FROM probes WHERE id=$2
		ON CONFLICT DO NOTHING
	`, deviceID, probeID, customName, config)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
