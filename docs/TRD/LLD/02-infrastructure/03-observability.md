# Infrastructure: Observability (`internal/observability`)

> Structured logging, request context, panic recovery, request IDs. Foundation for every other module.

## Purpose

- `slog`-based structured JSON logging shipped to stdout.
- Per-request `request_id` correlation.
- Logger flowed through context.
- Panic recovery middleware.
- Vector configuration for shipping to CloudWatch.

## Dependencies

- `log/slog` (stdlib)
- `context` (stdlib)
- `os` (stdlib)
- `runtime/debug` (stdlib)

## Package layout

```
internal/observability/
├── doc.go
├── logger.go            ← logger construction
├── context.go           ← context propagation
├── request_id.go        ← request ID generation + middleware
├── recover.go           ← panic recovery middleware
├── slog_handler.go      ← custom handler (adds default attrs)
└── tests
```

## Public API

```go
// Package observability provides logging, tracing, and request-correlation
// primitives used across all Pikshipp modules.
//
// At v0 we ship structured JSON logs to stdout; Vector ships them to
// CloudWatch. No metrics service; no distributed tracing. v1 adds Sentry +
// OpenTelemetry.
package observability

import (
    "context"
    "io"
    "log/slog"
    "os"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/core"
)

// NewLogger constructs the production slog.Logger.
//
// level: 'debug' | 'info' | 'warn' | 'error'.
// format: 'json' | 'text'.
// version: build version (git SHA); appears as 'version' attribute on every line.
//
// The returned logger writes to os.Stdout. Output is consumed by Vector
// (or, in dev, by terminal).
func NewLogger(level, format, version string) *slog.Logger {
    return newLoggerWithOutput(level, format, version, os.Stdout)
}

// newLoggerWithOutput is the testable variant.
func newLoggerWithOutput(level, format, version string, w io.Writer) *slog.Logger {
    var lvl slog.Level
    if err := lvl.UnmarshalText([]byte(level)); err != nil {
        lvl = slog.LevelInfo
    }

    opts := &slog.HandlerOptions{
        Level:     lvl,
        AddSource: true,
        ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
            // Convert time to RFC3339Nano UTC for consistency
            if a.Key == slog.TimeKey {
                t := a.Value.Time().UTC()
                return slog.String("timestamp", t.Format("2006-01-02T15:04:05.000Z07:00"))
            }
            // Rename levels
            if a.Key == slog.LevelKey {
                return slog.String("level", a.Value.String())
            }
            return a
        },
    }

    var h slog.Handler
    if format == "json" {
        h = slog.NewJSONHandler(w, opts)
    } else {
        h = slog.NewTextHandler(w, opts)
    }

    base := slog.New(h).With(
        slog.String("service", "pikshipp"),
        slog.String("version", version),
    )
    return base
}

// ---------- Context ----------

type ctxKey int

const (
    keyLogger    ctxKey = iota
    keyRequestID
    keyUserID
    keySellerID
)

// LoggerFrom returns the logger stored in ctx, or a no-op logger if none.
//
// Domain code should use this exclusively (never the bare slog.Default()).
func LoggerFrom(ctx context.Context) *slog.Logger {
    if l, ok := ctx.Value(keyLogger).(*slog.Logger); ok {
        return l
    }
    return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// WithLogger returns a context with logger attached.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
    return context.WithValue(ctx, keyLogger, log)
}

// RequestIDFrom returns the request ID stored in ctx, or "" if none.
func RequestIDFrom(ctx context.Context) string {
    if s, ok := ctx.Value(keyRequestID).(string); ok {
        return s
    }
    return ""
}

// WithRequestID returns a context with request ID attached.
func WithRequestID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, keyRequestID, id)
}

// UserIDFrom returns the user ID stored in ctx, or zero-value if none.
func UserIDFrom(ctx context.Context) core.UserID {
    if id, ok := ctx.Value(keyUserID).(core.UserID); ok {
        return id
    }
    return core.UserID{}
}

// WithUserID returns a context with user ID attached.
func WithUserID(ctx context.Context, id core.UserID) context.Context {
    return context.WithValue(ctx, keyUserID, id)
}

// SellerIDFrom returns the seller ID stored in ctx, or zero-value if none.
func SellerIDFrom(ctx context.Context) core.SellerID {
    if id, ok := ctx.Value(keySellerID).(core.SellerID); ok {
        return id
    }
    return core.SellerID{}
}

// WithSellerID returns a context with seller ID attached.
func WithSellerID(ctx context.Context, id core.SellerID) context.Context {
    return context.WithValue(ctx, keySellerID, id)
}

// EnrichLogger attaches request_id, user_id, seller_id to the context's
// logger if they're set on the context. Used by middleware after auth.
func EnrichLogger(ctx context.Context) context.Context {
    log := LoggerFrom(ctx)
    if id := RequestIDFrom(ctx); id != "" {
        log = log.With(slog.String("request_id", id))
    }
    if id := UserIDFrom(ctx); !id.IsZero() {
        log = log.With(slog.String("user_id", id.String()))
    }
    if id := SellerIDFrom(ctx); !id.IsZero() {
        log = log.With(slog.String("seller_id", id.String()))
    }
    return WithLogger(ctx, log)
}
```

## Request ID middleware

```go
// internal/observability/request_id.go
package observability

import (
    "net/http"

    "github.com/google/uuid"
)

const HeaderRequestID = "X-Request-ID"

// RequestIDMiddleware wraps a handler. For each request:
//   1. If the X-Request-ID header is present, use it.
//   2. Else, generate a new UUID.
//   3. Attach to context.
//   4. Echo back in response header.
//
// Request IDs flow through all logs and (where applicable) audit events,
// enabling end-to-end correlation.
func RequestIDMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := r.Header.Get(HeaderRequestID)
        if id == "" {
            id = uuid.New().String()
        } else if !isValidRequestID(id) {
            // Reject malformed inbound IDs (security: prevent log injection)
            id = uuid.New().String()
        }

        ctx := WithRequestID(r.Context(), id)
        ctx = EnrichLogger(ctx)
        w.Header().Set(HeaderRequestID, id)

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// isValidRequestID returns true if s is a UUID-shaped string of acceptable length.
//
// We accept any UUID format (with or without dashes), max 50 chars (allows for
// trace-id-style nested IDs), only hex/dash chars. This is a security check
// to prevent log injection from upstream services.
func isValidRequestID(s string) bool {
    if len(s) == 0 || len(s) > 50 {
        return false
    }
    for _, c := range s {
        if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-') {
            return false
        }
    }
    return true
}
```

## Panic recovery middleware

```go
// internal/observability/recover.go
package observability

import (
    "log/slog"
    "net/http"
    "runtime/debug"
)

// RecoverMiddleware wraps a handler with panic recovery.
//
// On panic:
//   1. Log the panic with full stack at error level.
//   2. Return 500 Internal Server Error.
//   3. Echo the request_id back so the user can reference it.
//
// Note: this catches goroutine panics ONLY in the request handler goroutine.
// Goroutines spawned by handlers must have their own recovery (or use the
// `safeGo` helper from this package).
func RecoverMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                log := LoggerFrom(r.Context())
                stack := debug.Stack()
                log.ErrorContext(r.Context(), "panic recovered",
                    slog.Any("panic", rec),
                    slog.String("stack", string(stack)),
                    slog.String("path", r.URL.Path),
                    slog.String("method", r.Method))

                if !headerWritten(w) {
                    w.Header().Set("Content-Type", "application/json")
                    w.WriteHeader(http.StatusInternalServerError)
                    w.Write([]byte(`{"error":{"type":"internal_error","code":"panic","request_id":"` + RequestIDFrom(r.Context()) + `"}}`))
                }
            }
        }()
        next.ServeHTTP(w, r)
    })
}

// headerWritten returns true if the ResponseWriter has already written headers.
// (chi.middleware.NewWrapResponseWriter exposes this; we use chi's wrapped writer.)
func headerWritten(w http.ResponseWriter) bool {
    if ww, ok := w.(interface{ Status() int }); ok {
        return ww.Status() != 0
    }
    return false
}

// SafeGo runs fn in a goroutine with panic recovery + logger from ctx.
// Use whenever you start a goroutine inside a handler that outlives the request.
func SafeGo(ctx context.Context, fn func(context.Context)) {
    go func() {
        defer func() {
            if rec := recover(); rec != nil {
                log := LoggerFrom(ctx)
                stack := debug.Stack()
                log.ErrorContext(ctx, "goroutine panic recovered",
                    slog.Any("panic", rec),
                    slog.String("stack", string(stack)))
            }
        }()
        fn(ctx)
    }()
}
```

## Vector configuration

```toml
# deploy/prod/vector.toml

[sources.journald]
type = "journald"
include_units = ["pikshipp.service"]
batch_size = 100

[transforms.parse]
type = "remap"
inputs = ["journald"]
source = '''
# Parse JSON from systemd journal MESSAGE field
parsed, err = parse_json(.message)
if err == null {
  . = parsed
}
.host = get_hostname!()
.env = "${ENVIRONMENT}"
'''

[transforms.scrub_secrets]
type = "remap"
inputs = ["parse"]
source = '''
# Defensive: scrub any field that LOOKS like a secret
del(.password, .secret, .token, .api_key, .authorization)
'''

[sinks.cloudwatch]
type = "aws_cloudwatch_logs"
inputs = ["scrub_secrets"]
region = "ap-south-1"
group_name = "/pikshipp/${ENVIRONMENT}"
stream_name = "{{ host }}"
encoding.codec = "json"
batch.timeout_secs = 5
batch.max_bytes = 1048576
buffer.type = "disk"
buffer.max_size = 1073741824  # 1 GB
buffer.when_full = "block"

[sinks.cloudwatch.healthcheck]
enabled = true
```

`pikshipp.service` runs as a systemd unit; `vector.service` runs alongside; both restart-on-failure.

## Logging conventions in code

```go
// In a handler:
log := observability.LoggerFrom(ctx)
log.InfoContext(ctx, "shipment booked",
    slog.String("awb", awb),
    slog.String("carrier", carrierID.String()),
    slog.Int64("amount_minor", int64(amount)))

// On a recoverable warning:
log.WarnContext(ctx, "carrier API slow",
    slog.String("carrier", carrierID.String()),
    slog.Duration("elapsed", elapsed))

// On an error path:
log.ErrorContext(ctx, "wallet operation failed",
    slog.Any("error", err))
return err

// Debug only in dev:
log.DebugContext(ctx, "computed allocation scores",
    slog.Any("scores", scores))
```

### Structured fields, not formatted strings

```go
// Bad
log.Info(fmt.Sprintf("booked shipment %s for seller %s", awb, sellerID))

// Good
log.InfoContext(ctx, "booked shipment",
    slog.String("awb", awb),
    slog.String("seller_id", sellerID.String()))
```

The structured form is queryable in CloudWatch Insights; the formatted string is a haystack.

### What never logs

- Secrets (passwords, tokens, API keys, session tokens).
- Credit card numbers, CVV.
- Aadhaar (raw or masked image data).
- Bank account numbers (only last 4 acceptable).
- Buyer phone numbers in full (last 4 digits OK with consent).

`gosec` lint rules + manual review enforce.

## Implementation notes

### Why slog (stdlib, Go 1.21+)

- Stdlib means no external dep churn.
- Structured by default; JSON output is one option.
- Context-aware; `*Context` variants pick up logger from ctx.
- Future-proof: when stdlib adds tracing, it'll integrate.

### AddSource for stack info

`slog.HandlerOptions{AddSource: true}` adds `source` field with file:line. Cheap; ~5% overhead per call. Worth it for debuggability.

### ReplaceAttr for normalization

We rename `time` → `timestamp` and force UTC RFC3339Nano. CloudWatch and AWS Logs Insights both prefer this form.

### Context propagation

slog 1.21 has `*Context` variants that automatically extract attributes from context. We use these everywhere — the logger always carries `request_id`, `seller_id`, `user_id` once set.

### No global logger singleton

`slog.SetDefault` is **not used**. Every place gets its logger from context. This makes test isolation trivial (tests inject their own logger) and prevents the "everything writes to a global, can't isolate" problem.

## Test patterns

```go
func TestRequestIDMiddleware_GeneratesUUID(t *testing.T) {
    var captured string
    next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        captured = observability.RequestIDFrom(r.Context())
    })
    h := observability.RequestIDMiddleware(next)

    req := httptest.NewRequest("GET", "/", nil)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, req)

    require.NotEmpty(t, captured)
    require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddleware_PreservesValid(t *testing.T) {
    valid := "abc-123-def"
    next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.Equal(t, valid, observability.RequestIDFrom(r.Context()))
    })
    h := observability.RequestIDMiddleware(next)

    req := httptest.NewRequest("GET", "/", nil)
    req.Header.Set("X-Request-ID", valid)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, req)
}

func TestRequestIDMiddleware_RejectsMalicious(t *testing.T) {
    bad := "abc\nLOG_INJECTION"
    var captured string
    next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        captured = observability.RequestIDFrom(r.Context())
    })
    h := observability.RequestIDMiddleware(next)

    req := httptest.NewRequest("GET", "/", nil)
    req.Header.Set("X-Request-ID", bad)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, req)

    // Should have generated a fresh UUID, not used the malicious string
    require.NotEqual(t, bad, captured)
    require.True(t, isValidRequestID(captured))
}

func TestRecoverMiddleware_CatchesPanic(t *testing.T) {
    next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        panic("oops")
    })
    var buf bytes.Buffer
    log := newLoggerWithOutput("debug", "json", "test", &buf)
    h := observability.RecoverMiddleware(next)

    ctx := observability.WithLogger(context.Background(), log)
    req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, req)

    require.Equal(t, http.StatusInternalServerError, rec.Code)
    require.Contains(t, buf.String(), "panic recovered")
    require.Contains(t, buf.String(), "oops")
}

func TestEnrichLogger_AddsAttrs(t *testing.T) {
    var buf bytes.Buffer
    log := newLoggerWithOutput("debug", "json", "test", &buf)

    ctx := context.Background()
    ctx = observability.WithLogger(ctx, log)
    ctx = observability.WithRequestID(ctx, "test-req-id")
    ctx = observability.WithSellerID(ctx, core.NewSellerID())
    ctx = observability.EnrichLogger(ctx)

    log = observability.LoggerFrom(ctx)
    log.InfoContext(ctx, "test")

    require.Contains(t, buf.String(), `"request_id":"test-req-id"`)
    require.Contains(t, buf.String(), `"seller_id"`)
}
```

## Performance

- `LoggerFrom`: 1 type assertion; ~1ns.
- `EnrichLogger`: 3 lookups + builds new logger; ~200ns.
- Per log call (slog JSON): ~1µs (allocations for the JSON line).
- Vector overhead per log line: ~10µs (parse + ship).

At 1000 RPS with avg 5 log lines per request = 5000 log lines/sec = ~5ms total CPU per second per instance. Negligible.

## Microbenchmarks

```go
func BenchmarkLogger_Info(b *testing.B) {
    log := newLoggerWithOutput("info", "json", "bench", io.Discard)
    ctx := context.Background()
    for i := 0; i < b.N; i++ {
        log.InfoContext(ctx, "test message",
            slog.String("k1", "v1"),
            slog.Int("k2", 42))
    }
}

func BenchmarkEnrichLogger(b *testing.B) {
    log := newLoggerWithOutput("info", "json", "bench", io.Discard)
    base := observability.WithLogger(context.Background(), log)
    base = observability.WithRequestID(base, "abc-123")
    base = observability.WithSellerID(base, core.NewSellerID())
    for i := 0; i < b.N; i++ {
        observability.EnrichLogger(base)
    }
}
```

Targets: `BenchmarkLogger_Info` < 2µs/op; `BenchmarkEnrichLogger` < 500ns/op.

## Open questions

- Sentry SDK at v1: integrate as a slog `Handler` that mirrors errors to Sentry, or as a separate observability primitive? Lean: slog handler — keeps domain code clean.
- OpenTelemetry tracing: when added, the `Logger` must include the active span/trace ID. Plumb via context; document at v1.
- Log sampling for very-high-cardinality events (e.g., per-event tracking-event ingest)? Currently log all; add sampling if Vector throughput becomes a bottleneck.

## References

- HLD `04-cross-cutting/02-observability.md` (high-level approach).
- ADR 0011 (Vector → CloudWatch from day 0).
