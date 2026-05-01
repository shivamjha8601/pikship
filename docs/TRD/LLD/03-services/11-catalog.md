# Catalog Service

## Purpose

The catalog service owns two seller-scoped reference datasets needed to create and ship orders:

1. **Pickup Locations** — physical addresses from which the seller ships. Required by every order. They feed into pricing (origin pincode), allocation (carrier serviceability), and shipment booking (carrier API "from" address).
2. **Products** — SKU catalog with weights, dimensions, HSN codes, and category hints. Optional but heavily used: when sellers reference a known SKU on an order, dimensions auto-populate; the special-handling category drives carrier filters during allocation.

Why both in one service: they're small, write-light, read-heavy reference data; they share patterns (CRUD, soft-delete, in-memory cache, simple search); separating them into two services would just mean two copies of the same skeleton.

Out of scope (owned elsewhere):

- Pickup-location KYC (sellers self-attest at v0; no verification flow yet).
- Product images / rich descriptions (catalog is operational, not merchandising).
- Pricing per SKU — pricing engine lives in pricing service (LLD §03-services/06).
- Inventory levels — Pikshipp does not run inventory.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, validators (pincode, weight). |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/seller` | `LifecycleCache.AssertActive` for write guards. |
| `internal/audit` | `pickup_location.activated/deactivated` and bulk product upserts are audited. |
| `internal/outbox` | `pickup_location.changed` triggers carrier serviceability cache rebuild. |
| `internal/policy` | Limit on max pickup locations per seller (`catalog.max_pickup_locations`). |

## Package Layout

```
internal/catalog/
├── service.go            // PickupService + ProductService interfaces
├── pickup_impl.go        // Pickup locations implementation
├── product_impl.go       // Products implementation
├── repo.go               // sqlc wrappers
├── types.go              // PickupLocation, Product, ProductPatch
├── cache.go              // PickupCache, ProductCache (in-process)
├── errors.go             // Sentinel errors
├── jobs.go               // CSV import worker for products
├── events.go             // Outbox payload schemas
├── pickup_test.go
├── product_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

### PickupService

```go
package catalog

type PickupService interface {
    // Create persists a new pickup location.
    // Idempotent on (seller_id, label) — re-issuing with same label
    // returns the existing record.
    Create(ctx context.Context, req PickupCreateRequest) (*PickupLocation, error)

    // Get returns the pickup location. RLS enforces seller scope.
    Get(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (*PickupLocation, error)

    // GetPickupLocation is the read used by the orders service to
    // validate a pickup location belongs to the seller. Cached.
    GetPickupLocation(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (*PickupLocation, error)

    // Update patches editable fields. The address is replace-not-patch:
    // pass the full new address. Activation status is NOT mutable here;
    // use Activate / Deactivate.
    Update(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, patch PickupPatch) (*PickupLocation, error)

    // List returns all non-deleted pickup locations for a seller, sorted
    // by `is_default DESC, label ASC`.
    List(ctx context.Context, sellerID core.SellerID) ([]*PickupLocation, error)

    // Activate / Deactivate flip the `active` flag. An inactive location
    // cannot be used on new orders but existing in-flight orders are
    // unaffected. Audited; emits outbox `pickup_location.changed`.
    Activate(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error
    Deactivate(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, reason string) error

    // SetDefault marks one location as the seller's default and unsets
    // the previous default in a single transaction.
    SetDefault(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error

    // SoftDelete is permanent for write purposes but preserves the row
    // so historical orders still reference it. After soft-delete the
    // location is hidden from List and rejected by orders.Create.
    SoftDelete(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, reason string) error
}
```

### ProductService

```go
type ProductService interface {
    // Upsert creates or updates a product by (seller_id, sku).
    Upsert(ctx context.Context, req ProductUpsertRequest) (*Product, error)

    // Get returns a product by SKU.
    Get(ctx context.Context, sellerID core.SellerID, sku string) (*Product, error)

    // List with pagination + optional filters.
    List(ctx context.Context, sellerID core.SellerID, q ProductListQuery) ([]*Product, int, error)

    // Delete removes a product. Soft-delete: existing orders that
    // referenced this SKU continue to see the snapshot they captured
    // at order creation time.
    Delete(ctx context.Context, sellerID core.SellerID, sku string) error

    // BulkUpsertCSV ingests a CSV (S3 key) and upserts each row.
    // Returns a job handle; actual processing happens in a river worker.
    BulkUpsertCSV(ctx context.Context, sellerID core.SellerID, req ProductCSVRequest) (*ImportJob, error)
}
```

### Request / Response Types

```go
type PickupCreateRequest struct {
    SellerID         core.SellerID
    Label            string         // unique within seller; e.g. "Mumbai HQ"
    ContactName      string
    ContactPhone     string         // E.164
    ContactEmail     string
    Address          Address
    PickupHours      string         // free text; e.g. "Mon-Sat 9am-6pm"
    Active           bool           // default true
    IsDefault        bool           // default false
    GSTIN            string         // optional; defaults to seller GSTIN if blank
}

type PickupPatch struct {
    Label        *string
    ContactName  *string
    ContactPhone *string
    ContactEmail *string
    Address      *Address
    PickupHours  *string
    GSTIN        *string
}

type ProductUpsertRequest struct {
    SellerID       core.SellerID
    SKU            string         // case-sensitive; unique within seller
    Name           string
    Description    string
    UnitWeightG    int
    LengthMM       int
    WidthMM        int
    HeightMM       int
    HSNCode        string
    CategoryHint   string         // e.g. "fragile" | "battery" | "perishable"
    UnitPricePaise core.Paise     // informational; not used for billing
    Active         bool
}

type ProductListQuery struct {
    Search string  // matches sku ILIKE or name ILIKE
    OnlyActive bool
    Page Page
}

type ProductCSVRequest struct {
    UploadedByUserID core.UserID
    UploadID         string         // S3 key
    DryRun           bool
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("catalog: not found")
    ErrInvalidLabel          = errors.New("catalog: pickup label invalid")
    ErrInvalidAddress        = errors.New("catalog: address invalid")
    ErrLabelDuplicate        = errors.New("catalog: pickup label already exists")
    ErrSKUDuplicate          = errors.New("catalog: SKU already exists")
    ErrInvalidWeight         = errors.New("catalog: weight must be > 0")
    ErrInvalidDimensions     = errors.New("catalog: dimensions must be > 0")
    ErrPickupLocationInactive = errors.New("catalog: pickup location is inactive or deleted")
    ErrPickupLimitExceeded   = errors.New("catalog: max pickup locations exceeded for seller")
    ErrCannotDeactivateLastDefault = errors.New("catalog: cannot deactivate the only default pickup location")
)
```

## DB Schema

```sql
-- Pickup locations
CREATE TABLE pickup_location (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id     uuid        NOT NULL REFERENCES seller(id),
    label         text        NOT NULL,
    contact_name  text        NOT NULL,
    contact_phone text        NOT NULL,
    contact_email text,
    address       jsonb       NOT NULL,
    -- denormalized for index/filter
    pincode       text        NOT NULL,
    state         text        NOT NULL,
    pickup_hours  text,
    gstin         text,

    active        boolean     NOT NULL DEFAULT true,
    is_default    boolean     NOT NULL DEFAULT false,
    deleted_at    timestamptz,

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT pickup_label_min_len CHECK (char_length(label) >= 2),
    CONSTRAINT pickup_pincode_format CHECK (pincode ~ '^[1-9][0-9]{5}$')
);

-- Unique (seller_id, label) only across non-deleted rows so seller can
-- recreate a deleted label.
CREATE UNIQUE INDEX pickup_seller_label_unique
    ON pickup_location (seller_id, label)
    WHERE deleted_at IS NULL;

-- At most one default per seller.
CREATE UNIQUE INDEX pickup_seller_default_unique
    ON pickup_location (seller_id)
    WHERE is_default = true AND deleted_at IS NULL;

CREATE INDEX pickup_seller_active_idx
    ON pickup_location(seller_id, active)
    WHERE deleted_at IS NULL;

CREATE INDEX pickup_pincode_idx
    ON pickup_location(seller_id, pincode);

ALTER TABLE pickup_location ENABLE ROW LEVEL SECURITY;
CREATE POLICY pickup_location_isolation ON pickup_location
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Products
CREATE TABLE product (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       uuid        NOT NULL REFERENCES seller(id),
    sku             text        NOT NULL,
    name            text        NOT NULL,
    description     text,
    unit_weight_g   integer     NOT NULL CHECK (unit_weight_g > 0),
    length_mm       integer     NOT NULL CHECK (length_mm > 0),
    width_mm        integer     NOT NULL CHECK (width_mm > 0),
    height_mm       integer     NOT NULL CHECK (height_mm > 0),
    hsn_code        text,
    category_hint   text,
    unit_price_paise bigint     NOT NULL DEFAULT 0,
    active          boolean     NOT NULL DEFAULT true,
    deleted_at      timestamptz,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT product_sku_min_len CHECK (char_length(sku) >= 1)
);

CREATE UNIQUE INDEX product_seller_sku_unique
    ON product(seller_id, sku)
    WHERE deleted_at IS NULL;

CREATE INDEX product_seller_active_idx
    ON product(seller_id, active)
    WHERE deleted_at IS NULL;

CREATE INDEX product_seller_search_idx
    ON product
    USING gin ((sku || ' ' || name) gin_trgm_ops)
    WHERE deleted_at IS NULL;

ALTER TABLE product ENABLE ROW LEVEL SECURITY;
CREATE POLICY product_isolation ON product
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Grants
GRANT SELECT, INSERT, UPDATE ON pickup_location, product TO pikshipp_app;
GRANT SELECT ON pickup_location, product TO pikshipp_reports;
```

### Why Two Tables Not One

Pickup locations and products differ in churn rate (pickup locations rarely change; products churn weekly), shape (an address vs. SKU dimensions), and semantics (operational endpoint vs. inventory metadata). Sharing a row would force a sparse-column model that is harder to reason about with no benefit.

## sqlc Queries

```sql
-- ─────────── PICKUP LOCATIONS ───────────

-- name: PickupInsert :one
INSERT INTO pickup_location (
    id, seller_id, label, contact_name, contact_phone, contact_email,
    address, pincode, state, pickup_hours, gstin, active, is_default
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: PickupGet :one
SELECT * FROM pickup_location WHERE id = $1 AND deleted_at IS NULL;

-- name: PickupGetByLabel :one
SELECT * FROM pickup_location
WHERE seller_id = $1 AND label = $2 AND deleted_at IS NULL;

-- name: PickupList :many
SELECT * FROM pickup_location
WHERE seller_id = $1 AND deleted_at IS NULL
ORDER BY is_default DESC, label ASC;

-- name: PickupCountActive :one
SELECT count(*) FROM pickup_location
WHERE seller_id = $1 AND deleted_at IS NULL AND active = true;

-- name: PickupUpdate :one
UPDATE pickup_location
SET label         = COALESCE(sqlc.narg('label'),         label),
    contact_name  = COALESCE(sqlc.narg('contact_name'),  contact_name),
    contact_phone = COALESCE(sqlc.narg('contact_phone'), contact_phone),
    contact_email = COALESCE(sqlc.narg('contact_email'), contact_email),
    address       = COALESCE(sqlc.narg('address'),       address),
    pincode       = COALESCE(sqlc.narg('pincode'),       pincode),
    state         = COALESCE(sqlc.narg('state'),         state),
    pickup_hours  = COALESCE(sqlc.narg('pickup_hours'),  pickup_hours),
    gstin         = COALESCE(sqlc.narg('gstin'),         gstin),
    updated_at    = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: PickupSetActive :one
UPDATE pickup_location
SET active = $2, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: PickupClearDefault :exec
UPDATE pickup_location
SET is_default = false, updated_at = now()
WHERE seller_id = $1 AND is_default = true AND deleted_at IS NULL AND id <> $2;

-- name: PickupSetDefault :one
UPDATE pickup_location
SET is_default = true, updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: PickupSoftDelete :exec
UPDATE pickup_location
SET deleted_at = now(), is_default = false, active = false
WHERE id = $1 AND deleted_at IS NULL;

-- ─────────── PRODUCTS ───────────

-- name: ProductUpsert :one
INSERT INTO product (
    id, seller_id, sku, name, description,
    unit_weight_g, length_mm, width_mm, height_mm,
    hsn_code, category_hint, unit_price_paise, active
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13
)
ON CONFLICT (seller_id, sku) WHERE deleted_at IS NULL DO UPDATE
    SET name             = EXCLUDED.name,
        description      = EXCLUDED.description,
        unit_weight_g    = EXCLUDED.unit_weight_g,
        length_mm        = EXCLUDED.length_mm,
        width_mm         = EXCLUDED.width_mm,
        height_mm        = EXCLUDED.height_mm,
        hsn_code         = EXCLUDED.hsn_code,
        category_hint    = EXCLUDED.category_hint,
        unit_price_paise = EXCLUDED.unit_price_paise,
        active           = EXCLUDED.active,
        updated_at       = now()
RETURNING *;

-- name: ProductGet :one
SELECT * FROM product
WHERE seller_id = $1 AND sku = $2 AND deleted_at IS NULL;

-- name: ProductList :many
SELECT * FROM product
WHERE seller_id = $1 AND deleted_at IS NULL
  AND ($2::boolean IS NULL OR active = $2)
  AND ($3::text IS NULL OR (sku || ' ' || name) ILIKE '%' || $3 || '%')
ORDER BY sku
LIMIT $4 OFFSET $5;

-- name: ProductCount :one
SELECT count(*) FROM product
WHERE seller_id = $1 AND deleted_at IS NULL
  AND ($2::boolean IS NULL OR active = $2)
  AND ($3::text IS NULL OR (sku || ' ' || name) ILIKE '%' || $3 || '%');

-- name: ProductSoftDelete :exec
UPDATE product
SET deleted_at = now(), active = false
WHERE seller_id = $1 AND sku = $2 AND deleted_at IS NULL;
```

## Implementation Highlights

### Pickup Create (with policy-enforced cap)

```go
func (s *pickupService) Create(ctx context.Context, req PickupCreateRequest) (*PickupLocation, error) {
    if err := validatePickupCreate(req); err != nil {
        return nil, err
    }
    if err := s.lifecycleCache.AssertActive(ctx, req.SellerID); err != nil {
        return nil, err
    }

    // Policy cap (e.g. 25 active pickup locations for small_business plan)
    cap, _ := s.policy.GetInt(ctx, req.SellerID, "catalog.max_pickup_locations")
    cnt, err := s.q.PickupCountActive(ctx, req.SellerID.UUID())
    if err != nil {
        return nil, err
    }
    if cap > 0 && int(cnt) >= cap {
        return nil, fmt.Errorf("%w: cap=%d", ErrPickupLimitExceeded, cap)
    }

    // Idempotency on label
    if existing, err := s.q.PickupGetByLabel(ctx, sqlcgen.PickupGetByLabelParams{
        SellerID: req.SellerID.UUID(), Label: req.Label,
    }); err == nil {
        return pickupFromRow(existing), nil
    } else if !errors.Is(err, pgx.ErrNoRows) {
        return nil, err
    }

    var out *PickupLocation
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        // If this is the first pickup location, force-default it
        // regardless of req.IsDefault — sellers nearly always want this.
        if cnt == 0 {
            req.IsDefault = true
        }

        // If marking default, clear previous default first.
        id := core.NewPickupLocationID()
        if req.IsDefault {
            if err := qtx.PickupClearDefault(ctx, sqlcgen.PickupClearDefaultParams{
                SellerID: req.SellerID.UUID(),
                ID:       id.UUID(), // exclude this row (which we're about to insert)
            }); err != nil {
                return err
            }
        }

        row, err := qtx.PickupInsert(ctx, sqlcgen.PickupInsertParams{
            ID:           id.UUID(),
            SellerID:     req.SellerID.UUID(),
            Label:        req.Label,
            ContactName:  req.ContactName,
            ContactPhone: req.ContactPhone,
            ContactEmail: pgxNullString(req.ContactEmail),
            Address:      jsonbFromAddress(req.Address),
            Pincode:      req.Address.Pincode,
            State:        req.Address.State,
            PickupHours:  pgxNullString(req.PickupHours),
            GSTIN:        pgxNullString(req.GSTIN),
            Active:       coalesceBool(req.Active, true),
            IsDefault:    req.IsDefault,
        })
        if err != nil {
            var pgErr *pgconn.PgError
            if errors.As(err, &pgErr) && pgErr.ConstraintName == "pickup_seller_label_unique" {
                return ErrLabelDuplicate
            }
            return err
        }
        out = pickupFromRow(row)

        if err := s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "pickup_location.created",
            Object:   audit.ObjPickup(out.ID),
            Payload:  map[string]any{"label": req.Label, "is_default": req.IsDefault},
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:    "pickup_location.changed",
            Key:     string(req.SellerID),
            Payload: map[string]any{"seller_id": req.SellerID, "pickup_location_id": out.ID, "kind": "created"},
        })
    })
    if err != nil {
        return nil, err
    }
    s.cache.Invalidate(req.SellerID)
    return out, nil
}
```

### SetDefault (atomic swap)

```go
func (s *pickupService) SetDefault(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        // 1. Clear current default (if different)
        if err := qtx.PickupClearDefault(ctx, sqlcgen.PickupClearDefaultParams{
            SellerID: sellerID.UUID(), ID: id.UUID(),
        }); err != nil {
            return err
        }
        // 2. Mark new default
        if _, err := qtx.PickupSetDefault(ctx, id.UUID()); err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrNotFound
            }
            return err
        }
        if err := s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: sellerID,
            Action:   "pickup_location.default.changed",
            Object:   audit.ObjPickup(id),
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "pickup_location.changed",
            Key:  string(sellerID),
            Payload: map[string]any{"seller_id": sellerID, "pickup_location_id": id, "kind": "default_changed"},
        })
    })
}
```

The unique partial index on `(seller_id) WHERE is_default=true AND deleted_at IS NULL` is what makes this safe even under concurrent SetDefault calls — the second tx blocks on the index lock until the first commits or rolls back, so we never end up with two defaults.

### Product Upsert

```go
func (s *productService) Upsert(ctx context.Context, req ProductUpsertRequest) (*Product, error) {
    if err := validateProductUpsert(req); err != nil {
        return nil, err
    }
    var out *Product
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ProductUpsert(ctx, sqlcgen.ProductUpsertParams{
            ID:             core.NewProductID().UUID(),
            SellerID:       req.SellerID.UUID(),
            SKU:            req.SKU,
            Name:           req.Name,
            Description:    pgxNullString(req.Description),
            UnitWeightG:    int32(req.UnitWeightG),
            LengthMM:       int32(req.LengthMM),
            WidthMM:        int32(req.WidthMM),
            HeightMM:       int32(req.HeightMM),
            HSNCode:        pgxNullString(req.HSNCode),
            CategoryHint:   pgxNullString(req.CategoryHint),
            UnitPricePaise: int64(req.UnitPricePaise),
            Active:         coalesceBool(req.Active, true),
        })
        if err != nil {
            return err
        }
        out = productFromRow(row)
        return nil
    })
    if err != nil {
        return nil, err
    }
    s.productCache.Invalidate(req.SellerID, req.SKU)
    return out, nil
}
```

### Validation

```go
func validatePickupCreate(r PickupCreateRequest) error {
    if r.SellerID.IsZero() {
        return fmt.Errorf("%w: seller_id required", ErrInvalidLabel)
    }
    if len(r.Label) < 2 || len(r.Label) > 64 {
        return fmt.Errorf("%w: label length must be 2..64", ErrInvalidLabel)
    }
    if !core.IsValidE164(r.ContactPhone) {
        return fmt.Errorf("%w: contact_phone must be E.164", ErrInvalidLabel)
    }
    if r.ContactEmail != "" && !core.IsValidEmail(r.ContactEmail) {
        return fmt.Errorf("%w: contact_email invalid", ErrInvalidLabel)
    }
    return validateAddressForPickup(r.Address)
}

func validateProductUpsert(r ProductUpsertRequest) error {
    if r.SellerID.IsZero() {
        return fmt.Errorf("%w: seller_id required", ErrInvalidWeight)
    }
    if r.SKU == "" || len(r.SKU) > 64 {
        return fmt.Errorf("%w: sku length must be 1..64", ErrSKUDuplicate)
    }
    if r.UnitWeightG <= 0 {
        return ErrInvalidWeight
    }
    if r.LengthMM <= 0 || r.WidthMM <= 0 || r.HeightMM <= 0 {
        return ErrInvalidDimensions
    }
    return nil
}
```

## Caching

Pickup-location reads are extremely hot: every single order create and every allocation pass loads them. We back them with a per-seller in-memory cache.

```go
package catalog

type PickupCache struct {
    mu      sync.RWMutex
    entries map[core.SellerID]pickupCacheEntry
    ttl     time.Duration
    repo    *repo
}

type pickupCacheEntry struct {
    locations  map[core.PickupLocationID]*PickupLocation
    fetchedAt  time.Time
}

func NewPickupCache(repo *repo) *PickupCache {
    return &PickupCache{
        entries: make(map[core.SellerID]pickupCacheEntry),
        ttl:     60 * time.Second,
        repo:    repo,
    }
}

func (c *PickupCache) Get(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (*PickupLocation, error) {
    c.mu.RLock()
    entry, ok := c.entries[sellerID]
    c.mu.RUnlock()
    if ok && time.Since(entry.fetchedAt) < c.ttl {
        if pl, ok := entry.locations[id]; ok {
            return pl, nil
        }
        // Cache miss within a fresh entry: definitively not present
        // (or freshly created on another instance — rebuild entry)
    }

    // Cold path: load all of seller's pickup locations
    if err := c.refresh(ctx, sellerID); err != nil {
        return nil, err
    }
    c.mu.RLock()
    defer c.mu.RUnlock()
    pl, ok := c.entries[sellerID].locations[id]
    if !ok {
        return nil, ErrNotFound
    }
    return pl, nil
}

func (c *PickupCache) Invalidate(sellerID core.SellerID) {
    c.mu.Lock()
    delete(c.entries, sellerID)
    c.mu.Unlock()
}

func (c *PickupCache) refresh(ctx context.Context, sellerID core.SellerID) error {
    list, err := c.repo.PickupList(ctx, sellerID)
    if err != nil {
        return err
    }
    m := make(map[core.PickupLocationID]*PickupLocation, len(list))
    for _, pl := range list {
        m[pl.ID] = pl
    }
    c.mu.Lock()
    c.entries[sellerID] = pickupCacheEntry{locations: m, fetchedAt: time.Now()}
    c.mu.Unlock()
    return nil
}
```

The cache is invalidated in two ways:
1. **Direct** — every mutation method calls `cache.Invalidate(sellerID)` after the tx commits.
2. **Cross-instance** — a Postgres `LISTEN pickup_location_changed` consumer (registered in `cmd/server/wire.go`) calls `Invalidate` when any other instance commits.

The 60s TTL is the safety net if LISTEN drops; real freshness is sub-second under normal operation.

A similar `ProductCache` exists for hot SKU lookups during order creation, but with smaller TTL (30s) because product churn is higher.

## Outbox Event Payloads

```go
type PickupChangedPayload struct {
    SchemaVersion    int        `json:"schema_version"` // = 1
    SellerID         string     `json:"seller_id"`
    PickupLocationID string     `json:"pickup_location_id"`
    Kind             string     `json:"kind"` // "created" | "updated" | "default_changed" | "deactivated" | "deleted"
    Pincode          string     `json:"pincode"`
    OccurredAt       time.Time  `json:"occurred_at"`
}
```

Forwarder routes:
- `pickup_location.changed` → `carrier.RebuildServiceabilityCacheJob(seller_id)`. (Carriers framework, LLD §03-services/12.)

Products do not currently emit outbox events — nothing downstream observes product churn. We would add `product.changed` if/when a feature needed it.

## Testing

### Unit Tests

```go
func TestValidatePickupCreate_BadPincode(t *testing.T) {
    r := goodPickupRequest()
    r.Address.Pincode = "012345"
    require.ErrorIs(t, validatePickupCreate(r), ErrInvalidAddress)
}

func TestValidatePickupCreate_LabelTooShort(t *testing.T) {
    r := goodPickupRequest()
    r.Label = "x"
    require.ErrorIs(t, validatePickupCreate(r), ErrInvalidLabel)
}

func TestValidateProductUpsert_ZeroWeight(t *testing.T) {
    r := goodProductRequest()
    r.UnitWeightG = 0
    require.ErrorIs(t, validateProductUpsert(r), ErrInvalidWeight)
}
```

### SLT

```go
func TestPickup_DefaultSemantics_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sellerID := slt.NewSeller(t, pg).ID
    svc := slt.Catalog(pg).Pickup

    // First pickup is auto-default
    a, _ := svc.Create(ctx, slt.PickupRequest(sellerID, "A"))
    require.True(t, a.IsDefault)

    // Second is not, by default
    b, _ := svc.Create(ctx, slt.PickupRequest(sellerID, "B"))
    require.False(t, b.IsDefault)

    // Setting B as default unsets A
    require.NoError(t, svc.SetDefault(ctx, sellerID, b.ID))
    list, _ := svc.List(ctx, sellerID)
    var aRefreshed, bRefreshed *PickupLocation
    for _, p := range list {
        if p.ID == a.ID { aRefreshed = p }
        if p.ID == b.ID { bRefreshed = p }
    }
    require.False(t, aRefreshed.IsDefault)
    require.True(t, bRefreshed.IsDefault)
}

func TestPickup_LabelUniqueButReusableAfterDelete_SLT(t *testing.T) {
    // Create "Mumbai", delete it, create "Mumbai" again — should succeed.
    // ...
}

func TestProduct_UpsertIdempotent_SLT(t *testing.T) {
    // Same SKU twice with different name → second wins.
    // ...
}

func TestProduct_SearchByName_SLT(t *testing.T) {
    // Trigram search for "blue widget" returns matches.
    // ...
}

func TestPickupCache_InvalidationOnUpdate_SLT(t *testing.T) {
    // Create → read (caches) → update label → read → must see new label.
    // ...
}

func TestRLS_PickupAndProductIsolation_SLT(t *testing.T) {
    // Two sellers; queries with one's GUC must not see the other's rows.
    // ...
}
```

### Microbenchmarks

```go
func BenchmarkPickupCache_Get_Hit(b *testing.B) {
    c := newCacheWithEntry(testSellerID, testPickupID, &PickupLocation{Label: "X"})
    for i := 0; i < b.N; i++ {
        _, _ = c.Get(context.Background(), testSellerID, testPickupID)
    }
}
// Target: < 80 ns, 0 allocs.

func BenchmarkValidatePickupCreate(b *testing.B) {
    r := goodPickupRequest()
    for i := 0; i < b.N; i++ {
        _ = validatePickupCreate(r)
    }
}
// Target: < 1 µs, 0 allocs (pre-compiled regex).
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Pickup.Create` | 5 ms | 14 ms | INSERT + ClearDefault + audit + outbox |
| `Pickup.Get` (cache hit) | 60 ns | 200 ns | 0 allocs |
| `Pickup.Get` (cache miss / cold) | 1.5 ms | 5 ms | List + cache fill |
| `Pickup.List` | 1.5 ms | 4 ms | indexed by `(seller_id) WHERE deleted_at IS NULL` |
| `Pickup.SetDefault` | 4 ms | 12 ms | clear + set + audit + outbox |
| `Product.Upsert` | 4 ms | 11 ms | single INSERT…ON CONFLICT |
| `Product.List` (50 rows, no search) | 3 ms | 8 ms | indexed by `(seller_id, active)` |
| `Product.List` (with trigram search) | 12 ms | 40 ms | gin scan |
| `Product.Get` (cache hit) | 60 ns | 200 ns | 0 allocs |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Label collides with existing pickup | UNIQUE violation on `pickup_seller_label_unique` | Map to `ErrLabelDuplicate`, return 409. |
| Two concurrent SetDefault | UNIQUE violation on `pickup_seller_default_unique` | Second tx fails; client sees 409 + retries; final state is well-defined. |
| Policy cap exceeded | `PickupCountActive` ≥ cap | Map to `ErrPickupLimitExceeded`, return 422 with cap value. |
| Soft-deleted pickup referenced on order | `orders.Create` calls `GetPickupLocation` which returns ErrNotFound | Maps to `ErrPickupLocationInvalid` at order layer. |
| Cache entry stale because LISTEN dropped | TTL forces refresh in 60s | Worst case: 60s of stale reads; never serves data from a different seller (RLS still applies on cold load). |
| Product CSV import: row N has bad weight | per-row validation | Skip row + record in error_report; final state `partial`. |
| Product Upsert race (same SKU, two requests) | ON CONFLICT clause handles it | Both succeed; last-writer-wins semantics, both clients see fresh data. |

## Open Questions

1. **Pickup-location verification.** We trust seller-supplied addresses today. Carriers occasionally reject pickups for invalid addresses, surfacing as booking errors rather than upfront validation. Adding a verification step (Google Maps geocode or pin-code-DB lookup) is a v1 candidate.
2. **Per-pickup-location operating hours / blackout dates.** We have a free-text `pickup_hours` field. Carriers do not consume this. **Decision: keep free-text for v0**; add structured `operating_window` when we introduce same-day pickup features.
3. **Multi-warehouse orchestration.** When a single order should split across warehouses, who decides? **Decision: out of scope for v0**. Sellers explicitly pick a pickup location per order.
4. **Product variants.** Different sizes/colors of the same SKU base. **Decision: each variant is its own row**; we don't model variant trees. Sellers who want variants can use SKU naming convention (`SHIRT-RED-M`).
5. **Bulk product CSV: COPY vs INSERT.** Same as orders; deferred.

## References

- HLD §03-services/02-pricing: pricing reads pickup origin pincode.
- HLD §03-services/03-allocation: allocation reads pickup pincode and product `category_hint` for filters.
- LLD §03-services/01-policy-engine: `catalog.max_pickup_locations` policy.
- LLD §03-services/02-audit: pickup mutations audited.
- LLD §03-services/03-outbox: `pickup_location.changed` event.
- LLD §03-services/09-seller: `LifecycleCache.AssertActive` guards mutations.
- LLD §03-services/10-orders: orders.Create resolves pickup via GetPickupLocation.
- LLD §03-services/12-carriers-framework: serviceability cache rebuild trigger.
