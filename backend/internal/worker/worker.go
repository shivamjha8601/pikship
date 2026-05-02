// Package worker provides the River background-job client and worker
// registration framework. Domain packages register their workers by calling
// Register before Build is called.
//
// Boot sequence in cmd/pikshipp/main.go (worker role):
//
//  1. Create the admin pool (for River migrations).
//  2. Run River migrations via RunMigrations.
//  3. Domain packages call Register to register their workers.
//  4. Call Build to get a *river.Client ready for Start.
//  5. Start the client; defer client.Stop(ctx).
//
// Per LLD §03-services/03-outbox and ADR 0004.
package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// WorkerRegistrar is a function that adds workers to the given Workers set.
// Domain packages export a function of this type and register it via Register.
type WorkerRegistrar func(workers *river.Workers)

var globalRegistrars []WorkerRegistrar

// Register queues a WorkerRegistrar. Must be called before Build.
// Typically called from package-level init or an explicit boot call in main.
func Register(r WorkerRegistrar) {
	globalRegistrars = append(globalRegistrars, r)
}

// Build constructs a River client with all registered workers.
// pool must have INSERT access to river_jobs (pikshipp_app is sufficient).
// periodicJobs is optional; pass nil if there are no cron jobs yet.
func Build(pool *pgxpool.Pool, periodicJobs []*river.PeriodicJob, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	for _, r := range globalRegistrars {
		r(workers)
	}

	cfg := &river.Config{
		Workers: workers,
		Logger:  log,
	}
	if len(periodicJobs) > 0 {
		cfg.PeriodicJobs = periodicJobs
	}

	client, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("worker.Build: %w", err)
	}
	return client, nil
}

// RunMigrations applies the River schema migrations to the DB.
// Must run before the worker client starts.
// pool should be the admin pool (BYPASSRLS, SUPERUSER-equivalent for DDL).
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
		Logger: log,
	})
	if err != nil {
		return fmt.Errorf("worker.RunMigrations: %w", err)
	}
	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("worker.RunMigrations migrate: %w", err)
	}
	for _, v := range res.Versions {
		log.InfoContext(ctx, "river migration applied", slog.Int("version", v.Version))
	}
	return nil
}
