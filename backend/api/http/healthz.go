// Package http hosts the chi router and HTTP-layer adapters. This file
// is the minimum viable surface — /healthz and /readyz — so the binary
// can boot before domain handlers are wired in.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/observability/metrics"
)

// Pools holds the three pgxpool instances the API process needs.
type Pools struct {
	App     *pgxpool.Pool
	Reports *pgxpool.Pool
	Admin   *pgxpool.Pool
}

// NewRouter returns a chi.Router with /healthz and /readyz wired.
//
// /healthz — process is alive (no DB check).
// /readyz  — process can serve traffic (pings the app pool).
func NewRouter(pools Pools, healthTimeout time.Duration) chi.Router {
	r := chi.NewRouter()
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(pools, healthTimeout))
	r.Handle("/metrics", metrics.Handler())
	return r
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func readyz(pools Pools, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := pools.App.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unready",
				"reason": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
