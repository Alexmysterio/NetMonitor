package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 24
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute

	var pool *pgxpool.Pool
	var lastErr error
	for attempt := 1; attempt <= 30; attempt++ {
		pool, lastErr = pgxpool.NewWithConfig(ctx, cfg)
		if lastErr == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			lastErr = pool.Ping(pingCtx)
			cancel()
			if lastErr == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("database unavailable: %w", lastErr)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool, seedDemo bool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// Serialise DDL across replicas. Session-level locks are fine here because
	// every statement below runs on this dedicated connection.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(982451649)`); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(982451649)`)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	// Databases created before versioned migrations already contain the v1
	// schema. Baseline them so the non-idempotent CREATE INDEX statements in
	// 001_init.sql are not replayed on upgrade.
	var appliedCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&appliedCount); err != nil {
		return err
	}
	if appliedCount == 0 {
		var legacy bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&legacy); err != nil {
			return err
		}
		if legacy {
			if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ('001_init.sql') ON CONFLICT DO NOTHING`); err != nil {
				return err
			}
		}
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || name == "002_seed.sql" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		schema, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := execStatements(ctx, conn, string(schema)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
			return err
		}
	}

	var seedApplied bool
	_ = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings WHERE key='demo_seed_applied')`).Scan(&seedApplied)
	if seedDemo && !seedApplied {
		var mapCount int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM maps`).Scan(&mapCount); err != nil {
			return err
		}
		if mapCount == 0 {
			seed, err := migrations.ReadFile("migrations/002_seed.sql")
			if err != nil {
				return err
			}
			if err := execStatements(ctx, conn, string(seed)); err != nil {
				return fmt.Errorf("apply demo seed: %w", err)
			}
		}
	}
	_, _ = conn.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('demo_seed_applied','true')
		ON CONFLICT (key) DO NOTHING
	`)
	return nil
}

type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func execStatements(ctx context.Context, db execer, script string) error {
	statements := splitSQL(script)
	for i, stmt := range statements {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(ctx, s); err != nil {
			return fmt.Errorf("statement %d: %w\n%s", i+1, err, truncate(s, 500))
		}
	}
	return nil
}

func splitSQL(script string) []string {
	var out []string
	var b strings.Builder
	inSingle := false
	inLineComment := false
	for i := 0; i < len(script); i++ {
		ch := script[i]
		if inLineComment {
			b.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if !inSingle && ch == '-' && i+1 < len(script) && script[i+1] == '-' {
			inLineComment = true
			b.WriteByte(ch)
			continue
		}
		if ch == '\'' {
			if inSingle && i+1 < len(script) && script[i+1] == '\'' {
				b.WriteByte(ch)
				b.WriteByte(script[i+1])
				i++
				continue
			}
			inSingle = !inSingle
			b.WriteByte(ch)
			continue
		}
		if ch == ';' && !inSingle {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(ch)
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
