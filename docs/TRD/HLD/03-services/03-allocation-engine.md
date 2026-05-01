# Service: Allocation engine

> **Module:** `internal/allocation/`
> **Maps to PRD:** [`PRD/04-features/25-allocation-engine.md`](../../../PRD/04-features/25-allocation-engine.md)
>
> Picks the carrier and service for every shipment. Auditable, multi-objective, fast.

## Responsibility

Given an order, produce a ranked list of `(carrier, service)` candidates with scores, plus a recommended pick. Persist the decision (always) for audit. Honor seller config (allowed/excluded carriers, weights). Feed allocation back to seller via Shipment record.

## Public interface

```go
package allocation

type Engine interface {
    Allocate(ctx context.Context, order core.Order) (Decision, error)
    AllocateBulk(ctx context.Context, orders []core.Order) ([]Decision, error)
}

type Decision struct {
    OrderID         core.OrderID
    Candidates      []Candidate    // ranked
    FilteredOut     []Filtered     // with reasons
    WeightsUsed     ObjectiveWeights
    StrategyVersion string         // 'v1.0'; bumped when scoring changes
    Recommended     int            // index into Candidates
    DecidedAt       time.Time
}

type Candidate struct {
    CarrierID       core.CarrierID
    ServiceType     core.ServiceType
    Quote           pricing.Quote
    ReliabilityScore float64
    Score           CompositeScore  // breakdown
}
```

## Data model

```sql
CREATE TABLE carrier_serviceability (
  id              UUID PRIMARY KEY,
  carrier_id      UUID NOT NULL REFERENCES carrier(id),
  pincode         TEXT NOT NULL,             -- 6-digit
  service_type    TEXT NOT NULL,
  cod_allowed     BOOLEAN NOT NULL,
  zone_code       TEXT NOT NULL,             -- carrier's own zone classification
  estimated_days  INT,
  source          TEXT NOT NULL,             -- 'api' | 'csv' | 'manual'
  last_updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (carrier_id, pincode, service_type)
);

CREATE INDEX carrier_serviceability_pincode_idx ON carrier_serviceability (pincode, service_type);

CREATE TABLE carrier_reliability (
  id                UUID PRIMARY KEY,
  carrier_id        UUID NOT NULL,
  pincode_zone      TEXT NOT NULL,           -- broader than pincode, e.g., 'mumbai_metro'
  service_type      TEXT NOT NULL,
  on_time_rate      NUMERIC(5,4),
  ndr_rate          NUMERIC(5,4),
  rto_rate          NUMERIC(5,4),
  api_success_rate  NUMERIC(5,4),
  composite_score   NUMERIC(5,4),
  sample_size       INT,
  computed_at       TIMESTAMPTZ NOT NULL,
  UNIQUE (carrier_id, pincode_zone, service_type, computed_at)
);

CREATE INDEX carrier_reliability_lookup_idx
  ON carrier_reliability (carrier_id, pincode_zone, service_type, computed_at DESC);

CREATE TABLE allocation_decision (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id         UUID NOT NULL,
  order_id          UUID NOT NULL,
  shipment_id       UUID,                    -- set when shipment is booked
  candidates_jsonb  JSONB NOT NULL,
  filtered_out_jsonb JSONB NOT NULL,
  weights_used_jsonb JSONB NOT NULL,
  recommended_idx   INT NOT NULL,
  strategy_version  TEXT NOT NULL,
  decided_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Algorithm

```
Allocate(order, sellerCtx):
  1. allowedCarriers = policy.Resolve(seller, "carriers.allowed_set")
  2. excludedCarriers = policy.Resolve(seller, "carriers.excluded_set")
  3. preferredCarriers = policy.Resolve(seller, "carriers.preferred_priority")
  4. weights = policy.Resolve(seller, "allocation.objective_weights")

  5. Filter:
     - serviceability(carrier, ship_to_pincode, service)
     - weight within carrier bounds
     - cod_allowed if order is cod
     - special handling supported
     - carrier not in excluded set
     - carrier health: not circuit-broken
     - cost ceiling (if seller config has one)

  6. quotes = pricing.QuoteAll(order, candidates)  // parallel

  7. For each candidate:
     reliability = lookup carrier_reliability(carrier, zone(ship_to), service)
     scores = {
       cost: normalize_cost_score(quote, all_quotes),
       speed: normalize_speed_score(quote.estimated_days, all_quotes),
       reliability: reliability.composite_score,
       pref: preference_boost(carrier, preferredCarriers),
     }
     total = weights.cost*scores.cost
            + weights.speed*scores.speed
            + weights.reliability*scores.reliability
            + weights.pref*scores.pref

  8. Sort descending by total. Apply tie-break (configurable).
  9. INSERT allocation_decision with full audit.
  10. Return Decision.
```

## Carrier circuit breaker integration

```go
package allocation

func (e *engineImpl) isCircuitOpen(carrierID core.CarrierID) bool {
    state := e.healthCache.Get(carrierID)  // backed by carrier_health_state table; 5s TTL
    return state.Status == "open"
}
```

When circuit is open, carrier is filtered out with reason `circuit_breaker_open`. The decision audit captures this.

## Reliability score precomputation

A daily river job (`allocation.compute_reliability`) at 02:30 IST:
1. Reads last 30 days of shipment outcomes by `(carrier, pincode_zone, service)`.
2. Computes: on_time_rate, ndr_rate, rto_rate, api_success_rate.
3. Computes composite_score = 0.4×on_time + 0.3×(1-ndr) + 0.2×(1-rto) + 0.1×api_success.
4. Inserts new row in `carrier_reliability` with `computed_at = now()`.
5. Old rows retained for 90 days; queries always fetch latest.

For fresh carrier-zone combos (sample_size < 50), use Bayesian prior with carrier-wide stats.

## Reliability cache

In-process cache keyed on `(carrier_id, pincode_zone, service_type)`. TTL 60s. Invalidated by NOTIFY on `carrier_reliability_updated` (fired by the daily job at end).

## Bulk allocation

`AllocateBulk` for the bulk-book endpoint:
- Workers pool (size = number of CPUs or 16, whichever smaller).
- Per-order goroutine; share rate-card cache for the request.
- Aggregate decisions into a single response.

Target: 1000 allocations/sec sustained.

## Persistence

`allocation_decision` is **always written** via outbox (so it's atomic with the booking transaction). Even when the seller manually picks a non-recommended carrier, the decision row captures: ranked recommendations, what the seller chose, the score gap.

## Performance

- Single Allocate (8 carriers): P95 < 200ms.
- Bulk Allocate (200 orders): P95 < 30s.
- Microbenchmarks track over time.

## Failure modes

| Failure | Behavior |
|---|---|
| All carriers filtered out | Return decision with empty candidates; allocator surface to seller "no carrier serves this combination" |
| Reliability data missing for carrier | Use Bayesian prior; flag in decision audit |
| Pricing engine fails for one carrier | Log, exclude that carrier with reason `pricing_error`, continue with others |
| Policy engine slow | Cached values likely available; fail open with last-known weights |
| `carrier_reliability` table empty (cold start) | All scores default to 0.5; flag in audit |

## Integration with shipments

After allocation, the Shipment record stores `allocation_decision_id`. The decision is queryable per shipment for "why this carrier?" UI.

## Test coverage

- **Unit**: filter logic, score computation, tie-breaking.
- **SLT**: end-to-end with PG + mocked carrier serviceability + reliability data; verify decision row written; verify outcome with synthetic carriers (sandbox adapter).
- **Bench**: single allocate, bulk allocate.

## Observability

- Counter: decisions per (seller, carrier, service) (recommended).
- Counter: filter reason distribution.
- Histogram: allocate latency.
- Counter: seller picked non-recommended (with score gap).
- Daily report: top 5 most-picked vs most-recommended carriers.

## Open questions

- **Q-AL-1.** Real-time carrier API for live rates / availability? Deferred to v3.
- **Q-AL-2.** ML-based scoring? v2 enhancement layered on top.
- **Q-AL-3.** Multi-shipment optimization (split a multi-pkg order across carriers)? Not v1.
