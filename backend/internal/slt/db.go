// Package slt contains System-Level Test helpers.
//
// Each test that needs a real Postgres calls slt.NewDB(t) which spins up
// a Postgres 17 container via testcontainers, runs all 18 migrations, and
// returns a ready *pgxpool.Pool. The container is torn down via t.Cleanup.
//
//	func TestFoo(t *testing.T) {
//	    pool := slt.NewDB(t)
//	    // build services directly on top of pool
//	}
package slt

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// migrationsDir is the absolute path to backend/migrations, resolved from
// this source file so it works regardless of test working directory.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// NewDB starts a throwaway Postgres 17 container, runs all migrations,
// and returns an admin pool (root user, bypasses RLS).
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:17",
		tcpostgres.WithDatabase("pikshipp_test"),
		tcpostgres.WithUsername("root"),
		tcpostgres.WithPassword("root"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("slt.NewDB: start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("slt.NewDB: connection string: %v", err)
	}

	// Install extensions required by the init migration.
	if err := installExtensions(ctx, connStr); err != nil {
		t.Fatalf("slt.NewDB: extensions: %v", err)
	}

	// Run all migrations.
	if err := applyMigrations(connStr); err != nil {
		t.Fatalf("slt.NewDB: migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("slt.NewDB: pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func installExtensions(ctx context.Context, connStr string) error {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return err
	}
	defer pool.Close()
	for _, ext := range []string{"pgcrypto", "pg_trgm"} {
		if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext); err != nil {
			return fmt.Errorf("extension %s: %w", ext, err)
		}
	}
	return nil
}

func applyMigrations(connStr string) error {
	// golang-migrate expects a "pgx5://" scheme.
	dbURL := "pgx5://" + connStr[len("postgres://"):]
	source := "file://" + migrationsDir()

	m, err := migrate.New(source, dbURL)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate.Up: %w", err)
	}
	return nil
}
