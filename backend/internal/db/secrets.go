package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/secure"
)

// EncryptExistingSecrets migrates legacy plaintext credentials in place. It is
// idempotent and runs automatically on startup.
func EncryptExistingSecrets(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT id, community, COALESCE(v3_auth_pass,''), COALESCE(v3_priv_pass,'')
		FROM snmp_profiles
	`)
	if err != nil {
		return err
	}
	type profile struct {
		id, community, authPass, privPass string
	}
	var profiles []profile
	for rows.Next() {
		var p profile
		if rows.Scan(&p.id, &p.community, &p.authPass, &p.privPass) == nil {
			profiles = append(profiles, p)
		}
	}
	rows.Close()
	for _, p := range profiles {
		community, err := maybeEncrypt(p.community)
		if err != nil {
			return fmt.Errorf("encrypt community: %w", err)
		}
		authPass, err := maybeEncrypt(p.authPass)
		if err != nil {
			return err
		}
		privPass, err := maybeEncrypt(p.privPass)
		if err != nil {
			return err
		}
		if community == p.community && authPass == p.authPass && privPass == p.privPass {
			continue
		}
		if _, err := pool.Exec(ctx, `
			UPDATE snmp_profiles SET community=$2, v3_auth_pass=NULLIF($3,''), v3_priv_pass=NULLIF($4,'')
			WHERE id=$1
		`, p.id, community, authPass, privPass); err != nil {
			return err
		}
	}

	chRows, err := pool.Query(ctx, `SELECT id, config::text FROM notification_channels`)
	if err != nil {
		return err
	}
	type channel struct{ id, config string }
	var channels []channel
	for chRows.Next() {
		var c channel
		if chRows.Scan(&c.id, &c.config) == nil {
			channels = append(channels, c)
		}
	}
	chRows.Close()
	for _, c := range channels {
		if secure.IsWrappedJSON(c.config) {
			continue
		}
		wrapped, err := secure.WrapJSON(c.config)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, `UPDATE notification_channels SET config=$2::jsonb WHERE id=$1`, c.id, wrapped); err != nil {
			return err
		}
	}

	routerRows, err := pool.Query(ctx, `SELECT id, password FROM routeros_credentials`)
	if err != nil {
		return err
	}
	type routerCredential struct{ id, password string }
	var routerCredentials []routerCredential
	for routerRows.Next() {
		var c routerCredential
		if routerRows.Scan(&c.id, &c.password) == nil {
			routerCredentials = append(routerCredentials, c)
		}
	}
	routerRows.Close()
	for _, c := range routerCredentials {
		password, err := maybeEncrypt(c.password)
		if err != nil {
			return err
		}
		if password != c.password {
			if _, err := pool.Exec(ctx, `UPDATE routeros_credentials SET password=$2 WHERE id=$1`, c.id, password); err != nil {
				return err
			}
		}
	}

	var storage string
	_ = pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='reports.storage'`).Scan(&storage)
	if storage != "" && !secure.IsWrappedJSON(storage) {
		wrapped, err := secure.WrapJSON(storage)
		if err != nil {
			return err
		}
		_, _ = pool.Exec(ctx, `UPDATE system_settings SET value=$1, updated_at=NOW() WHERE key='reports.storage'`, wrapped)
	}
	return nil
}

func maybeEncrypt(v string) (string, error) {
	if v == "" || secure.IsEncrypted(v) {
		return v, nil
	}
	return secure.Encrypt(v)
}

// ValidateSecrets checks that every encrypted value can be decrypted with the
// configured key. It runs at startup after the legacy-plaintext migration so a
// wrong ENCRYPTION_KEY stops the core instead of silently breaking alerts and
// SNMP polling.
func ValidateSecrets(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `
		SELECT id, name, community, COALESCE(v3_auth_pass,''), COALESCE(v3_priv_pass,'')
		FROM snmp_profiles
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, community, authPass, privPass string
		if err := rows.Scan(&id, &name, &community, &authPass, &privPass); err != nil {
			return err
		}
		fields := []struct{ label, value string }{
			{"community", community},
			{"auth password", authPass},
			{"privacy password", privPass},
		}
		for _, f := range fields {
			if secure.IsEncrypted(f.value) {
				if _, err := secure.Decrypt(f.value); err != nil {
					return fmt.Errorf("perfil SNMP %q (%s): la ENCRYPTION_KEY actual no puede descifrarlo: %w", name, f.label, err)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	chRows, err := pool.Query(ctx, `SELECT id, name, config::text FROM notification_channels`)
	if err != nil {
		return err
	}
	defer chRows.Close()
	for chRows.Next() {
		var id, name, config string
		if err := chRows.Scan(&id, &name, &config); err != nil {
			return err
		}
		if secure.IsWrappedJSON(config) {
			if _, err := unwrapChannelConfig(config); err != nil {
				return fmt.Errorf("canal de notificación %q: la ENCRYPTION_KEY actual no puede descifrarlo: %w", name, err)
			}
		}
	}
	if err := chRows.Err(); err != nil {
		return err
	}

	routerRows, err := pool.Query(ctx, `SELECT name, password FROM routeros_credentials`)
	if err != nil {
		return err
	}
	defer routerRows.Close()
	for routerRows.Next() {
		var name, password string
		if err := routerRows.Scan(&name, &password); err != nil {
			return err
		}
		if secure.IsEncrypted(password) {
			if _, err := secure.Decrypt(password); err != nil {
				return fmt.Errorf("credencial RouterOS %q: la ENCRYPTION_KEY actual no puede descifrarla: %w", name, err)
			}
		}
	}
	if err := routerRows.Err(); err != nil {
		return err
	}

	var storage string
	_ = pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='reports.storage'`).Scan(&storage)
	if secure.IsWrappedJSON(storage) {
		var wrapper struct {
			Enc string `json:"enc"`
		}
		if err := json.Unmarshal([]byte(storage), &wrapper); err != nil || wrapper.Enc == "" {
			return fmt.Errorf("configuración de almacenamiento de informes inválida")
		}
		if _, err := secure.Decrypt(wrapper.Enc); err != nil {
			return fmt.Errorf("configuración de almacenamiento de informes: %w", err)
		}
	}
	return nil
}

// unwrapChannelConfig decrypts {"enc":"enc:v1:..."} payloads and leaves legacy
// plain JSON documents untouched.
func unwrapChannelConfig(raw string) (string, error) {
	var wrapper struct {
		Enc string `json:"enc"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) != nil || wrapper.Enc == "" {
		return raw, nil
	}
	return secure.Decrypt(wrapper.Enc)
}
