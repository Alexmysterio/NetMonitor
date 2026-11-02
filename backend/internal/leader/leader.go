package leader

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryKey int64 = 0x4e4d4c44 // "NMLD"

type Elector struct {
	pool *pgxpool.Pool
	conn *pgxpool.Conn
}

func New(pool *pgxpool.Pool) *Elector { return &Elector{pool: pool} }

func (e *Elector) Acquire(ctx context.Context) (bool, error) {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryKey).Scan(&ok); err != nil {
		conn.Release()
		return false, err
	}
	if !ok {
		conn.Release()
		return false, nil
	}
	e.conn = conn
	return true, nil
}

func (e *Elector) Release(ctx context.Context) {
	if e.conn == nil {
		return
	}
	_, _ = e.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, advisoryKey)
	e.conn.Release()
	e.conn = nil
}
