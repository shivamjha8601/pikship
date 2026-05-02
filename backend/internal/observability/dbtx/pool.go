package dbtx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config drives pool construction.
type Config struct {
	URL               string        // base postgres URL; the role is set per-connection.
	MaxConns          int32         // default 50
	MinConns          int32         // default 5
	MaxConnLifetime   time.Duration // default 60min
	MaxConnIdleTime   time.Duration // default 30min
	HealthCheckPeriod time.Duration // default 1min
	StatementTimeout  time.Duration // default 5s; per-tx via SET LOCAL
}

// DefaultConfig returns sensible defaults; URL must be set by the caller.
func DefaultConfig(url string) Config {
	return Config{
		URL:               url,
		MaxConns:          50,
		MinConns:          5,
		MaxConnLifetime:   60 * time.Minute,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: 1 * time.Minute,
		StatementTimeout:  5 * time.Second,
	}
}

// NewPool constructs a *pgxpool.Pool whose connections SET ROLE to the
// given role on AfterConnect, and apply a default statement_timeout.
//
// Per LLD §02-infrastructure/01: api-role and worker-role processes MUST
// instantiate independent pools so worker concurrency cannot starve API
// handlers (review finding S3). When --role=all (dev), the binary
// instantiates two app-role pools — one for HTTP handlers and one for
// the river runner — sized independently.
func NewPool(ctx context.Context, cfg Config, role Role, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dbtx.NewPool: parse url: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	stmtTimeout := cfg.StatementTimeout
	if stmtTimeout <= 0 {
		stmtTimeout = 5 * time.Second
	}

	roleName := role.String()
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// SET ROLE — login user must have been GRANTed this role by the DBA
		// (migration 0001 takes care of this for dev).
		if _, err := conn.Exec(ctx, fmt.Sprintf("SET ROLE %s", roleName)); err != nil {
			return fmt.Errorf("set role %s: %w", roleName, err)
		}
		if _, err := conn.Exec(ctx,
			fmt.Sprintf("SET statement_timeout = '%dms'", stmtTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("set statement_timeout: %w", err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("dbtx.NewPool: create pool (role=%s): %w", roleName, err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("dbtx.NewPool: ping (role=%s): %w", roleName, err)
	}

	if log != nil {
		log.InfoContext(ctx, "db pool ready",
			slog.String("role", roleName),
			slog.Int("max_conns", int(poolCfg.MaxConns)),
			slog.Duration("statement_timeout", stmtTimeout),
		)
	}
	return pool, nil
}
