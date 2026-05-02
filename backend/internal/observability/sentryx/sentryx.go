// Package sentryx wraps github.com/getsentry/sentry-go with the bits we
// actually use: process init, a chi-compatible HTTP middleware that creates
// one transaction per request and forwards panics, and a flush helper for
// graceful shutdown.
//
// Empty DSN means Sentry is disabled — Init becomes a no-op and Middleware
// passes through. That keeps unit/integration tests free of network calls.
package sentryx

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/config"
)

// Init configures the global Sentry hub. Returns a flush function the caller
// should defer so events buffered at shutdown still get sent. Empty DSN
// disables everything.
func Init(cfg config.Config, log *slog.Logger) (flush func(), err error) {
	if cfg.SentryDSN == "" {
		log.Info("sentry disabled (empty DSN)")
		return func() {}, nil
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		Environment:      cfg.SentryEnvironment,
		Release:          cfg.Version,
		TracesSampleRate: cfg.SentryTracesSampleRate,
		EnableTracing:    true,
		AttachStacktrace: true,
		// SendDefaultPII=false keeps Authorization headers and request bodies
		// out of submitted events. Override only when you need them.
		SendDefaultPII: false,
	}); err != nil {
		return nil, fmt.Errorf("sentry.Init: %w", err)
	}
	log.Info("sentry enabled",
		slog.String("environment", cfg.SentryEnvironment),
		slog.Float64("traces_sample_rate", cfg.SentryTracesSampleRate))
	return func() { sentry.Flush(2 * time.Second) }, nil
}

// shouldSkip drops paths that would otherwise dominate the transaction list
// without telling us anything useful.
func shouldSkip(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return true
	}
	return false
}

// Middleware creates a Sentry transaction per HTTP request, names it
// "{METHOD} {chi-route-pattern}" once chi has resolved the route, and
// forwards any panic into Sentry before re-raising it for the outer
// Recover middleware to render a 500.
//
// Place this AFTER the panic-recovery middleware in the chain — outer
// Recover catches the re-raised panic; inner sentryx records it.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldSkip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		hub := sentry.CurrentHub().Clone()
		ctx := sentry.SetHubOnContext(r.Context(), hub)

		// Initial name = method + raw URL path; refined to chi pattern below.
		txn := sentry.StartTransaction(ctx,
			fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			sentry.WithOpName("http.server"),
			sentry.ContinueFromRequest(r),
			sentry.WithTransactionSource(sentry.SourceURL),
		)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if pattern := rctx.RoutePattern(); pattern != "" {
					txn.Name = fmt.Sprintf("%s %s", r.Method, pattern)
					txn.Source = sentry.SourceRoute
				}
			}
			txn.Status = httpStatusToSpanStatus(rec.status)
			txn.SetData("http.response.status_code", rec.status)
			txn.Finish()

			if p := recover(); p != nil {
				hub.RecoverWithContext(ctx, p)
				// Re-raise so the outer Recover middleware can still render 500.
				panic(p)
			}
		}()

		next.ServeHTTP(rec, r.WithContext(txn.Context()))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func httpStatusToSpanStatus(code int) sentry.SpanStatus {
	switch {
	case code < 400:
		return sentry.SpanStatusOK
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return sentry.SpanStatusPermissionDenied
	case code == http.StatusNotFound:
		return sentry.SpanStatusNotFound
	case code == http.StatusRequestTimeout:
		return sentry.SpanStatusDeadlineExceeded
	case code == http.StatusTooManyRequests:
		return sentry.SpanStatusResourceExhausted
	case code < 500:
		return sentry.SpanStatusInvalidArgument
	case code == http.StatusServiceUnavailable:
		return sentry.SpanStatusUnavailable
	default:
		return sentry.SpanStatusInternalError
	}
}
