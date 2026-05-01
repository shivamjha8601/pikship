# Cross-cutting: Testing

> Three layers: SLTs, unit tests, microbenchmarks. Plus the multi-instance simulation. CI runs all three on every PR.

## Layered approach

```
                    ┌──────────────────────────┐
                    │   Multi-instance sim     │   1 test, runs on PR
                    │   (two processes,        │
                    │    one PG, one S3)       │
                    └────────────┬─────────────┘
                                 │
                    ┌────────────▼─────────────┐
                    │   SLTs                   │   ~30 tests, ~1 per public interface
                    │   testcontainers PG +    │   ~5 min total
                    │   LocalStack             │
                    └────────────┬─────────────┘
                                 │
                    ┌────────────▼─────────────┐
                    │   Unit tests             │   thousands; ~30 sec total
                    │   pure functions, no I/O │
                    └────────────┬─────────────┘
                                 │
                    ┌────────────▼─────────────┐
                    │   Benchmarks             │   ~10; tracked over time
                    │   testing.B              │
                    └──────────────────────────┘
```

## Unit tests

`internal/<module>/*_test.go` — same package as the code under test. Pure-function logic only. Examples:
- Pricing: rate math, zone matching, adjustment ordering, volumetric weight.
- Allocation: filter logic, score normalization, tie-breaking.
- Status normalization: per-carrier code → canonical mapping.
- Idempotency keys: hash, parse, validation.
- Money arithmetic: paise overflow, addition, subtraction signs.

### Conventions
- Table-driven tests:
```go
func TestNormalize(t *testing.T) {
    cases := []struct {
        name string
        carrier string
        code string
        want CanonicalStatus
        wantOK bool
    }{
        {"delhivery_delivered", "delhivery", "DL", StatusDelivered, true},
        {"delhivery_unknown", "delhivery", "ZZZ", "", false},
        // ...
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, ok := Normalize(tc.carrier, tc.code)
            if got != tc.want || ok != tc.wantOK {
                t.Errorf("got %v/%v, want %v/%v", got, ok, tc.want, tc.wantOK)
            }
        })
    }
}
```
- Use `t.Parallel()` where tests are independent.
- Mock external deps via the package's interfaces (`policy.Engine`, `carriers.Adapter`); never via `gomock`. Hand-written fakes.

### Coverage targets
- `internal/<domain>` packages: ≥ 80%.
- Cross-cutting modules (`auth`, `policy`, `audit`, `wallet`): ≥ 90%.
- Handlers (`api/http/handlers`): no gate; SLTs cover them.

## Service-level tests (SLTs)

`internal/<module>/*_slt_test.go` — exercise the **public interface** of the module against real Postgres and LocalStack.

### Setup

```go
func TestMain(m *testing.M) {
    pg := testcontainers.NewPostgresContainer(t,
        postgres.WithDatabase("pikshipp_test"),
        postgres.WithInitScripts("../../migrations/*.up.sql"),
    )
    defer pg.Terminate(ctx)

    ls := localstack.New(t, localstack.WithServices("s3"))
    defer ls.Terminate(ctx)

    // Set env vars
    os.Setenv("DATABASE_URL", pg.URL())
    os.Setenv("S3_ENDPOINT", ls.S3Endpoint())
    os.Setenv("S3_BUCKET", "pikshipp-test")
    // ...

    os.Exit(m.Run())
}
```

### What SLTs verify
- The interface contract from end to end: input → side effects.
- Concurrent calls behave correctly.
- DB state matches expectations after operations.
- Outbox events emitted as expected.
- Audit events emitted as expected.

### Example SLT

```go
func TestWalletReserveConfirm_SLT(t *testing.T) {
    ctx := newTestContext()
    db := newTestDB(t)
    audit := audit.New(db)
    outbox := outbox.New(db)
    clock := core.FakeClock{Now: now}

    svc := wallet.New(db, audit, outbox, clock)

    // Setup: seller with ₹10,000
    sellerID := makeSeller(t, db)
    seedBalance(t, db, sellerID, paise(1_000_000))

    // Reserve ₹500
    holdID, err := svc.Reserve(ctx, sellerID, paise(50_000), 2*time.Minute)
    requireNoError(t, err)

    // Verify hold + available decremented
    bal, _ := svc.Balance(ctx, sellerID)
    require.Equal(t, paise(950_000), bal.Available)

    // Confirm
    entryID, err := svc.Confirm(ctx, holdID, ref{Type: "shipment_charge", ID: "S1"})
    requireNoError(t, err)

    // Verify ledger entry + balance
    bal, _ = svc.Balance(ctx, sellerID)
    require.Equal(t, paise(950_000), bal.Total)

    // Verify audit emitted
    events := readAuditEvents(t, db, sellerID)
    requireEventKind(t, events, "wallet.charged")
}
```

### Concurrency test (within SLT)

```go
func TestWalletConcurrent_SLT(t *testing.T) {
    // ... setup ...
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() { defer wg.Done()
            holdID, err := svc.Reserve(ctx, sellerID, paise(100), time.Minute)
            if err != nil { return } // expected: some fail when balance exhausted
            svc.Confirm(ctx, holdID, ref{Type: "shipment_charge", ID: uuid.New().String()})
        }()
    }
    wg.Wait()

    // Verify ledger sum equals balance
    requireInvariant(t, db, sellerID)
}
```

## Multi-instance simulation

A single test in `slt_multi_instance_test.go`:

```go
func TestMultiInstanceCorrectness(t *testing.T) {
    pg := testcontainers.NewPostgresContainer(t)

    instanceA := startProcess(t, pg.URL())
    instanceB := startProcess(t, pg.URL())

    // 1. Session created on A is valid on B.
    token := login(instanceA, user)
    require.NoError(t, requestAs(instanceB, token, ...))

    // 2. Session revoked on A is invalid on B within 5s.
    revoke(instanceA, token)
    eventually(t, 5*time.Second, func() bool {
        _, err := requestAs(instanceB, token, ...)
        return errors.Is(err, ErrUnauthenticated)
    })

    // 3. Wallet ops from A and B don't conflict.
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(inst Instance) {
            defer wg.Done()
            inst.Reserve(...)  // alternating A and B
        }(pickInstance(i))
    }
    wg.Wait()
    requireInvariant(...)

    // 4. Outbox event written on A is forwarded by either A or B exactly once.
    writeOutboxEvent(instanceA, ...)
    eventually(t, 5*time.Second, ...)
    requireExactlyOneJob(...)

    // 5. Carrier circuit-breaker trip on A is observed on B within 5s.
    tripCircuit(instanceA, "delhivery")
    eventually(t, 5*time.Second, func() bool {
        return instanceB.IsCircuitOpen("delhivery")
    })
}
```

This test is the gate against multi-instance bugs. If it ever fails, multi-instance correctness is broken.

## Microbenchmarks

`internal/<module>/bench_test.go` — `testing.B`. Track over time via `benchstat`.

### Hot paths to bench

| Bench | Target P95 |
|---|---|
| `BenchmarkPolicyResolveCacheHit` | < 100 ns |
| `BenchmarkPolicyResolveCacheMiss` | < 500 µs |
| `BenchmarkPricingQuote` | < 30 ms |
| `BenchmarkPricingQuoteAll8Carriers` | < 100 ms |
| `BenchmarkAllocationDecide` | < 200 ms |
| `BenchmarkWalletReserveConfirm` | < 30 ms |
| `BenchmarkTrackingNormalize1Event` | < 1 ms |
| `BenchmarkTrackingIngestBatch1000` | < 5 s |

### CI gate
- Benchmarks run on every PR.
- `benchstat` compares to main; > 5% regression in any flagged bench fails the build.

## Sandbox carrier adapter

Real `carriers.Adapter` implementation that produces deterministic responses for SLTs and seller sandbox env. Same code path; the adapter implementation is local + scripted.

```go
package sandbox

func (a *Adapter) Book(ctx, req) (BookingResult, error) {
    awb := fmt.Sprintf("SBX-%s", uuid.New())
    // Schedule scripted tracking events via river jobs (delivered in 3 days, etc.)
    return BookingResult{AWB: awb, ...}, nil
}
```

Used by:
- SLTs (real adapter, fake responses).
- Seller sandbox environment (KYC pending → bookings via sandbox adapter only).
- Local development.

## Test fixtures

`testdata/` per module:
- Sample webhook payloads from each carrier.
- Sample CSV imports.
- Sample contracts.
- Anonymized real-world payloads (with PII scrubbed) for regression cases.

## Running locally

```
make test             # unit tests
make slt              # SLTs (requires Docker)
make bench            # benchmarks
make ci               # all of the above
```

## Performance regressions in CI

PRs that break a benchmark by >5% fail the lint stage. Author can:
1. Fix the regression.
2. Override with explicit reason in PR description (`bench-override: <reason>`).

Override is rare; logged.

## Test coverage tooling

`go test -coverprofile=coverage.out ./...` → reports surfaced in PR. Per-package gates enforced.

## Anti-patterns banned

- Tests that sleep without clock injection (use `core.FakeClock`).
- Tests that depend on external services (mock them; use sandbox adapter).
- Tests that share state across runs (each `t.Run` should be independent).
- Tests that reach into another module's internals (use the public interface).
- `*_test.go` files >500 lines (split by concern).
