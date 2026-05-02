// Command pikshipp is the single-binary entrypoint. The --role flag selects
// which subsystems boot:
//
//	api     — HTTP + webhook receivers (no jobs).
//	worker  — river runner + outbox forwarder (no HTTP except /healthz).
//	all     — both, for v0 single-instance + local dev.
//
// Boot sequence per LLD §05-cross-cutting/03-deployment:
//
//	parse flags → load config → logger → pgxpools (per role) →
//	verify schema version → wire HTTP server → block until SIGTERM →
//	graceful shutdown.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpapi "github.com/vishal1132/pikshipp/backend/api/http"
	"github.com/vishal1132/pikshipp/backend/internal/config"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
	"github.com/vishal1132/pikshipp/backend/internal/observability/logger"
)

func main() {
	roleFlag := flag.String("role", "", "override PIKSHIPP_ROLE: api|worker|all")
	flag.Parse()

	if *roleFlag != "" {
		_ = os.Setenv("PIKSHIPP_ROLE", *roleFlag)
	}

	cfg, err := config.Load()
	if err != nil {
		// Logger isn't built yet, so fall through to stderr.
		_, _ = os.Stderr.WriteString("config: " + err.Error() + "\n")
		os.Exit(2)
	}

	log, err := logger.New(logger.Options{Level: cfg.LogLevel, Version: cfg.Version})
	if err != nil {
		_, _ = os.Stderr.WriteString("logger: " + err.Error() + "\n")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, log); err != nil {
		log.ErrorContext(ctx, "fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	log.InfoContext(ctx, "pikshipp starting", slog.String("role", string(cfg.Role)))

	// Build the three pools. Per review finding S3, api and worker MUST
	// have separate pools so worker concurrency cannot starve API
	// handlers. With --role=all this single process holds both — sized
	// independently so even in dev we mirror prod's isolation.
	poolApp, err := dbtx.NewPool(ctx, dbtx.Config{
		URL:              cfg.DatabaseURL,
		MaxConns:         cfg.DBAppMaxConns,
		MinConns:         cfg.DBAppMinConns,
		StatementTimeout: cfg.DBStatementTimeout,
	}, dbtx.RoleApp, log)
	if err != nil {
		return err
	}
	defer poolApp.Close()

	poolReports, err := dbtx.NewPool(ctx, dbtx.Config{
		URL:              cfg.DatabaseURL,
		MaxConns:         cfg.DBReportsMaxConns,
		MinConns:         cfg.DBReportsMinConns,
		StatementTimeout: cfg.DBStatementTimeout,
	}, dbtx.RoleReports, log)
	if err != nil {
		return err
	}
	defer poolReports.Close()

	poolAdmin, err := dbtx.NewPool(ctx, dbtx.Config{
		URL:              cfg.DatabaseURL,
		MaxConns:         cfg.DBAdminMaxConns,
		MinConns:         cfg.DBAdminMinConns,
		StatementTimeout: cfg.DBStatementTimeout,
	}, dbtx.RoleAdmin, log)
	if err != nil {
		return err
	}
	defer poolAdmin.Close()

	// Refuse to serve traffic against an out-of-date schema. (LLD
	// §02-infrastructure/01: migrations are a CI step, not run on
	// startup; the binary just verifies.)
	if err := dbtx.CheckSchemaVersion(ctx, poolApp); err != nil {
		return err
	}

	pools := httpapi.Pools{App: poolApp, Reports: poolReports, Admin: poolAdmin}

	// HTTP server — runs unless we're worker-only.
	var srv *http.Server
	if cfg.Role == config.RoleAPI || cfg.Role == config.RoleAll {
		srv = &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           httpapi.NewRouter(pools, cfg.HealthcheckTimeout),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.InfoContext(ctx, "http listening", slog.String("addr", cfg.HTTPAddr))
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.ErrorContext(ctx, "http server stopped", slog.String("err", err.Error()))
				stopFromError(ctx)
			}
		}()
	}

	// Worker boot would go here (river runner + outbox forwarder).
	// Skipping for the bootstrap milestone — domain services come next.
	if cfg.Role == config.RoleWorker || cfg.Role == config.RoleAll {
		log.InfoContext(ctx, "worker role enabled (no jobs registered yet)")
	}

	<-ctx.Done()
	log.InfoContext(context.Background(), "shutdown signal received")

	// Graceful shutdown: stop accepting new HTTP, then drain workers, then
	// close pools (deferred above).
	if srv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.ErrorContext(shutdownCtx, "http shutdown", slog.String("err", err.Error()))
		}
	}
	log.InfoContext(context.Background(), "pikshipp stopped")
	return nil
}

// stopFromError lets the http goroutine signal the main goroutine to exit
// without ranging over channels. The signal-derived ctx will fire on the
// next signal anyway; this just shortens the wait when the listener dies.
func stopFromError(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	// Best-effort: send ourselves a SIGTERM so signal.NotifyContext fires.
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}
