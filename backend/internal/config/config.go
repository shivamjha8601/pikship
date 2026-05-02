// Package config parses Pikshipp's runtime configuration from env vars.
//
// Per LLD §02-infrastructure/02-configuration: env-var only at v0 (ADR-0010).
// Each env var is prefixed PIKSHIPP_; defaults are conservative; the only
// hard-required value is the database URL.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Role distinguishes which subsystems a process runs.
type Role string

const (
	RoleAPI    Role = "api"
	RoleWorker Role = "worker"
	RoleAll    Role = "all"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAPI, RoleWorker, RoleAll:
		return true
	}
	return false
}

// Config is the resolved runtime config.
type Config struct {
	// Process
	Role     Role   `env:"ROLE" envDefault:"all"`
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	Version  string `env:"VERSION" envDefault:"dev"`

	// Database
	DatabaseURL          string        `env:"DATABASE_URL,required"`
	DBAppMaxConns        int32         `env:"DB_APP_MAX_CONNS"        envDefault:"50"`
	DBAppMinConns        int32         `env:"DB_APP_MIN_CONNS"        envDefault:"5"`
	DBReportsMaxConns    int32         `env:"DB_REPORTS_MAX_CONNS"    envDefault:"10"`
	DBReportsMinConns    int32         `env:"DB_REPORTS_MIN_CONNS"    envDefault:"2"`
	DBAdminMaxConns      int32         `env:"DB_ADMIN_MAX_CONNS"      envDefault:"5"`
	DBAdminMinConns      int32         `env:"DB_ADMIN_MIN_CONNS"      envDefault:"1"`
	DBStatementTimeout   time.Duration `env:"DB_STATEMENT_TIMEOUT"    envDefault:"5s"`

	// Observability
	HealthcheckTimeout time.Duration `env:"HEALTHCHECK_TIMEOUT" envDefault:"3s"`

	// DevMode exposes /v1/auth/dev-login and similar test-only endpoints.
	// MUST be false in production.
	DevMode bool `env:"DEV_MODE" envDefault:"false"`

	// Google OAuth — when ClientID is empty the /v1/auth/google/* routes
	// return 503 (not configured) and the dev-login path stays the only way in.
	GoogleOAuthClientID         string `env:"GOOGLE_OAUTH_CLIENT_ID"          envDefault:""`
	GoogleOAuthRedirectURI      string `env:"GOOGLE_OAUTH_REDIRECT_URI"       envDefault:""`
	GoogleOAuthFrontendReturnURL string `env:"GOOGLE_OAUTH_FRONTEND_RETURN_URL" envDefault:""`
}

// Load parses env vars into Config and validates them.
//
// All env vars use the PIKSHIPP_ prefix.
func Load() (Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{Prefix: "PIKSHIPP_"}); err != nil {
		return Config{}, fmt.Errorf("config.Load: %w", err)
	}
	if !cfg.Role.Valid() {
		return Config{}, fmt.Errorf("config.Load: invalid PIKSHIPP_ROLE=%q (want api|worker|all)", cfg.Role)
	}
	return cfg, nil
}
