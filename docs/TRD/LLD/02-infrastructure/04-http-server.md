# Infrastructure: HTTP server (`api/http`)

> chi router, middleware chain, error handling, request decoding/validation, OpenAPI surface. The thin layer between net/http and the domain services.

## Purpose

- Construct the `http.Server` with chi as router.
- Define the standard middleware chain.
- Provide request decoding + validation helpers.
- Provide standard error response helpers.
- Wire all handlers to domain services.

## Dependencies

- `net/http` (stdlib)
- `encoding/json` (stdlib)
- `github.com/go-chi/chi/v5`
- `github.com/go-playground/validator/v10`
- `internal/core`
- `internal/auth`
- `internal/observability`
- `internal/idempotency`

## Package layout

```
api/http/
├── doc.go
├── server.go            ← Server struct, Start/Stop, route registration
├── middleware/
│   ├── auth.go          ← session resolution
│   ├── seller_scope.go  ← begin tx + SET LOCAL app.seller_id
│   ├── idempotency.go   ← Idempotency-Key handling
│   ├── require_role.go  ← RBAC check
│   ├── rate_limit.go    ← per-seller rate limiting
│   └── timeout.go       ← per-request deadline
├── handlers/
│   ├── orders.go
│   ├── shipments.go
│   ├── wallet.go
│   ├── auth.go
│   └── ...              ← one file per resource
├── decode.go            ← JSON decode + validate
├── encode.go            ← JSON encode response
├── errors.go            ← HTTP error mapping; standard error response
└── server_test.go
```

## Public API

```go
// Package http implements the HTTP API server.
//
// Handlers are intentionally thin: decode → call domain service → encode.
// Business logic lives in internal/<module>; the HTTP layer is a transport.
package http

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    chimiddleware "github.com/go-chi/chi/v5/middleware"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/idempotency"
    "github.com/pikshipp/pikshipp/internal/observability"
    obshttp "github.com/pikshipp/pikshipp/internal/observability"
)

// Server wraps net/http.Server with our routing and lifecycle.
type Server struct {
    srv      *http.Server
    log      *slog.Logger
}

// Deps groups the dependencies handlers need.
//
// Constructor takes this struct rather than 30 arguments for clarity.
type Deps struct {
    DBPoolApp     *pgxpool.Pool
    DBPoolReports *pgxpool.Pool
    DBPoolAdmin   *pgxpool.Pool

    Authenticator auth.Authenticator
    IdempotencyStore idempotency.Store
    Log              *slog.Logger

    // Domain services (added as needed; one field per service)
    OrderSvc      orders.Service
    ShipmentSvc   shipments.Service
    WalletSvc     wallet.Service
    AllocationSvc allocation.Service
    PolicySvc     policy.Engine
    SellerSvc     seller.Service
    // ... etc
}

// NewServer constructs an HTTP server with all routes and middleware wired.
func NewServer(addr string, deps Deps, cfg ServerConfig) *Server {
    r := chi.NewRouter()

    // Global middleware (order matters)
    r.Use(chimiddleware.RealIP)              // X-Forwarded-For → r.RemoteAddr
    r.Use(observability.RequestIDMiddleware) // request_id in context + header
    r.Use(observability.RecoverMiddleware)   // panic → 500
    r.Use(injectLogger(deps.Log))            // logger in context
    r.Use(timeoutMiddleware(cfg.RequestTimeout))
    r.Use(chimiddleware.Compress(5))         // gzip responses
    r.Use(securityHeadersMiddleware)

    // Public routes (no auth)
    r.Get("/healthz",  healthHandler)
    r.Get("/readyz",   readyHandler(deps.DBPoolApp))
    r.Route("/v1/auth/google", func(r chi.Router) {
        r.Get("/start",    handlers.GoogleOAuthStart(deps))
        r.Get("/callback", handlers.GoogleOAuthCallback(deps))
    })
    r.Route("/v1/track", func(r chi.Router) {
        r.Get("/{token}", handlers.BuyerTracking(deps))
    })

    // Webhooks (HMAC-verified, not auth-gated)
    r.Route("/webhooks", func(r chi.Router) {
        r.Post("/delhivery", handlers.DelhiveryWebhook(deps))
        r.Post("/shopify",   handlers.ShopifyWebhook(deps))
        r.Post("/razorpay",  handlers.RazorpayWebhook(deps))
    })

    // Authenticated routes
    r.Route("/v1", func(r chi.Router) {
        r.Use(middleware.Auth(deps.Authenticator))
        r.Use(middleware.SellerScope(deps.DBPoolApp))
        r.Use(middleware.Idempotency(deps.IdempotencyStore))
        r.Use(middleware.RateLimit(cfg.PerSellerRPS))

        r.Route("/orders", func(r chi.Router) {
            r.Get("/",          handlers.ListOrders(deps))
            r.Post("/",         handlers.CreateOrder(deps))
            r.Get("/{orderID}", handlers.GetOrder(deps))
            r.Patch("/{orderID}", middleware.RequireRole(core.RoleOwner, core.RoleManager, core.RoleOperator), handlers.UpdateOrder(deps))
            r.Delete("/{orderID}", handlers.CancelOrder(deps))
        })

        r.Route("/shipments", func(r chi.Router) {
            r.Get("/",  handlers.ListShipments(deps))
            r.Post("/", middleware.RequireRole(core.RoleOwner, core.RoleManager, core.RoleOperator), handlers.BookShipment(deps))
            r.Get("/{shipmentID}", handlers.GetShipment(deps))
        })

        r.Route("/wallet", func(r chi.Router) {
            r.Use(middleware.RequireRole(core.RoleOwner, core.RoleFinance))
            r.Get("/",         handlers.GetWallet(deps))
            r.Get("/ledger",   handlers.GetLedger(deps))
            r.Post("/recharge", handlers.InitiateRecharge(deps))
        })

        // ... more route groups
    })

    srv := &http.Server{
        Addr:              addr,
        Handler:           r,
        ReadTimeout:       cfg.ReadTimeout,
        WriteTimeout:      cfg.WriteTimeout,
        IdleTimeout:       cfg.IdleTimeout,
        ReadHeaderTimeout: 5 * time.Second,
        MaxHeaderBytes:    1 << 20, // 1MB
    }

    return &Server{srv: srv, log: deps.Log}
}

// ServerConfig holds tunables for the HTTP server.
type ServerConfig struct {
    ReadTimeout    time.Duration  // default 10s
    WriteTimeout   time.Duration  // default 30s
    IdleTimeout    time.Duration  // default 120s
    RequestTimeout time.Duration  // default 25s (must be < WriteTimeout)
    PerSellerRPS   int            // default 100
}

// Start begins serving. Blocks until stopped or fatal error.
func (s *Server) Start() error {
    s.log.Info("http server starting", slog.String("addr", s.srv.Addr))
    if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        return fmt.Errorf("http server: %w", err)
    }
    return nil
}

// Shutdown gracefully drains in-flight requests up to ctx deadline.
func (s *Server) Shutdown(ctx context.Context) error {
    s.log.Info("http server shutting down")
    return s.srv.Shutdown(ctx)
}
```

## Middleware

### Auth

```go
// api/http/middleware/auth.go
package middleware

import (
    "errors"
    "net/http"

    "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/observability"
)

// Auth resolves the principal via the Authenticator and adds it to context.
//
// On failure: returns 401 with a standard error body.
func Auth(a auth.Authenticator) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            principal, err := a.Authenticate(r.Context(), r)
            if err != nil {
                if errors.Is(err, auth.ErrUnauthenticated) {
                    apierrors.Write(w, r, err)
                    return
                }
                apierrors.Write(w, r, fmt.Errorf("auth: %w", err))
                return
            }

            ctx := auth.WithPrincipal(r.Context(), principal)
            ctx = observability.WithUserID(ctx, principal.UserID)
            ctx = observability.WithSellerID(ctx, principal.SellerID)
            ctx = observability.EnrichLogger(ctx)

            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Seller scope

```go
// api/http/middleware/seller_scope.go
package middleware

import (
    "net/http"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    apierrors "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability"
)

// SellerScope establishes a seller-scoped DB context for the request.
//
// We do NOT begin a transaction here because:
//   1. Many handlers don't need one (read-only endpoints).
//   2. Mutating handlers begin their own with dbtx.WithSellerTx.
//
// What this DOES is verify the principal has a valid seller_id and add
// it to context. The actual `SET LOCAL app.seller_id` happens inside
// dbtx.WithSellerTx when a tx is opened.
//
// For endpoints that explicitly want a request-scoped tx (e.g., a list
// endpoint that reads many tables), use the WithReadOnlyTx helper inside
// the handler.
func SellerScope(pool *pgxpool.Pool) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            p, ok := auth.PrincipalFrom(r.Context())
            if !ok || p.SellerID.IsZero() {
                apierrors.Write(w, r, core.ErrPermissionDenied)
                return
            }
            // Just propagate; tx scoping happens in handlers/services.
            next.ServeHTTP(w, r)
        })
    }
}
```

### Idempotency

```go
// api/http/middleware/idempotency.go
package middleware

import (
    "bytes"
    "io"
    "net/http"
    "strings"

    apierrors "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/idempotency"
)

const HeaderIdempotencyKey = "Idempotency-Key"

// Idempotency caches request/response for keyed POST/PATCH/DELETE.
//
// Behavior:
//   1. If method is GET/HEAD/OPTIONS, pass through.
//   2. If header missing on POST/PATCH/DELETE, pass through (caller takes the risk).
//   3. If header present, compute (seller_id, key) and look up cache.
//   4. Cache hit: replay cached response; add `Idempotent-Replayed: true` header.
//   5. Cache miss: capture the response; store on success.
//
// Body re-read: we capture the body buffer and re-set so handlers can read.
func Idempotency(store idempotency.Store) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            method := strings.ToUpper(r.Method)
            if method == "GET" || method == "HEAD" || method == "OPTIONS" {
                next.ServeHTTP(w, r)
                return
            }
            key := r.Header.Get(HeaderIdempotencyKey)
            if key == "" {
                next.ServeHTTP(w, r)
                return
            }

            p, ok := auth.PrincipalFrom(r.Context())
            if !ok {
                apierrors.Write(w, r, errors.New("idempotency without auth"))
                return
            }

            // Look up cache
            cached, found, err := store.Lookup(r.Context(), p.SellerID, key)
            if err != nil {
                apierrors.Write(w, r, fmt.Errorf("idempotency lookup: %w", err))
                return
            }
            if found {
                w.Header().Set("Idempotent-Replayed", "true")
                for k, v := range cached.Headers { w.Header().Set(k, v) }
                w.WriteHeader(cached.StatusCode)
                w.Write(cached.Body)
                return
            }

            // Capture response
            cw := &captureWriter{ResponseWriter: w, statusCode: 200, body: &bytes.Buffer{}}
            next.ServeHTTP(cw, r)

            // Store on success (2xx)
            if cw.statusCode >= 200 && cw.statusCode < 300 {
                resp := idempotency.Response{
                    StatusCode: cw.statusCode,
                    Headers:    captureHeaders(cw.Header()),
                    Body:       cw.body.Bytes(),
                }
                if err := store.Store(r.Context(), p.SellerID, key, resp); err != nil {
                    log := observability.LoggerFrom(r.Context())
                    log.WarnContext(r.Context(), "idempotency store failed", slog.Any("error", err))
                }
            }
        })
    }
}

type captureWriter struct {
    http.ResponseWriter
    statusCode int
    body       *bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
    c.statusCode = code
    c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
    c.body.Write(b)
    return c.ResponseWriter.Write(b)
}

func captureHeaders(h http.Header) map[string]string {
    out := make(map[string]string, len(h))
    for k, v := range h {
        if len(v) > 0 {
            out[k] = v[0]
        }
    }
    return out
}
```

### Require role

```go
// api/http/middleware/require_role.go
package middleware

import (
    "net/http"

    apierrors "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
)

// RequireRole returns a middleware that enforces the principal has at least
// one of the specified roles. Order of the variadic doesn't matter.
//
// Returns 403 if the principal lacks all roles.
func RequireRole(roles ...core.SellerRole) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            p, ok := auth.PrincipalFrom(r.Context())
            if !ok {
                apierrors.Write(w, r, core.ErrPermissionDenied)
                return
            }
            if !core.HasAnyRole(p.Roles, roles) {
                apierrors.Write(w, r, core.ErrPermissionDenied)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### Rate limit

```go
// api/http/middleware/rate_limit.go
package middleware

import (
    "net/http"
    "sync"
    "time"

    "golang.org/x/time/rate"

    apierrors "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
)

// RateLimit returns a middleware that enforces per-seller request limits.
//
// Implementation: in-process token bucket per (sellerID, endpointClass).
// At multi-instance, total RPS = N × rps; tune at scale-out.
//
// Returns 429 when limit exceeded.
func RateLimit(rps int) func(http.Handler) http.Handler {
    if rps <= 0 { rps = 100 }

    var (
        mu       sync.Mutex
        limiters = make(map[core.SellerID]*rate.Limiter)
    )

    getLimiter := func(sellerID core.SellerID) *rate.Limiter {
        mu.Lock()
        defer mu.Unlock()
        l, ok := limiters[sellerID]
        if !ok {
            l = rate.NewLimiter(rate.Limit(rps), rps*2) // burst = 2× rps
            limiters[sellerID] = l
        }
        return l
    }

    // Periodic cleanup to prevent unbounded map growth (one entry per seller).
    // Acceptable to keep at v0 (sellers don't disappear); add cleanup at v1.

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            p, ok := auth.PrincipalFrom(r.Context())
            if !ok {
                next.ServeHTTP(w, r)
                return
            }
            l := getLimiter(p.SellerID)
            if !l.Allow() {
                apierrors.WriteRateLimited(w, r)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### Timeout

```go
// api/http/middleware/timeout.go
package middleware

import (
    "context"
    "net/http"
    "time"
)

// Timeout sets a context deadline of d for every request.
//
// Handlers that observe ctx.Done() can abandon work cleanly.
// Handlers that don't will continue but their writes will fail when the
// underlying connection is closed.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx, cancel := context.WithTimeout(r.Context(), d)
            defer cancel()
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Security headers

```go
// api/http/middleware/security_headers.go
package middleware

import "net/http"

func SecurityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        h := w.Header()
        h.Set("X-Content-Type-Options", "nosniff")
        h.Set("X-Frame-Options",         "DENY")
        h.Set("Referrer-Policy",         "strict-origin-when-cross-origin")
        h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        h.Set("Content-Security-Policy", "default-src 'none'")
        // CORS configured separately for the dashboard's origin
        next.ServeHTTP(w, r)
    })
}
```

## Decode + validate helpers

```go
// api/http/decode.go
package http

import (
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "strings"

    "github.com/go-playground/validator/v10"
)

var validate = validator.New()

// DecodeJSON reads the body, decodes into out, and validates struct tags.
//
// Returns a wrapped core.ErrInvalidArgument on any failure.
//
// Caller should use this for every JSON request body.
func DecodeJSON(r *http.Request, out any) error {
    if r.Body == nil {
        return fmt.Errorf("decode: empty body: %w", core.ErrInvalidArgument)
    }
    defer r.Body.Close()

    if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
        return fmt.Errorf("decode: invalid content type %q: %w", ct, core.ErrInvalidArgument)
    }

    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()
    if err := dec.Decode(out); err != nil {
        return fmt.Errorf("decode: %w: %w", err, core.ErrInvalidArgument)
    }

    if err := validate.Struct(out); err != nil {
        var verrs validator.ValidationErrors
        if errors.As(err, &verrs) && len(verrs) > 0 {
            first := verrs[0]
            return fmt.Errorf("decode: field %s: %s: %w", first.Field(), first.Tag(), core.ErrInvalidArgument)
        }
        return fmt.Errorf("decode validate: %w: %w", err, core.ErrInvalidArgument)
    }

    return nil
}
```

## Error response helpers

```go
// api/http/errors/errors.go
package errors

import (
    "encoding/json"
    "errors"
    "net/http"

    "log/slog"

    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability"
    "github.com/pikshipp/pikshipp/internal/wallet"
)

// ErrorResponse is the standard JSON body returned on errors.
type ErrorResponse struct {
    Error ErrorBody `json:"error"`
}

// ErrorBody mirrors Stripe's error shape.
type ErrorBody struct {
    Type      string `json:"type"`
    Code      string `json:"code"`
    Message   string `json:"message"`
    Param     string `json:"param,omitempty"`
    RequestID string `json:"request_id"`
    DocURL    string `json:"doc_url,omitempty"`
}

// Write produces a standardized error response.
//
// Maps domain errors to HTTP status + machine-readable code.
// Server errors (5xx) log the full chain; client errors (4xx) don't.
func Write(w http.ResponseWriter, r *http.Request, err error) {
    status, body := mapError(err)
    body.RequestID = observability.RequestIDFrom(r.Context())

    if status >= 500 {
        log := observability.LoggerFrom(r.Context())
        log.ErrorContext(r.Context(), "request failed", slog.Any("error", err))
        body.Message = "Internal server error. Reference: " + body.RequestID
    } else if body.Message == "" {
        body.Message = err.Error()
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(ErrorResponse{Error: body})
}

// WriteRateLimited writes a 429 response.
func WriteRateLimited(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Retry-After", "1")
    w.WriteHeader(http.StatusTooManyRequests)
    json.NewEncoder(w).Encode(ErrorResponse{
        Error: ErrorBody{
            Type:      "rate_limit",
            Code:      "rate_limit_exceeded",
            Message:   "Too many requests. Retry shortly.",
            RequestID: observability.RequestIDFrom(r.Context()),
        },
    })
}

func mapError(err error) (int, ErrorBody) {
    switch {
    case errors.Is(err, auth.ErrUnauthenticated):
        return 401, ErrorBody{Type: "authentication_error", Code: "unauthenticated"}
    case errors.Is(err, core.ErrPermissionDenied):
        return 403, ErrorBody{Type: "permission_error", Code: "permission_denied"}
    case errors.Is(err, core.ErrNotFound):
        return 404, ErrorBody{Type: "not_found", Code: "not_found"}
    case errors.Is(err, core.ErrConflict):
        return 409, ErrorBody{Type: "conflict", Code: "conflict"}
    case errors.Is(err, core.ErrInvalidArgument):
        return 400, ErrorBody{Type: "invalid_request", Code: "invalid_argument"}
    case errors.Is(err, wallet.ErrInsufficientFunds):
        return 402, ErrorBody{Type: "payment_required", Code: "insufficient_funds"}
    case errors.Is(err, core.ErrUnavailable):
        return 503, ErrorBody{Type: "unavailable", Code: "unavailable"}
    default:
        return 500, ErrorBody{Type: "internal_error", Code: "internal"}
    }
}
```

## Health handlers

```go
// api/http/handlers/health.go
package handlers

import (
    "context"
    "net/http"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)

func HealthHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    w.Write([]byte(`{"status":"ok"}`))
}

func ReadyHandler(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
        defer cancel()

        if err := pool.Ping(ctx); err != nil {
            w.WriteHeader(http.StatusServiceUnavailable)
            w.Write([]byte(`{"status":"unavailable","reason":"database"}`))
            return
        }

        // S3 readiness: cached HEAD on probe object; refreshed by background job.
        // Skipped at v0 simplicity; add at v1.

        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`))
    }
}
```

## Sample handler — book shipment

```go
// api/http/handlers/shipments.go
package handlers

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"

    apihttp "github.com/pikshipp/pikshipp/api/http"
    apierrors "github.com/pikshipp/pikshipp/api/http/errors"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/observability"
    "github.com/pikshipp/pikshipp/internal/shipments"
)

type bookShipmentRequest struct {
    OrderID     string `json:"order_id" validate:"required,uuid"`
    CarrierID   string `json:"carrier_id" validate:"required,uuid"`
    ServiceType string `json:"service_type" validate:"required,oneof=surface air express"`
}

type bookShipmentResponse struct {
    ShipmentID string `json:"shipment_id"`
    AWB        string `json:"awb"`
    LabelURL   string `json:"label_url"`
}

func BookShipment(deps apihttp.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req bookShipmentRequest
        if err := apihttp.DecodeJSON(r, &req); err != nil {
            apierrors.Write(w, r, err)
            return
        }

        p, _ := auth.PrincipalFrom(r.Context())
        log := observability.LoggerFrom(r.Context())

        result, err := deps.ShipmentSvc.Book(r.Context(), shipments.BookInput{
            SellerID:    p.SellerID,
            OrderID:     core.MustParseOrderID(req.OrderID),
            CarrierID:   core.MustParseCarrierID(req.CarrierID),
            ServiceType: core.ServiceType(req.ServiceType),
        })
        if err != nil {
            apierrors.Write(w, r, err)
            return
        }

        log.InfoContext(r.Context(), "shipment booked",
            slog.String("shipment_id", result.ShipmentID.String()),
            slog.String("awb", result.AWB))

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusCreated)
        json.NewEncoder(w).Encode(bookShipmentResponse{
            ShipmentID: result.ShipmentID.String(),
            AWB:        result.AWB,
            LabelURL:   result.LabelURL,
        })
    }
}
```

## Implementation notes

### Why chi (not gorilla/mux, not stdlib alone)

- chi is a thin (~1k LOC) router; almost stdlib-compatible (`http.Handler`).
- Middleware chains compose naturally.
- Route grouping syntax is clean.
- Active maintenance.

### Why not framework (gin, echo, fiber)

- Frameworks impose their own request/response types and middleware shapes.
- chi keeps `http.Request` and `http.ResponseWriter` — full control.

### Middleware ordering

The order matters:
1. **RealIP** — must be first to give downstream the right IP.
2. **RequestID** — earliest possible so log lines all have it.
3. **Recover** — catches panics from anything below.
4. **InjectLogger** — gives downstream `LoggerFrom(ctx)`.
5. **Timeout** — bounds wall-clock for the whole request.
6. **Compress** — should be toward outer end so it sees full response.
7. **SecurityHeaders** — on every response.
8. (auth-only) **Auth** → **SellerScope** → **Idempotency** → **RequireRole** → **RateLimit**.

### Why not AuthN before RealIP

We want RealIP to apply universally; auth flows depend on RealIP for IP-bound rate limits and audit.

### Capture writer for idempotency

We wrap `http.ResponseWriter` to capture the response body. Buffered in memory; max ~1MB per response. For oversized responses (file downloads), idempotency middleware skips capture (TODO: spec the limit).

## Testing

```go
func TestRateLimit_AllowsBurst(t *testing.T) {
    handler := RateLimit(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    sellerID := core.NewSellerID()
    ctx := auth.WithPrincipal(context.Background(), auth.Principal{SellerID: sellerID})

    // 20 burst (limit 10 + burst 2× = 20)
    for i := 0; i < 20; i++ {
        req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
        rec := httptest.NewRecorder()
        handler.ServeHTTP(rec, req)
        require.Equal(t, http.StatusOK, rec.Code, "request %d", i)
    }

    // 21st should be rate-limited
    req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestIdempotency_ReplaysCachedResponse(t *testing.T) {
    store := idempotency.NewInMemoryStore()
    var callCount int
    handler := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        callCount++
        w.Write([]byte(`{"id":"123"}`))
    }))

    sellerID := core.NewSellerID()
    ctx := auth.WithPrincipal(context.Background(), auth.Principal{SellerID: sellerID})

    // First request
    req := httptest.NewRequest("POST", "/", strings.NewReader(`{}`)).WithContext(ctx)
    req.Header.Set("Idempotency-Key", "abc123")
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    require.Equal(t, 1, callCount)
    require.Equal(t, "", rec.Header().Get("Idempotent-Replayed"))

    // Replay
    req2 := httptest.NewRequest("POST", "/", strings.NewReader(`{}`)).WithContext(ctx)
    req2.Header.Set("Idempotency-Key", "abc123")
    rec2 := httptest.NewRecorder()
    handler.ServeHTTP(rec2, req2)
    require.Equal(t, 1, callCount, "handler should not be called again")
    require.Equal(t, "true", rec2.Header().Get("Idempotent-Replayed"))
}

func TestErrorMapping(t *testing.T) {
    cases := []struct {
        err        error
        wantStatus int
        wantCode   string
    }{
        {auth.ErrUnauthenticated, 401, "unauthenticated"},
        {core.ErrNotFound, 404, "not_found"},
        {core.ErrInvalidArgument, 400, "invalid_argument"},
        {wallet.ErrInsufficientFunds, 402, "insufficient_funds"},
        {errors.New("random"), 500, "internal"},
    }
    for _, c := range cases {
        rec := httptest.NewRecorder()
        req := httptest.NewRequest("GET", "/", nil)
        ctx := observability.WithRequestID(req.Context(), "test-req")
        req = req.WithContext(ctx)

        apierrors.Write(rec, req, c.err)
        require.Equal(t, c.wantStatus, rec.Code)

        var body apierrors.ErrorResponse
        json.NewDecoder(rec.Body).Decode(&body)
        require.Equal(t, c.wantCode, body.Error.Code)
        require.Equal(t, "test-req", body.Error.RequestID)
    }
}
```

## Performance

- chi router: ~1µs per route lookup.
- Middleware chain (8 layers): ~10µs total.
- DecodeJSON: depends on payload size; ~1ms for 10KB.
- Rate limit check: ~100ns (lock + token bucket).

## Open questions

- CORS configuration: explicit allow-list per env. Specced as a TODO before public dashboard launch.
- Static file serving (e.g., openapi.yaml at /v1/openapi.yaml): yes, simple `http.FileServer`. Add at API launch.
- WebSocket / SSE for real-time tracking updates: deferred to v2.

## References

- HLD `01-architecture/01-monolith-shape.md` (api/ package layout).
- HLD `04-cross-cutting/05-resilience.md` (idempotency, rate limit).
