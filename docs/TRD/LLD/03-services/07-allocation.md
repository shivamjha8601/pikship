# Service: Allocation engine (`internal/allocation`)

> Picks the carrier and service for a shipment. Multi-objective scoring. Auditable: every decision persisted with full ranking + filter reasons.

## Purpose

- `Allocate(order)` → ranked list of (carrier, service) with scores; recommended pick.
- `AllocateBulk` for bulk-book paths.
- Reliability score precomputation (daily river job).
- Allocation decision persistence for audit.
- Carrier circuit breaker integration (filter out unhealthy carriers).

## Dependencies

- `internal/core`
- `internal/policy` (reads weights, allowed/excluded carrier sets)
- `internal/pricing` (QuoteAll on candidate set)
- `internal/carriers` (serviceability, circuit breaker state)
- `internal/observability/dbtx`
- `internal/audit`

## Package layout

```
internal/allocation/
├── doc.go
├── service.go              ← Engine interface
├── service_impl.go
├── repo.go
├── types.go                ← Decision, Candidate, ObjectiveWeights
├── filter.go               ← hard-constraint filter
├── score.go                ← weighted scoring
├── reliability.go          ← daily reliability computation job
├── reliability_cache.go
├── policy_keys.go
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package allocation picks the carrier and service for a shipment.
//
// Hard constraints (filter): serviceability, weight bounds, payment support,
// carrier health, seller's allowed/excluded set.
//
// Soft objectives (score): cost (from pricing engine), speed (estimated days),
// reliability (precomputed per carrier × pincode-zone), seller preference.
//
// Every Allocate call persists an AllocationDecision row for full audit
// traceability — including filtered-out candidates and their rejection reasons.
package allocation

import (
    "context"
    "errors"
    "time"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/pricing"
)

// Engine is the public API.
type Engine interface {
    // Allocate scores all candidate carriers for an order and persists the decision.
    Allocate(ctx context.Context, in AllocateInput) (Decision, error)

    // AllocateBulk batches; returns one Decision per order.
    AllocateBulk(ctx context.Context, ins []AllocateInput) ([]BulkResult, error)
}

// AllocateInput is per-order.
type AllocateInput struct {
    SellerID         core.SellerID
    OrderID          core.OrderID

    PickupPincode    core.Pincode
    ShipToPincode    core.Pincode
    DeclaredWeightG  int
    DimsMM           pricing.Dimensions
    PaymentMode      core.PaymentMode
    DeclaredValue    core.Paise
    SpecialHandling  []pricing.SpecialHandling

    // Optional overrides (rare); usually we read from policy
    OverrideWeights  *ObjectiveWeights
    PreferredCarriers []core.CarrierID  // ordered preferences
}

// BulkResult pairs an order with its decision (or error).
type BulkResult struct {
    OrderID  core.OrderID
    Decision Decision
    Err      error
}

// Decision is the persisted output.
type Decision struct {
    ID                core.AllocationDecisionID
    SellerID          core.SellerID
    OrderID           core.OrderID
    Candidates        []Candidate          // ranked, descending score
    FilteredOut       []FilteredCandidate
    WeightsUsed       ObjectiveWeights
    StrategyVersion   string
    RecommendedIdx    int                  // 0 = top of Candidates; -1 if none
    DecidedAt         time.Time
}

// Candidate is one (carrier, service) option.
type Candidate struct {
    CarrierID         core.CarrierID
    ServiceType       core.ServiceType
    Quote             pricing.Quote
    ReliabilityScore  float64              // [0, 1]
    Score             CompositeScore
}

type CompositeScore struct {
    CostScore        float64
    SpeedScore       float64
    ReliabilityScore float64
    PrefScore        float64
    Total            float64
}

// FilteredCandidate is a (carrier, service) pair we excluded; with reason.
type FilteredCandidate struct {
    CarrierID   core.CarrierID
    ServiceType core.ServiceType
    Reason      FilterReason
    Detail      string
}

type FilterReason string

const (
    ReasonNotInAllowedSet      FilterReason = "not_in_allowed_set"
    ReasonInExcludedSet        FilterReason = "in_excluded_set"
    ReasonPincodeUnserviceable FilterReason = "pincode_unserviceable"
    ReasonWeightOutOfBounds    FilterReason = "weight_out_of_bounds"
    ReasonPaymentNotSupported  FilterReason = "payment_not_supported"
    ReasonSpecialHandlingNotSupported FilterReason = "special_handling_not_supported"
    ReasonCircuitBreakerOpen   FilterReason = "circuit_breaker_open"
    ReasonPricingFailed        FilterReason = "pricing_failed"
    ReasonCostCeilingExceeded  FilterReason = "cost_ceiling_exceeded"
)

// ObjectiveWeights are the policy-driven scoring weights.
type ObjectiveWeights struct {
    Cost       float64
    Speed      float64
    Reliability float64
    SellerPref float64
}

// Sentinel errors.
var (
    ErrNoCandidates = fmt.Errorf("allocation: no carriers serve this combination: %w", core.ErrNotFound)
)

// StrategyVersion is bumped when the scoring algorithm changes.
const StrategyVersion = "v1.0"
```

## DB schema

```sql
-- migrations/00NN_create_allocation.up.sql

CREATE TABLE carrier_serviceability (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_id      UUID NOT NULL,
    pincode         TEXT NOT NULL,
    service_type    TEXT NOT NULL,
    cod_allowed     BOOLEAN NOT NULL,
    zone_code       TEXT NOT NULL,
    estimated_days  INT,
    source          TEXT NOT NULL DEFAULT 'manual',
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (carrier_id, pincode, service_type)
);

CREATE INDEX carrier_serviceability_pincode_idx ON carrier_serviceability (pincode, service_type);

-- Platform-level table, no RLS
GRANT SELECT ON carrier_serviceability TO pikshipp_app, pikshipp_reports;
GRANT ALL ON carrier_serviceability TO pikshipp_admin;

CREATE TABLE carrier_reliability (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_id        UUID NOT NULL,
    pincode_zone      TEXT NOT NULL,
    service_type      TEXT NOT NULL,
    on_time_rate      NUMERIC(5,4) NOT NULL DEFAULT 0.5,
    ndr_rate          NUMERIC(5,4) NOT NULL DEFAULT 0.5,
    rto_rate          NUMERIC(5,4) NOT NULL DEFAULT 0.5,
    api_success_rate  NUMERIC(5,4) NOT NULL DEFAULT 1.0,
    composite_score   NUMERIC(5,4) NOT NULL DEFAULT 0.5,
    sample_size       INT NOT NULL DEFAULT 0,
    computed_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX carrier_reliability_lookup_idx ON carrier_reliability (carrier_id, pincode_zone, service_type, computed_at DESC);

GRANT SELECT ON carrier_reliability TO pikshipp_app, pikshipp_reports;
GRANT ALL ON carrier_reliability TO pikshipp_admin;

CREATE TABLE allocation_decision (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID NOT NULL,
    order_id            UUID NOT NULL,
    shipment_id         UUID,                     -- set when shipment is booked
    candidates_jsonb    JSONB NOT NULL,
    filtered_out_jsonb  JSONB NOT NULL,
    weights_used_jsonb  JSONB NOT NULL,
    recommended_idx     INT NOT NULL,
    strategy_version    TEXT NOT NULL,
    decided_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX allocation_decision_order_idx ON allocation_decision (order_id);
CREATE INDEX allocation_decision_seller_idx ON allocation_decision (seller_id, decided_at DESC);

ALTER TABLE allocation_decision ENABLE ROW LEVEL SECURITY;
CREATE POLICY allocation_decision_seller ON allocation_decision
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE ON allocation_decision TO pikshipp_app;
GRANT SELECT ON allocation_decision TO pikshipp_reports;
GRANT ALL ON allocation_decision TO pikshipp_admin;
```

## SQL queries

```sql
-- query/allocation.sql

-- name: GetServiceabilityForPincode :many
SELECT carrier_id, service_type, cod_allowed, zone_code, estimated_days
FROM carrier_serviceability
WHERE pincode = $1
  AND carrier_id = ANY($2::uuid[]);

-- name: GetReliabilityScore :one
SELECT composite_score, on_time_rate, ndr_rate, rto_rate, sample_size
FROM carrier_reliability
WHERE carrier_id = $1 AND pincode_zone = $2 AND service_type = $3
ORDER BY computed_at DESC
LIMIT 1;

-- name: InsertAllocationDecision :exec
INSERT INTO allocation_decision
  (id, seller_id, order_id, candidates_jsonb, filtered_out_jsonb, weights_used_jsonb, recommended_idx, strategy_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetShipmentOnDecision :exec
UPDATE allocation_decision SET shipment_id = $2 WHERE id = $1;

-- name: ComputeReliabilityFromShipments :many
-- Aggregates last 30 days of shipments into per-(carrier, zone, service) stats.
SELECT
    s.carrier_id,
    cs.zone_code AS pincode_zone,
    s.service_type,
    COUNT(*)                                                                       AS sample_size,
    AVG(CASE WHEN s.delivered_on_time THEN 1.0 ELSE 0.0 END)::numeric(5,4)         AS on_time_rate,
    AVG(CASE WHEN s.had_ndr THEN 1.0 ELSE 0.0 END)::numeric(5,4)                   AS ndr_rate,
    AVG(CASE WHEN s.was_rto THEN 1.0 ELSE 0.0 END)::numeric(5,4)                   AS rto_rate
FROM shipment s
JOIN carrier_serviceability cs ON cs.carrier_id = s.carrier_id AND cs.pincode = s.ship_to_pincode AND cs.service_type = s.service_type
WHERE s.created_at > now() - INTERVAL '30 days'
  AND s.status IN ('delivered','rto_delivered')
GROUP BY s.carrier_id, cs.zone_code, s.service_type
HAVING COUNT(*) >= 50;  -- min sample size

-- name: UpsertReliability :exec
INSERT INTO carrier_reliability
  (carrier_id, pincode_zone, service_type, on_time_rate, ndr_rate, rto_rate, composite_score, sample_size)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
```

## Implementation

```go
// internal/allocation/service_impl.go
package allocation

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/carriers"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
    "github.com/pikshipp/pikshipp/internal/policy"
    "github.com/pikshipp/pikshipp/internal/pricing"
)

type engineImpl struct {
    pool        *pgxpool.Pool
    repo        *repo
    pricing     pricing.Engine
    carriers    carriers.Registry
    policy      policy.Engine
    audit       audit.Emitter
    relCache    *reliabilityCache
    clock       core.Clock
    log         *slog.Logger
}

func New(pool *pgxpool.Pool, p pricing.Engine, c carriers.Registry, pol policy.Engine, audit audit.Emitter, clock core.Clock, log *slog.Logger) Engine {
    return &engineImpl{
        pool: pool, repo: newRepo(pool),
        pricing: p, carriers: c, policy: pol, audit: audit,
        relCache: newReliabilityCache(60*time.Second, clock),
        clock: clock, log: log,
    }
}

func (e *engineImpl) Allocate(ctx context.Context, in AllocateInput) (Decision, error) {
    decision := Decision{
        ID:              core.AllocationDecisionID(uuid.New()),
        SellerID:        in.SellerID,
        OrderID:         in.OrderID,
        StrategyVersion: StrategyVersion,
        DecidedAt:       e.clock.Now(),
        RecommendedIdx:  -1,
    }

    // Resolve weights
    weights := in.OverrideWeights
    if weights == nil {
        w, err := e.resolveWeights(ctx, in.SellerID)
        if err != nil { return decision, err }
        weights = &w
    }
    decision.WeightsUsed = *weights

    // Load allowed/excluded sets
    allowed, excluded, err := e.resolveCarrierSets(ctx, in.SellerID)
    if err != nil { return decision, err }

    // 1. Build candidate (carrier, service) list from allowed - excluded
    universe := e.carriers.AllRegistered(ctx)  // [(carrier, service), ...]
    var candidates []candidatePre
    for _, cs := range universe {
        if !allowed.Has(string(cs.CarrierID)) {
            decision.FilteredOut = append(decision.FilteredOut, FilteredCandidate{
                CarrierID: cs.CarrierID, ServiceType: cs.ServiceType,
                Reason: ReasonNotInAllowedSet,
            })
            continue
        }
        if excluded.Has(string(cs.CarrierID)) {
            decision.FilteredOut = append(decision.FilteredOut, FilteredCandidate{
                CarrierID: cs.CarrierID, ServiceType: cs.ServiceType,
                Reason: ReasonInExcludedSet,
            })
            continue
        }
        candidates = append(candidates, candidatePre{CarrierID: cs.CarrierID, ServiceType: cs.ServiceType})
    }

    // 2. Hard-constraint filter: serviceability, weight, payment, special, circuit
    candidates, filteredOut := e.applyFilters(ctx, in, candidates)
    decision.FilteredOut = append(decision.FilteredOut, filteredOut...)

    if len(candidates) == 0 {
        decision.Candidates = []Candidate{}
        e.persist(ctx, decision)
        return decision, ErrNoCandidates
    }

    // 3. Get pricing for surviving candidates (parallel via QuoteAll)
    pricingCandidates := make([]pricing.CarrierServiceCandidate, len(candidates))
    for i, c := range candidates {
        pricingCandidates[i] = pricing.CarrierServiceCandidate{CarrierID: c.CarrierID, ServiceType: c.ServiceType}
    }

    quotes, err := e.pricing.QuoteAll(ctx, pricing.QuoteAllInput{
        SellerID:        in.SellerID,
        PickupPincode:   in.PickupPincode,
        ShipToPincode:   in.ShipToPincode,
        DeclaredWeightG: in.DeclaredWeightG,
        DimsMM:          in.DimsMM,
        PaymentMode:     in.PaymentMode,
        DeclaredValue:   in.DeclaredValue,
        SpecialHandling: in.SpecialHandling,
        Candidates:      pricingCandidates,
    })
    if err != nil { return decision, fmt.Errorf("allocation: pricing: %w", err) }

    // Index quotes by (carrier, service)
    quoteByCS := make(map[string]pricing.Quote, len(quotes))
    for _, q := range quotes {
        quoteByCS[csKey(q.CarrierID, q.ServiceType)] = q
    }

    // Drop candidates that didn't get a quote
    var withQuotes []candidatePre
    for _, c := range candidates {
        if _, ok := quoteByCS[csKey(c.CarrierID, c.ServiceType)]; !ok {
            decision.FilteredOut = append(decision.FilteredOut, FilteredCandidate{
                CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                Reason: ReasonPricingFailed,
            })
            continue
        }
        withQuotes = append(withQuotes, c)
    }
    candidates = withQuotes

    if len(candidates) == 0 {
        e.persist(ctx, decision)
        return decision, ErrNoCandidates
    }

    // 4. Reliability lookup per (carrier, pincode_zone, service)
    reliabilities := make(map[string]float64, len(candidates))
    for _, c := range candidates {
        q := quoteByCS[csKey(c.CarrierID, c.ServiceType)]
        rel, _ := e.relCache.Get(ctx, c.CarrierID, q.Inputs.ZoneCode, c.ServiceType)
        reliabilities[csKey(c.CarrierID, c.ServiceType)] = rel
    }

    // 5. Score
    scored := scoreCandidates(candidates, quoteByCS, reliabilities, in.PreferredCarriers, *weights)

    // 6. Apply auto-book gap check (informational; allocation engine doesn't block, just flags)
    decision.Candidates = scored
    decision.RecommendedIdx = 0  // top by Total score

    // 7. Persist
    if err := e.persist(ctx, decision); err != nil {
        return decision, err
    }

    return decision, nil
}

func (e *engineImpl) AllocateBulk(ctx context.Context, ins []AllocateInput) ([]BulkResult, error) {
    results := make([]BulkResult, len(ins))
    sem := make(chan struct{}, runtime.NumCPU())  // worker pool
    var wg sync.WaitGroup

    for i, in := range ins {
        wg.Add(1)
        sem <- struct{}{}
        go func(i int, in AllocateInput) {
            defer wg.Done()
            defer func() { <-sem }()
            d, err := e.Allocate(ctx, in)
            results[i] = BulkResult{OrderID: in.OrderID, Decision: d, Err: err}
        }(i, in)
    }
    wg.Wait()
    return results, nil
}
```

## Filter

```go
// internal/allocation/filter.go
package allocation

import (
    "context"

    "github.com/pikshipp/pikshipp/internal/core"
)

type candidatePre struct {
    CarrierID   core.CarrierID
    ServiceType core.ServiceType
}

func (e *engineImpl) applyFilters(ctx context.Context, in AllocateInput, candidates []candidatePre) ([]candidatePre, []FilteredCandidate) {
    var keep []candidatePre
    var dropped []FilteredCandidate

    // Bulk lookup serviceability for ship_to_pincode
    carrierIDs := make([]core.CarrierID, len(candidates))
    for i, c := range candidates {
        carrierIDs[i] = c.CarrierID
    }

    serviceability, _ := e.repo.GetServiceabilityForPincode(ctx, in.ShipToPincode, carrierIDs)
    serviceabilityMap := make(map[string]ServiceabilityRow, len(serviceability))
    for _, sr := range serviceability {
        serviceabilityMap[csKey(sr.CarrierID, sr.ServiceType)] = sr
    }

    for _, c := range candidates {
        sr, ok := serviceabilityMap[csKey(c.CarrierID, c.ServiceType)]
        if !ok {
            dropped = append(dropped, FilteredCandidate{
                CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                Reason: ReasonPincodeUnserviceable,
            })
            continue
        }

        // COD support
        if in.PaymentMode == core.PaymentModeCOD && !sr.CODAllowed {
            dropped = append(dropped, FilteredCandidate{
                CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                Reason: ReasonPaymentNotSupported,
                Detail: "COD not allowed",
            })
            continue
        }

        // Weight bounds (read carrier capability)
        adapter, ok := e.carriers.Adapter(c.CarrierID)
        if !ok {
            dropped = append(dropped, FilteredCandidate{
                CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                Reason: ReasonPincodeUnserviceable,
                Detail: "adapter not registered",
            })
            continue
        }
        caps := adapter.Capabilities()
        weightCaps, ok := caps.WeightBounds[c.ServiceType]
        if ok {
            if in.DeclaredWeightG < weightCaps.MinG || in.DeclaredWeightG > weightCaps.MaxG {
                dropped = append(dropped, FilteredCandidate{
                    CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                    Reason: ReasonWeightOutOfBounds,
                    Detail: fmt.Sprintf("weight %d not in [%d, %d]", in.DeclaredWeightG, weightCaps.MinG, weightCaps.MaxG),
                })
                continue
            }
        }

        // Special handling support
        for _, h := range in.SpecialHandling {
            if !caps.SupportsSpecialHandling(h) {
                dropped = append(dropped, FilteredCandidate{
                    CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                    Reason: ReasonSpecialHandlingNotSupported,
                    Detail: string(h),
                })
                goto nextCandidate
            }
        }

        // Circuit breaker
        if e.carriers.IsCircuitOpen(c.CarrierID) {
            dropped = append(dropped, FilteredCandidate{
                CarrierID: c.CarrierID, ServiceType: c.ServiceType,
                Reason: ReasonCircuitBreakerOpen,
            })
            continue
        }

        keep = append(keep, c)
        nextCandidate:
    }

    return keep, dropped
}
```

## Score

```go
// internal/allocation/score.go
package allocation

import (
    "sort"
)

// scoreCandidates assigns weighted scores and returns ranked.
func scoreCandidates(
    candidates []candidatePre,
    quotes map[string]pricing.Quote,
    reliabilities map[string]float64,
    preferred []core.CarrierID,
    weights ObjectiveWeights,
) []Candidate {
    if len(candidates) == 0 { return nil }

    // Find min/max for normalization
    var minCost, maxCost = pricing.Quote{}.Total, pricing.Quote{}.Total
    var minDays, maxDays int
    for i, c := range candidates {
        q := quotes[csKey(c.CarrierID, c.ServiceType)]
        if i == 0 {
            minCost, maxCost = q.Total, q.Total
            minDays, maxDays = q.EstimatedDeliveryDays, q.EstimatedDeliveryDays
        } else {
            if q.Total < minCost   { minCost = q.Total }
            if q.Total > maxCost   { maxCost = q.Total }
            if q.EstimatedDeliveryDays < minDays { minDays = q.EstimatedDeliveryDays }
            if q.EstimatedDeliveryDays > maxDays { maxDays = q.EstimatedDeliveryDays }
        }
    }

    // Build preference map
    prefMap := make(map[core.CarrierID]float64, len(preferred))
    for i, c := range preferred {
        prefMap[c] = 1.0 - float64(i)*0.1  // top preferred = 1.0; second = 0.9; ...
        if prefMap[c] < 0 { prefMap[c] = 0 }
    }

    out := make([]Candidate, 0, len(candidates))
    for _, c := range candidates {
        q := quotes[csKey(c.CarrierID, c.ServiceType)]
        rel := reliabilities[csKey(c.CarrierID, c.ServiceType)]
        if rel == 0 { rel = 0.5 }  // default if no data

        // Normalize: 1.0 = best (lowest cost / fastest); 0.0 = worst.
        var costScore, speedScore float64 = 1.0, 1.0
        if maxCost != minCost {
            costScore = float64(maxCost-q.Total) / float64(maxCost-minCost)
        }
        if maxDays != minDays {
            speedScore = float64(maxDays-q.EstimatedDeliveryDays) / float64(maxDays-minDays)
        }
        prefScore := prefMap[c.CarrierID]

        score := CompositeScore{
            CostScore:        costScore,
            SpeedScore:       speedScore,
            ReliabilityScore: rel,
            PrefScore:        prefScore,
        }
        score.Total = weights.Cost*costScore + weights.Speed*speedScore + weights.Reliability*rel + weights.SellerPref*prefScore

        out = append(out, Candidate{
            CarrierID:        c.CarrierID,
            ServiceType:      c.ServiceType,
            Quote:            q,
            ReliabilityScore: rel,
            Score:            score,
        })
    }

    sort.Slice(out, func(i, j int) bool {
        if out[i].Score.Total != out[j].Score.Total {
            return out[i].Score.Total > out[j].Score.Total
        }
        // Tiebreak: lower cost first
        return out[i].Quote.Total < out[j].Quote.Total
    })

    return out
}

func csKey(c core.CarrierID, s core.ServiceType) string {
    return c.String() + ":" + string(s)
}
```

## Reliability cache + precomputation

```go
// internal/allocation/reliability_cache.go
package allocation

type reliabilityCache struct {
    mu    sync.RWMutex
    data  map[string]float64
    ttl   time.Duration
    clock core.Clock
}

func newReliabilityCache(ttl time.Duration, clock core.Clock) *reliabilityCache {
    return &reliabilityCache{data: make(map[string]float64), ttl: ttl, clock: clock}
}

func (c *reliabilityCache) Get(ctx context.Context, carrierID core.CarrierID, zone string, svc core.ServiceType) (float64, bool) {
    key := fmt.Sprintf("%s|%s|%s", carrierID, zone, svc)
    c.mu.RLock()
    val, ok := c.data[key]
    c.mu.RUnlock()
    return val, ok
}

func (c *reliabilityCache) Put(carrierID core.CarrierID, zone string, svc core.ServiceType, score float64) {
    key := fmt.Sprintf("%s|%s|%s", carrierID, zone, svc)
    c.mu.Lock()
    c.data[key] = score
    c.mu.Unlock()
}
```

```go
// internal/allocation/reliability.go
package allocation

import (
    "context"

    "github.com/riverqueue/river"
)

type ComputeReliabilityArgs struct{}

func (ComputeReliabilityArgs) Kind() string { return "allocation.compute_reliability" }

type ComputeReliabilityWorker struct {
    river.WorkerDefaults[ComputeReliabilityArgs]
    pool *pgxpool.Pool
    log  *slog.Logger
}

// Work computes per-(carrier, zone, service) reliability scores from last
// 30 days of shipments. Inserts into carrier_reliability with new computed_at.
//
// Schedule: daily at 02:30 IST.
func (w *ComputeReliabilityWorker) Work(ctx context.Context, j *river.Job[ComputeReliabilityArgs]) error {
    rows, err := db.New(w.pool).ComputeReliabilityFromShipments(ctx)
    if err != nil { return err }

    var inserted int
    for _, r := range rows {
        composite := 0.4*float64(r.OnTimeRate) + 0.3*(1-float64(r.NDRRate)) + 0.2*(1-float64(r.RTORate)) + 0.1*1.0
        if err := db.New(w.pool).UpsertReliability(ctx, db.UpsertReliabilityParams{
            CarrierID: r.CarrierID,
            PincodeZone: r.PincodeZone,
            ServiceType: r.ServiceType,
            OnTimeRate: r.OnTimeRate,
            NDRRate: r.NDRRate,
            RTORate: r.RTORate,
            CompositeScore: composite,
            SampleSize: r.SampleSize,
        }); err == nil {
            inserted++
        }
    }

    w.log.InfoContext(ctx, "reliability computed", slog.Int("rows", inserted))
    return nil
}
```

## Persistence

```go
func (e *engineImpl) persist(ctx context.Context, d Decision) error {
    return dbtx.WithSellerTx(ctx, e.pool, d.SellerID, func(ctx context.Context, tx pgx.Tx) error {
        candJSON, _ := json.Marshal(d.Candidates)
        filterJSON, _ := json.Marshal(d.FilteredOut)
        weightsJSON, _ := json.Marshal(d.WeightsUsed)

        return e.repo.queriesWith(tx).InsertAllocationDecision(ctx, db.InsertAllocationDecisionParams{
            ID:               uuid.UUID(d.ID),
            SellerID:         uuid.UUID(d.SellerID),
            OrderID:          uuid.UUID(d.OrderID),
            CandidatesJsonb:  candJSON,
            FilteredOutJsonb: filterJSON,
            WeightsUsedJsonb: weightsJSON,
            RecommendedIdx:   int32(d.RecommendedIdx),
            StrategyVersion:  d.StrategyVersion,
        })
    })
}
```

## Tests

```go
func TestAllocate_FiltersByAllowedSet_SLT(t *testing.T) {
    // Seller's allowed set is [delhivery]; universe has [delhivery, bluedart].
    // Verify decision.FilteredOut contains bluedart with ReasonNotInAllowedSet.
}

func TestAllocate_RanksByScore_SLT(t *testing.T) {
    // Setup: 3 carriers; identical reliability; different cost.
    // Verify recommended is the cheapest.
}

func TestAllocate_TieBreaker_LowerCost(t *testing.T) {
    // Two carriers with equal score; verify lower cost wins.
}

func TestAllocate_NoCandidatesReturnsErrNoCandidates_SLT(t *testing.T) {
    // Pincode unserviceable by any carrier.
    // Verify ErrNoCandidates returned and decision is persisted with empty Candidates.
}

func TestAllocateBulk_Parallel_SLT(t *testing.T) {
    // 100 orders bulk; verify all 100 results returned, no decision conflicts.
}

func BenchmarkAllocateSingle(b *testing.B) {
    // ... realistic setup with 8 carriers ...
    for i := 0; i < b.N; i++ {
        _, _ = engine.Allocate(context.Background(), input)
    }
}

func BenchmarkAllocateBulk100(b *testing.B) {
    // 100 orders in bulk
}
```

Targets: `BenchmarkAllocateSingle` < 200 ms/op (P95); `BenchmarkAllocateBulk100` < 30 s.

## Performance

- `Allocate` (8 carriers): policy resolve (~1ms cached) + serviceability lookup (~5ms) + filter (~1ms) + QuoteAll (~50ms parallel) + score+sort (~1ms) + persist (~5ms) = ~70ms typical, ~200ms P95.
- Reliability lookup: in-memory cache hit ~O(1); miss ~5ms.

## Failure modes

| Failure | Behavior |
|---|---|
| All carriers filter out | Returns `ErrNoCandidates`; decision persisted with empty Candidates and full filter explanation |
| Pricing fails for one carrier | That carrier is filtered out (`ReasonPricingFailed`); others continue |
| Carrier circuit broken | Filtered with `ReasonCircuitBreakerOpen`; allocation continues with healthy carriers |
| Reliability data missing | Default to 0.5 (Bayesian neutral); flagged in decision audit |
| Bulk allocation: half succeed, half fail | Caller sees per-order results; partial success acceptable |

## Open questions

- **Per-zone weight overrides** (e.g., reliability matters more in tier-3): not v0; static per-seller weights.
- **ML-driven scoring** to predict outcomes (not just historical aggregates): v2.
- **Multi-shipment optimization** (split a multi-pkg order across carriers): v2+.
- **Real-time carrier API for live capacity**: v3+.

## References

- HLD `03-services/03-allocation-engine.md`.
- LLD `03-services/06-pricing.md` (consumed via QuoteAll).
- LLD `03-services/01-policy-engine.md` (weights, allowed/excluded sets).
