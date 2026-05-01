# Go conventions

> The hard rules. Read this once on onboarding. PRs that violate are blocked.

## Layout

### Repository

```
pikshipp/
├── cmd/
│   └── pikshipp/
│       └── main.go              ← single binary entrypoint
├── internal/
│   ├── core/                    ← pure types, no I/O
│   ├── auth/                    ← cross-cutting
│   ├── policy/
│   ├── audit/
│   ├── outbox/
│   ├── idempotency/
│   ├── observability/
│   ├── identity/                ← domain modules
│   ├── seller/
│   ├── orders/
│   ├── catalog/
│   ├── carriers/                ← framework + adapters
│   │   ├── adapter.go
│   │   ├── registry.go
│   │   ├── delhivery/
│   │   └── sandbox/
│   ├── channels/
│   │   ├── adapter.go
│   │   ├── shopify/
│   │   └── ...
│   ├── pricing/
│   ├── allocation/
│   ├── shipments/
│   ├── tracking/
│   ├── ndr/
│   ├── rto/
│   ├── cod/
│   ├── wallet/
│   ├── recon/
│   ├── notifications/
│   ├── reports/
│   ├── buyerexp/
│   ├── support/
│   ├── admin/
│   ├── risk/
│   └── contracts/
├── api/
│   ├── http/
│   │   ├── server.go
│   │   ├── middleware/
│   │   └── handlers/
│   ├── webhooks/
│   └── openapi.yaml
├── migrations/                  ← golang-migrate .sql files
├── query/                       ← sqlc query .sql files; mirrors internal/<module>/
├── sqlc.yaml
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── .golangci.yml
├── .github/workflows/           ← CI
└── docs/
```

### Within a domain module

```
internal/<module>/
├── doc.go                       ← package-level godoc; one file per package
├── service.go                   ← public Service interface (THE module API)
├── service_impl.go              ← the implementation; constructor New(...)
├── repo.go                      ← DB access; private; uses sqlc
├── types.go                     ← module-specific types
├── events.go                    ← outbox event payloads + emit helpers
├── policy_keys.go               ← named constants for policy keys this module reads
├── jobs.go                      ← river job handlers this module owns
├── errors.go                    ← sentinel errors; only the module's
├── service_test.go              ← unit tests
├── service_slt_test.go          ← SLT against testcontainers
└── bench_test.go                ← microbenchmarks
```

A module's **only public symbols** are: `Service` interface, `New(...)` constructor, public types in `types.go`, and named errors in `errors.go`. Everything else is lowercase.

## Imports

### Allowed external dependencies

| Package | Purpose |
|---|---|
| `github.com/jackc/pgx/v5` (and `/pgxpool`) | Postgres driver |
| `github.com/jackc/pgx/v5/stdlib` | for sqlc compatibility |
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/riverqueue/river` | job queue |
| `github.com/golang-migrate/migrate/v4` | migrations |
| `github.com/go-playground/validator/v10` | struct validation |
| `github.com/google/uuid` | UUIDs |
| `github.com/aws/aws-sdk-go-v2/...` | AWS clients (S3) |
| `golang.org/x/oauth2` | OAuth client |
| `golang.org/x/crypto` | non-stdlib crypto helpers |

### Forbidden

| Package | Why |
|---|---|
| Any logging library | Use `log/slog` |
| Any ORM (GORM, Bun, Ent) | Use sqlc |
| `net/http/httptest` outside test files | tests only |
| `init()` for setup | Use `New(...)` constructors |
| `unsafe` | Forbidden without ADR |
| `reflect` in domain code | Forbidden in `internal/<domain>/`; OK in adapters when needed |
| `sync/atomic` for mutable shared state | Use channels or `sync.Mutex` |

### Adding a new dependency

1. Open an ADR in `docs/TRD/HLD/05-decisions/` justifying the addition.
2. PR the ADR alongside the import.
3. Reviewer checks: license, maintenance status, GitHub stars, security record.

## Naming

- **Packages**: lowercase, single word, no underscores. `wallet`, `tracking`, `policyengine` (avoid).
- **Files**: lowercase with underscores. `service_impl.go`, `policy_keys.go`.
- **Types**: PascalCase. `WalletAccount`, `LedgerEntry`. Acronyms preserved: `HTTPClient`, `URL`, `ID` (not `Id`).
- **Constants**: PascalCase or all-caps depending on idiom.
- **Errors**: `Err...` for sentinels. `wallet.ErrInsufficientFunds`.
- **Interfaces**: noun-er or noun. `Authenticator`, `Service`, `Sender`. **Not** `Iauthenticator`.
- **Tests**: `Test<Method>_<Scenario>`. `TestReserve_InsufficientFunds`.

## Public API surface

Every module exposes:

```go
// internal/<module>/service.go
package <module>

// Service is THE public API of this module.
// All other modules consume <module>.Service; never the implementation.
type Service interface {
    // ... methods with godoc on each
}
```

```go
// internal/<module>/service_impl.go
package <module>

// New constructs a Service implementation.
// Dependencies are injected; the constructor never reaches into globals.
func New(
    db DB,
    audit audit.Emitter,
    outbox outbox.Emitter,
    clock core.Clock,
    log *slog.Logger,
) Service {
    return &serviceImpl{...}
}
```

The implementation type is **lowercase** (`serviceImpl`); not exported.

## Function signatures

- **First parameter is `context.Context`** for any function that does I/O or might be cancelled.
- **Return errors as the last value.**
- **Prefer concrete types over interfaces** in domain APIs.
- **Avoid `interface{}` and `any`** in domain code. If genuinely needed, document why.
- **Avoid variadic** unless it really helps. `Foo(items ...Item)` only when 0-or-many is the natural call site.

```go
// Good
func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error)

// Bad
func (s *serviceImpl) Reserve(args interface{}) interface{}
```

## Errors

### Sentinel errors

Define in `errors.go`:

```go
package wallet

import "errors"

var (
    ErrInsufficientFunds   = errors.New("wallet: insufficient funds")
    ErrHoldNotFound        = errors.New("wallet: hold not found")
    ErrHoldExpired         = errors.New("wallet: hold expired")
    ErrGraceCapBreached    = errors.New("wallet: grace cap breached")
    ErrInvariantViolation  = errors.New("wallet: invariant check failed")
)
```

### Wrapping with `%w`

```go
if err := s.repo.GetWallet(ctx, sellerID); err != nil {
    return Hold{}, fmt.Errorf("wallet.Reserve: load wallet: %w", err)
}
```

### Checking with `errors.Is`

```go
if errors.Is(err, wallet.ErrInsufficientFunds) {
    // handle
}
```

### Never:
- Match error strings: `strings.Contains(err.Error(), "duplicate key")`. Use `errors.Is` against pgconn types.
- Return raw vendor errors at module boundaries: wrap them.
- Discard errors silently: `_ = doSomething()` is forbidden in production code (CI lint).

### HTTP error mapping

Defined in `api/http/errors.go`. Domain errors map to HTTP status:

```go
func toHTTPStatus(err error) (int, string) {
    switch {
    case errors.Is(err, wallet.ErrInsufficientFunds): return 402, "insufficient_funds"
    case errors.Is(err, auth.ErrUnauthenticated):     return 401, "unauthenticated"
    // ...
    default:                                            return 500, "internal_error"
    }
}
```

## Logging

Use `log/slog` only. Get logger from context:

```go
log := logging.From(ctx)
log.InfoContext(ctx, "reserved wallet hold",
    slog.String("hold_id", holdID.String()),
    slog.Int64("amount_minor", int64(amount)),
)
```

Do **not**:
- `fmt.Println`, `log.Println` (stdlib `log`).
- `log.Fatal` outside `main`.
- `log.Panic` ever.
- Log secrets, tokens, raw payment info.

Levels:
- `Debug`: dev-only; off in prod.
- `Info`: significant business events.
- `Warn`: recoverable anomalies.
- `Error`: returned-to-user errors or background failures.

## Context

- Pass `ctx` as first parameter through every layer.
- Honor `ctx.Done()` for cancellation in long-running loops.
- Don't store `ctx` in structs (with rare exceptions for goroutine roots).
- Don't leak ctx with values across module boundaries except for: `request_id`, `seller_id`, `user_id`, `logger`, `clock`. Define helper accessors.

## Concurrency

### Goroutines

- Goroutines started in domain code must be bounded (worker pool) and trackable (via `sync.WaitGroup` or context-driven shutdown).
- Goroutines in `cmd/main.go` for top-level workers are OK with explicit shutdown.
- **Never** start a goroutine that outlives the request without a clear lifecycle.

### Channels

- Closed by the sender, not the receiver.
- Buffer size justified in code comment; default 0 (unbuffered).
- `select` with `ctx.Done()` for cancellation.

### Mutex

- Prefer message-passing (channels) over shared memory.
- When using `sync.Mutex`: keep critical sections short; never hold across I/O.
- `sync.RWMutex` only when reads dominate writes by >10×.

### Atomic

- Use `sync/atomic` only for simple counters where mutex overhead matters. Most code shouldn't.

## Database access

### Through repos, not raw

```go
// Good: every module has its own repo wrapping sqlc
type repo struct { db DB }
func (r *repo) GetWallet(ctx context.Context, sellerID core.SellerID) (*WalletAccount, error)
```

### Transactions

Use the helper from `internal/observability/dbtx`:

```go
err := dbtx.WithSellerTx(ctx, db, sellerID, func(tx pgx.Tx) error {
    // RLS GUC is set; queries are seller-scoped
    return doStuff(ctx, tx)
})
```

Never use a raw `db.Begin()` outside the helper.

### sqlc

- One `query.sql` file per module under `query/<module>.sql`.
- sqlc generates `query.sql.go` with typed methods.
- All queries named: `-- name: GetWalletBySellerID :one`.
- Generated code is committed to repo (deterministic; reviewed).

## Testing

### Unit tests

- Same package as code under test. `service_impl_test.go`.
- Table-driven tests with named cases.
- `t.Parallel()` where independent.
- Hand-written fakes for dependency interfaces; no `gomock`.

### SLTs

- `<module>_slt_test.go`.
- Bring up real PG + LocalStack via testcontainers.
- Test the public Service interface end-to-end.

### Benchmarks

- `bench_test.go`.
- Measure hot paths: pricing, allocation, wallet, policy resolve.
- Tracked over time via `benchstat` in CI.

### Coverage gates

- `internal/<domain>/`: ≥ 80%.
- `auth`, `policy`, `audit`, `wallet`: ≥ 90%.

### Forbidden in tests

- Sleeping (`time.Sleep`). Use `core.FakeClock`.
- Calling external services. Use sandbox adapters or fakes.
- Sharing state across `t.Run` cases.
- Reading test fixtures that aren't in `testdata/`.

## Comments and godoc

### Package doc

```go
// Package wallet implements the seller wallet, ledger, and money operations.
//
// All money values are paise (int64). Two-phase reservations support booking;
// direct posting handles recharges, refunds, and reverse-leg charges.
//
// See docs/TRD/HLD/03-services/04-wallet-and-ledger.md for design rationale.
package wallet
```

One `doc.go` per package.

### Method doc

Every exported method has a godoc comment:

```go
// Reserve places a hold on the seller's wallet for the given amount.
// The hold expires after ttl unless confirmed or released.
//
// Returns ErrInsufficientFunds if available balance is below amount.
// The returned HoldID must be passed to Confirm or Release.
func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error)
```

### Inline

- Use comments to explain *why*, not *what*.
- Reference HLD/PRD section when relevant: `// See HLD §03-services/04 wallet two-phase`.
- TODO comments include name + ticket: `// TODO(jane): handle the multi-currency case (PIK-123)`.

## Performance idioms

- Pre-allocate slices when length is known: `make([]T, 0, n)`. Always do this when the size is reachable at the call site, even if the slice is small.
- Avoid concatenating strings in hot paths; use `strings.Builder`.
- Don't `defer` in hot loops; do it once outside.
- Pool buffers for known-hot allocations (`sync.Pool`).
- Read `pprof` output before optimizing — never guess.
- Worker loops that call external services (HTTP, vendor SDKs) MUST scope each iteration in its own `context.WithTimeout` so a single slow call cannot stall the whole sweep.
- sqlc-generated queries use **positional** args. Avoid `pgx.NamedArgs` in hand-written queries; positional is faster and prevents naming-collision bugs.

## Service patterns

### `InTx` method discipline

Most service interfaces expose two parallel forms of mutating methods:

- The **public method** (`Reserve`, `Cancel`, `MarkBooked`) opens its own transaction via `db.WithTx` and is the default.
- The **`InTx` variant** (`ReserveInTx`, `CancelInTx`, `MarkBookedInTx`) takes a `pgx.Tx` as the second parameter and **must not** open a transaction. It is callable only from another service that is already inside a transaction.

Rules:
- The two methods do exactly the same logical work; the only difference is who owns the tx.
- The `InTx` form must accept the tx **as the second positional argument**, not via context.
- The two should be listed adjacent in the interface, with the `InTx` group at the bottom of the interface.
- Calling the non-`InTx` form from inside an existing tx is a bug (deadlock risk on shared rows).

### Sentinel errors

A sentinel error must indicate a **domain condition the caller is expected to handle differently**. Examples: `wallet.ErrInsufficientBalance`, `orders.ErrChannelDuplicate`, `shipments.ErrAllocationStale`.

Pure validation errors (bad pincode, missing field, format violation) collapse to **one** sentinel per service: `ErrInvalidInput`, with the specific reason wrapped via `fmt.Errorf("%w: %s", ErrInvalidInput, detail)`.

If a sentinel error has only one call site triggering it AND only one call site checking for it, consider whether you actually need the sentinel — a wrapped `ErrInvalidInput` is usually sufficient.

### Two-phase external-call pattern

Any flow shaped as **DB write → external mutation → DB write** (booking a shipment, raising an NDR action, etc.) MUST follow:

1. **Phase A** (single tx): write a `pending_*` row capturing intent, reserve any wallet holds, emit an "X requested" outbox event. Tx commits before any external call.
2. **External call**: outside any tx; with a per-call timeout; routed through `framework.Call` if it's a carrier-style call (so the breaker sees it).
3. **Phase B** (single tx): on success, transition state and emit "X succeeded"; on permanent failure, transition to failed and release wallet; on transient failure, leave in `pending_*` state and let the reconcile cron retry.
4. **Reconcile cron**: scans `pending_*` rows older than threshold, probes the external system to see if the operation actually succeeded, and either commits success or retries.

Reference implementation: shipments booking (LLD §03-services/13).

## Hot-path money formatting

`core.Paise.String()` and `core.Paise.Format()` allocate. They're fine for one-off rendering but in tight loops (CSV exports, daily-digest renders) prefer `core.Paise.AppendFormat(dst []byte) []byte` which writes into a caller-owned buffer and avoids allocation.

## Forbidden patterns (recap)

| Pattern | Reason |
|---|---|
| `init()` in domain code | Hidden coupling; un-testable |
| Package-level mutable `var` | Multi-instance unsafe |
| `time.Now()` in domain code | Use `core.Clock` |
| `panic()` outside `main` | Loses error context |
| `interface{}` / `any` in domain APIs | Type erasure hides bugs |
| Reaching into another module's `*_repo.go` | Layering violation |
| Logging without context | Loses request_id, seller_id |
| `_ = err` (silently dropping) | Hides bugs |
| Returning vendor errors directly | Coupling to driver internals |
| Long-held DB transactions | Lock contention |
| Goroutines without lifecycle | Leaks |

CI lint catches most of these. Reviewers catch the rest.
