// Command pikshipp is the single-binary entrypoint.
//
//	--role=api     HTTP + webhook receivers (no background jobs)
//	--role=worker  River runner + outbox forwarder (no HTTP except /healthz)
//	--role=all     both (v0 single-instance / local dev)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpapi "github.com/vishal1132/pikshipp/backend/api/http"
	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/buyerexp"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/config"
	"github.com/vishal1132/pikshipp/backend/internal/contracts"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/ndr"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
	"github.com/vishal1132/pikshipp/backend/internal/observability/logger"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
	"github.com/vishal1132/pikshipp/backend/internal/reports"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

func main() {
	roleFlag := flag.String("role", "", "override PIKSHIPP_ROLE: api|worker|all")
	flag.Parse()
	if *roleFlag != "" {
		_ = os.Setenv("PIKSHIPP_ROLE", *roleFlag)
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}

	log, err := logger.New(logger.Options{Level: cfg.LogLevel, Version: cfg.Version})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logger: %v\n", err)
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
	log.InfoContext(ctx, "pikshipp starting",
		slog.String("role", string(cfg.Role)),
		slog.String("version", cfg.Version),
	)

	// ── Secrets ──────────────────────────────────────────────────────────
	// v0: read from env vars. Key names match PIKSHIPP_<UPPER> convention.
	store := secrets.NewEnvStore("PIKSHIPP_")

	hmacKey, err := store.Get(ctx, "session_hmac_key")
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	// ── Database pools ────────────────────────────────────────────────────
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

	// Refuse to serve on out-of-date schema.
	if err := dbtx.CheckSchemaVersion(ctx, poolApp); err != nil {
		return err
	}

	// ── Domain services ───────────────────────────────────────────────────
	clock := core.SystemClock{}

	// Audit — used by most services; OutboxEmitter wired later.
	auditSvc := audit.New(poolApp, nil, clock, log)

	// Policy — seeds 22 definitions into policy_setting_definition on startup.
	policyEngine, err := policy.New(poolAdmin, auditSvc, clock, log)
	if err != nil {
		return fmt.Errorf("policy.New: %w", err)
	}
	_ = policyEngine // consumed by allocation in a future wiring pass

	// Identity + auth.
	identitySvc := identity.New(poolApp, auditSvc, log)

	authSvc, err := auth.NewOpaqueSessionAuth(poolApp, auth.SessionAuthConfig{
		HMACKey:      hmacKey,
		MaxIdle:      24 * time.Hour,
		CookieName:   "pikshipp_session",
		CookiePath:   "/",
		CookieSecure: false, // set true in production via reverse-proxy / load balancer
	}, clock, log)
	if err != nil {
		return fmt.Errorf("auth.New: %w", err)
	}

	// Seller (uses admin pool — lifecycle transitions bypass RLS).
	sellerSvc := seller.New(poolAdmin, auditSvc, log)

	// Wallet.
	walletSvc := wallet.New(poolApp, auditSvc, log)

	// Catalog.
	pickupSvc := catalog.NewPickupService(poolApp, log)
	productSvc := catalog.NewProductService(poolApp, log)

	// Orders.
	orderSvc := orders.New(poolApp, nil, log)

	// Carriers registry — sandbox registered in dev; real adapters in prod.
	// (Delhivery + others added by wiring code that reads carrier credentials
	// from secrets.Store; omitted here until credentials are configured.)

	// Shipments (needs carriers registry — create a stub registry for now).
	import_carriers := func() interface{} { return nil }
	_ = import_carriers
	// shipSvc := shipments.New(poolApp, carriersRegistry, walletSvc, orderSvc, log)
	// For now serve without shipment booking until carrier keys are wired.
	var shipSvc shipments.Service

	// Tracking.
	trackingSvc := tracking.New(poolApp, shipSvc, nil, log)

	// NDR + buyer experience.
	ndrSvc := ndr.New(poolApp)
	buyerSvc := buyerexp.New(poolApp, trackingSvc)

	// Reports (uses reports pool — read-only).
	reportsSvc := reports.New(poolReports)

	// Contracts + limits — needed by the enterprise upgrade flow.
	contractsSvc := contracts.New(poolAdmin, auditSvc, policyEngine)
	limitsGuard := limits.New(poolReports, policyEngine)

	// ── HTTP server ───────────────────────────────────────────────────────
	var srv *http.Server
	if cfg.Role == config.RoleAPI || cfg.Role == config.RoleAll {
		appRouter := httpapi.NewAppRouter(httpapi.AppDeps{
			Pools:     httpapi.Pools{App: poolApp, Reports: poolReports, Admin: poolAdmin},
			Log:       log,
			Auth:      authSvc,
			Identity:  identitySvc,
			Seller:    sellerSvc,
			Pickup:    pickupSvc,
			Product:   productSvc,
			Orders:    orderSvc,
			Shipments: shipSvc,
			Wallet:    walletSvc,
			Tracking:  trackingSvc,
			BuyerExp:  buyerSvc,
			NDR:       ndrSvc,
			Reports:   reportsSvc,
			Contracts: contractsSvc,
			Limits:    limitsGuard,
			AppPool:   poolApp,
			DevMode:   cfg.DevMode,
		}, cfg.HealthcheckTimeout)

		srv = &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           appRouter,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.InfoContext(ctx, "http listening", slog.String("addr", cfg.HTTPAddr))
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.ErrorContext(ctx, "http server stopped", slog.String("err", err.Error()))
				stopFromError()
			}
		}()
	}

	if cfg.Role == config.RoleWorker || cfg.Role == config.RoleAll {
		log.InfoContext(ctx, "worker role ready (river migrations run on first start)")
		// River worker boot: register handlers then start client.
		// Full wiring deferred to P1.river task — outbox forwarder first.
	}

	<-ctx.Done()
	log.InfoContext(context.Background(), "shutdown signal received")
	if srv != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.ErrorContext(shutCtx, "http shutdown", slog.String("err", err.Error()))
		}
	}
	log.InfoContext(context.Background(), "pikshipp stopped")
	return nil
}

func stopFromError() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
}
