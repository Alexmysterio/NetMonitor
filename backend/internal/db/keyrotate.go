package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/secure"
)

type secretProfile struct {
	id, community, auth, priv string
}

type secretChannel struct {
	id, config string
}

type secretRouterCredential struct {
	id, password string
}

type secretSetting struct {
	key, value string
}

// RotateSecrets re-encrypts SNMP credentials and notification channel configs
// from oldKey to newKey in a single transaction. Every encrypted value must be
// decryptable with oldKey before any write happens, so a wrong key aborts the
// rotation instead of silently blanking secrets.
//
// Stop the NetMonitor container before running it, or run it during a maintenance
// window. Restart the core with newKey afterwards.
func RotateSecrets(ctx context.Context, pool *pgxpool.Pool, oldKey, newKey string) (int, error) {
	if oldKey == "" || newKey == "" {
		return 0, errors.New("old and new encryption keys are required")
	}

	secure.SetKey(oldKey)
	profiles, err := readProfiles(ctx, pool)
	if err != nil {
		return 0, err
	}
	channels, err := readChannels(ctx, pool)
	if err != nil {
		return 0, err
	}
	routers, err := readRouterCredentials(ctx, pool)
	if err != nil {
		return 0, err
	}
	settings, err := readWrappedSettings(ctx, pool)
	if err != nil {
		return 0, err
	}

	secure.SetKey(newKey)
	for i := range profiles {
		if profiles[i].community, err = secure.Encrypt(profiles[i].community); err != nil {
			return 0, fmt.Errorf("encrypt profile %s community: %w", profiles[i].id, err)
		}
		if profiles[i].auth, err = secure.Encrypt(profiles[i].auth); err != nil {
			return 0, fmt.Errorf("encrypt profile %s auth password: %w", profiles[i].id, err)
		}
		if profiles[i].priv, err = secure.Encrypt(profiles[i].priv); err != nil {
			return 0, fmt.Errorf("encrypt profile %s privacy password: %w", profiles[i].id, err)
		}
	}
	for i := range channels {
		if channels[i].config, err = secure.WrapJSON(channels[i].config); err != nil {
			return 0, fmt.Errorf("encrypt channel %s: %w", channels[i].id, err)
		}
	}
	for i := range routers {
		if routers[i].password, err = secure.Encrypt(routers[i].password); err != nil {
			return 0, fmt.Errorf("encrypt RouterOS credential %s: %w", routers[i].id, err)
		}
	}
	for i := range settings {
		if settings[i].value, err = secure.WrapJSON(settings[i].value); err != nil {
			return 0, fmt.Errorf("encrypt setting %s: %w", settings[i].key, err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	count := 0
	for _, p := range profiles {
		if _, err := tx.Exec(ctx, `
			UPDATE snmp_profiles SET community=$2, v3_auth_pass=NULLIF($3,''), v3_priv_pass=NULLIF($4,'')
			WHERE id=$1
		`, p.id, p.community, p.auth, p.priv); err != nil {
			return 0, fmt.Errorf("rotate profile %s: %w", p.id, err)
		}
		count++
	}
	for _, c := range channels {
		if _, err := tx.Exec(ctx, `UPDATE notification_channels SET config=$2::jsonb WHERE id=$1`, c.id, c.config); err != nil {
			return 0, fmt.Errorf("rotate channel %s: %w", c.id, err)
		}
		count++
	}
	for _, r := range routers {
		if _, err := tx.Exec(ctx, `UPDATE routeros_credentials SET password=$2 WHERE id=$1`, r.id, r.password); err != nil {
			return 0, fmt.Errorf("rotate RouterOS credential %s: %w", r.id, err)
		}
		count++
	}
	for _, setting := range settings {
		if _, err := tx.Exec(ctx, `UPDATE system_settings SET value=$2, updated_at=NOW() WHERE key=$1`, setting.key, setting.value); err != nil {
			return 0, fmt.Errorf("rotate setting %s: %w", setting.key, err)
		}
		count++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func readProfiles(ctx context.Context, pool *pgxpool.Pool) ([]secretProfile, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, community, COALESCE(v3_auth_pass,''), COALESCE(v3_priv_pass,'')
		FROM snmp_profiles
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []secretProfile
	for rows.Next() {
		var p secretProfile
		if err := rows.Scan(&p.id, &p.community, &p.auth, &p.priv); err != nil {
			return nil, err
		}
		if p.community, err = decryptSecret(p.community); err != nil {
			return nil, fmt.Errorf("profile %s community: %w", p.id, err)
		}
		if p.auth, err = decryptSecret(p.auth); err != nil {
			return nil, fmt.Errorf("profile %s auth password: %w", p.id, err)
		}
		if p.priv, err = decryptSecret(p.priv); err != nil {
			return nil, fmt.Errorf("profile %s privacy password: %w", p.id, err)
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func readChannels(ctx context.Context, pool *pgxpool.Pool) ([]secretChannel, error) {
	rows, err := pool.Query(ctx, `SELECT id, config::text FROM notification_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []secretChannel
	for rows.Next() {
		var c secretChannel
		if err := rows.Scan(&c.id, &c.config); err != nil {
			return nil, err
		}
		if c.config, err = unwrapChannelConfig(c.config); err != nil {
			return nil, fmt.Errorf("channel %s: %w", c.id, err)
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

func decryptSecret(v string) (string, error) {
	if !secure.IsEncrypted(v) {
		return v, nil
	}
	return secure.Decrypt(v)
}

func readRouterCredentials(ctx context.Context, pool *pgxpool.Pool) ([]secretRouterCredential, error) {
	rows, err := pool.Query(ctx, `SELECT id, password FROM routeros_credentials`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secretRouterCredential
	for rows.Next() {
		var c secretRouterCredential
		if err := rows.Scan(&c.id, &c.password); err != nil {
			return nil, err
		}
		if c.password, err = decryptSecret(c.password); err != nil {
			return nil, fmt.Errorf("RouterOS credential %s: %w", c.id, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func readWrappedSettings(ctx context.Context, pool *pgxpool.Pool) ([]secretSetting, error) {
	rows, err := pool.Query(ctx, `SELECT key, value FROM system_settings WHERE key='reports.storage'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secretSetting
	for rows.Next() {
		var s secretSetting
		if err := rows.Scan(&s.key, &s.value); err != nil {
			return nil, err
		}
		if secure.IsWrappedJSON(s.value) {
			var wrapper struct {
				Enc string `json:"enc"`
			}
			if err := json.Unmarshal([]byte(s.value), &wrapper); err != nil || wrapper.Enc == "" {
				return nil, fmt.Errorf("setting %s is malformed", s.key)
			}
			if s.value, err = secure.Decrypt(wrapper.Enc); err != nil {
				return nil, fmt.Errorf("setting %s: %w", s.key, err)
			}
			out = append(out, s)
		}
	}
	return out, rows.Err()
}
