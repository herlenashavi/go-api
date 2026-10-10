// Package dbx wires up a pgx connection pool with OpenTelemetry query
// tracing and an explicit, handler-controlled transaction boundary. There
// is no per-request transaction middleware: whether a request needs a
// transaction — and whether to commit it — is decided by calling WithTx
// from inside your handler or repository, never inferred from the
// eventual HTTP response status.
package dbx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
)

// Pool wraps *pgxpool.Pool. Embedding means every pgxpool method (Query,
// Exec, Begin, Acquire, Stat, ...) is available directly on *dbx.Pool.
type Pool struct {
	*pgxpool.Pool
}

// Config controls NewPool.
type Config struct {
	// DSN is a postgres:// connection string.
	DSN string
	// MaxConns overrides pgx's default pool size. Zero keeps the default.
	MaxConns int32
	// Tracer, if set, wraps every query in a client span (db.statement,
	// error status on failure). Pass telemetry.Provider.Tracer; nil
	// disables query tracing.
	Tracer trace.Tracer
}

// NewPool parses cfg.DSN, connects, and verifies connectivity with a Ping —
// failing fast at startup rather than on the first request is deliberate:
// a service that can't reach its database has no business reporting ready.
func NewPool(ctx context.Context, cfg Config) (*Pool, error) {
	pgCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("dbx: parse DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pgCfg.MaxConns = cfg.MaxConns
	}
	if cfg.Tracer != nil {
		pgCfg.ConnConfig.Tracer = &queryTracer{tracer: cfg.Tracer}
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		return nil, fmt.Errorf("dbx: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("dbx: ping: %w", err)
	}
	return &Pool{pool}, nil
}

// HealthCheck satisfies pkg/health's Checker signature directly (it IS
// exactly `func(context.Context) error`) — pass p.HealthCheck, not
// p.Ping, only because Ping is on the embedded *pgxpool.Pool and some
// callers prefer a method that's unambiguously dbx's own.
func (p *Pool) HealthCheck(ctx context.Context) error {
	return p.Ping(ctx)
}
