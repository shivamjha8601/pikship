# Monolith shape — packages, layers, dependencies

> The internal organization of the binary. Every Go package's role, the strict rules about who imports whom, and why.

## Layered structure

Three layers, top to bottom:

```
                                  ┌──────────────────┐
                                  │       cmd/       │  wiring + main.go
                                  └────────┬─────────┘
                                           │
                ┌──────────────────────────▼──────────────────────────┐
                │                       api/                          │
                │   http handlers · webhooks · openapi · middleware   │
                │              (thin layer; zero business logic)      │
                └──────────────────────────┬──────────────────────────┘
                                           │
        ┌──────────────────────────────────▼──────────────────────────────┐
        │                            internal/                             │
        │                                                                  │
        │  ┌─────────────────────────────────────────────────────────┐     │
        │  │          Cross-cutting                                  │     │
        │  │  auth · policy · audit · idempotency · outbox · obs     │     │
        │  └─────────────────────────────────────────────────────────┘     │
        │                                                                  │
        │  ┌─────────────────────────────────────────────────────────┐     │
        │  │          Domain modules (one per bounded context)       │     │
        │  │  identity · seller · orders · catalog · ...             │     │
        │  └─────────────────────────────────────────────────────────┘     │
        │                                                                  │
        │  ┌─────────────────────────────────────────────────────────┐     │
        │  │          Adapter modules (per-vendor implementations)   │     │
        │  │  carriers/delhivery · channels/shopify · ...            │     │
        │  └─────────────────────────────────────────────────────────┘     │
        │                                                                  │
        │  ┌─────────────────────────────────────────────────────────┐     │
        │  │          Core: pure types, no I/O                       │     │
        │  │  Money, Pincode, Order, Shipment, ...                   │     │
        │  └─────────────────────────────────────────────────────────┘     │
        └──────────────────────────────────┬──────────────────────────────┘
                                           │
                                  ┌────────▼─────────┐
                                  │   migrations/    │
                                  │   query/  (sqlc) │
                                  └──────────────────┘
```

## Dependency rules

**A module at layer N may import:**
- Anything at the same layer (same row) — limited to *interfaces* of other modules, not their internal types.
- Anything at any layer below (lower row).

**A module may NOT import:**
- Anything at a layer above (higher row).
- Another module's internal/private packages (Go's `internal` keyword enforces this).
- Database, vendor SDKs, or HTTP clients directly — those are **only** in adapters or in the module's own `*_repo.go`.

This is enforced by:
- Go's package visibility (`internal` directories).
- `golangci-lint` with `depguard` rules in `.golangci.yml`.
- Code review.

## The core package

```
internal/core/
├── money.go         (Paise int64; arithmetic; format)
├── pincode.go       (validated Indian pincode)
├── address.go
├── order.go         (canonical Order struct + invariants)
├── shipment.go      (canonical Shipment + status enum)
├── canonical_status.go  (state machine; transition validation)
├── carrier.go       (carrier ID type, capabilities flags)
├── channel.go       (channel ID, platform enum)
├── time.go          (Clock interface; FakeClock for tests)
├── ids.go           (typed IDs: SellerID, OrderID, ShipmentID, etc.)
└── errors.go        (sentinel errors used across modules)
```

`core` has **zero I/O**, **zero external deps** (other than stdlib). Importable by anyone, depended on by everyone.

## Cross-cutting modules

Each is a small, stable package that almost every domain module depends on.

### `internal/auth/`
```go
package auth

type Authenticator interface {
    Authenticate(ctx context.Context, req *http.Request) (Principal, error)
}
type Principal struct {
    UserID   core.UserID
    SellerID core.SellerID    // selected seller for this request
    Roles    []Role
}
```
Implementations: `OpaqueSessionAuthenticator` (v0). Future: `JWTAuthenticator`, etc.

### `internal/policy/`
```go
package policy

type Engine interface {
    Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error)
    SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, source Source) error
}
```
Single resolver function. Cache per request; cross-instance invalidated via LISTEN/NOTIFY + 5s TTL fallback.

### `internal/audit/`
```go
package audit

type Emitter interface {
    Emit(ctx context.Context, event Event) error           // sync emit (in-tx)
    EmitAsync(ctx context.Context, event Event) error      // async via outbox
}
```
High-value events emit synchronously inside the originating transaction; lower-value via outbox.

### `internal/idempotency/`
```go
package idempotency

type Store interface {
    Lookup(ctx context.Context, sellerID core.SellerID, key string) (cached *Response, found bool, err error)
    Store(ctx context.Context, sellerID core.SellerID, key string, resp Response) error
}
```
Used by HTTP middleware and by per-domain idempotent operations (booking, wallet).

### `internal/outbox/`
```go
package outbox

type Emitter interface {
    Emit(ctx context.Context, tx Transaction, event Event) error
}
```
Always called with an existing transaction. Forwarder is a separate runtime.

### `internal/observability/`
Slog setup, request-ID middleware, panic recovery, metrics counters.

## Domain modules (one per bounded context)

Each domain module has the same internal shape:

```
internal/<module>/
├── service.go        (public Service interface; root of the package)
├── service_impl.go   (the implementation; constructor `New(...)`)
├── repo.go           (DB access via sqlc; private)
├── types.go          (domain-specific types not in core)
├── events.go         (event types this module emits)
├── policy_keys.go    (policy keys this module reads, named constants)
├── jobs.go           (river job handlers this module owns)
└── service_slt_test.go  (SLT against testcontainers)
```

The `service.go` interface is **the public API** of the module. Everything else is internal. Other modules import only `<module>.Service`, never `<module>.repository` or similar.

### Example: `internal/wallet/service.go`
```go
package wallet

import (
    "context"
    "github.com/pikshipp/pikshipp/internal/core"
)

type Service interface {
    Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error)
    Confirm(ctx context.Context, holdID HoldID, ref Ref) (LedgerEntryID, error)
    Release(ctx context.Context, holdID HoldID) error
    Post(ctx context.Context, sellerID core.SellerID, entry LedgerPost) (LedgerEntryID, error)
    Balance(ctx context.Context, sellerID core.SellerID) (Balance, error)
}

// Constructor: only the implementation depends on db, audit, outbox, etc.
func New(db DB, audit audit.Emitter, ob outbox.Emitter, clock core.Clock) Service { ... }
```

Other modules consume `wallet.Service`. Wiring happens in `cmd/pikshipp/main.go`.

## Adapter modules

Per-vendor implementations of framework interfaces.

### `internal/carriers/`
```
internal/carriers/
├── adapter.go            (Adapter interface; framework types)
├── registry.go           (carrier ID → adapter map)
├── delhivery/            (one folder per carrier)
│   ├── adapter.go
│   ├── status_map.go
│   └── adapter_slt_test.go
├── bluedart/             (v1+)
└── sandbox/              (deterministic fixture; used in SLTs and sandbox env)
```

The framework defines:
```go
type Adapter interface {
    Capabilities() Capabilities
    Serviceability(ctx, query) ([]Result, error)
    Book(ctx, req) (BookingResult, error)
    Cancel(ctx, awb, reason) error
    Label(ctx, awb, format) (Label, error)
    Manifest(ctx, req) (Manifest, error)
    Track(ctx, awb) ([]Event, error)
    VerifyWebhook(ctx, req) ([]NormalizedEvent, error)
    RequestNDRAction(ctx, awb, action, payload) error
    // ... etc
}
```

A new carrier = a new folder + an entry in the registry. **Zero changes to domain modules.**

### `internal/channels/`
Same pattern. Each platform (Shopify, Amazon, ...) is a folder implementing `channels.Adapter`.

## API layer

```
api/
├── http/
│   ├── server.go          (chi router setup)
│   ├── middleware/        (auth, idempotency, request ID, recovery, RLS scope)
│   ├── handlers/          (one file per resource: orders, shipments, wallet, ...)
│   └── errors.go          (HTTP error response shape)
├── webhooks/
│   ├── carriers/          (one handler per carrier; HMAC verify)
│   ├── channels/          (one handler per platform)
│   └── razorpay/
└── openapi.yaml
```

**Handlers do no business logic.** They:
1. Decode + validate request.
2. Resolve principal from auth middleware.
3. Call domain service.
4. Encode response.

Anything more belongs in the domain module.

## main.go (wiring)

```go
package main

func main() {
    cfg := config.LoadFromEnv()
    db := pgxpool.New(cfg.DatabaseURL)
    s3 := s3client.New(cfg.AWS)
    clock := core.SystemClock{}

    // Cross-cutting
    auditSvc := audit.New(db)
    outboxSvc := outbox.New(db)
    policySvc := policy.New(db)

    // Domain
    walletSvc := wallet.New(db, auditSvc, outboxSvc, clock)
    pricingSvc := pricing.New(db, policySvc, clock)
    allocationSvc := allocation.New(db, pricingSvc, policySvc, clock)
    // ... etc

    // Adapters registry
    carrierReg := carriers.NewRegistry()
    carrierReg.Register("delhivery", delhivery.New(cfg.Delhivery, clock))
    // ... etc

    // HTTP
    if cfg.Role.IncludesAPI() {
        srv := http.NewServer(cfg, allHandlers...)
        go srv.ListenAndServe()
    }

    // Workers
    if cfg.Role.IncludesWorker() {
        runner := river.NewRunner(db, allJobHandlers...)
        go runner.Start(ctx)
    }

    // Graceful shutdown
    waitForSignal()
    drain(srv, runner)
}
```

This file is the **only** place that knows how everything wires together. Tests provide alternative wiring.

## Forbidden patterns

Captured here so they're catchable in code review:

| Forbidden | Why | Allowed instead |
|---|---|---|
| `init()` in domain code | Hidden coupling; non-obvious test setup | Explicit constructors |
| Package-level `var` for mutable state | Multi-instance unsafe; test isolation broken | Pass via constructor |
| Importing another module's `*_repo.go` | Layering violation | Use that module's `Service` interface |
| `time.Now()` in domain code | Untestable | `clock.Now()` |
| `panic()` outside `main` | Loss of stack context; obscures error path | Return error |
| `interface{}` / `any` in domain APIs | Type-erasure hides bugs | Concrete types or generics |
| Reaching into `*sql.DB` from a handler | Layering violation | Call domain service |
| Logging without `request_id`, `seller_id` | Unobservable | Use the `slog.Logger` from context |

## Test layout

```
internal/<module>/service_slt_test.go   ← SLT (testcontainers)
internal/<module>/<file>_test.go         ← unit tests
internal/<module>/bench_test.go          ← microbenchmarks
```

SLTs run against a real PG + LocalStack (S3, etc.). Unit tests run in seconds. Benchmarks track performance over time.

## Diagram

See [`../diagrams/01-monolith-package-graph.png`](../diagrams/01-monolith-package-graph.png) (TODO).
