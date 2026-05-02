package http

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
)

// --- context keys ---

type loggerCtxKey struct{}
type requestIDCtxKey struct{}

// LoggerFromCtx returns the slog.Logger stored in ctx by InjectLogger, or
// the default logger if none is present.
func LoggerFromCtx(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// RequestIDFromCtx returns the request ID stored by RequestID middleware.
func RequestIDFromCtx(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return id
	}
	return ""
}

// --- middlewares ---

// RequestID generates a UUID request ID per request, stores it in context,
// and echoes it via X-Request-Id response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.New().String()
		}
		ctx := context.WithValue(r.Context(), requestIDCtxKey{}, id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// InjectLogger stores log in context, enriched with the request ID.
// Must run after RequestID.
func InjectLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enriched := log.With(slog.String("request_id", RequestIDFromCtx(r.Context())))
			ctx := context.WithValue(r.Context(), loggerCtxKey{}, enriched)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestLogger logs every request with method, path, status, and latency.
// Must run after InjectLogger and RequestID.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		LoggerFromCtx(r.Context()).InfoContext(r.Context(), "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("latency", time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Recover catches panics in handlers and returns 500 with a generic message.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout wraps each request in a context with the given deadline.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SecurityHeaders adds standard security response headers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		// HSTS only for TLS — in production TLS is terminated at the load balancer
		// which sets HSTS itself, so we skip it here to avoid dev confusion.
		next.ServeHTTP(w, r)
	})
}

// SellerScope enforces that the authenticated user has a seller selected
// before reaching seller-scoped handlers. Must run after auth.Middleware.
//
// If Principal.SellerID is zero the request is rejected with 403 and a
// machine-readable body directing the client to POST /v1/auth/select-seller.
func SellerScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if p.SellerID.IsZero() {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "seller_not_selected",
				"hint":  "POST /v1/auth/select-seller first",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
