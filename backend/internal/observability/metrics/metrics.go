// Package metrics registers the Pikshipp Prometheus metrics and provides
// helpers for instrumenting HTTP handlers and DB transactions.
//
// All metrics live in the "pikshipp" namespace. Register() must be called
// once at binary startup before any metric is observed.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestDuration tracks latency per method+route+status.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "pikshipp",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request latency by method, route, and status code.",
		Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", "route", "status"})

	// DBQueryDuration tracks latency per DB operation (query name + role).
	DBQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "pikshipp",
		Subsystem: "db",
		Name:      "query_duration_seconds",
		Help:      "DB query latency by operation name and role.",
		Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
	}, []string{"op", "role"})

	// DBErrors counts DB errors by operation and role.
	DBErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "pikshipp",
		Subsystem: "db",
		Name:      "errors_total",
		Help:      "Total DB errors by operation and role.",
	}, []string{"op", "role"})

	// OutboxPending tracks how many outbox_event rows are unforwarded.
	OutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "pikshipp",
		Subsystem: "outbox",
		Name:      "pending_rows",
		Help:      "Number of outbox_event rows with enqueued_at IS NULL.",
	})

	// AuditChainVerifications counts verifier runs by result.
	AuditChainVerifications = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "pikshipp",
		Subsystem: "audit",
		Name:      "chain_verifications_total",
		Help:      "Audit chain verification runs by result (ok|broken).",
	}, []string{"result"})

	// ActiveSessions tracks the in-process session cache size (approximation).
	ActiveSessionCacheSize = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "pikshipp",
		Subsystem: "auth",
		Name:      "session_cache_entries",
		Help:      "Approximate number of entries in the in-process session cache.",
	})
)

// Handler returns an http.Handler that exposes /metrics for Prometheus scraping.
func Handler() http.Handler {
	return promhttp.Handler()
}

// InstrumentHandler wraps h with Prometheus HTTP instrumentation.
// route should be the chi route pattern (e.g. "/v1/orders/{orderID}").
func InstrumentHandler(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rw, r)
		HTTPRequestDuration.WithLabelValues(
			r.Method,
			route,
			strconv.Itoa(rw.status),
		).Observe(time.Since(start).Seconds())
	})
}

// ObserveDBOp records a single DB operation's latency and error state.
// Intended to wrap repo calls: defer metrics.ObserveDBOp("getSession", "app", time.Now())(&err)
func ObserveDBOp(op, role string, start time.Time) func(*error) {
	return func(err *error) {
		dur := time.Since(start).Seconds()
		DBQueryDuration.WithLabelValues(op, role).Observe(dur)
		if err != nil && *err != nil {
			DBErrors.WithLabelValues(op, role).Inc()
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
