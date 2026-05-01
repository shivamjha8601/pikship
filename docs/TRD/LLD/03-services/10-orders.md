# Orders Service

## Purpose

The orders service owns the **canonical Order record** — the seller's intent to ship something, before any carrier is selected. An order is the input to allocation, pricing, and shipment booking. It is **not** the same as a shipment: one order can produce multiple shipments (split fulfillment), and one order can be cancelled before any shipment is booked.

Responsibilities:

- Validate, store, and update orders from any source (Shopify webhook, CSV import, manual creation, API).
- Maintain order lifecycle (`draft → ready → allocating → booked → in_transit → delivered → closed | cancelled | rto`).
- Provide a stable, normalized order schema regardless of origin (channel adapters convert to this schema).
- Bulk import: ingest CSV uploads, validate row-by-row, defer-and-resolve to channel orders.
- Provide query APIs (list/filter, get, search) used by the seller dashboard and ops.
- Emit `order.*` events for downstream services.

Out of scope (owned elsewhere):

- Carrier selection — allocation (LLD §03-services/07).
- Pricing/quote — pricing (LLD §03-services/06).
- Booking call to carrier — shipments (LLD §03-services/13, future).
- Catalog lookups — catalog (LLD §03-services/11).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | `core.Paise`, IDs, errors, validators. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/seller` | `LifecycleCache.AssertActive`. |
| `internal/catalog` | resolve product references and pickup location. |
| `internal/audit` | high-value mutations (cancel, manual override). |
| `internal/outbox` | `order.created`, `order.updated`, `order.cancelled`. |
| `internal/idempotency` | API-level idempotency keys for create. |
| `internal/policy` | cancellation window, max line items, etc. |
| `internal/core/csv` | streaming CSV parser used by bulk import. |

## Package Layout

```
internal/orders/
├── service.go            // Service interface + constructor
├── service_impl.go       // Implementation
├── repo.go               // sqlc wrapper + helpers
├── types.go              // Order, OrderLine, OrderAddress, OrderStatus
├── lifecycle.go          // State machine
├── validation.go         // Input validation rules
├── csv_import.go         // Bulk-import logic
├── errors.go             // Sentinel errors
├── events.go             // Outbox payload schemas
├── jobs.go               // River jobs (CSVImportJob, OrderCleanupJob)
├── service_test.go
├── service_slt_test.go
├── csv_import_test.go
└── bench_test.go
```

## Public API

```go
package orders

type Service interface {
    // Create persists a new order in `draft` or `ready` state depending
    // on whether all required fields are populated.
    //
    // Idempotent on (seller_id, channel, channel_order_id) when those are set.
    // For manual creation, callers MUST supply an idempotency key via
    // request context (see §Idempotency below).
    Create(ctx context.Context, req CreateRequest) (*Order, error)

    // Get returns the order. Returns ErrNotFound if absent or belongs
    // to a different seller (RLS handles the second case).
    Get(ctx context.Context, id core.OrderID) (*Order, error)

    // GetByChannelRef looks up an order by its channel + channel_order_id.
    // Used by channel adapters to detect duplicate webhooks.
    GetByChannelRef(ctx context.Context, channel string, channelOrderID string) (*Order, error)

    // Update mutates a `draft` or `ready` order. Once `allocating` or
    // beyond, only specific allowlisted fields are mutable (see
    // updateAllowedByState in lifecycle.go).
    Update(ctx context.Context, id core.OrderID, patch UpdatePatch) (*Order, error)

    // MarkReady transitions `draft → ready`. Validates that all required
    // fields are present.
    MarkReady(ctx context.Context, id core.OrderID) (*Order, error)

    // Cancel transitions to `cancelled`. Allowed from `draft`, `ready`,
    // or `allocating`. Returns ErrCancellationBlocked once a shipment is
    // booked (the shipments service handles in-flight cancellation
    // separately, and may call back into Orders.MarkCancelledByShipment
    // when the carrier accepts a cancellation).
    Cancel(ctx context.Context, id core.OrderID, reason string) error

    // MarkAllocating is called by the allocation service when allocation
    // begins. Idempotent (no-op if already allocating).
    MarkAllocating(ctx context.Context, id core.OrderID) error

    // MarkBooked is called by the shipments service when a shipment is
    // successfully booked. Records the awb_number on the order for fast
    // status lookups.
    MarkBooked(ctx context.Context, id core.OrderID, ref BookedRef) error

    // List queries with filters + pagination. Used by dashboard.
    List(ctx context.Context, q ListQuery) (ListResult, error)

    // Bulk operations
    BulkImportCSV(ctx context.Context, sellerID core.SellerID, req CSVImportRequest) (*ImportJob, error)
    GetImportJob(ctx context.Context, jobID core.ImportJobID) (*ImportJob, error)
}
```

### Request / Response Types

```go
type CreateRequest struct {
    SellerID         core.SellerID
    Channel          string         // "manual" | "shopify" | "csv" | "api"
    ChannelOrderID   string         // unique within (seller, channel)
    OrderRef         string         // human-friendly seller-facing ref (e.g. invoice number)

    BuyerName        string
    BuyerPhone       string         // E.164
    BuyerEmail       string

    BillingAddress   Address
    ShippingAddress  Address

    Lines            []OrderLineInput

    PaymentMethod    string         // "prepaid" | "cod"
    SubtotalPaise    core.Paise
    ShippingPaise    core.Paise     // what the seller charged the buyer (informational)
    DiscountPaise    core.Paise
    TaxPaise         core.Paise
    TotalPaise       core.Paise
    CODAmountPaise   core.Paise     // 0 if PaymentMethod != "cod"

    PickupLocationID core.PickupLocationID

    PackageWeightG   int            // total package weight in grams
    PackageLengthMM  int
    PackageWidthMM   int
    PackageHeightMM  int

    Notes            string
    Tags             []string
}

type OrderLineInput struct {
    SKU              string
    Name             string
    Quantity         int
    UnitPricePaise   core.Paise
    UnitWeightG      int
    HSNCode          string         // optional
    CategoryHint     string         // for special-handling routing
}

type UpdatePatch struct {
    BuyerPhone       *string
    BuyerEmail       *string
    ShippingAddress  *Address
    BillingAddress   *Address
    Lines            *[]OrderLineInput
    PackageDimensions *Dimensions
    Notes            *string
    Tags             *[]string
}

type ListQuery struct {
    SellerID    core.SellerID
    States      []OrderState        // any-of filter
    Channels    []string
    DateRange   *DateRange
    SearchQ     string              // matches order_ref, channel_order_id, buyer_name, awb
    Tags        []string
    Page        Page                // limit, offset
    SortBy      string              // "created_at_desc" | "total_desc"
}

type ListResult struct {
    Orders     []*Order
    TotalCount int
    Page       Page
}

type CSVImportRequest struct {
    UploadedByUserID core.UserID
    UploadID         string         // S3 key (or file path in dev)
    SchemaName       string         // "default" | "shopify_export" | "manual_v1"
    DryRun           bool           // validate only; don't insert
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("order: not found")
    ErrInvalidState          = errors.New("order: invalid state for operation")
    ErrCancellationBlocked   = errors.New("order: cannot cancel in current state")
    ErrInvalidPaymentMethod  = errors.New("order: invalid payment method")
    ErrCODAmountMismatch     = errors.New("order: cod_amount must match total when payment_method=cod")
    ErrInvalidAddress        = errors.New("order: invalid address (missing required field)")
    ErrInvalidPincode        = errors.New("order: pincode must be 6 digits")
    ErrInvalidWeight         = errors.New("order: package weight must be > 0")
    ErrInvalidDimensions     = errors.New("order: package dimensions must be > 0")
    ErrEmptyLines            = errors.New("order: must have at least one line item")
    ErrLineQuantityInvalid   = errors.New("order: line quantity must be > 0")
    ErrTotalsMismatch        = errors.New("order: subtotal + tax - discount + shipping != total")
    ErrPickupLocationInvalid = errors.New("order: pickup location does not belong to seller or is inactive")
    ErrChannelDuplicate      = errors.New("order: channel_order_id already exists for this channel")
    ErrSellerNotActive       = errors.New("order: seller not in operational state")
)
```

## Order State Machine

```
                     Create
                       │
                       ▼
      ┌──────────────────────────┐
      │          draft           │  some required fields missing
      └────────────┬─────────────┘
                   │  MarkReady() (validates all required fields)
                   ▼
      ┌──────────────────────────┐
      │          ready           │  ready for allocation
      └────────────┬─────────────┘
                   │  MarkAllocating()  (allocation service triggers)
                   ▼
      ┌──────────────────────────┐
      │       allocating         │
      └────────────┬─────────────┘
                   │  MarkBooked()  (shipments service triggers)
                   ▼
      ┌──────────────────────────┐
      │         booked           │  shipment exists; awb assigned
      └────────────┬─────────────┘
                   │  shipment status updates
                   ▼
      ┌──────────────────────────┐    ┌────────────┐
      │       in_transit         │───►│    rto     │  return-to-origin
      └────────────┬─────────────┘    └────────────┘
                   │
                   ▼
      ┌──────────────────────────┐
      │        delivered         │
      └────────────┬─────────────┘
                   │  COD remit + recon settle (closure cron)
                   ▼
      ┌──────────────────────────┐
      │          closed          │
      └──────────────────────────┘

Cancellation:
   draft / ready / allocating  ──Cancel()──►  cancelled
   booked / in_transit         (not via Orders.Cancel; only via
                                shipments service which calls back
                                MarkCancelledByShipment)
```

State machine encoded in `lifecycle.go`:

```go
type OrderState string

const (
    StateDraft       OrderState = "draft"
    StateReady       OrderState = "ready"
    StateAllocating  OrderState = "allocating"
    StateBooked      OrderState = "booked"
    StateInTransit   OrderState = "in_transit"
    StateDelivered   OrderState = "delivered"
    StateClosed      OrderState = "closed"
    StateCancelled   OrderState = "cancelled"
    StateRTO         OrderState = "rto"
)

var allowedTransitions = map[OrderState]map[OrderState]struct{}{
    StateDraft:      {StateReady: {}, StateCancelled: {}},
    StateReady:      {StateAllocating: {}, StateCancelled: {}, StateDraft: {}}, // back to draft for fixes
    StateAllocating: {StateBooked: {}, StateReady: {}, StateCancelled: {}},      // alloc failure → back to ready
    StateBooked:     {StateInTransit: {}, StateCancelled: {}, StateRTO: {}},
    StateInTransit:  {StateDelivered: {}, StateRTO: {}},
    StateDelivered:  {StateClosed: {}, StateRTO: {}},   // RTO can happen post-delivery for COD undelivered
    StateClosed:     {},
    StateCancelled:  {},
    StateRTO:        {StateClosed: {}},
}

// updateAllowedByState defines which fields can be patched in each state.
// `draft` and `ready` allow full editing. Once `allocating`, only buyer
// phone (for SMS retries) and notes are editable.
var updateAllowedByState = map[OrderState]map[string]bool{
    StateDraft:      allFieldsEditable,
    StateReady:      allFieldsEditable,
    StateAllocating: {"BuyerPhone": true, "Notes": true, "Tags": true},
    StateBooked:     {"BuyerPhone": true, "Notes": true, "Tags": true},
    StateInTransit:  {"Notes": true, "Tags": true},
    // delivered/closed/cancelled/rto: no edits.
}
```

## DB Schema

```sql
CREATE TABLE order_record (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           uuid        NOT NULL REFERENCES seller(id),

    state               text        NOT NULL CHECK (state IN
        ('draft','ready','allocating','booked','in_transit','delivered','closed','cancelled','rto')),

    channel             text        NOT NULL,
    channel_order_id    text        NOT NULL,
    order_ref           text,

    buyer_name          text        NOT NULL,
    buyer_phone         text        NOT NULL,
    buyer_email         text,

    billing_address     jsonb       NOT NULL,
    shipping_address    jsonb       NOT NULL,
    -- denormalized for common filters/indexes
    shipping_pincode    text        NOT NULL,
    shipping_state      text        NOT NULL,

    payment_method      text        NOT NULL CHECK (payment_method IN ('prepaid','cod')),

    subtotal_paise      bigint      NOT NULL CHECK (subtotal_paise >= 0),
    shipping_paise      bigint      NOT NULL DEFAULT 0,
    discount_paise      bigint      NOT NULL DEFAULT 0,
    tax_paise           bigint      NOT NULL DEFAULT 0,
    total_paise         bigint      NOT NULL CHECK (total_paise >= 0),
    cod_amount_paise    bigint      NOT NULL DEFAULT 0
        CHECK ((payment_method = 'cod' AND cod_amount_paise > 0) OR
               (payment_method = 'prepaid' AND cod_amount_paise = 0)),

    pickup_location_id  uuid        NOT NULL REFERENCES pickup_location(id),

    package_weight_g    integer     NOT NULL CHECK (package_weight_g > 0),
    package_length_mm   integer     NOT NULL CHECK (package_length_mm > 0),
    package_width_mm    integer     NOT NULL CHECK (package_width_mm > 0),
    package_height_mm   integer     NOT NULL CHECK (package_height_mm > 0),

    -- Set when MarkBooked. Convenient for label/track endpoints without
    -- needing to join shipments.
    awb_number          text,
    carrier_code        text,
    booked_at           timestamptz,

    notes               text,
    tags                text[]      NOT NULL DEFAULT '{}',

    cancelled_at        timestamptz,
    cancelled_reason    text,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT order_channel_unique UNIQUE (seller_id, channel, channel_order_id)
);

-- Indexes
CREATE INDEX order_seller_state_created_idx ON order_record(seller_id, state, created_at DESC);
CREATE INDEX order_seller_created_idx ON order_record(seller_id, created_at DESC);
CREATE INDEX order_awb_idx ON order_record(awb_number) WHERE awb_number IS NOT NULL;
CREATE INDEX order_buyer_phone_idx ON order_record(seller_id, buyer_phone);
CREATE INDEX order_pincode_idx ON order_record(seller_id, shipping_pincode);
CREATE INDEX order_search_trgm_idx ON order_record
    USING gin (
        (buyer_name || ' ' || COALESCE(order_ref, '') || ' ' || channel_order_id) gin_trgm_ops
    );

ALTER TABLE order_record ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_isolation ON order_record
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- ──────────────────────────────────────────────────────────────────────
-- Order lines: separate table to keep order_record narrow
-- ──────────────────────────────────────────────────────────────────────

CREATE TABLE order_line (
    id              bigserial   PRIMARY KEY,
    order_id        uuid        NOT NULL REFERENCES order_record(id) ON DELETE CASCADE,
    seller_id       uuid        NOT NULL REFERENCES seller(id), -- denormalized for RLS

    line_no         integer     NOT NULL,
    sku             text        NOT NULL,
    name            text        NOT NULL,
    quantity        integer     NOT NULL CHECK (quantity > 0),
    unit_price_paise bigint     NOT NULL CHECK (unit_price_paise >= 0),
    unit_weight_g   integer     NOT NULL CHECK (unit_weight_g >= 0),
    hsn_code        text,
    category_hint   text,

    UNIQUE (order_id, line_no)
);

CREATE INDEX order_line_order_idx ON order_line(order_id);

ALTER TABLE order_line ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_line_isolation ON order_line
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- ──────────────────────────────────────────────────────────────────────
-- Order state history: append-only
-- ──────────────────────────────────────────────────────────────────────

CREATE TABLE order_state_event (
    id           bigserial   PRIMARY KEY,
    order_id     uuid        NOT NULL REFERENCES order_record(id) ON DELETE CASCADE,
    seller_id    uuid        NOT NULL REFERENCES seller(id),
    from_state   text        NOT NULL,
    to_state     text        NOT NULL,
    reason       text,
    actor_id     uuid,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX order_state_event_order_idx ON order_state_event(order_id, created_at);

ALTER TABLE order_state_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_state_event_isolation ON order_state_event
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- ──────────────────────────────────────────────────────────────────────
-- Bulk import jobs
-- ──────────────────────────────────────────────────────────────────────

CREATE TABLE order_import_job (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       uuid        NOT NULL REFERENCES seller(id),
    uploaded_by     uuid        NOT NULL REFERENCES app_user(id),
    upload_id       text        NOT NULL,         -- S3 key
    schema_name     text        NOT NULL,
    dry_run         boolean     NOT NULL DEFAULT false,

    state           text        NOT NULL CHECK (state IN ('queued','running','succeeded','failed','partial')),
    rows_total      integer     NOT NULL DEFAULT 0,
    rows_succeeded  integer     NOT NULL DEFAULT 0,
    rows_failed     integer     NOT NULL DEFAULT 0,
    error_report    jsonb       NOT NULL DEFAULT '[]'::jsonb, -- array of {row_no, errors[]}

    started_at      timestamptz,
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX order_import_job_seller_idx ON order_import_job(seller_id, created_at DESC);

ALTER TABLE order_import_job ENABLE ROW LEVEL SECURITY;
CREATE POLICY order_import_job_isolation ON order_import_job
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Grants
GRANT SELECT, INSERT, UPDATE ON
    order_record, order_line, order_state_event, order_import_job
TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE
    order_line_id_seq, order_state_event_id_seq
TO pikshipp_app;
GRANT SELECT ON
    order_record, order_line, order_state_event, order_import_job
TO pikshipp_reports;
```

### Index Rationale

- `order_seller_state_created_idx`: drives the dashboard "show me ready orders" / "show me booked orders" queries.
- `order_awb_idx`: supports tracking lookups by AWB (the most common ops query).
- `order_search_trgm_idx`: trigram index for the dashboard search box (buyer name, order ref, channel order id). Requires `pg_trgm` extension.
- All seller-scoped indexes start with `seller_id` to align with RLS predicate planning.

## sqlc Queries

```sql
-- name: OrderInsert :one
INSERT INTO order_record (
    id, seller_id, state, channel, channel_order_id, order_ref,
    buyer_name, buyer_phone, buyer_email,
    billing_address, shipping_address, shipping_pincode, shipping_state,
    payment_method,
    subtotal_paise, shipping_paise, discount_paise, tax_paise, total_paise, cod_amount_paise,
    pickup_location_id,
    package_weight_g, package_length_mm, package_width_mm, package_height_mm,
    notes, tags
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14,
    $15, $16, $17, $18, $19, $20,
    $21,
    $22, $23, $24, $25,
    $26, $27
)
RETURNING *;

-- name: OrderLineInsert :exec
INSERT INTO order_line (
    order_id, seller_id, line_no, sku, name, quantity,
    unit_price_paise, unit_weight_g, hsn_code, category_hint
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: OrderGet :one
SELECT * FROM order_record WHERE id = $1;

-- name: OrderGetByChannelRef :one
SELECT * FROM order_record
WHERE seller_id = $1 AND channel = $2 AND channel_order_id = $3;

-- name: OrderLinesByOrder :many
SELECT * FROM order_line WHERE order_id = $1 ORDER BY line_no;

-- name: OrderTransitionState :one
UPDATE order_record
SET state = $2,
    cancelled_at = COALESCE(sqlc.narg('cancelled_at'), cancelled_at),
    cancelled_reason = COALESCE(sqlc.narg('cancelled_reason'), cancelled_reason),
    awb_number = COALESCE(sqlc.narg('awb_number'), awb_number),
    carrier_code = COALESCE(sqlc.narg('carrier_code'), carrier_code),
    booked_at = COALESCE(sqlc.narg('booked_at'), booked_at),
    updated_at = now()
WHERE id = $1 AND state = $3
RETURNING *;

-- name: OrderStateEventInsert :exec
INSERT INTO order_state_event (
    order_id, seller_id, from_state, to_state, reason, actor_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: OrderUpdatePatch :one
-- Generic patch update; null-coalesce per field. Caller is responsible
-- for verifying state allows mutation.
UPDATE order_record
SET buyer_phone     = COALESCE(sqlc.narg('buyer_phone'), buyer_phone),
    buyer_email     = COALESCE(sqlc.narg('buyer_email'), buyer_email),
    shipping_address = COALESCE(sqlc.narg('shipping_address'), shipping_address),
    billing_address = COALESCE(sqlc.narg('billing_address'), billing_address),
    shipping_pincode = COALESCE(sqlc.narg('shipping_pincode'), shipping_pincode),
    shipping_state   = COALESCE(sqlc.narg('shipping_state'), shipping_state),
    package_weight_g = COALESCE(sqlc.narg('package_weight_g'), package_weight_g),
    package_length_mm = COALESCE(sqlc.narg('package_length_mm'), package_length_mm),
    package_width_mm  = COALESCE(sqlc.narg('package_width_mm'), package_width_mm),
    package_height_mm = COALESCE(sqlc.narg('package_height_mm'), package_height_mm),
    notes = COALESCE(sqlc.narg('notes'), notes),
    tags  = COALESCE(sqlc.narg('tags'), tags),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: OrderListFiltered :many
SELECT * FROM order_record
WHERE seller_id = $1
  AND ($2::text[] IS NULL OR state = ANY($2::text[]))
  AND ($3::text[] IS NULL OR channel = ANY($3::text[]))
  AND ($4::timestamptz IS NULL OR created_at >= $4)
  AND ($5::timestamptz IS NULL OR created_at <  $5)
  AND ($6::text IS NULL OR
        (buyer_name || ' ' || COALESCE(order_ref, '') || ' ' || channel_order_id)
        ILIKE '%' || $6 || '%')
ORDER BY created_at DESC
LIMIT $7 OFFSET $8;

-- name: OrderCountFiltered :one
SELECT count(*) FROM order_record
WHERE seller_id = $1
  AND ($2::text[] IS NULL OR state = ANY($2::text[]))
  AND ($3::text[] IS NULL OR channel = ANY($3::text[]))
  AND ($4::timestamptz IS NULL OR created_at >= $4)
  AND ($5::timestamptz IS NULL OR created_at <  $5)
  AND ($6::text IS NULL OR
        (buyer_name || ' ' || COALESCE(order_ref, '') || ' ' || channel_order_id)
        ILIKE '%' || $6 || '%');

-- name: OrderImportJobInsert :one
INSERT INTO order_import_job (
    id, seller_id, uploaded_by, upload_id, schema_name, dry_run, state, rows_total
) VALUES ($1, $2, $3, $4, $5, $6, 'queued', 0)
RETURNING *;

-- name: OrderImportJobUpdateProgress :exec
UPDATE order_import_job
SET state = $2,
    rows_total = $3,
    rows_succeeded = $4,
    rows_failed = $5,
    error_report = $6,
    started_at = COALESCE(started_at, sqlc.narg('started_at')),
    finished_at = sqlc.narg('finished_at')
WHERE id = $1;
```

## Implementation

### Create

```go
package orders

func (s *service) Create(ctx context.Context, req CreateRequest) (*Order, error) {
    // 1. Cheap validations BEFORE the lifecycle gate so the dashboard
    //    can show errors fast even for a suspended seller.
    if err := validateCreateRequest(req); err != nil {
        return nil, err
    }
    // 2. Seller must be operational. Ignore for sandbox/active; reject suspended/wound_down.
    if err := s.lifecycleCache.AssertActive(ctx, req.SellerID); err != nil {
        return nil, fmt.Errorf("orders: %w", err)
    }
    // 3. Dedupe on (seller, channel, channel_order_id) — short-circuit before tx.
    if req.Channel != "manual" && req.ChannelOrderID != "" {
        existing, err := s.q.OrderGetByChannelRef(ctx, sqlcgen.OrderGetByChannelRefParams{
            SellerID:        req.SellerID.UUID(),
            Channel:         req.Channel,
            ChannelOrderID:  req.ChannelOrderID,
        })
        if err == nil {
            return s.hydrate(ctx, orderFromRow(existing))
        }
        if !errors.Is(err, pgx.ErrNoRows) {
            return nil, fmt.Errorf("orders: dedupe lookup: %w", err)
        }
    }

    // 4. Pickup location must belong to seller and be active.
    pl, err := s.catalog.GetPickupLocation(ctx, req.SellerID, req.PickupLocationID)
    if err != nil {
        return nil, fmt.Errorf("orders: %w", ErrPickupLocationInvalid)
    }
    if !pl.Active {
        return nil, ErrPickupLocationInvalid
    }

    // 5. Determine initial state.
    initialState := StateDraft
    if isComplete(req) {
        initialState = StateReady
    }

    var out *Order
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        id := core.NewOrderID()
        row, err := qtx.OrderInsert(ctx, sqlcgen.OrderInsertParams{
            ID:              id.UUID(),
            SellerID:        req.SellerID.UUID(),
            State:           string(initialState),
            Channel:         req.Channel,
            ChannelOrderID:  req.ChannelOrderID,
            OrderRef:        pgxNullString(req.OrderRef),
            BuyerName:       req.BuyerName,
            BuyerPhone:      req.BuyerPhone,
            BuyerEmail:      pgxNullString(req.BuyerEmail),
            BillingAddress:  jsonbFromAddress(req.BillingAddress),
            ShippingAddress: jsonbFromAddress(req.ShippingAddress),
            ShippingPincode: req.ShippingAddress.Pincode,
            ShippingState:   req.ShippingAddress.State,
            PaymentMethod:   req.PaymentMethod,
            SubtotalPaise:   int64(req.SubtotalPaise),
            ShippingPaise:   int64(req.ShippingPaise),
            DiscountPaise:   int64(req.DiscountPaise),
            TaxPaise:        int64(req.TaxPaise),
            TotalPaise:      int64(req.TotalPaise),
            CODAmountPaise:  int64(req.CODAmountPaise),
            PickupLocationID: req.PickupLocationID.UUID(),
            PackageWeightG:  int32(req.PackageWeightG),
            PackageLengthMM: int32(req.PackageLengthMM),
            PackageWidthMM:  int32(req.PackageWidthMM),
            PackageHeightMM: int32(req.PackageHeightMM),
            Notes:           pgxNullString(req.Notes),
            Tags:            req.Tags,
        })
        if err != nil {
            // Map UNIQUE violation to ErrChannelDuplicate
            var pgErr *pgconn.PgError
            if errors.As(err, &pgErr) && pgErr.ConstraintName == "order_channel_unique" {
                return ErrChannelDuplicate
            }
            return fmt.Errorf("orders: insert: %w", err)
        }
        out = orderFromRow(row)

        // Insert lines
        for i, line := range req.Lines {
            if err := qtx.OrderLineInsert(ctx, sqlcgen.OrderLineInsertParams{
                OrderID:        id.UUID(),
                SellerID:       req.SellerID.UUID(),
                LineNo:         int32(i + 1),
                SKU:            line.SKU,
                Name:           line.Name,
                Quantity:       int32(line.Quantity),
                UnitPricePaise: int64(line.UnitPricePaise),
                UnitWeightG:    int32(line.UnitWeightG),
                HSNCode:        pgxNullString(line.HSNCode),
                CategoryHint:   pgxNullString(line.CategoryHint),
            }); err != nil {
                return err
            }
        }

        // State event
        if err := qtx.OrderStateEventInsert(ctx, sqlcgen.OrderStateEventInsertParams{
            OrderID:   id.UUID(),
            SellerID:  req.SellerID.UUID(),
            FromState: "",
            ToState:   string(initialState),
            Reason:    "create",
            Payload:   jsonbFrom(map[string]any{"channel": req.Channel}),
        }); err != nil {
            return err
        }

        // Outbox
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:    "order.created",
            Key:     string(id),
            Payload: out.toCreatedPayload(),
        })
    })
    if err != nil {
        return nil, err
    }
    return s.hydrate(ctx, out)
}
```

### Validation

```go
package orders

func validateCreateRequest(r CreateRequest) error {
    if r.SellerID.IsZero() {
        return fmt.Errorf("%w: seller_id required", ErrInvalidState)
    }
    if r.PaymentMethod != "prepaid" && r.PaymentMethod != "cod" {
        return ErrInvalidPaymentMethod
    }
    if r.PaymentMethod == "cod" && r.CODAmountPaise != r.TotalPaise {
        return ErrCODAmountMismatch
    }
    if r.PaymentMethod == "prepaid" && r.CODAmountPaise != 0 {
        return ErrCODAmountMismatch
    }
    if len(r.Lines) == 0 {
        return ErrEmptyLines
    }
    for _, l := range r.Lines {
        if l.Quantity <= 0 {
            return ErrLineQuantityInvalid
        }
    }
    if r.PackageWeightG <= 0 {
        return ErrInvalidWeight
    }
    if r.PackageLengthMM <= 0 || r.PackageWidthMM <= 0 || r.PackageHeightMM <= 0 {
        return ErrInvalidDimensions
    }
    if err := validateAddress(r.ShippingAddress, "shipping_address"); err != nil {
        return err
    }
    if err := validateAddress(r.BillingAddress, "billing_address"); err != nil {
        return err
    }
    // Money invariant: subtotal + shipping + tax - discount == total
    expected := r.SubtotalPaise.Add(r.ShippingPaise).Add(r.TaxPaise).Sub(r.DiscountPaise)
    if expected != r.TotalPaise {
        return fmt.Errorf("%w: expected %d got %d", ErrTotalsMismatch, expected, r.TotalPaise)
    }
    return nil
}

var pincodeRegex = regexp.MustCompile(`^[1-9][0-9]{5}$`)

func validateAddress(a Address, field string) error {
    if a.Line1 == "" || a.City == "" || a.State == "" || a.Pincode == "" {
        return fmt.Errorf("%w: %s missing required field", ErrInvalidAddress, field)
    }
    if !pincodeRegex.MatchString(a.Pincode) {
        return fmt.Errorf("%w: %s pincode=%s", ErrInvalidPincode, field, a.Pincode)
    }
    return nil
}

// isComplete = does this order have everything needed to allocate?
// Today: yes if all addresses and dimensions are present, since validation
// already enforced that. Reserved for future "draft means missing X" logic.
func isComplete(r CreateRequest) bool {
    return r.PackageWeightG > 0 &&
        r.PackageLengthMM > 0 &&
        r.ShippingAddress.Pincode != "" &&
        r.PickupLocationID != core.PickupLocationID{}
}
```

### State Transition Helper

```go
// transitionState is the single helper used by every state-changing
// public method. It enforces the lifecycle table and emits the state
// event + outbox event.
func (s *service) transitionState(
    ctx context.Context, tx pgx.Tx,
    id core.OrderID, sellerID core.SellerID,
    from, to OrderState,
    reason string, actor core.UserID,
    extra map[string]any,
    sqlExtras sqlcgen.OrderTransitionStateParams, // null-fields for awb/cancelled etc.
) (*Order, error) {
    if !canTransition(from, to) {
        return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidState, from, to)
    }
    qtx := sqlcgen.New(tx)
    sqlExtras.ID = id.UUID()
    sqlExtras.State = string(to)
    sqlExtras.FromState = string(from) // optimistic guard
    row, err := qtx.OrderTransitionState(ctx, sqlExtras)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrInvalidState // race: state changed under us
        }
        return nil, err
    }
    if err := qtx.OrderStateEventInsert(ctx, sqlcgen.OrderStateEventInsertParams{
        OrderID:   id.UUID(),
        SellerID:  sellerID.UUID(),
        FromState: string(from),
        ToState:   string(to),
        Reason:    pgxNullString(reason),
        ActorID:   pgxNullUUID(&actor),
        Payload:   jsonbFrom(extra),
    }); err != nil {
        return nil, err
    }
    if err := s.outb.Emit(ctx, tx, outbox.Event{
        Kind: "order.state.changed",
        Key:  string(id),
        Payload: map[string]any{
            "order_id": id, "from": from, "to": to, "reason": reason,
        },
    }); err != nil {
        return nil, err
    }
    return orderFromRow(row), nil
}
```

### Cancel

```go
func (s *service) Cancel(ctx context.Context, id core.OrderID, reason string) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.OrderGet(ctx, id.UUID())
        if err != nil {
            return ErrNotFound
        }
        switch OrderState(cur.State) {
        case StateDraft, StateReady, StateAllocating:
            // ok
        default:
            return ErrCancellationBlocked
        }
        now := s.clock.Now()
        _, err = s.transitionState(ctx, tx,
            id, core.SellerIDFromUUID(cur.SellerID),
            OrderState(cur.State), StateCancelled,
            reason, actorFromCtx(ctx),
            map[string]any{"reason": reason},
            sqlcgen.OrderTransitionStateParams{
                CancelledAt:     pgxNullTimestamp(now),
                CancelledReason: pgxNullString(reason),
            })
        if err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(cur.SellerID),
            Action:   "order.cancelled",
            Object:   audit.ObjOrder(id),
            Payload:  map[string]any{"reason": reason},
        })
    })
}
```

### Bulk CSV Import

```go
package orders

func (s *service) BulkImportCSV(ctx context.Context, sellerID core.SellerID, req CSVImportRequest) (*ImportJob, error) {
    if err := s.lifecycleCache.AssertActive(ctx, sellerID); err != nil {
        return nil, err
    }
    if _, ok := csvSchemas[req.SchemaName]; !ok {
        return nil, fmt.Errorf("orders: unknown CSV schema %q", req.SchemaName)
    }
    var job *ImportJob
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.OrderImportJobInsert(ctx, sqlcgen.OrderImportJobInsertParams{
            ID:           core.NewImportJobID().UUID(),
            SellerID:     sellerID.UUID(),
            UploadedBy:   req.UploadedByUserID.UUID(),
            UploadID:     req.UploadID,
            SchemaName:   req.SchemaName,
            DryRun:       req.DryRun,
        })
        if err != nil {
            return err
        }
        job = importJobFromRow(row)

        // Enqueue the river job that will stream the CSV from S3
        // and call s.processImportRow for each row.
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "order.import.queued",
            Key:  string(job.ID),
            Payload: map[string]any{
                "import_job_id": job.ID,
                "seller_id":     sellerID,
                "upload_id":     req.UploadID,
                "schema_name":   req.SchemaName,
                "dry_run":       req.DryRun,
            },
        })
    })
    return job, err
}

// CSVImportWorker is the river worker that runs the actual import.
// Streams rows from S3, validates each via the schema, calls Service.Create
// for each successful row. Updates ImportJob progress every N rows.

type CSVImportWorker struct {
    river.WorkerDefaults[CSVImportJob]
    svc Service
    s3  ObjectStore
}

const importBatchSize = 100

func (w *CSVImportWorker) Work(ctx context.Context, j *river.Job[CSVImportJob]) error {
    args := j.Args
    schema := csvSchemas[args.SchemaName]

    // Mark running
    if err := w.markRunning(ctx, args.ImportJobID); err != nil {
        return err
    }

    rdr, err := w.s3.Open(ctx, args.UploadID)
    if err != nil {
        return w.markFailed(ctx, args.ImportJobID, fmt.Sprintf("open upload: %v", err))
    }
    defer rdr.Close()

    csvr := csv.NewReader(rdr)
    csvr.ReuseRecord = true   // amortize allocations on large files

    headers, err := csvr.Read()
    if err != nil {
        return w.markFailed(ctx, args.ImportJobID, fmt.Sprintf("read headers: %v", err))
    }
    if err := schema.Validate(headers); err != nil {
        return w.markFailed(ctx, args.ImportJobID, err.Error())
    }

    var rowsTotal, rowsSucceeded, rowsFailed int
    var errReport []ImportRowError

    for {
        rec, err := csvr.Read()
        if err == io.EOF {
            break
        }
        if err != nil {
            errReport = append(errReport, ImportRowError{RowNo: rowsTotal + 1, Errors: []string{err.Error()}})
            rowsFailed++
            rowsTotal++
            continue
        }
        rowsTotal++

        createReq, validationErrs := schema.ToCreateRequest(args.SellerID, rec)
        if len(validationErrs) > 0 {
            errReport = append(errReport, ImportRowError{RowNo: rowsTotal, Errors: validationErrs})
            rowsFailed++
            continue
        }
        if args.DryRun {
            rowsSucceeded++
            continue
        }
        if _, err := w.svc.Create(ctx, createReq); err != nil {
            errReport = append(errReport, ImportRowError{RowNo: rowsTotal, Errors: []string{err.Error()}})
            rowsFailed++
            continue
        }
        rowsSucceeded++

        if rowsTotal%importBatchSize == 0 {
            if err := w.updateProgress(ctx, args.ImportJobID, rowsTotal, rowsSucceeded, rowsFailed, errReport); err != nil {
                slog.Warn("orders: import progress update failed", "err", err)
            }
        }
    }

    finalState := "succeeded"
    if rowsFailed > 0 && rowsSucceeded > 0 {
        finalState = "partial"
    } else if rowsFailed > 0 {
        finalState = "failed"
    }
    return w.markFinal(ctx, args.ImportJobID, finalState, rowsTotal, rowsSucceeded, rowsFailed, errReport)
}
```

### CSV Schema Plug-in Pattern

```go
package orders

// CSVSchema lets us add new CSV variants (Shopify export, custom seller
// templates) without touching the worker.
type CSVSchema interface {
    Name() string
    RequiredHeaders() []string
    Validate(headers []string) error
    ToCreateRequest(sellerID core.SellerID, row []string) (CreateRequest, []string)
}

var csvSchemas = map[string]CSVSchema{
    "default":         &defaultSchema{},
    "shopify_export":  &shopifyExportSchema{},
}
```

The `defaultSchema` is the canonical mapping and is the one we point sellers to. Channel-specific schemas (Shopify export, Amazon seller central export) live in this same package and are tested against fixture files in `testdata/csv/`.

## Outbox Event Payloads

```go
type CreatedPayload struct {
    SchemaVersion int                `json:"schema_version"` // = 1
    OrderID       string             `json:"order_id"`
    SellerID      string             `json:"seller_id"`
    Channel       string             `json:"channel"`
    State         string             `json:"state"`
    PaymentMethod string             `json:"payment_method"`
    TotalPaise    int64              `json:"total_paise"`
    PickupLocID   string             `json:"pickup_location_id"`
    Pincode       string             `json:"shipping_pincode"`
    WeightG       int                `json:"package_weight_g"`
    OccurredAt    time.Time          `json:"occurred_at"`
}

type StateChangedPayload struct {
    SchemaVersion int                `json:"schema_version"`
    OrderID       string             `json:"order_id"`
    SellerID      string             `json:"seller_id"`
    From          string             `json:"from"`
    To            string             `json:"to"`
    Reason        string             `json:"reason,omitempty"`
    OccurredAt    time.Time          `json:"occurred_at"`
}
```

Outbox forwarder routes:
- `order.created` (state=ready) → `allocation.AllocateJob` (with `unique_args: order_id`)
- `order.state.changed (to=ready)` → `allocation.AllocateJob`
- `order.state.changed (to=cancelled)` → `wallet.RefundReserveJob` if any reserve exists

## Idempotency

Order creation is exposed via two HTTP endpoints:
- `POST /api/orders` — manual creation; idempotency-key middleware enforced.
- `POST /api/channels/:channel/webhooks` — channel adapters; dedupe via `(seller_id, channel, channel_order_id)` UNIQUE.

The middleware (LLD §03-services/04-idempotency) hashes the body and stores `idempotency_key`; on retry with same key + same body returns the cached response. Same key + different body returns 409.

## Testing

### Unit Tests

```go
func TestValidateCreateRequest_TotalsMismatch(t *testing.T) {
    r := goodCreateRequest()
    r.TotalPaise = r.SubtotalPaise + 1 // off by one paisa
    err := validateCreateRequest(r)
    require.ErrorIs(t, err, ErrTotalsMismatch)
}

func TestValidateAddress_Pincode(t *testing.T) {
    a := goodAddress()
    a.Pincode = "012345" // invalid leading zero
    err := validateAddress(a, "shipping")
    require.ErrorIs(t, err, ErrInvalidPincode)
}

func TestStateTransitions(t *testing.T) {
    cases := []struct {
        from, to OrderState
        ok       bool
    }{
        {StateDraft, StateReady, true},
        {StateReady, StateCancelled, true},
        {StateBooked, StateCancelled, true},
        {StateInTransit, StateCancelled, false},
        {StateClosed, StateRTO, false},
    }
    for _, c := range cases {
        if got := canTransition(c.from, c.to); got != c.ok {
            t.Errorf("%s -> %s: got %v want %v", c.from, c.to, got, c.ok)
        }
    }
}
```

### SLT (`service_slt_test.go`)

```go
func TestCreate_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    s := slt.NewSeller(t, pg)
    pl := slt.NewPickupLocation(t, pg, s.ID)

    ord, err := slt.Orders(pg).Create(ctx, CreateRequest{
        SellerID:        s.ID,
        Channel:         "manual",
        BuyerName:       "Test Buyer",
        BuyerPhone:      "+919999999999",
        ShippingAddress: slt.GoodAddress(),
        BillingAddress:  slt.GoodAddress(),
        Lines:           []OrderLineInput{{SKU: "ABC", Name: "Widget", Quantity: 1, UnitPricePaise: 10000, UnitWeightG: 200}},
        PaymentMethod:   "prepaid",
        SubtotalPaise:   10000, TotalPaise: 10000,
        PickupLocationID: pl.ID,
        PackageWeightG:   200,
        PackageLengthMM:  100, PackageWidthMM: 100, PackageHeightMM: 100,
    })
    require.NoError(t, err)
    require.Equal(t, StateReady, ord.State)
    require.True(t, slt.OutboxHas(t, pg, "order.created", string(ord.ID)))
}

func TestCreate_DuplicateChannelOrder_SLT(t *testing.T) {
    // Same (seller, channel="shopify", channel_order_id="X") returns
    // the existing record without inserting again.
    // ...
}

func TestCancel_FromReady_SLT(t *testing.T) { /* ... */ }
func TestCancel_FromInTransit_BlocksError_SLT(t *testing.T) { /* ... */ }
func TestUpdate_RestrictedFieldsAfterAllocating_SLT(t *testing.T) { /* ... */ }
func TestList_TrigramSearch_SLT(t *testing.T) { /* ... */ }
func TestRLSIsolation_SLT(t *testing.T) { /* ... */ }

func TestCSVImport_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    s := slt.NewSeller(t, pg)
    csvBytes, err := os.ReadFile("testdata/csv/default_100rows.csv")
    require.NoError(t, err)
    objKey := slt.PutObject(t, csvBytes)

    job, err := slt.Orders(pg).BulkImportCSV(ctx, s.ID, CSVImportRequest{
        UploadedByUserID: s.OwnerUserID,
        UploadID:         objKey,
        SchemaName:       "default",
    })
    require.NoError(t, err)

    slt.WaitForImportJob(t, pg, job.ID, "succeeded", 30*time.Second)
    finished, _ := slt.Orders(pg).GetImportJob(ctx, job.ID)
    require.Equal(t, 100, finished.RowsSucceeded)
    require.Equal(t, 0, finished.RowsFailed)
}

func TestCSVImport_InvalidRows_SLT(t *testing.T) {
    // Mix of valid + invalid rows. Final state should be 'partial' with
    // error_report populated.
    // ...
}
```

### Microbenchmarks

```go
func BenchmarkValidateCreateRequest(b *testing.B) {
    req := goodCreateRequest()
    for i := 0; i < b.N; i++ {
        _ = validateCreateRequest(req)
    }
}
// Target: < 1.5 µs, 0 allocs (regex pre-compiled).

func BenchmarkCanTransition(b *testing.B) {
    for i := 0; i < b.N; i++ {
        _ = canTransition(StateReady, StateAllocating)
    }
}
// Target: < 20 ns, 0 allocs.
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Create` (no dedupe hit) | 6 ms | 18 ms | 1 INSERT order + N INSERT lines + 1 state event + outbox |
| `Create` (dedupe hit) | 1 ms | 3 ms | 1 SELECT |
| `Get` | 0.6 ms | 2 ms | 1 SELECT by PK |
| `List` (default page=50) | 8 ms | 30 ms | composite index covers sort |
| `List` (with trigram search) | 25 ms | 90 ms | gin_trgm scan; degrades if seller has > 500k orders |
| `Cancel` | 4 ms | 12 ms | UPDATE + state event + audit + outbox |
| `BulkImport` per row | 3 ms | 8 ms | reusing one prepared statement chain per worker |
| `BulkImport` 10k rows total | 30 s | 90 s | bottleneck is per-row INSERT; future: COPY |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Channel webhook duplicate | UNIQUE violation on `order_channel_unique` | Map to `ErrChannelDuplicate`, return 200 with existing order (idempotent webhook). |
| Pickup location belongs to another seller | RLS returns 0 rows in catalog.GetPickupLocation | Map to `ErrPickupLocationInvalid`, return 422. |
| Seller suspended | `lifecycleCache.AssertActive` returns `seller.ErrSuspended` | Return 403. |
| Address pincode invalid | regex check | Return 422 with field path. |
| CSV row N invalid | per-row validation | Continue processing other rows; record in error_report; final state `partial`. |
| Race: state changed between read and update | `OrderTransitionState` returns 0 rows | Return `ErrInvalidState`; client retries. |
| S3 fetch fails for CSV | reader.Open error | Mark import_job `failed` with reason; do not retry automatically (operator action). |
| River queue down during BulkImport | outbox forwarder retries with backoff | Job runs eventually; ImportJob stays `queued`. |
| Trigram search too slow on huge sellers | p99 > 1s | Fallback: paginate without search; require date range filter for sellers > 200k orders. |

## Open Questions

1. **Splits / multi-shipment orders.** Today an order = a shipment (one parcel). Multi-package orders are deferred. When added, add `order_package` table with FK to `order_record`; `awb_number` moves to `order_package`.
2. **Editing closed orders.** Compliance may require editing addresses post-delivery for invoice corrections. **Decision: Operator-only via admin interface, audited; not exposed in seller API.**
3. **Soft delete vs. archive.** Cancelled orders accumulate; partition by `created_at` quarter at v1+ if `order_record` exceeds 100M rows.
4. **CSV via `COPY` instead of per-row INSERT.** ~10× faster for large imports. Deferred until imports become a bottleneck (target: > 10k rows/min throughput).
5. **Channel rate limiting.** Shopify can fire many webhooks per second during a flash sale. **Decision: rely on connection-pool backpressure for v0**; if it becomes a problem, add per-(seller, channel) token bucket in front of `Create`.

## References

- HLD §03-services/02-pricing: pricing reads order package + pincode for quotes.
- HLD §03-services/03-allocation: allocation consumes `order.state.changed (ready)` to begin selection.
- HLD §03-services/04-wallet-and-ledger: cancel triggers `wallet.RefundReserveJob` if a reserve exists.
- LLD §03-services/01-policy-engine: cancellation window, max line items.
- LLD §03-services/03-outbox: event emission contract.
- LLD §03-services/04-idempotency: API-level idempotency middleware.
- LLD §03-services/09-seller: lifecycle cache used for seller state checks.
- LLD §03-services/11-catalog: pickup locations + product catalog.
- LLD §02-infrastructure/04-http-server: handler wiring + error mapping.
