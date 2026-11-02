package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netmonitor/netmonitor/internal/config"
	"github.com/netmonitor/netmonitor/internal/db"
	"github.com/netmonitor/netmonitor/internal/keyring"
	"github.com/netmonitor/netmonitor/internal/secure"
)

func startKeyRotation(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, initial keyring.Key) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('encryption_key_created_at', NOW()::text)
		ON CONFLICT (key) DO NOTHING
	`)
	if cfg.KeyRotationDays <= 0 {
		return
	}
	if initial.Source == "env" || initial.Source == "cmd" {
		log.Printf("key rotation: ENCRYPTION_KEY_ROTATION_DAYS requiere ENCRYPTION_KEY_FILE o Vault; desactivado")
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		check := func() {
			var created string
			_ = pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key='encryption_key_created_at'`).Scan(&created)
			t, err := time.Parse(time.RFC3339Nano, created)
			if err != nil {
				t, _ = time.Parse("2006-01-02 15:04:05.999999-07", created)
			}
			if err != nil || time.Since(t) < time.Duration(cfg.KeyRotationDays)*24*time.Hour {
				return
			}
			current, err := keyring.Resolve(cfg)
			if err != nil {
				log.Printf("key rotation: %v", err)
				return
			}
			if err := rotateKey(ctx, pool, cfg, current.Current); err != nil {
				log.Printf("key rotation failed: %v", err)
			}
		}
		check()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}

func rotateKey(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, oldKey string) error {
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lockConn.Release()
	var locked bool
	if err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock(982451655)`).Scan(&locked); err != nil || !locked {
		return nil
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock(982451655)`)
	}()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	newKey := base64.StdEncoding.EncodeToString(buf)
	// Persist current+previous before touching the database so a restart can
	// decrypt both old and new values if the process dies mid-rotation.
	if err := keyring.Persist(cfg, keyring.Key{Current: newKey, Previous: oldKey}); err != nil {
		return err
	}
	if _, err := db.RotateSecrets(ctx, pool, oldKey, newKey); err != nil {
		secure.SetKeys(oldKey, newKey)
		return err
	}
	secure.SetKeys(newKey, oldKey)
	_, err = pool.Exec(ctx, `
		UPDATE system_settings SET value=NOW()::text, updated_at=NOW() WHERE key='encryption_key_created_at'
	`)
	if err == nil {
		log.Printf("encryption key rotated successfully")
	}
	return err
}
