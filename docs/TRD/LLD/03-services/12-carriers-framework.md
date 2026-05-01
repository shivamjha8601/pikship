# Carriers Framework

## Purpose

The carriers framework is the **adapter layer** between Pikshipp's domain code (allocation, shipments, tracking, NDR) and external carrier APIs (Delhivery, Bluedart, Ekart, etc.). It exists for one reason: every domain service treats every carrier the same way, and concrete carrier integrations live behind a single Go interface.

Responsibilities:

- Define the `Adapter` interface that every carrier integration implements.
- Maintain the in-process `Registry` of installed adapters, indexed by `carrier_code`.
- Own carrier-level metadata (capabilities, supported services, base URL, credential reference).
- Own the **circuit breaker** and **carrier health state** (open/closed/half-open per carrier).
- Own carrier **serviceability data** (which carrier serves which pincode) and the cache that feeds allocation's serviceability filter.
- Provide a typed `Result` model that wraps every adapter response with success/error/transient classifications.

It does **not** own:

- Carrier choice — allocation (LLD §03-services/07).
- Booking transaction state — shipments (LLD §03-services/13).
- Status normalization — tracking (LLD §03-services/14).
- The actual integration code for a specific carrier — those live in `internal/carriers/<carrier>/` adapter packages.

This document defines the **framework**. The Delhivery adapter is the reference implementation in `internal/carriers/delhivery/`; that adapter has its own LLD (§04-adapters).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, `core.Paise`, `core.Clock`. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/secrets` | `secrets.Secret[string]` for API keys. |
| `internal/audit` | Circuit-breaker open/close events audited. |
| `internal/outbox` | `carrier.health.changed` events. |
| `internal/policy` | per-seller-allowed/excluded carrier lists, weight limits. |
| Postgres LISTEN/NOTIFY | Cross-instance breaker state propagation. |

The framework does **not** depend on allocation, shipments, or tracking. The dependency edge is one-way: those services depend on the framework.

## Package Layout

```
internal/carriers/
├── framework/
│   ├── adapter.go             // Adapter interface + types
│   ├── registry.go            // Registry struct + Install/Get
│   ├── result.go              // Result, ErrorClass enums
│   ├── breaker.go             // Per-carrier circuit breaker
│   ├── health_repo.go         // DB-backed carrier_health_state
│   ├── serviceability.go      // Pincode → carriers cache
│   ├── capabilities.go        // Capability flags / feature matrix
│   ├── http.go                // Shared HTTP client (timeouts, retries, redaction)
│   ├── credentials.go         // Per-seller credential store
│   ├── errors.go              // Sentinel errors
│   ├── jobs.go                // River jobs (HealthSweep, ServiceabilityRebuild)
│   ├── events.go              // Outbox payloads
│   ├── adapter_test.go
│   ├── breaker_test.go
│   ├── registry_test.go
│   └── framework_slt_test.go
├── delhivery/                 // concrete adapter (see §04-adapters)
└── sandbox/                   // in-memory test adapter (see §04-adapters)
```

## The Adapter Interface

```go
package framework

import (
    "context"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Adapter is the single contract every carrier integration implements.
// Methods are grouped: identity, serviceability, quote, book, label, track, NDR.
//
// Every method MUST honor ctx cancellation and return Result so callers
// can distinguish transient (retry) from permanent (give up) failures.

type Adapter interface {
    // Identity
    Code() string                          // stable, lowercase, e.g. "delhivery", "bluedart"
    DisplayName() string                   // human-readable, e.g. "Delhivery"
    Capabilities() Capabilities

    // Serviceability
    //
    // CheckServiceability is called sparingly (during allocation filter
    // pass) and returns whether this carrier serves the (origin, dest,
    // service_type) tuple. Implementations should answer from a local
    // cache; the framework's ServiceabilityCache wraps this.
    CheckServiceability(ctx context.Context, q ServiceabilityQuery) (bool, error)

    // RebuildServiceability is called periodically (per HLD §03-services
    // /03-allocation) to refresh local pincode coverage. Most adapters
    // pull a CSV/JSON from the carrier's portal; some hit a "list pincodes"
    // API. This method is allowed to take minutes and should be invoked
    // from a river worker, not the request path.
    RebuildServiceability(ctx context.Context) (ServiceabilityRefreshResult, error)

    // Quote returns a price estimate. Idempotent: same input must yield
    // same output. Used by the pricing engine when seller-side rate
    // cards are not the source of truth.
    Quote(ctx context.Context, q QuoteRequest) Result[QuoteResponse]

    // Book creates a shipment with the carrier. NOT idempotent at the
    // carrier level for most providers; the framework handles
    // idempotency via shipment-side dedupe.
    Book(ctx context.Context, req BookRequest) Result[BookResponse]

    // Cancel cancels a booked shipment.
    Cancel(ctx context.Context, req CancelRequest) Result[CancelResponse]

    // FetchLabel returns the shipping label PDF (or URL).
    FetchLabel(ctx context.Context, req LabelRequest) Result[LabelResponse]

    // FetchTrackingEvents returns all events since `since`.
    // Adapters that push via webhook return a no-op here; the framework
    // handles webhook routing in tracking service.
    FetchTrackingEvents(ctx context.Context, awb string, since time.Time) Result[[]TrackingEvent]

    // RaiseNDRAction sends a buyer-driven NDR action (reattempt, RTO,
    // address change) to the carrier. Adapters that don't support
    // a given action return Result with ErrorClass = ErrUnsupported.
    RaiseNDRAction(ctx context.Context, req NDRActionRequest) Result[NDRActionResponse]
}
```

### Capabilities

```go
type Capabilities struct {
    // Service types this carrier offers
    Services []ServiceType   // "surface", "air", "express", "same_day", ...

    // Supports COD?
    SupportsCOD bool

    // Max declared value supported (in paise)
    MaxDeclaredValuePaise core.Paise

    // Weight bounds (grams)
    MinWeightG int
    MaxWeightG int

    // Dimension bounds (mm)
    MaxLengthMM int
    MaxWidthMM  int
    MaxHeightMM int

    // Special-handling categories supported
    SupportsFragile     bool
    SupportsBattery     bool
    SupportsPerishable  bool
    SupportsDangerous   bool

    // Tracking model
    PushesWebhookEvents bool   // if true, framework registers webhook
    PullsTrackingEvents bool   // if true, scheduler polls FetchTrackingEvents

    // Label format
    LabelFormat string         // "pdf-a4", "pdf-4x6", "zpl", ...
}
```

### Service Type & Result

```go
type ServiceType string

const (
    ServiceSurface  ServiceType = "surface"
    ServiceAir      ServiceType = "air"
    ServiceExpress  ServiceType = "express"
    ServiceSameDay  ServiceType = "same_day"
    ServiceReverse  ServiceType = "reverse" // returns
)

// Result wraps every adapter response. Inspecting Err / ErrorClass tells
// callers whether to retry, abort, or open the breaker.
type Result[T any] struct {
    OK         bool
    Value      T

    Err        error
    ErrorClass ErrorClass
    HTTPStatus int
    CarrierMsg string             // raw error message from carrier (logged, not surfaced)
    Retryable  bool

    Latency    time.Duration       // observed end-to-end latency for metrics
}

type ErrorClass int

const (
    ErrNone ErrorClass = iota

    // Permanent: caller should NOT retry; surface to user / mark failed.
    ErrInvalidInput        // 4xx-equivalent: bad data
    ErrServiceUnsupported  // capability mismatch (e.g., COD on a non-COD carrier)
    ErrAuth                // bad/expired credentials
    ErrCarrierRefused      // carrier rejected for business reasons (declared value too high, etc.)
    ErrUnsupported         // adapter doesn't implement this op

    // Transient: caller SHOULD retry; opens breaker if pattern persists.
    ErrTimeout
    ErrCarrierUnavailable  // 5xx from carrier or network failure
    ErrRateLimited         // 429
    ErrUnknown             // unparseable
)
```

The `ErrorClass` is the **only** thing breaker logic and retry logic look at. Adapters MUST classify every error correctly; mis-classification is the most common bug.

### Request / Response Types

These live in `framework/adapter.go` (excerpted; full file would be ~600 lines):

```go
type ServiceabilityQuery struct {
    OriginPincode string
    DestPincode   string
    ServiceType   ServiceType
    PaymentMode   PaymentMode  // "prepaid" | "cod"
    WeightG       int
    DeclaredValuePaise core.Paise
}

type QuoteRequest struct {
    SellerID      core.SellerID
    OriginPincode string
    DestPincode   string
    WeightG       int
    LengthMM      int
    WidthMM       int
    HeightMM      int
    ServiceType   ServiceType
    PaymentMode   PaymentMode
    CODAmountPaise core.Paise
    DeclaredValuePaise core.Paise
}

type QuoteResponse struct {
    BasePaise           core.Paise
    FuelSurchargePaise  core.Paise
    CODFeePaise         core.Paise
    OtherChargesPaise   core.Paise
    TotalPaise          core.Paise
    EstimatedTransitDays int
    QuoteRef            string  // carrier-issued (if any) for later booking dedupe
    QuoteValidUntil     time.Time
}

type BookRequest struct {
    SellerID         core.SellerID
    ShipmentID       core.ShipmentID  // our internal ID, surfaced in adapter logs
    PickupAddress    Address
    DropAddress      Address
    PackageWeightG   int
    PackageDimensions Dimensions
    LineItems        []LineItem
    PaymentMode      PaymentMode
    CODAmountPaise   core.Paise
    DeclaredValuePaise core.Paise
    ServiceType      ServiceType
    QuoteRef         string  // carried from earlier Quote when supported
}

type BookResponse struct {
    AWB              string
    CarrierShipmentID string
    LabelURL         string  // some carriers return label inline
    EstimatedDelivery time.Time
}

type CancelRequest struct {
    SellerID core.SellerID
    AWB      string
    Reason   string
}

type CancelResponse struct {
    Cancelled bool
}

type LabelRequest struct {
    SellerID core.SellerID
    AWB      string
    Format   string  // overrides the default if supported
}

type LabelResponse struct {
    Format string
    Bytes  []byte           // raw label content
    URL    string           // OR a URL if the carrier returned one
    GeneratedAt time.Time
}

type TrackingEvent struct {
    AWB        string
    Status     string                  // raw carrier status string
    Location   string
    OccurredAt time.Time
    RawPayload map[string]any
}

type NDRActionRequest struct {
    SellerID core.SellerID
    AWB      string
    Action   NDRAction               // "reattempt" | "rto" | "change_address" | ...
    Notes    string
    NewAddress *Address
}

type NDRActionResponse struct {
    Accepted bool
    NextEventEstimate time.Time
}
```

## Registry

```go
package framework

type Registry struct {
    mu       sync.RWMutex
    adapters map[string]Adapter
}

func NewRegistry() *Registry {
    return &Registry{adapters: make(map[string]Adapter)}
}

// Install registers an adapter under its Code(). Panics if the same
// code is registered twice — this is wiring code, run once at startup.
func (r *Registry) Install(a Adapter) {
    r.mu.Lock()
    defer r.mu.Unlock()
    code := a.Code()
    if _, ok := r.adapters[code]; ok {
        panic(fmt.Sprintf("carrier adapter %q already registered", code))
    }
    r.adapters[code] = a
}

// Get returns the adapter or ErrUnknownCarrier.
func (r *Registry) Get(code string) (Adapter, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    a, ok := r.adapters[code]
    if !ok {
        return nil, fmt.Errorf("%w: %s", ErrUnknownCarrier, code)
    }
    return a, nil
}

// All returns a snapshot of all registered adapters in stable order.
func (r *Registry) All() []Adapter {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]Adapter, 0, len(r.adapters))
    for _, a := range r.adapters {
        out = append(out, a)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Code() < out[j].Code() })
    return out
}
```

Wiring (`cmd/server/wire.go`):

```go
func registerCarriers(r *framework.Registry, cfg Config, secrets secrets.Manager, http *framework.HTTPClient) {
    r.Install(delhivery.New(delhivery.Config{
        BaseURL:     cfg.Carriers.Delhivery.BaseURL,
        Credentials: secrets.Get("carrier/delhivery"),
        HTTP:        http,
    }))
    if cfg.SandboxCarrierEnabled {
        r.Install(sandbox.New())
    }
}
```

## Circuit Breaker

The breaker's job: when a carrier is misbehaving (timeouts, 5xx storms), stop sending it traffic so we don't pile up failed bookings and so allocation can route around it.

### State Machine

```
                      ┌──────────────┐
                      │    closed    │  normal traffic
                      └──────┬───────┘
                             │ failures > threshold within window
                             ▼
                      ┌──────────────┐
                      │     open     │  reject all calls
                      └──────┬───────┘
                             │ cooldown elapses
                             ▼
                      ┌──────────────┐
                      │  half_open   │  allow N probe calls
                      └──┬───────┬───┘
              probes ok  │       │  probes fail
                         ▼       ▼
                      closed    open
```

### Per-Carrier Settings

Settings come from the policy engine (LLD §03-services/01) under keys like `carrier.<code>.breaker.threshold`. Defaults:

```go
type BreakerConfig struct {
    FailureThreshold     int           // default 8
    FailureWindow        time.Duration // default 30 * time.Second
    OpenDuration         time.Duration // default 60 * time.Second
    HalfOpenProbeCount   int           // default 3
}
```

### State Storage

The breaker is **DB-backed** (so it survives restarts and is consistent across instances) plus in-process cached.

```sql
CREATE TABLE carrier_health_state (
    carrier_code   text        PRIMARY KEY,
    state          text        NOT NULL CHECK (state IN ('closed','open','half_open')),
    failure_count  integer     NOT NULL DEFAULT 0,
    success_count  integer     NOT NULL DEFAULT 0,
    last_failure_at timestamptz,
    last_change_at  timestamptz NOT NULL DEFAULT now(),
    next_allowed_at timestamptz,
    -- For half-open: how many probe slots remain
    half_open_slots integer    NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- No RLS: this table is platform-global, not seller-scoped.
GRANT SELECT, INSERT, UPDATE ON carrier_health_state TO pikshipp_app;
GRANT SELECT ON carrier_health_state TO pikshipp_reports;
```

### Implementation

```go
package framework

type Breaker struct {
    repo   *healthRepo
    cfg    BreakerConfig
    cache  *breakerCache
    clock  core.Clock
    logger *slog.Logger
}

type breakerCache struct {
    mu      sync.RWMutex
    entries map[string]breakerCacheEntry
    ttl     time.Duration
}

type breakerCacheEntry struct {
    state     BreakerState
    fetchedAt time.Time
}

// Allow is the gate every adapter call passes through.
//   if err := breaker.Allow(ctx, "delhivery"); err != nil { ... }
//
// In closed state: returns nil quickly from cache.
// In open state: returns ErrBreakerOpen.
// In half_open: returns nil only if a probe slot is available.
func (b *Breaker) Allow(ctx context.Context, code string) error {
    state, err := b.cache.Get(ctx, code, b.repo)
    if err != nil {
        return err
    }
    switch state.State {
    case "closed":
        return nil
    case "open":
        if b.clock.Now().After(state.NextAllowedAt) {
            // Cooldown elapsed; transition to half-open.
            return b.transitionToHalfOpen(ctx, code)
        }
        return ErrBreakerOpen
    case "half_open":
        return b.tryAcquireProbeSlot(ctx, code)
    default:
        return fmt.Errorf("unknown breaker state %q", state.State)
    }
}

// Record reports the outcome of an adapter call.
// Adapters DO NOT call this directly; the framework's `Call` wrapper
// calls Allow → adapter method → Record.
func (b *Breaker) Record(ctx context.Context, code string, ok bool, class ErrorClass) {
    // Only retryable errors count against the breaker.
    // ErrInvalidInput, ErrAuth, etc. do not — they reflect bad input,
    // not carrier illness.
    counts := b.classify(ok, class)
    if counts.Skip {
        return
    }
    if err := b.recordOutcome(ctx, code, counts); err != nil {
        b.logger.Warn("breaker: record outcome", "code", code, "err", err)
    }
}

type outcomeCounts struct {
    Skip    bool
    Failure bool
    Success bool
}

func (b *Breaker) classify(ok bool, class ErrorClass) outcomeCounts {
    if ok {
        return outcomeCounts{Success: true}
    }
    switch class {
    case ErrTimeout, ErrCarrierUnavailable, ErrRateLimited, ErrUnknown:
        return outcomeCounts{Failure: true}
    default:
        return outcomeCounts{Skip: true} // permanent error class
    }
}
```

### State Transitions (Postgres-backed, atomic)

```go
// recordOutcome updates the health state row in a single transaction
// using an advisory lock keyed by carrier_code so that failure-count
// increments don't race.

func (b *Breaker) recordOutcome(ctx context.Context, code string, c outcomeCounts) error {
    return db.WithTx(ctx, b.repo.pool, func(tx pgx.Tx) error {
        // pg_advisory_xact_lock keyed by hash(code) — released on commit.
        if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", code); err != nil {
            return err
        }
        cur, err := b.repo.GetForUpdate(ctx, tx, code)
        if errors.Is(err, pgx.ErrNoRows) {
            // First-ever call: insert closed row.
            cur = &healthRow{CarrierCode: code, State: "closed"}
            if err := b.repo.Insert(ctx, tx, cur); err != nil {
                return err
            }
        } else if err != nil {
            return err
        }

        now := b.clock.Now()
        nextState := cur.State
        var notify bool

        switch cur.State {
        case "closed":
            if c.Failure {
                if now.Sub(cur.LastFailureAt) > b.cfg.FailureWindow {
                    cur.FailureCount = 1
                } else {
                    cur.FailureCount++
                }
                cur.LastFailureAt = now
                if cur.FailureCount >= b.cfg.FailureThreshold {
                    nextState = "open"
                    cur.NextAllowedAt = now.Add(b.cfg.OpenDuration)
                    notify = true
                }
            } else if c.Success {
                cur.FailureCount = 0
            }
        case "half_open":
            if c.Failure {
                nextState = "open"
                cur.NextAllowedAt = now.Add(b.cfg.OpenDuration)
                cur.HalfOpenSlots = 0
                notify = true
            } else if c.Success {
                cur.SuccessCount++
                if cur.SuccessCount >= b.cfg.HalfOpenProbeCount {
                    nextState = "closed"
                    cur.FailureCount = 0
                    cur.SuccessCount = 0
                    notify = true
                }
            }
        case "open":
            // No-op: in open we shouldn't be receiving outcomes.
            // (Allow() rejects calls before they hit adapter.)
        }

        if nextState != cur.State {
            cur.State = nextState
            cur.LastChangeAt = now
        }
        if err := b.repo.Update(ctx, tx, cur); err != nil {
            return err
        }
        if notify {
            // Cross-instance notification.
            if _, err := tx.Exec(ctx, `SELECT pg_notify('carrier_health', $1)`, code); err != nil {
                return err
            }
            // Audit + outbox (for ops alert)
            _ = b.audit.Emit(ctx, tx, audit.Event{
                Action: "carrier.breaker." + nextState,
                Object: audit.ObjCarrier(code),
                Payload: map[string]any{"failure_count": cur.FailureCount},
            })
            _ = b.outb.Emit(ctx, tx, outbox.Event{
                Kind:    "carrier.health.changed",
                Key:     code,
                Payload: map[string]any{"carrier_code": code, "state": nextState},
            })
        }
        return nil
    })
}
```

### LISTEN/NOTIFY for Cross-Instance Awareness

```go
func (b *Breaker) startListener(ctx context.Context) error {
    conn, err := b.repo.pool.Acquire(ctx)
    if err != nil {
        return err
    }
    if _, err := conn.Exec(ctx, `LISTEN carrier_health`); err != nil {
        return err
    }
    go func() {
        defer conn.Release()
        for {
            n, err := conn.Conn().WaitForNotification(ctx)
            if err != nil {
                if ctx.Err() != nil {
                    return
                }
                b.logger.Warn("breaker: listener", "err", err)
                time.Sleep(time.Second)
                continue
            }
            // Invalidate cache for the named carrier
            b.cache.Invalidate(n.Payload)
        }
    }()
    return nil
}
```

The cache TTL (5 seconds) is the safety net if NOTIFY drops; the actual freshness target is sub-second.

## The `Call` Wrapper

Every adapter method invocation goes through this wrapper. It is the **single chokepoint** for breaker enforcement, latency tracking, error classification, and audit.

```go
package framework

func Call[T any](
    ctx context.Context, fw *Framework, code string, op string,
    fn func(ctx context.Context) Result[T],
) Result[T] {
    if err := fw.breaker.Allow(ctx, code); err != nil {
        return Result[T]{
            OK: false, Err: err, ErrorClass: ErrCarrierUnavailable,
            Retryable: false, // breaker open is its own concept
        }
    }

    // Per-call timeout. Adapters can override via context but this is
    // the framework-level safety net.
    ctx, cancel := context.WithTimeout(ctx, fw.cfg.PerCallTimeout)
    defer cancel()

    start := fw.clock.Now()
    res := fn(ctx)
    res.Latency = fw.clock.Now().Sub(start)

    fw.metrics.RecordCarrierCall(code, op, res.OK, res.ErrorClass, res.Latency)
    fw.breaker.Record(ctx, code, res.OK, res.ErrorClass)

    return res
}
```

Adapters call this via:

```go
// In delhivery/adapter.go
func (a *Adapter) Quote(ctx context.Context, q QuoteRequest) framework.Result[QuoteResponse] {
    return framework.Call(ctx, a.fw, a.Code(), "quote", func(ctx context.Context) framework.Result[QuoteResponse] {
        return a.doQuote(ctx, q)
    })
}
```

## Serviceability Cache

Allocation calls `CheckServiceability` for every (order, candidate-carrier) pair during filter pass. Hitting the carrier API per call is unaffordable. We materialize a per-carrier pincode coverage table.

```sql
CREATE TABLE carrier_serviceability (
    carrier_code     text        NOT NULL,
    origin_pincode   text        NOT NULL,
    dest_pincode     text        NOT NULL,
    service_type     text        NOT NULL,
    supports_cod     boolean     NOT NULL DEFAULT true,
    estimated_days   integer,
    last_refreshed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (carrier_code, origin_pincode, dest_pincode, service_type)
);

CREATE INDEX carrier_serviceability_origin_dest_idx
    ON carrier_serviceability(origin_pincode, dest_pincode, service_type);
```

For carriers with too many entries to fit (Delhivery has ~30k pincodes × all-pairs), we use a **coverage range** model:

```sql
CREATE TABLE carrier_serviceability_coverage (
    carrier_code text NOT NULL,
    pincode_pattern text NOT NULL,  -- glob pattern: "1100*", "1234??"
    direction text NOT NULL CHECK (direction IN ('origin','dest','both')),
    service_type text NOT NULL,
    supports_cod boolean NOT NULL DEFAULT true,
    estimated_days integer,
    last_refreshed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (carrier_code, pincode_pattern, direction, service_type)
);
```

The serviceability cache is populated by `RebuildServiceabilityJob` (runs nightly per carrier). The in-memory layer:

```go
type ServiceabilityCache struct {
    mu        sync.RWMutex
    perCarrier map[string]*carrierCoverage
    fetchedAt  map[string]time.Time
    ttl       time.Duration
    repo      *serviceabilityRepo
}

type carrierCoverage struct {
    exact   map[serviceabilityKey]coverage
    patterns []patternEntry
}

type serviceabilityKey struct {
    OriginPincode string
    DestPincode   string
    ServiceType   ServiceType
}

func (c *ServiceabilityCache) Check(ctx context.Context, code string, q ServiceabilityQuery) (bool, error) {
    cov, err := c.getCoverage(ctx, code)
    if err != nil {
        return false, err
    }
    if e, ok := cov.exact[serviceabilityKey{q.OriginPincode, q.DestPincode, q.ServiceType}]; ok {
        return c.matchesPaymentAndWeight(e, q), nil
    }
    // Try patterns
    for _, p := range cov.patterns {
        if p.matches(q.OriginPincode, q.DestPincode, q.ServiceType) {
            return c.matchesPaymentAndWeight(p.coverage, q), nil
        }
    }
    return false, nil
}
```

This cache is the read path used by allocation's filter; the write path is the nightly refresh job.

## Credential Storage

Credentials are per-carrier (not per-seller, in v0). Sellers don't bring their own carrier accounts at v0; the platform negotiates rates.

```go
package framework

// CredentialRef is an opaque key into the secrets manager.
// e.g. "carrier/delhivery" → { api_key, password }
type CredentialRef string

func (a *Adapter) Credentials() map[string]secrets.Secret[string] {
    // Load from the secrets manager at startup.
    // The secrets.Secret type ensures these never log accidentally
    // (LLD §02-infrastructure/05-secrets).
}
```

When sellers bring their own carrier accounts (v1+), introduce `seller_carrier_credential` table.

## Background Jobs

```go
// HealthSweepJob: sanity-check the health table every minute.
// Mostly a no-op in healthy times; useful for detecting `open` rows
// stuck past their next_allowed_at because no one's calling that
// carrier (so the breaker would never naturally probe).

type HealthSweepJob struct{ river.JobArgs }
func (HealthSweepJob) Kind() string { return "carrier.health_sweep" }

type HealthSweepWorker struct {
    river.WorkerDefaults[HealthSweepJob]
    breaker *Breaker
    clock   core.Clock
}

func (w *HealthSweepWorker) Work(ctx context.Context, j *river.Job[HealthSweepJob]) error {
    rows, err := w.breaker.repo.ListOpenStuckPastDeadline(ctx, w.clock.Now())
    if err != nil {
        return err
    }
    for _, r := range rows {
        if err := w.breaker.transitionToHalfOpen(ctx, r.CarrierCode); err != nil {
            slog.Warn("carrier health sweep: half-open transition failed", "code", r.CarrierCode, "err", err)
        }
    }
    return nil
}

// ServiceabilityRebuildJob: nightly per-carrier rebuild.
type ServiceabilityRebuildJob struct {
    river.JobArgs
    CarrierCode string
}
func (ServiceabilityRebuildJob) Kind() string { return "carrier.serviceability_rebuild" }

type ServiceabilityRebuildWorker struct {
    river.WorkerDefaults[ServiceabilityRebuildJob]
    registry *Registry
    repo     *serviceabilityRepo
    cache    *ServiceabilityCache
}

func (w *ServiceabilityRebuildWorker) Work(ctx context.Context, j *river.Job[ServiceabilityRebuildJob]) error {
    a, err := w.registry.Get(j.Args.CarrierCode)
    if err != nil {
        return err
    }
    res, err := a.RebuildServiceability(ctx)
    if err != nil {
        return err
    }
    if err := w.repo.ReplaceCoverage(ctx, j.Args.CarrierCode, res); err != nil {
        return err
    }
    w.cache.Invalidate(j.Args.CarrierCode)
    return nil
}
```

## Outbox Event Payloads

```go
type CarrierHealthChangedPayload struct {
    SchemaVersion int       `json:"schema_version"` // = 1
    CarrierCode   string    `json:"carrier_code"`
    State         string    `json:"state"` // closed | open | half_open
    FailureCount  int       `json:"failure_count"`
    OccurredAt    time.Time `json:"occurred_at"`
}
```

Forwarder routes:
- `carrier.health.changed` → `notifications.OnCarrierHealthChangedJob` (alert ops on `open`).
- `carrier.health.changed` → `allocation.InvalidateCandidateCacheJob`.

## Sentinel Errors

```go
var (
    ErrUnknownCarrier        = errors.New("carriers: unknown carrier code")
    ErrBreakerOpen           = errors.New("carriers: breaker open")
    ErrUnsupportedOperation  = errors.New("carriers: operation not supported by adapter")
    ErrServiceUnsupported    = errors.New("carriers: service type unsupported by adapter")
    ErrCredentialsMissing    = errors.New("carriers: credentials not configured")
    ErrInvalidServiceability = errors.New("carriers: invalid serviceability data returned by adapter")
)
```

## Testing

### Sandbox Adapter

The framework ships a `sandbox` adapter (`internal/carriers/sandbox/`) that:
- Implements `Adapter`.
- Returns deterministic answers from in-memory data.
- Lets tests inject failures, latencies, and capability quirks.
- Is registered only in test/dev environments via `cfg.SandboxCarrierEnabled`.

```go
package sandbox

type Adapter struct {
    code string
    behaviors map[string]Behavior // op name → injected behavior
}

type Behavior struct {
    LatencyMs int
    FailWith  *framework.ErrorClass
    ResponseOverride any
}

func (a *Adapter) WithBehavior(op string, b Behavior) {
    a.behaviors[op] = b
}
```

### Unit Tests

```go
func TestBreaker_OpensAfterThreshold(t *testing.T) {
    pg := slt.StartPG(t)
    b := framework.NewBreaker(slt.HealthRepo(pg), framework.BreakerConfig{
        FailureThreshold: 3,
        FailureWindow:    1 * time.Hour,
        OpenDuration:     1 * time.Minute,
    }, slt.FakeClock(0))

    for i := 0; i < 3; i++ {
        b.Record(ctx, "test", false, framework.ErrTimeout)
    }
    err := b.Allow(ctx, "test")
    require.ErrorIs(t, err, framework.ErrBreakerOpen)
}

func TestBreaker_PermanentErrorsDoNotOpen(t *testing.T) {
    b := newTestBreaker(t, 3)
    for i := 0; i < 10; i++ {
        b.Record(ctx, "test", false, framework.ErrInvalidInput)
    }
    require.NoError(t, b.Allow(ctx, "test"))
}

func TestBreaker_HalfOpenRecoversOnSuccess(t *testing.T) {
    clk := slt.FakeClock(0)
    b := newTestBreakerWithClock(t, 3, clk)
    for i := 0; i < 3; i++ {
        b.Record(ctx, "test", false, framework.ErrTimeout)
    }
    clk.Advance(1 * time.Minute) // past OpenDuration
    require.NoError(t, b.Allow(ctx, "test")) // transitions to half-open

    for i := 0; i < 3; i++ {
        b.Record(ctx, "test", true, framework.ErrNone)
    }
    require.Equal(t, "closed", b.StateOf("test"))
}

func TestBreaker_HalfOpenReverts(t *testing.T) {
    // Half-open → first probe fails → back to open with reset cooldown.
    // ...
}

func TestRegistry_DoubleInstallPanics(t *testing.T) {
    r := framework.NewRegistry()
    r.Install(sandbox.New("x"))
    require.Panics(t, func() { r.Install(sandbox.New("x")) })
}

func TestServiceabilityCache_PatternMatch(t *testing.T) {
    c := framework.NewServiceabilityCacheWith(map[string]*framework.CarrierCoverage{
        "test": {Patterns: []framework.PatternEntry{{Pattern: "1100*", Direction: "dest", ServiceType: framework.ServiceSurface}}},
    })
    ok, _ := c.Check(ctx, "test", framework.ServiceabilityQuery{
        OriginPincode: "560001",
        DestPincode:   "110011",
        ServiceType:   framework.ServiceSurface,
    })
    require.True(t, ok)
}
```

### SLT (`framework_slt_test.go`)

```go
func TestBreaker_CrossInstance_SLT(t *testing.T) {
    // Two breaker instances sharing one Postgres; instance A opens
    // the breaker; instance B sees `open` within ~1s via LISTEN.
    pg := slt.StartPG(t)
    bA := framework.NewBreakerOn(pg)
    bB := framework.NewBreakerOn(pg)

    for i := 0; i < 8; i++ {
        bA.Record(ctx, "test", false, framework.ErrTimeout)
    }
    slt.WaitFor(t, 2*time.Second, func() bool {
        return errors.Is(bB.Allow(ctx, "test"), framework.ErrBreakerOpen)
    })
}

func TestSandboxAdapter_Quote_SLT(t *testing.T) {
    // End-to-end: registry → call wrapper → adapter → response.
    fw := slt.NewFramework(t)
    fw.Registry.Install(sandbox.New("sb"))

    r := framework.Call(ctx, fw, "sb", "quote", func(ctx context.Context) framework.Result[framework.QuoteResponse] {
        a, _ := fw.Registry.Get("sb")
        return a.Quote(ctx, sampleQuoteRequest())
    })
    require.True(t, r.OK)
    require.Greater(t, r.Latency, time.Duration(0))
}
```

### Microbenchmarks

```go
func BenchmarkBreaker_Allow_Closed_Hit(b *testing.B) {
    br := newTestBreakerInClosedState(b)
    for i := 0; i < b.N; i++ {
        _ = br.Allow(context.Background(), "test")
    }
}
// Target: < 100 ns, 0 allocs (cache hit path).

func BenchmarkServiceability_Check_ExactHit(b *testing.B) {
    c := slt.SeedServiceability("test", 50000) // 50k entries
    q := framework.ServiceabilityQuery{
        OriginPincode: "110001", DestPincode: "560001",
        ServiceType: framework.ServiceSurface,
    }
    for i := 0; i < b.N; i++ {
        _, _ = c.Check(context.Background(), "test", q)
    }
}
// Target: < 200 ns, 0 allocs (map lookup).

func BenchmarkServiceability_Check_PatternFallback(b *testing.B) {
    // 1000 patterns; ensure linear scan stays under 5 µs.
    // ...
}
// Target: < 5 µs.
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Breaker.Allow` (closed, cache hit) | 80 ns | 200 ns | 0 allocs |
| `Breaker.Allow` (cache miss) | 1.5 ms | 4 ms | 1 SELECT |
| `Breaker.Record` (no transition) | 0.6 ms | 2 ms | 1 advisory_lock + 1 UPDATE |
| `Breaker.Record` (transition open) | 5 ms | 12 ms | + audit + outbox + NOTIFY |
| `ServiceabilityCache.Check` (exact) | 150 ns | 400 ns | map lookup |
| `ServiceabilityCache.Check` (pattern) | 1 µs | 5 µs | up to 1000 patterns scanned |
| `Registry.Get` | 50 ns | 100 ns | RLock + map lookup |
| `Call` overhead (closed breaker) | 1 µs | 3 µs | Allow + timer + Record (excludes adapter time) |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Adapter returns `ErrTimeout` repeatedly | Breaker counts increment | Breaker opens after threshold; allocation routes around. |
| Adapter returns `ErrAuth` | Recorded but does NOT count against breaker | Surface to ops dashboard via audit; manual rotation needed. |
| Adapter returns `ErrUnsupported` for an op we called | `Result.ErrorClass = ErrUnsupported` | Caller must check `Capabilities()` before calling; this is a programming bug. |
| LISTEN connection drops | Listener loop logs error and reconnects | TTL (5s) ensures eventual freshness. |
| `pg_advisory_xact_lock` held too long | Long tx blocks other Record calls | The transactions are tiny (< 5ms); held lock ~ 5ms; problematic only under extreme contention. |
| Serviceability data corrupt (carrier API returned garbage) | RebuildServiceability returns `ErrInvalidServiceability` | Job fails; previous coverage stays; ops alerted. |
| Carrier credentials missing at startup | Adapter constructor returns error | `cmd/server/wire.go` aborts startup; deploy fails; ops sees clear error. |
| Sandbox adapter accidentally registered in prod | Config check at startup | `cfg.SandboxCarrierEnabled` is false in prod; double-checked in `wire.go`. |
| Breaker state in DB diverges from caches due to clock skew | `last_change_at` vs cache `fetchedAt` | All times are server-side UTC; we never use local clocks for breaker decisions. |

## Open Questions

1. **Per-(seller, carrier) breaker.** Today the breaker is global per carrier. A specific seller's bookings might fail (e.g., bad pickup address) while others succeed; aggregating those into a global counter slightly inflates the failure rate but greatly simplifies operation. **Decision: keep global for v0**; revisit if false-open rates exceed 0.5%.
2. **Health-state TTL on cache.** 5s is a guess. Telemetry will show whether stale-by-5s causes any harm. **Action item:** instrument time-from-NOTIFY-to-cache-invalidate; alarm on p99 > 1s.
3. **Sandbox-vs-real swap during ops debugging.** Operators occasionally need to redirect production traffic to a sandbox carrier. **Decision:** out of scope; we won't ship a flag for this. Ops can patch the registry in a hotfix branch if absolutely needed.
4. **Bring-your-own-carrier accounts.** Deferred to v1; design implies adding `seller_carrier_credential` and a per-(seller, carrier) breaker variant.
5. **Webhook authentication strategy across carriers.** Each carrier signs webhooks differently (HMAC, IP allow-list, secret-in-URL). **Decision:** define the signature-verification API in adapter, with framework-side helpers for HMAC-SHA256. Tracking service consumes.

## References

- HLD §03-services/03-allocation: serviceability filter consumes `ServiceabilityCache.Check`.
- HLD §03-services/05-tracking-and-status: tracking consumes `FetchTrackingEvents` and webhook routing.
- HLD §01-architecture/04-multi-instance-readiness: cross-instance breaker state via LISTEN/NOTIFY.
- LLD §03-services/01-policy-engine: per-carrier breaker thresholds.
- LLD §03-services/02-audit: breaker open/close audited.
- LLD §03-services/03-outbox: `carrier.health.changed` event routing.
- LLD §02-infrastructure/05-secrets: `secrets.Secret[string]` for credentials.
- LLD §04-adapters/01-delhivery (future): reference adapter implementing this contract.
- LLD §04-adapters/sandbox (future): in-memory test adapter.
