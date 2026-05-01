# Service: Pricing engine

> **Module:** `internal/pricing/`
> **Maps to PRD:** [`PRD/04-features/07-rate-engine.md`](../../../PRD/04-features/07-rate-engine.md)
>
> Computes the price for a (carrier × service × order × seller). Subsystem of allocation. Versioned, configurable, simulator-backed.

## Responsibility

Given an order and a candidate carrier/service, return a `RateQuote`:
- Base + adjustments + GST = total.
- Estimated transit days.
- Breakdown for explainability.

Plus: maintain rate cards (CRUD); run the simulator.

## Public interface

```go
package pricing

type Engine interface {
    Quote(ctx context.Context, order core.Order, carrierID core.CarrierID, serviceType core.ServiceType) (Quote, error)
    QuoteAll(ctx context.Context, order core.Order) ([]Quote, error)  // for allocation hot path

    PublishCard(ctx context.Context, card RateCard, publishedBy core.UserID) (RateCardID, error)
    GetActiveCard(ctx context.Context, scope CardScope, scopeRef string, carrierID core.CarrierID, serviceType core.ServiceType, at time.Time) (RateCard, error)
    Simulate(ctx context.Context, draftCard RateCard, periodStart, periodEnd time.Time) (SimulationResult, error)
}

type Quote struct {
    CarrierID, ServiceType
    Inputs    QuoteInputs
    Breakdown QuoteBreakdown
    Total     core.Paise
    EstimatedDeliveryDays int
    RateCardID, RateCardVersion
    ComputedAt, ExpiresAt
}
```

## Data model

```sql
CREATE TABLE rate_card (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope           TEXT NOT NULL,             -- 'pikshipp' | 'seller_type:<n>' | 'seller'
  scope_ref       TEXT,                      -- seller_id or seller_type name; NULL for pikshipp
  carrier_id      UUID NOT NULL,
  service_type    TEXT NOT NULL,
  version         INT NOT NULL,
  parent_card_id  UUID,                      -- for delta cards over a base
  effective_from  TIMESTAMPTZ NOT NULL,
  effective_to    TIMESTAMPTZ,
  status          TEXT NOT NULL,             -- 'draft' | 'published' | 'archived'
  published_at    TIMESTAMPTZ,
  published_by    UUID,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (scope, scope_ref, carrier_id, service_type, version)
);

CREATE TABLE rate_card_zone (
  id            UUID PRIMARY KEY,
  rate_card_id  UUID NOT NULL REFERENCES rate_card(id),
  zone_code     TEXT NOT NULL,        -- 'metro_metro', 'regional', 'rest_of_india', 'special_ne', ...
  pincode_pattern_jsonb JSONB NOT NULL,  -- list of patterns: ["110*", "400070"]
  estimated_days INT NOT NULL
);

CREATE TABLE rate_card_slab (
  id              UUID PRIMARY KEY,
  rate_card_id    UUID NOT NULL REFERENCES rate_card(id),
  zone_code       TEXT NOT NULL,
  weight_min_g    INT NOT NULL,
  weight_max_g    INT NOT NULL,
  payment_mode    TEXT NOT NULL,         -- 'prepaid' | 'cod'
  base_first_slab BIGINT NOT NULL,        -- paise
  additional_per_slab BIGINT NOT NULL,    -- paise
  UNIQUE (rate_card_id, zone_code, weight_min_g, payment_mode)
);

CREATE TABLE rate_card_adjustment (
  id              UUID PRIMARY KEY,
  rate_card_id    UUID NOT NULL REFERENCES rate_card(id),
  kind            TEXT NOT NULL,          -- 'fuel' | 'cod' | 'oda' | 'peak' | 'promo'
  condition_jsonb JSONB,                  -- e.g., {"value_min": 1000, "value_max": 5000} for COD bands
  value_pct       NUMERIC(5,2),           -- nullable
  value_inr_paise BIGINT,                 -- nullable
  effective_from  TIMESTAMPTZ,
  effective_to    TIMESTAMPTZ,
  priority        INT NOT NULL DEFAULT 0  -- ordering within same kind
);
```

The `parent_card_id` allows delta cards (a seller card that overrides only some adjustments while inheriting from a base).

RLS:
- `rate_card` with `scope='seller'` is RLS-scoped (`seller_id` in `scope_ref`).
- `rate_card` with `scope='pikshipp'` or `scope='seller_type:*'` is platform-level; readable by all sellers (no RLS).

## Resolution & computation

```
Quote(order, carrier, service):
  1. Resolve seller's rate_card_ref via policy engine (key: pricing.rate_card_ref).
  2. Load RateCard + zones + slabs + adjustments.
  3. Compute zone(pickup, ship_to, card.zones).
  4. Compute chargeable_weight = max(declared, volumetric).
  5. Look up slab(zone, chargeable_weight, payment_mode).
  6. Apply adjustments in priority order.
  7. Compute GST (18% on logistics, 12% on insurance).
  8. Return Quote.
```

All in-process. Sub-50ms target. Caches:
- Active card by `(scope, scope_ref, carrier, service, at_time)` — 5min TTL, NOTIFY-invalidated on publish.
- Card details (zones + slabs + adjustments) — 5min TTL.

## Versioning & publishing

States:
1. **Draft**: editable, can run simulator on it.
2. **Published**: immutable; new edits create a new draft with `parent_card_id` set.
3. **Archived**: superseded by a newer version with later `effective_from`.

Publishing a card:
- Validates zones, slabs, adjustments.
- Sets `status='published'`, `published_at=now()`.
- If a previously-published card with the same `(scope, scope_ref, carrier, service)` exists, it gets `effective_to=now()` and stays published (active-history pattern).
- Emits NOTIFY for cache invalidation.
- Emits audit event.

## Simulator

`Simulate(draftCard, period)`:
1. Load all shipments for the matching scope in `period`.
2. Re-quote each through the draft card.
3. Aggregate: total cost old vs new, breakdown by zone, by adjustment kind, by seller (if scope=pikshipp).

Used in publish workflow: ops sees impact before publishing.

## Quote object lifecycle

A `Quote` is **not persisted by default**. It's a return value.

When a Shipment is booked from a Quote, the Shipment's `rate_quote_jsonb` column captures the full quote (denormalized for audit). The originating `RateCard` is referenced by ID.

If we ever need historical per-quote analysis without a Shipment, we'd add a `rate_quote_log` table — not needed at v0/v1.

## Performance

- Single Quote: P95 < 30ms.
- `QuoteAll` (8 carriers): P95 < 100ms (parallel).
- Bulk: 1000 quotes/sec target via vectorized batch queries.

Microbenchmarks in `bench_test.go`.

## Failure modes

| Failure | Behavior |
|---|---|
| Card load fails | Error to caller; allocation excludes carrier |
| Zone not matched | Default to `rest_of_india` zone if exists; else error |
| Weight exceeds carrier max | Caller (allocation) handles; we don't quote |
| Active card has no slab for the weight | Error; caller handles |

## Integration with allocation

`pricing.QuoteAll` is the hot-path call. Allocation calls it with the order and a list of candidate carriers; pricing returns one Quote per (carrier, service). Allocation scores them.

## Test coverage

- **Unit**: rate computation, zone matching, adjustment ordering, volumetric weight, edge cases.
- **SLT**: publish a card; quote against it; verify quote correctness; publish v2; verify old quotes unaffected, new quotes use v2.
- **Bench**: single quote, batch quote, zone resolver.

## Observability

- Counter: quotes per (carrier, service, zone).
- Histogram: quote latency.
- Counter: cache hit rate on card lookup.
- Log: quote breakdowns at debug level.

## Open questions

- **Q-PR-1.** Live carrier API fallback for special-handling cases? Deferred; static cards at v0/v1.
- **Q-PR-2.** A/B pricing experiments? Deferred; v3+.
- **Q-PR-3.** Per-seller promo credits with usage-bound expiry: tracked in `rate_card_adjustment` with `condition_jsonb`. Need a separate `promo_credit_usage` ledger? Yes — flagged for LLD.
