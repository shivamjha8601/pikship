# Testing Patterns

## Purpose

This document defines the **rules of the road** for testing in Pikshipp. The goal is consistency — if every junior engineer follows the same testing patterns, code review focuses on logic instead of test scaffolding, and the test suite stays runnable, reliable, and useful.

Three types of tests, with clear roles:

1. **Unit tests** (`*_test.go`) — pure-Go, no I/O, no DB, no network. One file per implementation file. Fast (< 5 ms each).
2. **System-level tests** (`*_slt_test.go`) — testcontainers Postgres, exercise full service code. Slow (~ 100 ms each), but real.
3. **Microbenchmarks** (`bench_test.go`) — `go test -bench` for hot paths.

We do **not** write end-to-end browser tests in this repo. The frontend has its own Playwright suite.

## Layout

```
internal/<module>/
├── service.go
├── service_impl.go
├── service_test.go            // Unit
├── service_slt_test.go        // SLT
└── bench_test.go              // Microbench
```

The `_slt_` infix is enforced by a **build tag**: `//go:build slt` so SLT tests run only when explicitly requested:

```go
//go:build slt

package wallet

func TestReserveConfirmRelease_HappyPath_SLT(t *testing.T) { /* ... */ }
```

Run modes:
- `go test ./...` runs only unit tests (fast; default for IDE saves and pre-commit).
- `go test -tags slt ./...` runs unit + SLT (CI default).
- `go test -bench . ./...` runs benchmarks.

## Unit Tests

### Pure functions

Validators, normalizers, classifiers — anything without I/O — go in unit tests with table-driven cases:

```go
func TestValidatePincode(t *testing.T) {
    cases := []struct {
        in    string
        want  error
    }{
        {"110011", nil},
        {"012345", ErrInvalidPincode},
        {"99999",  ErrInvalidPincode},
        {"",       ErrInvalidPincode},
    }
    for _, c := range cases {
        t.Run(c.in, func(t *testing.T) {
            err := validatePincode(c.in)
            if !errors.Is(err, c.want) {
                t.Fatalf("got %v want %v", err, c.want)
            }
        })
    }
}
```

### Mocking

Mock the **direct dependencies** of the unit under test, not the world. Generated mocks via `mockery` are acceptable but not required; hand-written interfaces are often clearer.

Pattern: define interfaces at the **consumer** boundary (`internal/orders/types.go` defines `interface PolicyReader { GetInt(...) }`); supply hand-written test doubles.

```go
type fakePolicy struct {
    values map[string]int
}
func (f *fakePolicy) GetInt(_ context.Context, _ core.SellerID, key string) (int, error) {
    return f.values[key], nil
}
```

Reject the temptation to mock SQL queries; if the unit hits the DB, write an SLT instead.

## System-Level Tests (SLTs)

### testcontainers Postgres

```go
package slt

import (
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
)

type PG struct {
    Pool      *pgxpool.Pool
    URL       string
    container testcontainers.Container
}

func StartPG(t *testing.T) *PG {
    t.Helper()
    ctx := context.Background()
    container, err := postgres.RunContainer(ctx,
        testcontainers.WithImage("postgres:16-alpine"),
        postgres.WithDatabase("pikshipp_test"),
        postgres.WithUsername("test"), postgres.WithPassword("test"),
    )
    require.NoError(t, err)

    url, err := container.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)

    pool, err := pgxpool.New(ctx, url)
    require.NoError(t, err)

    runMigrations(t, url)

    pg := &PG{Pool: pool, URL: url, container: container}
    t.Cleanup(func() {
        pool.Close()
        _ = container.Terminate(ctx)
    })
    return pg
}
```

The container starts in ~3 seconds; subsequent tests in the same `TestMain` reuse it, so a typical SLT is < 200 ms.

### Migrations

`runMigrations` invokes `golang-migrate` against the test DB. Migrations live in `db/migrations/` and are pure SQL. The tests run **the same migrations as prod**.

### Test Helpers

Higher-level constructors live in `internal/slt/` and abstract common setup:

```go
package slt

func NewSeller(t *testing.T, pg *PG) *seller.Seller { /* ... */ }
func NewBookedShipment(t *testing.T, pg *PG) *Bundle { /* seller + order + shipment */ }
func Wallet(pg *PG) wallet.Service { /* construct service with real deps */ }
func Outbox(pg *PG) outbox.Emitter { /* ... */ }
func OutboxHas(t *testing.T, pg *PG, kind, key string) bool { /* helper */ }
func OutboxEventsFor(t *testing.T, pg *PG, sellerID core.SellerID) []outbox.Event { /* ... */ }
```

These are the **idiom** for SLTs:

```go
func TestBook_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    seed := slt.SeedFullStackForBooking(t, pg)
    slt.SandboxCarrier(t, pg, "sb").SetBookResult(/* success */)

    sh, err := slt.Shipments(pg).Book(ctx, BookRequest{...})
    require.NoError(t, err)
    require.Equal(t, StateBooked, sh.State)
    require.True(t, slt.OutboxHas(t, pg, "shipment.booked", string(sh.ID)))
}
```

### Clock

All services accept `core.Clock` as a dependency. SLTs inject `slt.FakeClock(0)` and advance time deterministically:

```go
clk := slt.FakeClock(0)
svc := someService.New(..., clk)

clk.Advance(2 * time.Hour) // any time-dependent logic uses this clock
```

This eliminates time-flake in tests.

### Parallelism

SLTs are **parallel-safe**: each test gets its own DB schema (or its own container in extreme cases). Use `t.Parallel()` aggressively to keep CI fast:

```go
func TestX_SLT(t *testing.T) {
    t.Parallel()
    pg := slt.StartPG(t)
    // ...
}
```

The `slt.StartPG` helper uses a per-test container to avoid cross-test interference. Containers are pooled across the parent `t` and torn down at end of test.

### RLS Tests

Every seller-scoped table must have an RLS isolation test:

```go
func TestRLS_Order_Isolation_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    a := slt.NewSeller(t, pg)
    b := slt.NewSeller(t, pg)
    slt.AsSeller(pg, a.ID).Insert(/* order owned by A */)

    rows := slt.AsSeller(pg, b.ID).Query("SELECT * FROM order_record")
    require.Len(t, rows, 0, "B must not see A's orders")
}
```

`slt.AsSeller(pool, sellerID)` returns a connection wrapper that issues `SET LOCAL app.seller_id = ...` before every query.

## Microbenchmarks

### When to write

Microbenchmark a function when:
- It runs on every request (validators, classifiers, marshallers).
- A regression would be invisible in latency dashboards.

Skip benchmarks for one-off code (admin endpoints, ETL).

### Pattern

```go
func BenchmarkValidatePincode(b *testing.B) {
    for i := 0; i < b.N; i++ {
        _ = validatePincode("110011")
    }
}

// With allocs
func BenchmarkRenderTemplate(b *testing.B) {
    tmpl, _ := template.New("x").Parse("hi {{.Name}}")
    data := map[string]string{"Name": "test"}
    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        var buf bytes.Buffer
        _ = tmpl.Execute(&buf, data)
    }
}
```

### Targets

Each LLD specifies budgets for hot-path functions (e.g., `BenchmarkPaiseAdd: < 5 ns, 0 allocs`). CI runs benchmarks weekly and surfaces regressions; we don't gate PRs on micro-bench numbers because they're noisy.

## Code Coverage

We do not enforce a coverage gate. Instead:
- Every public method has at least one happy-path test.
- Every error branch (sentinel error returned) has at least one test that triggers it.
- RLS, idempotency, and state-machine code are exhaustively tested.

If coverage falls below 60% on a new package, `go vet -lostcancel` is the least of your concerns. Reviewers ask why.

## Test Naming

```
TestServiceMethod_Condition[_SLT]
```

Examples:
- `TestPolicyEngine_Resolve_HappyPath`
- `TestPolicyEngine_Resolve_Locked`
- `TestWallet_ReserveConfirm_Idempotent_SLT`
- `TestOrders_Create_DuplicateChannelOrderID_SLT`

The trailing `_SLT` is required for tests that use the `slt` build tag.

## Test Data

### Fixtures

CSV files, sample JSON payloads, and other test data live in `testdata/` next to the test file:

```
internal/channels/csv/
├── adapter_test.go
└── testdata/
    ├── default_v1_happy.csv
    ├── default_v1_bad_pincode.csv
    └── shopify_export_sample.csv
```

Go's `testing` package treats `testdata/` as a magic directory and excludes it from build globs.

### Fakers

For seeded data, use the `slt.Fake*` helpers which produce deterministic-ish records:

```go
func FakeOrder(seed int) orders.CreateRequest { /* deterministic by seed */ }
func FakePincode(seed int) string { /* known good pincode */ }
```

Avoid third-party faker libraries; they generate localizable data we don't need.

## CI Profiles

| Suite | When | Command |
|---|---|---|
| Unit | every PR | `go test ./...` |
| SLT | every PR | `go test -tags slt -timeout 10m ./...` |
| Bench | weekly cron | `go test -tags slt -bench . -benchtime 3x ./...` |
| Race | every PR | `go test -tags slt -race ./...` |
| Lint | every PR | `golangci-lint run` |

PR merges require all four green.

## Anti-Patterns

- **Sleep in tests.** Use `slt.WaitFor(t, dur, condFn)` instead.
- **Shared global state.** Each test owns its own `*PG` and creates its own seller.
- **Mocked SQL.** If you're tempted to mock a sqlc query, write an SLT.
- **Snapshot tests for SQL/JSON.** They drift and break for cosmetic changes; assert on specific fields instead.
- **Tests that depend on test ordering.** `t.Parallel()` should always work.
- **Tests that hit a real third-party service.** Use stub adapters; SLTs against vendor sandboxes are gated by env var (e.g., `DELHIVERY_SLT=1`) and skipped in CI by default.

## References

- LLD §00-conventions/01-go-conventions: package conventions.
- LLD §02-infrastructure/01-database-access: pool wiring used by SLTs.
- LLD §02-infrastructure/03-observability: log capture during tests.
- HLD §04-cross-cutting/03-testing: test strategy at the architecture level.
