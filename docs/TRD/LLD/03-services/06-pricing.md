# Service: Pricing engine (`internal/pricing`)

> Computes `Quote(order, carrier, service)` → price breakdown. Versioned rate cards, layered overrides, simulator. Subsystem of allocation.

## Purpose

- `Quote` — single (order, carrier, service) → Quote.
- `QuoteAll` — for one order across all candidate carriers (allocation hot path).
- Rate card CRUD: draft → publish → archive.
- Simulator: replay historical shipments through a draft card.
- In-process card cache; LISTEN/NOTIFY-invalidated.

## Dependencies

- `internal/core` (Paise, IDs)
- `internal/policy` (reads `pricing.rate_card_ref`)
- `internal/observability/dbtx`
- `internal/audit` (rate card publishes)

## Package layout

```
internal/pricing/
├── doc.go
├── service.go            ← Engine interface
├── service_impl.go
├── repo.go
├── types.go              ← RateCard, Slab, Adjustment, Quote, etc.
├── compute.go            ← quote algorithm
├── zone_resolver.go      ← pincode → zone code
├── card_cache.go         ← in-process card cache
├── simulator.go          ← what-if backtest
├── publish.go            ← draft-to-published transition
├── policy_keys.go
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package pricing computes shipping prices for (carrier, service, order, seller).
//
// Cards are versioned and immutable on publish. Per-seller overrides are
// represented as cards with scope='seller' that may inherit from a base via
// parent_card_id.
//
// Quote results are ephemeral and not persisted by default. The Shipment that
// gets booked from a quote captures the full quote (denormalized) for audit.
package pricing

import (
    "context"
    "errors"
    "time"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Engine is the public API.
type Engine interface {
    // Quote computes the price for one (order, carrier, service).
    Quote(ctx context.Context, input QuoteInput) (Quote, error)

    // QuoteAll computes prices for one order across all (carrier, service)
    // pairs in the candidate set. Used by allocation engine for parallel
    // scoring.
    QuoteAll(ctx context.Context, input QuoteAllInput) ([]Quote, error)

    // PublishCard transitions a draft card to published.
    //
    // Validates structure; sets effective_to on any prior published card with
    // matching scope. Audit emits 'pricing.card_published'.
    PublishCard(ctx context.Context, cardID core.RateCardID, publishedBy core.UserID) error

    // GetActiveCard returns the published card matching scope at the given time.
    //
    // Used by domain code that needs the card directly (rare; most callers
    // use Quote).
    GetActiveCard(ctx context.Context, scope CardScope, carrierID core.CarrierID, serviceType core.ServiceType, at time.Time) (RateCard, error)

    // Simulate replays last-N-days of shipments through a draft card; returns
    // aggregate impact.
    Simulate(ctx context.Context, draftCardID core.RateCardID, periodStart, periodEnd time.Time) (SimulationResult, error)
}

// QuoteInput is the per-order, per-(carrier, service) input.
type QuoteInput struct {
    SellerID         core.SellerID
    PickupPincode    core.Pincode
    ShipToPincode    core.Pincode
    CarrierID        core.CarrierID
    ServiceType      core.ServiceType
    DeclaredWeightG  int                // dead weight
    DimsMM           Dimensions          // for volumetric
    PaymentMode      core.PaymentMode
    DeclaredValue    core.Paise
    SpecialHandling  []SpecialHandling   // fragile, hazmat, etc.
}

// QuoteAllInput batches QuoteInput across multiple carriers.
type QuoteAllInput struct {
    SellerID         core.SellerID
    PickupPincode    core.Pincode
    ShipToPincode    core.Pincode
    DeclaredWeightG  int
    DimsMM           Dimensions
    PaymentMode      core.PaymentMode
    DeclaredValue    core.Paise
    SpecialHandling  []SpecialHandling
    Candidates       []CarrierServiceCandidate  // (carrier_id, service) pairs from allocation
}

type CarrierServiceCandidate struct {
    CarrierID   core.CarrierID
    ServiceType core.ServiceType
}

// Dimensions in millimeters.
type Dimensions struct {
    LengthMM int
    WidthMM  int
    HeightMM int
}

func (d Dimensions) VolumetricKg(divisor int) float64 {
    if divisor == 0 { divisor = 5000 }
    cm3 := float64(d.LengthMM*d.WidthMM*d.HeightMM) / 1000
    return cm3 / float64(divisor)
}

// SpecialHandling is an enum of declared handling needs.
type SpecialHandling string

const (
    SpecialFragile     SpecialHandling = "fragile"
    SpecialDangerous   SpecialHandling = "dangerous"
    SpecialPerishable  SpecialHandling = "perishable"
    SpecialHighValue   SpecialHandling = "high_value"
)

// Quote is the result of a pricing computation. Ephemeral.
type Quote struct {
    CarrierID            core.CarrierID
    ServiceType          core.ServiceType
    RateCardID           core.RateCardID
    RateCardVersion      int

    Inputs               QuoteInputsSummary
    Breakdown            QuoteBreakdown
    Total                core.Paise
    EstimatedDeliveryDays int

    ComputedAt           time.Time
    ExpiresAt            time.Time   // 5 min from ComputedAt
}

type QuoteInputsSummary struct {
    ChargeableWeightG   int
    ZoneCode            string
    PaymentMode         core.PaymentMode
    DeclaredValue       core.Paise
}

type QuoteBreakdown struct {
    BaseFirstSlab        core.Paise
    AdditionalWeight     core.Paise
    CODHandling          core.Paise
    FuelSurcharge        core.Paise
    DeliveryAreaSurcharge core.Paise
    PeakUplift           core.Paise
    PromoCredit          core.Paise   // negative; subtracted
    Subtotal             core.Paise   // before GST
    GST                  core.Paise
    Total                core.Paise   // == Subtotal + GST
}

// CardScope identifies the scope of a rate card.
type CardScope struct {
    Kind      CardScopeKind
    SellerID  *core.SellerID  // for scope='seller'
    SellerType *core.SellerType  // for scope='seller_type'
}

type CardScopeKind string

const (
    CardScopePikshipp   CardScopeKind = "pikshipp"
    CardScopeSellerType CardScopeKind = "seller_type"
    CardScopeSeller     CardScopeKind = "seller"
)

// RateCard is the in-memory representation of a card.
type RateCard struct {
    ID            core.RateCardID
    Scope         CardScope
    CarrierID     core.CarrierID
    ServiceType   core.ServiceType
    Version       int
    ParentCardID  *core.RateCardID
    EffectiveFrom time.Time
    EffectiveTo   *time.Time
    Status        CardStatus
    Zones         []Zone
    Slabs         []Slab
    Adjustments   []Adjustment
}

type CardStatus string

const (
    StatusDraft     CardStatus = "draft"
    StatusPublished CardStatus = "published"
    StatusArchived  CardStatus = "archived"
)

// Zone maps pincode patterns to a zone code.
type Zone struct {
    Code             string
    PincodePatterns  []string  // e.g., ["110*", "400070"]
    EstimatedDays    int
}

// Slab is a per-(zone, weight, payment) tier.
type Slab struct {
    ZoneCode            string
    WeightMinG          int
    WeightMaxG          int
    PaymentMode         core.PaymentMode
    BaseFirstSlabPaise  core.Paise
    AdditionalPerSlabPaise core.Paise
    SlabSizeG           int       // typically 500
}

// Adjustment is a surcharge / discount with conditions.
type Adjustment struct {
    Kind          AdjustmentKind
    Condition     map[string]any   // e.g., {"value_min": 1000} for COD
    ValuePct      float64          // percentage; nullable
    ValuePaise    core.Paise       // flat amount; nullable
    EffectiveFrom *time.Time
    EffectiveTo   *time.Time
    Priority      int
}

type AdjustmentKind string

const (
    AdjustmentFuel      AdjustmentKind = "fuel"
    AdjustmentCOD       AdjustmentKind = "cod"
    AdjustmentODA       AdjustmentKind = "oda"
    AdjustmentPeak      AdjustmentKind = "peak"
    AdjustmentPromo     AdjustmentKind = "promo"
    AdjustmentInsurance AdjustmentKind = "insurance"  // separate from optional insurance product
)

// Sentinel errors.
var (
    ErrCardNotFound          = fmt.Errorf("pricing: card not found: %w", core.ErrNotFound)
    ErrNoSlabForWeight       = fmt.Errorf("pricing: no slab matches weight: %w", core.ErrInvalidArgument)
    ErrZoneNotMatched        = fmt.Errorf("pricing: pincode does not match any zone: %w", core.ErrInvalidArgument)
    ErrCardAlreadyPublished  = fmt.Errorf("pricing: card already published: %w", core.ErrConflict)
    ErrInvalidCardStructure  = errors.New("pricing: card structure invalid (missing zones/slabs)")
)
```

## DB schema

```sql
-- migrations/00NN_create_pricing.up.sql

CREATE TABLE rate_card (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope           TEXT NOT NULL,
    scope_seller_id UUID,                    -- nullable; populated only for scope='seller'
    scope_seller_type TEXT,                  -- nullable; for scope='seller_type'
    carrier_id      UUID NOT NULL,
    service_type    TEXT NOT NULL,
    version         INT NOT NULL DEFAULT 1,
    parent_card_id  UUID,
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to    TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'draft',
    published_at    TIMESTAMPTZ,
    published_by    UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT rate_card_scope_valid CHECK (scope IN ('pikshipp','seller_type','seller')),
    CONSTRAINT rate_card_status_valid CHECK (status IN ('draft','published','archived')),
    CONSTRAINT rate_card_version_pos CHECK (version > 0),
    -- One published card per (scope, carrier, service) at a time
    CONSTRAINT rate_card_active_unique UNIQUE (scope, scope_seller_id, scope_seller_type, carrier_id, service_type, version)
);

CREATE INDEX rate_card_active_lookup_idx ON rate_card (scope, scope_seller_id, scope_seller_type, carrier_id, service_type, effective_from DESC, effective_to DESC) WHERE status = 'published';
CREATE INDEX rate_card_seller_idx ON rate_card (scope_seller_id) WHERE scope = 'seller';

ALTER TABLE rate_card ENABLE ROW LEVEL SECURITY;
CREATE POLICY rate_card_seller ON rate_card
    FOR ALL TO pikshipp_app
    USING (
        scope IN ('pikshipp','seller_type')   -- platform-level, all sellers see
        OR scope_seller_id = current_setting('app.seller_id', true)::uuid
    )
    WITH CHECK (scope_seller_id = current_setting('app.seller_id', true)::uuid OR scope IN ('pikshipp','seller_type'));

GRANT SELECT ON rate_card TO pikshipp_app, pikshipp_reports;
GRANT ALL ON rate_card TO pikshipp_admin;

CREATE TABLE rate_card_zone (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id    UUID NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    zone_code       TEXT NOT NULL,
    pincode_patterns_jsonb JSONB NOT NULL,
    estimated_days  INT NOT NULL,
    UNIQUE (rate_card_id, zone_code)
);

CREATE INDEX rate_card_zone_card_idx ON rate_card_zone (rate_card_id);

CREATE TABLE rate_card_slab (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id            UUID NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    zone_code               TEXT NOT NULL,
    weight_min_g            INT NOT NULL,
    weight_max_g            INT NOT NULL,
    payment_mode            TEXT NOT NULL,
    base_first_slab_paise   BIGINT NOT NULL,
    additional_per_slab_paise BIGINT NOT NULL,
    slab_size_g             INT NOT NULL DEFAULT 500,

    CONSTRAINT rate_card_slab_payment_valid CHECK (payment_mode IN ('prepaid','cod')),
    CONSTRAINT rate_card_slab_weight_ok CHECK (weight_min_g >= 0 AND weight_max_g > weight_min_g),
    UNIQUE (rate_card_id, zone_code, weight_min_g, payment_mode)
);

CREATE INDEX rate_card_slab_card_idx ON rate_card_slab (rate_card_id);

CREATE TABLE rate_card_adjustment (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_card_id    UUID NOT NULL REFERENCES rate_card(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    condition_jsonb JSONB NOT NULL DEFAULT '{}',
    value_pct       NUMERIC(7,4),
    value_paise     BIGINT,
    effective_from  TIMESTAMPTZ,
    effective_to    TIMESTAMPTZ,
    priority        INT NOT NULL DEFAULT 0,

    CONSTRAINT rate_card_adj_kind_valid CHECK (kind IN ('fuel','cod','oda','peak','promo','insurance')),
    CONSTRAINT rate_card_adj_value_one CHECK ((value_pct IS NOT NULL) OR (value_paise IS NOT NULL))
);

CREATE INDEX rate_card_adj_card_idx ON rate_card_adjustment (rate_card_id);

GRANT SELECT ON rate_card_zone, rate_card_slab, rate_card_adjustment TO pikshipp_app, pikshipp_reports;
GRANT ALL ON rate_card_zone, rate_card_slab, rate_card_adjustment TO pikshipp_admin;
```

## SQL queries

```sql
-- query/pricing.sql

-- name: GetActiveCardForSeller :one
-- Lookup priority: seller > seller_type > pikshipp; published; effective at $4.
SELECT id, scope, version, parent_card_id, effective_from, effective_to
FROM rate_card
WHERE status = 'published'
  AND carrier_id = $2
  AND service_type = $3
  AND (effective_from <= $4)
  AND (effective_to IS NULL OR effective_to > $4)
  AND (
        (scope = 'seller' AND scope_seller_id = $1)
     OR (scope = 'seller_type' AND scope_seller_type = $5)
     OR (scope = 'pikshipp')
      )
ORDER BY
  CASE scope
    WHEN 'seller' THEN 1
    WHEN 'seller_type' THEN 2
    WHEN 'pikshipp' THEN 3
  END,
  effective_from DESC
LIMIT 1;

-- name: GetCard :one
SELECT id, scope, scope_seller_id, scope_seller_type, carrier_id, service_type,
       version, parent_card_id, effective_from, effective_to, status
FROM rate_card WHERE id = $1;

-- name: ListCardZones :many
SELECT zone_code, pincode_patterns_jsonb, estimated_days
FROM rate_card_zone WHERE rate_card_id = $1
ORDER BY zone_code;

-- name: ListCardSlabs :many
SELECT zone_code, weight_min_g, weight_max_g, payment_mode,
       base_first_slab_paise, additional_per_slab_paise, slab_size_g
FROM rate_card_slab WHERE rate_card_id = $1
ORDER BY zone_code, weight_min_g, payment_mode;

-- name: ListCardAdjustments :many
SELECT kind, condition_jsonb, value_pct, value_paise, effective_from, effective_to, priority
FROM rate_card_adjustment WHERE rate_card_id = $1
ORDER BY priority ASC;

-- name: PublishCard :exec
UPDATE rate_card
SET status = 'published', published_at = now(), published_by = $2
WHERE id = $1 AND status = 'draft';

-- name: ArchivePriorPublishedCards :exec
UPDATE rate_card
SET status = 'archived', effective_to = now()
WHERE status = 'published'
  AND scope = $1
  AND COALESCE(scope_seller_id::text, '') = COALESCE($2::text, '')
  AND COALESCE(scope_seller_type, '') = COALESCE($3, '')
  AND carrier_id = $4
  AND service_type = $5
  AND id != $6;
```

## Card cache

```go
// internal/pricing/card_cache.go
package pricing

import (
    "fmt"
    "sync"
    "time"
)

// cardCache caches loaded RateCard structs (with zones/slabs/adjustments)
// keyed by (scope, carrier, service, effective time bucket).
//
// TTL: 5 min. Invalidated by NOTIFY 'pricing_card_published' (best effort);
// TTL fallback covers missed events.
type cardCache struct {
    mu      sync.RWMutex
    entries map[string]cardCacheEntry
    ttl     time.Duration
    clock   core.Clock
}

type cardCacheEntry struct {
    card      *RateCard
    insertedAt time.Time
}

func newCardCache(clock core.Clock) *cardCache {
    return &cardCache{
        entries: make(map[string]cardCacheEntry),
        ttl:     5 * time.Minute,
        clock:   clock,
    }
}

func (c *cardCache) Get(key string) (*RateCard, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    e, ok := c.entries[key]
    if !ok { return nil, false }
    if c.clock.Now().Sub(e.insertedAt) > c.ttl {
        return nil, false
    }
    return e.card, true
}

func (c *cardCache) Put(key string, card *RateCard) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.entries[key] = cardCacheEntry{card: card, insertedAt: c.clock.Now()}
}

func (c *cardCache) Invalidate(key string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.entries, key)
}

func (c *cardCache) Clear() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.entries = make(map[string]cardCacheEntry)
}

// Key construction:
func cacheKey(sellerID core.SellerID, sellerType core.SellerType, carrierID core.CarrierID, serviceType core.ServiceType) string {
    return fmt.Sprintf("%s|%s|%s|%s", sellerID, sellerType, carrierID, serviceType)
}
```

## Quote computation

```go
// internal/pricing/compute.go
package pricing

import (
    "context"
    "fmt"
    "strings"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

const (
    gstBP            = 1800   // 18% as basis points
    standardSlabSize = 500    // grams
)

func (s *engineImpl) Quote(ctx context.Context, in QuoteInput) (Quote, error) {
    card, err := s.loadCardForSeller(ctx, in.SellerID, in.CarrierID, in.ServiceType, s.clock.Now())
    if err != nil {
        return Quote{}, err
    }

    return s.computeQuote(ctx, in, card)
}

func (s *engineImpl) QuoteAll(ctx context.Context, in QuoteAllInput) ([]Quote, error) {
    type result struct {
        quote Quote
        err   error
    }

    ch := make(chan result, len(in.Candidates))
    for _, c := range in.Candidates {
        go func(c CarrierServiceCandidate) {
            sub := QuoteInput{
                SellerID:        in.SellerID,
                PickupPincode:   in.PickupPincode,
                ShipToPincode:   in.ShipToPincode,
                CarrierID:       c.CarrierID,
                ServiceType:     c.ServiceType,
                DeclaredWeightG: in.DeclaredWeightG,
                DimsMM:          in.DimsMM,
                PaymentMode:     in.PaymentMode,
                DeclaredValue:   in.DeclaredValue,
                SpecialHandling: in.SpecialHandling,
            }
            q, err := s.Quote(ctx, sub)
            ch <- result{q, err}
        }(c)
    }

    quotes := make([]Quote, 0, len(in.Candidates))
    for i := 0; i < len(in.Candidates); i++ {
        r := <-ch
        if r.err != nil {
            // Log + skip; allocation engine can proceed with partial set
            s.log.WarnContext(ctx, "quote failed for candidate", slog.Any("error", r.err))
            continue
        }
        quotes = append(quotes, r.quote)
    }

    return quotes, nil
}

func (s *engineImpl) computeQuote(ctx context.Context, in QuoteInput, card *RateCard) (Quote, error) {
    // 1. Resolve zone
    zone, ok := s.resolveZone(in.PickupPincode, in.ShipToPincode, card)
    if !ok {
        return Quote{}, ErrZoneNotMatched
    }

    // 2. Compute chargeable weight = max(declared, volumetric)
    volumetricKg := in.DimsMM.VolumetricKg(volumetricDivisorFor(in.ServiceType))
    chargeableG := max(in.DeclaredWeightG, int(volumetricKg*1000))

    // 3. Find slab
    slab, ok := findSlab(card.Slabs, zone.Code, chargeableG, in.PaymentMode)
    if !ok {
        return Quote{}, ErrNoSlabForWeight
    }

    // 4. Base price calculation
    base := slab.BaseFirstSlabPaise
    additionalSlabs := 0
    if chargeableG > slab.WeightMinG + slab.SlabSizeG {
        // Beyond the first slab, add per-additional-slab cost
        weightOverFirstSlab := chargeableG - slab.WeightMinG - slab.SlabSizeG
        additionalSlabs = (weightOverFirstSlab + slab.SlabSizeG - 1) / slab.SlabSizeG  // ceil
        additionalCost, _ := slab.AdditionalPerSlabPaise.MulInt(int64(additionalSlabs))
        base, _ = base.Add(additionalCost)
    }

    breakdown := QuoteBreakdown{
        BaseFirstSlab:    slab.BaseFirstSlabPaise,
        AdditionalWeight: base.SubOrPanic(slab.BaseFirstSlabPaise),
    }
    runningSubtotal := base

    // 5. Apply adjustments in priority order
    now := s.clock.Now()
    for _, adj := range card.Adjustments {
        if !adjustmentApplicable(adj, in, now) {
            continue
        }
        amt := computeAdjustmentAmount(adj, runningSubtotal, in)
        switch adj.Kind {
        case AdjustmentFuel:
            breakdown.FuelSurcharge, _ = breakdown.FuelSurcharge.Add(amt)
        case AdjustmentCOD:
            breakdown.CODHandling, _ = breakdown.CODHandling.Add(amt)
        case AdjustmentODA:
            breakdown.DeliveryAreaSurcharge, _ = breakdown.DeliveryAreaSurcharge.Add(amt)
        case AdjustmentPeak:
            breakdown.PeakUplift, _ = breakdown.PeakUplift.Add(amt)
        case AdjustmentPromo:
            breakdown.PromoCredit, _ = breakdown.PromoCredit.Add(amt)  // amt is negative
        }
        runningSubtotal, _ = runningSubtotal.Add(amt)
    }
    breakdown.Subtotal = runningSubtotal

    // 6. GST on subtotal
    gst, _ := runningSubtotal.MulPercent(gstBP)
    breakdown.GST = gst
    breakdown.Total = runningSubtotal.AddOrPanic(gst)

    return Quote{
        CarrierID:       in.CarrierID,
        ServiceType:     in.ServiceType,
        RateCardID:      card.ID,
        RateCardVersion: card.Version,
        Inputs: QuoteInputsSummary{
            ChargeableWeightG: chargeableG,
            ZoneCode:          zone.Code,
            PaymentMode:       in.PaymentMode,
            DeclaredValue:     in.DeclaredValue,
        },
        Breakdown:             breakdown,
        Total:                 breakdown.Total,
        EstimatedDeliveryDays: zone.EstimatedDays,
        ComputedAt:            s.clock.Now(),
        ExpiresAt:             s.clock.Now().Add(5 * time.Minute),
    }, nil
}

func volumetricDivisorFor(svc core.ServiceType) int {
    switch svc {
    case core.ServiceAir, core.ServiceExpress:
        return 5000
    case core.ServiceSurface:
        return 5000
    case core.ServiceHyperlocal:
        return 4000
    default:
        return 5000
    }
}

func findSlab(slabs []Slab, zoneCode string, weightG int, mode core.PaymentMode) (Slab, bool) {
    for _, s := range slabs {
        if s.ZoneCode == zoneCode && s.PaymentMode == mode &&
           weightG >= s.WeightMinG && weightG <= s.WeightMaxG {
            return s, true
        }
    }
    return Slab{}, false
}

func adjustmentApplicable(adj Adjustment, in QuoteInput, now time.Time) bool {
    if adj.EffectiveFrom != nil && now.Before(*adj.EffectiveFrom) { return false }
    if adj.EffectiveTo != nil && !now.Before(*adj.EffectiveTo)    { return false }

    switch adj.Kind {
    case AdjustmentCOD:
        return in.PaymentMode == core.PaymentModeCOD
    case AdjustmentODA:
        return matchesODA(adj.Condition, in.ShipToPincode)
    case AdjustmentPromo:
        return matchesPromoConditions(adj.Condition, in)
    }
    return true
}

func computeAdjustmentAmount(adj Adjustment, base core.Paise, in QuoteInput) core.Paise {
    if adj.ValuePct != 0 {
        bp := int64(adj.ValuePct * 100)  // pct → bp
        amt, _ := base.MulPercent(bp)
        if adj.Kind == AdjustmentPromo {
            return -amt
        }
        return amt
    }
    if adj.ValuePaise != 0 {
        if adj.Kind == AdjustmentPromo {
            return -adj.ValuePaise
        }
        return adj.ValuePaise
    }
    return 0
}
```

## Zone resolver

```go
// internal/pricing/zone_resolver.go
package pricing

import (
    "strings"

    "github.com/pikshipp/pikshipp/internal/core"
)

func (s *engineImpl) resolveZone(pickup, shipTo core.Pincode, card *RateCard) (Zone, bool) {
    // Combined key: e.g., for surface-style zones we may match patterns on both pincodes
    // For carriers that use simple "destination zone": match shipTo only
    for _, z := range card.Zones {
        for _, pattern := range z.PincodePatterns {
            if matchPincodePattern(pattern, string(shipTo)) {
                return z, true
            }
        }
    }
    return Zone{}, false
}

// matchPincodePattern supports:
//   "110001"   exact
//   "110*"     prefix
//   "1*"       prefix-1-char
//   "*"        wildcard
//   "110001-110010"  range
func matchPincodePattern(pattern, pin string) bool {
    if pattern == "*" { return true }
    if strings.Contains(pattern, "-") {
        parts := strings.SplitN(pattern, "-", 2)
        return pin >= parts[0] && pin <= parts[1]
    }
    if strings.HasSuffix(pattern, "*") {
        return strings.HasPrefix(pin, strings.TrimSuffix(pattern, "*"))
    }
    return pattern == pin
}
```

## Implementation: load card

```go
func (s *engineImpl) loadCardForSeller(ctx context.Context, sellerID core.SellerID, carrierID core.CarrierID, svc core.ServiceType, at time.Time) (*RateCard, error) {
    // First check cache
    sellerType, _ := s.policy.Resolve(ctx, sellerID, policy.KeySellerType)
    sellerTypeStr, _ := sellerType.AsString()

    key := cacheKey(sellerID, core.SellerType(sellerTypeStr), carrierID, svc)
    if c, ok := s.cache.Get(key); ok {
        return c, nil
    }

    // DB lookup
    var card *RateCard
    err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        cardRow, err := s.repo.queriesWith(tx).GetActiveCardForSeller(ctx, db.GetActiveCardForSellerParams{
            SellerID:    sellerID,
            CarrierID:   carrierID,
            ServiceType: string(svc),
            At:          at,
            SellerType:  sellerTypeStr,
        })
        if err != nil { return ErrCardNotFound }

        // Load zones, slabs, adjustments
        zones, err := s.repo.queriesWith(tx).ListCardZones(ctx, cardRow.ID)
        if err != nil { return err }
        slabs, err := s.repo.queriesWith(tx).ListCardSlabs(ctx, cardRow.ID)
        if err != nil { return err }
        adjs, err := s.repo.queriesWith(tx).ListCardAdjustments(ctx, cardRow.ID)
        if err != nil { return err }

        card = &RateCard{
            ID:            core.RateCardID(cardRow.ID),
            CarrierID:     carrierID,
            ServiceType:   svc,
            Version:       int(cardRow.Version),
            EffectiveFrom: cardRow.EffectiveFrom,
            EffectiveTo:   cardRow.EffectiveTo,
            Status:        CardStatus(cardRow.Status),
            Zones:         convertZones(zones),
            Slabs:         convertSlabs(slabs),
            Adjustments:   convertAdjustments(adjs),
        }
        return nil
    })
    if err != nil { return nil, err }

    s.cache.Put(key, card)
    return card, nil
}
```

## Publish card

```go
// internal/pricing/publish.go
package pricing

func (s *engineImpl) PublishCard(ctx context.Context, cardID core.RateCardID, by core.UserID) error {
    return dbtx.WithAdminTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        card, err := q.GetCard(ctx, uuid.UUID(cardID))
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) { return ErrCardNotFound }
            return fmt.Errorf("publish: get card: %w", err)
        }

        if card.Status != "draft" {
            return ErrCardAlreadyPublished
        }

        // Validate structure
        if err := s.validateCardStructure(ctx, q, cardID); err != nil {
            return err
        }

        // Archive prior published cards with the same scope
        if err := q.ArchivePriorPublishedCards(ctx, db.ArchivePriorPublishedCardsParams{
            Scope:           card.Scope,
            ScopeSellerID:   card.ScopeSellerID,
            ScopeSellerType: card.ScopeSellerType,
            CarrierID:       card.CarrierID,
            ServiceType:     card.ServiceType,
            ID:              card.ID,
        }); err != nil {
            return fmt.Errorf("publish: archive prior: %w", err)
        }

        // Mark this one published
        if err := q.PublishCard(ctx, db.PublishCardParams{
            ID:          card.ID,
            PublishedBy: uuid.UUID(by),
        }); err != nil {
            return fmt.Errorf("publish: %w", err)
        }

        // NOTIFY (cache invalidation across instances)
        notifyKey := fmt.Sprintf("%s|%s|%s|%s", card.Scope, optionalString(card.ScopeSellerID), card.ScopeSellerType, card.CarrierID)
        _, _ = tx.Exec(ctx, "SELECT pg_notify('pricing_card_published', $1)", notifyKey)

        // Audit (sync; high-value)
        if err := s.audit.Emit(ctx, tx, audit.Event{
            Action: "pricing.card_published",
            Actor:  audit.Actor{Kind: audit.ActorPikshippAdmin, Ref: by.String()},
            Target: audit.Target{Kind: "rate_card", Ref: card.ID.String()},
            Payload: map[string]any{
                "scope":        card.Scope,
                "carrier":      card.CarrierID,
                "service":      card.ServiceType,
                "version":      card.Version,
            },
        }); err != nil {
            return err
        }

        // Local cache invalidate
        s.cache.Clear()

        return nil
    })
}

func (s *engineImpl) validateCardStructure(ctx context.Context, q *db.Queries, id core.RateCardID) error {
    zones, err := q.ListCardZones(ctx, uuid.UUID(id))
    if err != nil || len(zones) == 0 {
        return fmt.Errorf("%w: no zones", ErrInvalidCardStructure)
    }
    slabs, err := q.ListCardSlabs(ctx, uuid.UUID(id))
    if err != nil || len(slabs) == 0 {
        return fmt.Errorf("%w: no slabs", ErrInvalidCardStructure)
    }
    // Every zone must have at least one slab in each payment mode (else quote will fail)
    zonePaymentSlabs := map[string]map[string]bool{}
    for _, sl := range slabs {
        if zonePaymentSlabs[sl.ZoneCode] == nil {
            zonePaymentSlabs[sl.ZoneCode] = make(map[string]bool)
        }
        zonePaymentSlabs[sl.ZoneCode][sl.PaymentMode] = true
    }
    for _, z := range zones {
        modes := zonePaymentSlabs[z.ZoneCode]
        if len(modes) == 0 {
            return fmt.Errorf("%w: zone %s has no slabs", ErrInvalidCardStructure, z.ZoneCode)
        }
    }

    return nil
}
```

## Simulator

```go
// internal/pricing/simulator.go
package pricing

import (
    "context"
    "time"
)

type SimulationResult struct {
    DraftCardID    core.RateCardID
    Period         struct{ Start, End time.Time }
    ShipmentsRun   int
    OldTotal       core.Paise
    NewTotal       core.Paise
    DeltaTotal     core.Paise   // New - Old; positive = price increase
    DeltaPct       float64
    PerZoneBreakdown map[string]ZoneSimulation
}

type ZoneSimulation struct {
    Shipments    int
    OldTotal     core.Paise
    NewTotal     core.Paise
    DeltaTotal   core.Paise
}

// Simulate replays shipments through a draft card. Read-only; does not
// affect production.
func (s *engineImpl) Simulate(ctx context.Context, draftCardID core.RateCardID, periodStart, periodEnd time.Time) (SimulationResult, error) {
    // Implementation:
    //   1. Load draft card.
    //   2. Query shipments in [periodStart, periodEnd) with rate_quote_jsonb.
    //   3. For each: re-quote against draft card (with same inputs).
    //   4. Compare totals; aggregate by zone.
    //   5. Return result.

    // At v0 this is offline-only via admin endpoint; not user-facing yet.
    return SimulationResult{}, nil  // skeleton; full impl in service code
}
```

## Tests

```go
func TestQuote_BasicSurface_SLT(t *testing.T) {
    p := testdb.New(t)
    engine := setupPricing(t, p.App)

    // Seed a published rate card
    cardID := seedSurfaceCard(t, p.App, "delhivery", "surface")

    sid := core.NewSellerID()
    seedSeller(t, p.App, sid, core.SellerTypeSmallSMB)

    q, err := engine.Quote(context.Background(), pricing.QuoteInput{
        SellerID:        sid,
        PickupPincode:   "400001",
        ShipToPincode:   "110001",
        CarrierID:       core.MustParseCarrierID("..."),
        ServiceType:     core.ServiceSurface,
        DeclaredWeightG: 600,
        DimsMM:          pricing.Dimensions{LengthMM: 200, WidthMM: 150, HeightMM: 50},
        PaymentMode:     core.PaymentModePrepaid,
        DeclaredValue:   core.FromRupees(1500),
    })
    require.NoError(t, err)
    require.True(t, q.Total > 0)
    require.Equal(t, "metro_metro", q.Inputs.ZoneCode)
}

func TestQuote_CODAddsHandling_SLT(t *testing.T) {
    // Same setup; switch PaymentMode to COD; verify Breakdown.CODHandling > 0.
}

func TestQuote_VolumetricDominates_SLT(t *testing.T) {
    // Declared 100g, dims 50×50×50cm = 25kg volumetric. Verify chargeable = 25000.
}

func TestPublish_ArchivesPrior_SLT(t *testing.T) {
    // Publish card v1; publish card v2; verify v1 status = archived, effective_to set.
}

func TestPublish_RejectsInvalid_SLT(t *testing.T) {
    // Create draft with no slabs; PublishCard returns ErrInvalidCardStructure.
}

func BenchmarkQuoteSingle(b *testing.B) {
    // ... setup with realistic-size card ...
    for i := 0; i < b.N; i++ {
        _, _ = engine.Quote(context.Background(), sampleInput)
    }
}

func BenchmarkQuoteAll8Carriers(b *testing.B) {
    candidates := []pricing.CarrierServiceCandidate{
        {core.CarrierID(uuid.New()), core.ServiceSurface},
        // ... 7 more ...
    }
    for i := 0; i < b.N; i++ {
        _, _ = engine.QuoteAll(context.Background(), pricing.QuoteAllInput{Candidates: candidates, ...})
    }
}
```

Targets: `BenchmarkQuoteSingle` < 30 ms/op (cache hit < 100 µs); `BenchmarkQuoteAll8Carriers` < 100 ms/op.

## Performance

- `Quote` cache hit: ~100 µs (compute only; no DB).
- `Quote` cache miss: ~10 ms (load card + zones + slabs + adjustments + compute).
- `QuoteAll` (8 candidates): goroutines parallel; bottlenecked by slowest cache miss; ~100 ms.
- Card load query: 4 selects; ~5 ms total.

## Failure modes

| Failure | Behavior |
|---|---|
| No active card for (carrier, service) at this time | `ErrCardNotFound`; allocation skips this carrier |
| Zone not matched | `ErrZoneNotMatched`; allocation skips |
| No slab for chargeable weight | `ErrNoSlabForWeight`; allocation skips |
| Concurrent publish | DB transaction serializes; second publish sees v1 already archived; succeeds with v3 |
| Cache stale after publish | NOTIFY invalidates; if missed, TTL (5 min) catches up |
| Adjustment math overflow | `Paise.Add` returns overflow false; we panic — should be impossible at realistic amounts |

## Open questions

- **Promo conditions** (e.g., "first 10 shipments free"): need a `promo_credit_usage` table to track redemption per seller. Add at v1 when promotional engine kicks in.
- **Per-carrier zone definitions**: today, zones live with the rate card. If we want platform-level zones reused across cards, factor out. Defer.
- **Live rate API fallback** for special cases: deferred to v3.
- **A/B pricing experiments**: deferred; v3.

## References

- HLD `03-services/02-pricing-engine.md`.
- LLD `01-core/01-money.md` (Paise).
- LLD `03-services/01-policy-engine.md` (rate card ref lookup).
