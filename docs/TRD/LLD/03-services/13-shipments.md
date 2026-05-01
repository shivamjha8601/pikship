# Shipments Service

## Purpose

The shipments service is the **booking engine**: it takes an allocated order and produces a shipment with the chosen carrier — an AWB, a label, and a manifest entry. It owns the **booking transaction protocol** that bridges our ACID database with the carrier's non-transactional API.

Responsibilities:

- Book shipments via carrier adapters using the **two-phase tx + reconcile** pattern (HLD §01-architecture/03-async-and-outbox, ADR 0006).
- Persist `shipment` records and emit lifecycle events.
- Generate / fetch / cache shipping labels.
- Build daily / on-demand **manifests** (the printable list of AWBs handed to the pickup agent).
- Cancel shipments (own-carrier-side).
- Drive the **reconcile cron** that sweeps `pending_carrier` shipments to recover from in-flight failures.

Out of scope (owned elsewhere):

- Choosing the carrier — allocation (LLD §03-services/07).
- Quoting price — pricing (LLD §03-services/06).
- Status updates after booking — tracking (LLD §03-services/14).
- COD remittance — COD service (LLD §03-services/16).
- Wallet debits — wallet (LLD §03-services/05); shipments calls `wallet.Confirm`/`wallet.Release`.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, money, clock. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/orders` | order lookup + `MarkBooked`/`MarkAllocating`. |
| `internal/wallet` | reserve/confirm/release for shipping fees. |
| `internal/pricing` | final quote at booking time. |
| `internal/carriers/framework` | `Adapter.Book`, `Adapter.Cancel`, `Adapter.FetchLabel`. |
| `internal/seller` + `internal/catalog` | pickup address resolution. |
| `internal/audit`, `internal/outbox`, `internal/idempotency` | standard. |

## Package Layout

```
internal/shipments/
├── service.go             // Service interface
├── service_impl.go        // Booking/cancel/label/manifest implementation
├── booking.go             // The two-phase booking protocol
├── reconcile.go           // Reconcile worker + helpers
├── labels.go              // Label fetch + cache
├── manifest.go            // Manifest build + PDF generation
├── repo.go                // sqlc wrapper
├── types.go               // Shipment, ShipmentState, Manifest
├── lifecycle.go           // State machine
├── errors.go
├── events.go              // Outbox payloads
├── jobs.go                // River job definitions
├── service_test.go
├── booking_test.go
├── reconcile_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
package shipments

type Service interface {
    // Book begins the two-phase booking flow for an allocated order.
    // Returns the shipment in `pending_carrier` state if Phase A
    // committed but the carrier call hasn't completed yet, or in
    // `booked` state if both phases completed synchronously.
    //
    // Idempotent on (order_id): re-calling for the same order returns
    // the existing shipment.
    Book(ctx context.Context, req BookRequest) (*Shipment, error)

    // Get returns the shipment.
    Get(ctx context.Context, id core.ShipmentID) (*Shipment, error)

    // GetByAWB returns the shipment by its AWB number. Used by tracking
    // webhook routers.
    GetByAWB(ctx context.Context, awb string) (*Shipment, error)

    // Cancel attempts to cancel the shipment with the carrier.
    // Allowed states: booked (always), in_transit (carrier-permitting).
    // On success, transitions shipment to `cancelled` and triggers
    // wallet refund.
    Cancel(ctx context.Context, id core.ShipmentID, req CancelRequest) error

    // FetchLabel returns the label bytes (PDF/ZPL). On first call, fetches
    // from carrier and caches in object storage; subsequent calls serve
    // from cache.
    FetchLabel(ctx context.Context, id core.ShipmentID, format string) (*Label, error)

    // BuildManifest creates a printable manifest covering shipments
    // booked since the last manifest for this (seller, pickup_location,
    // carrier). Idempotent on (seller_id, pickup_location_id, carrier_code, date).
    BuildManifest(ctx context.Context, req ManifestRequest) (*Manifest, error)

    // List supports the seller dashboard's shipments tab.
    List(ctx context.Context, q ListQuery) (ListResult, error)
}
```

### Request / Response Types

```go
type BookRequest struct {
    SellerID  core.SellerID
    OrderID   core.OrderID
    // AllocationDecisionID is the binding choice from allocation. The
    // shipments service does NOT re-run allocation; it trusts this
    // decision. If allocation needs to change, the caller cancels and
    // re-allocates explicitly.
    AllocationDecisionID core.AllocationDecisionID
    // OperatorID is set when an operator manually triggers a booking
    // from the ops console. Audited.
    OperatorID *core.UserID
}

type CancelRequest struct {
    Reason     string
    OperatorID *core.UserID
}

type ManifestRequest struct {
    SellerID         core.SellerID
    PickupLocationID core.PickupLocationID
    CarrierCode      string
    Date             time.Time      // local YYYY-MM-DD; defaults to today
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("shipment: not found")
    ErrInvalidState          = errors.New("shipment: invalid state for operation")
    ErrAlreadyBooked         = errors.New("shipment: order already has a booked shipment")
    ErrAllocationStale       = errors.New("shipment: allocation decision stale or invalid")
    ErrCarrierBookFailed     = errors.New("shipment: carrier rejected booking")
    ErrCarrierTransient      = errors.New("shipment: carrier call transient failure")
    ErrCancelNotPermitted    = errors.New("shipment: carrier does not permit cancellation in current state")
    ErrLabelUnavailable      = errors.New("shipment: label not yet available from carrier")
    ErrManifestEmpty         = errors.New("shipment: manifest has no shipments")
)
```

## Shipment State Machine

```
                     Book() Phase A
                          │
                          ▼
            ┌──────────────────────────┐
            │     pending_carrier      │  carrier call in flight or unconfirmed
            └────────┬───────────┬─────┘
                     │           │
        Phase B ok   │           │  carrier returned ErrCarrierBookFailed
                     ▼           ▼
            ┌──────────────┐  ┌────────────┐
            │   booked     │  │ failed     │  terminal
            └──────┬───────┘  └────────────┘
                   │  carrier reports first event
                   ▼
            ┌──────────────────────────┐
            │       in_transit         │
            └────────┬─────────────────┘
                     │
              ┌──────┴──────┐
              ▼             ▼
       delivered          rto_in_progress
              │             │
              ▼             ▼
         (closed)         rto_completed

Cancel paths:
  pending_carrier (after grace) → cancelled
  booked                        → cancelled (carrier-permitting)
  in_transit                    → cancelled (rare; carrier-permitting)
```

```go
type ShipmentState string

const (
    StatePendingCarrier   ShipmentState = "pending_carrier"
    StateBooked           ShipmentState = "booked"
    StateInTransit        ShipmentState = "in_transit"
    StateDelivered        ShipmentState = "delivered"
    StateRTOInProgress    ShipmentState = "rto_in_progress"
    StateRTOCompleted     ShipmentState = "rto_completed"
    StateCancelled        ShipmentState = "cancelled"
    StateFailed           ShipmentState = "failed"
)

var allowedTransitions = map[ShipmentState]map[ShipmentState]struct{}{
    StatePendingCarrier:   {StateBooked: {}, StateFailed: {}, StateCancelled: {}},
    StateBooked:           {StateInTransit: {}, StateCancelled: {}, StateRTOInProgress: {}},
    StateInTransit:        {StateDelivered: {}, StateRTOInProgress: {}, StateCancelled: {}},
    StateDelivered:        {StateRTOInProgress: {}}, // delivered-but-undeliverable COD
    StateRTOInProgress:    {StateRTOCompleted: {}},
    StateRTOCompleted:     {},
    StateCancelled:        {},
    StateFailed:           {},
}
```

## DB Schema

```sql
CREATE TABLE shipment (
    id                       uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id                uuid        NOT NULL REFERENCES seller(id),
    order_id                 uuid        NOT NULL REFERENCES order_record(id),
    allocation_decision_id   uuid        NOT NULL REFERENCES allocation_decision(id),

    state                    text        NOT NULL CHECK (state IN
        ('pending_carrier','booked','in_transit','delivered','rto_in_progress','rto_completed','cancelled','failed')),

    carrier_code             text        NOT NULL,
    service_type             text        NOT NULL,

    -- Set at Phase B (carrier confirmation)
    awb                      text,
    carrier_shipment_id      text,
    estimated_delivery_at    timestamptz,
    booked_at                timestamptz,

    -- Charges captured at booking time (immutable post-booking)
    charges_paise            bigint      NOT NULL DEFAULT 0,
    cod_amount_paise         bigint      NOT NULL DEFAULT 0,

    -- Carrier-side ref + last error (for failed/pending_carrier rows)
    carrier_request_id       text,        -- our idempotency token sent to carrier (if supported)
    last_carrier_error       text,
    last_attempt_at          timestamptz,
    attempt_count            integer     NOT NULL DEFAULT 0,

    -- Pickup metadata snapshot (immutable post-booking)
    pickup_location_id       uuid        NOT NULL,
    pickup_address_snapshot  jsonb       NOT NULL,

    -- Drop snapshot (immutable post-booking)
    drop_address_snapshot    jsonb       NOT NULL,
    drop_pincode             text        NOT NULL,

    package_weight_g         integer     NOT NULL,
    package_length_mm        integer     NOT NULL,
    package_width_mm         integer     NOT NULL,
    package_height_mm        integer     NOT NULL,

    cancelled_at             timestamptz,
    cancelled_reason         text,
    failed_at                timestamptz,
    failed_reason            text,

    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT shipment_one_per_order UNIQUE (order_id)  -- one active shipment per order; cancelled before re-book
);

CREATE INDEX shipment_seller_state_created_idx ON shipment(seller_id, state, created_at DESC);
CREATE UNIQUE INDEX shipment_awb_unique ON shipment(awb) WHERE awb IS NOT NULL;
CREATE INDEX shipment_pickup_carrier_date_idx
    ON shipment(seller_id, pickup_location_id, carrier_code, (booked_at::date))
    WHERE booked_at IS NOT NULL;
CREATE INDEX shipment_pending_carrier_idx
    ON shipment(last_attempt_at)
    WHERE state = 'pending_carrier';

ALTER TABLE shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY shipment_isolation ON shipment
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Shipment state events: append-only audit trail
CREATE TABLE shipment_state_event (
    id           bigserial   PRIMARY KEY,
    shipment_id  uuid        NOT NULL REFERENCES shipment(id),
    seller_id    uuid        NOT NULL REFERENCES seller(id),
    from_state   text        NOT NULL,
    to_state     text        NOT NULL,
    reason       text,
    actor_id     uuid,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX shipment_state_event_shipment_idx
    ON shipment_state_event(shipment_id, created_at);

ALTER TABLE shipment_state_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY shipment_state_event_isolation ON shipment_state_event
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Manifests
CREATE TABLE manifest (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id            uuid        NOT NULL REFERENCES seller(id),
    pickup_location_id   uuid        NOT NULL,
    carrier_code         text        NOT NULL,
    manifest_date        date        NOT NULL,
    state                text        NOT NULL CHECK (state IN ('open','closed')),
    object_storage_key   text,                       -- PDF location once generated
    shipment_count       integer     NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    closed_at            timestamptz,
    UNIQUE (seller_id, pickup_location_id, carrier_code, manifest_date)
);

ALTER TABLE manifest ENABLE ROW LEVEL SECURITY;
CREATE POLICY manifest_isolation ON manifest
    USING (seller_id = current_setting('app.seller_id')::uuid);

CREATE TABLE manifest_shipment (
    manifest_id  uuid        NOT NULL REFERENCES manifest(id) ON DELETE CASCADE,
    shipment_id  uuid        NOT NULL REFERENCES shipment(id),
    seller_id    uuid        NOT NULL REFERENCES seller(id),
    PRIMARY KEY (manifest_id, shipment_id)
);

ALTER TABLE manifest_shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY manifest_shipment_isolation ON manifest_shipment
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Labels (cached fetch results)
CREATE TABLE shipment_label (
    shipment_id   uuid        NOT NULL REFERENCES shipment(id) ON DELETE CASCADE,
    seller_id     uuid        NOT NULL REFERENCES seller(id),
    format        text        NOT NULL,
    object_key    text        NOT NULL,
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz,
    PRIMARY KEY (shipment_id, format)
);

ALTER TABLE shipment_label ENABLE ROW LEVEL SECURITY;
CREATE POLICY shipment_label_isolation ON shipment_label
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Grants
GRANT SELECT, INSERT, UPDATE ON
    shipment, shipment_state_event, manifest, manifest_shipment, shipment_label
TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE shipment_state_event_id_seq TO pikshipp_app;
GRANT SELECT ON
    shipment, shipment_state_event, manifest, manifest_shipment, shipment_label
TO pikshipp_reports;
```

## sqlc Queries

```sql
-- name: ShipmentInsertPending :one
INSERT INTO shipment (
    id, seller_id, order_id, allocation_decision_id,
    state, carrier_code, service_type,
    charges_paise, cod_amount_paise,
    pickup_location_id, pickup_address_snapshot,
    drop_address_snapshot, drop_pincode,
    package_weight_g, package_length_mm, package_width_mm, package_height_mm,
    carrier_request_id, last_attempt_at, attempt_count
) VALUES (
    $1, $2, $3, $4,
    'pending_carrier', $5, $6,
    $7, $8,
    $9, $10,
    $11, $12,
    $13, $14, $15, $16,
    $17, now(), 1
)
RETURNING *;

-- name: ShipmentGetByOrder :one
SELECT * FROM shipment WHERE order_id = $1;

-- name: ShipmentGet :one
SELECT * FROM shipment WHERE id = $1;

-- name: ShipmentGetByAWB :one
SELECT * FROM shipment WHERE awb = $1;

-- name: ShipmentMarkBooked :one
UPDATE shipment
SET state = 'booked',
    awb = $2,
    carrier_shipment_id = $3,
    estimated_delivery_at = $4,
    booked_at = now(),
    last_carrier_error = NULL,
    updated_at = now()
WHERE id = $1 AND state = 'pending_carrier'
RETURNING *;

-- name: ShipmentMarkFailed :one
UPDATE shipment
SET state = 'failed',
    failed_at = now(),
    failed_reason = $2,
    last_carrier_error = $2,
    updated_at = now()
WHERE id = $1 AND state = 'pending_carrier'
RETURNING *;

-- name: ShipmentRecordAttempt :exec
UPDATE shipment
SET attempt_count = attempt_count + 1,
    last_attempt_at = now(),
    last_carrier_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: ShipmentTransitionState :one
UPDATE shipment
SET state = $2,
    cancelled_at = COALESCE(sqlc.narg('cancelled_at'), cancelled_at),
    cancelled_reason = COALESCE(sqlc.narg('cancelled_reason'), cancelled_reason),
    updated_at = now()
WHERE id = $1 AND state = $3
RETURNING *;

-- name: ShipmentStateEventInsert :exec
INSERT INTO shipment_state_event (
    shipment_id, seller_id, from_state, to_state, reason, actor_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ShipmentListPendingForReconcile :many
-- Bypass RLS using BYPASSRLS background-job role; not seller-scoped.
SELECT * FROM shipment
WHERE state = 'pending_carrier'
  AND last_attempt_at < $1
ORDER BY last_attempt_at
LIMIT $2;

-- Manifests
-- name: ManifestUpsertOpen :one
INSERT INTO manifest (
    id, seller_id, pickup_location_id, carrier_code, manifest_date, state
) VALUES ($1, $2, $3, $4, $5, 'open')
ON CONFLICT (seller_id, pickup_location_id, carrier_code, manifest_date) DO UPDATE
    SET state = 'open' -- keeps existing shipments
RETURNING *;

-- name: ManifestAddShipment :exec
INSERT INTO manifest_shipment (manifest_id, shipment_id, seller_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ManifestClose :one
UPDATE manifest
SET state = 'closed',
    object_storage_key = $2,
    shipment_count = $3,
    closed_at = now()
WHERE id = $1
RETURNING *;

-- name: ManifestShipmentsForBuild :many
SELECT s.* FROM shipment s
WHERE s.seller_id = $1
  AND s.pickup_location_id = $2
  AND s.carrier_code = $3
  AND s.state = 'booked'
  AND s.booked_at::date = $4
  AND NOT EXISTS (
      SELECT 1 FROM manifest_shipment ms WHERE ms.shipment_id = s.id
  );

-- Labels
-- name: ShipmentLabelGet :one
SELECT * FROM shipment_label
WHERE shipment_id = $1 AND format = $2 AND (expires_at IS NULL OR expires_at > now());

-- name: ShipmentLabelUpsert :exec
INSERT INTO shipment_label (shipment_id, seller_id, format, object_key, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (shipment_id, format) DO UPDATE
    SET object_key = EXCLUDED.object_key,
        expires_at = EXCLUDED.expires_at,
        fetched_at = now();
```

## The Two-Phase Booking Protocol

This is the **most important piece** of this LLD. It's the pattern for any DB → external-mutation → DB call in the codebase.

### Phase A — Reserve & Persist Pending

```go
package shipments

func (s *service) Book(ctx context.Context, req BookRequest) (*Shipment, error) {
    // 1. Idempotency: if a shipment already exists for this order, return it.
    if existing, err := s.q.ShipmentGetByOrder(ctx, req.OrderID.UUID()); err == nil {
        return shipmentFromRow(existing), nil
    } else if !errors.Is(err, pgx.ErrNoRows) {
        return nil, err
    }

    // 2. Hydrate inputs (read-only) BEFORE the tx, so we keep tx short.
    decision, err := s.allocation.GetDecision(ctx, req.AllocationDecisionID)
    if err != nil {
        return nil, fmt.Errorf("%w: %v", ErrAllocationStale, err)
    }
    if decision.OrderID != req.OrderID || decision.SellerID != req.SellerID {
        return nil, ErrAllocationStale
    }
    if time.Since(decision.CreatedAt) > 30*time.Minute {
        // Allocation decisions are stale after 30 min — re-allocate.
        return nil, ErrAllocationStale
    }

    order, err := s.orders.Get(ctx, req.OrderID)
    if err != nil {
        return nil, err
    }
    if order.State != orders.StateAllocating && order.State != orders.StateReady {
        return nil, fmt.Errorf("%w: order state %s", ErrInvalidState, order.State)
    }

    pickup, err := s.catalog.GetPickupLocation(ctx, req.SellerID, order.PickupLocationID)
    if err != nil {
        return nil, err
    }

    quote, err := s.pricing.QuoteForBooking(ctx, pricing.QuoteForBookingRequest{
        SellerID:     req.SellerID,
        OrderID:      req.OrderID,
        CarrierCode:  decision.CarrierCode,
        ServiceType:  decision.ServiceType,
        OriginPin:    pickup.Address.Pincode,
        DestPin:      order.ShippingAddress.Pincode,
        WeightG:      order.PackageWeightG,
        // ... dimensions, COD, declared value
    })
    if err != nil {
        return nil, fmt.Errorf("shipments: pricing: %w", err)
    }

    // 3. PHASE A — single tx that:
    //    a. Reserves wallet for charges_paise.
    //    b. Inserts shipment row with state=pending_carrier.
    //    c. Marks order=allocating (idempotent).
    //    d. Emits booking-requested outbox event for visibility.
    //    Carrier API IS NOT CALLED HERE.
    var shipment *Shipment
    var carrierRequestID string
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        // a. Wallet reserve
        if _, err := s.wallet.ReserveInTx(ctx, tx, wallet.ReserveRequest{
            SellerID:    req.SellerID,
            AmountPaise: quote.TotalPaise,
            RefType:     "shipment",
            RefID:       req.OrderID.String(),
            Direction:   wallet.DirectionDebit,
            Reason:      "shipping_charges_reserve",
        }); err != nil {
            return fmt.Errorf("shipments: wallet reserve: %w", err)
        }

        // b. Insert shipment
        carrierRequestID = generateCarrierRequestID(req.OrderID, decision.CarrierCode)
        row, err := qtx.ShipmentInsertPending(ctx, sqlcgen.ShipmentInsertPendingParams{
            ID:                    core.NewShipmentID().UUID(),
            SellerID:              req.SellerID.UUID(),
            OrderID:               req.OrderID.UUID(),
            AllocationDecisionID:  req.AllocationDecisionID.UUID(),
            CarrierCode:           decision.CarrierCode,
            ServiceType:           decision.ServiceType,
            ChargesPaise:          int64(quote.TotalPaise),
            CODAmountPaise:        int64(order.CODAmountPaise),
            PickupLocationID:      pickup.ID.UUID(),
            PickupAddressSnapshot: jsonbFromAddress(pickup.Address),
            DropAddressSnapshot:   jsonbFromAddress(order.ShippingAddress),
            DropPincode:           order.ShippingAddress.Pincode,
            PackageWeightG:        int32(order.PackageWeightG),
            PackageLengthMM:       int32(order.PackageLengthMM),
            PackageWidthMM:        int32(order.PackageWidthMM),
            PackageHeightMM:       int32(order.PackageHeightMM),
            CarrierRequestID:      carrierRequestID,
        })
        if err != nil {
            var pgErr *pgconn.PgError
            if errors.As(err, &pgErr) && pgErr.ConstraintName == "shipment_one_per_order" {
                return ErrAlreadyBooked
            }
            return err
        }
        shipment = shipmentFromRow(row)

        // c. Order state event (allocating, idempotent)
        if err := s.orders.MarkAllocatingInTx(ctx, tx, req.OrderID); err != nil {
            return err
        }

        // d. Outbox event
        if err := s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.booking.requested",
            Key:  string(shipment.ID),
            Payload: map[string]any{
                "shipment_id": shipment.ID, "carrier": decision.CarrierCode,
            },
        }); err != nil {
            return err
        }
        // Audit
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "shipment.booking.requested",
            Object:   audit.ObjShipment(shipment.ID),
            Payload:  map[string]any{"carrier": decision.CarrierCode, "charges_paise": quote.TotalPaise},
        })
    })
    if err != nil {
        return nil, err
    }

    // 4. PHASE B — call carrier OUTSIDE any tx.
    return s.callCarrierAndCommit(ctx, shipment, decision, order, pickup, quote, carrierRequestID)
}
```

### Phase B — Call Carrier and Commit

```go
func (s *service) callCarrierAndCommit(
    ctx context.Context,
    shipment *Shipment,
    decision *allocation.Decision,
    order *orders.Order,
    pickup *catalog.PickupLocation,
    quote *pricing.QuoteForBooking,
    carrierRequestID string,
) (*Shipment, error) {
    a, err := s.carriers.Get(decision.CarrierCode)
    if err != nil {
        return nil, err
    }

    // Per-call timeout configurable per carrier (defaults: 25s).
    ctx, cancel := context.WithTimeout(ctx, s.cfg.CarrierBookTimeout)
    defer cancel()

    res := a.Book(ctx, framework.BookRequest{
        SellerID:           shipment.SellerID,
        ShipmentID:         shipment.ID,
        PickupAddress:      pickup.Address,
        DropAddress:        order.ShippingAddress,
        PackageWeightG:     order.PackageWeightG,
        PackageDimensions:  order.Dimensions(),
        PaymentMode:        order.PaymentMode(),
        CODAmountPaise:     order.CODAmountPaise,
        DeclaredValuePaise: order.TotalPaise,
        ServiceType:        framework.ServiceType(decision.ServiceType),
        QuoteRef:           quote.QuoteRef,
        // CarrierRequestID inserted into adapter-specific idempotency
        // header / field where supported.
    })

    // Phase B commit decisions:
    if res.OK {
        return s.commitBookingSuccess(ctx, shipment, res.Value, quote)
    }

    // Failure path. Distinguish permanent vs transient.
    if res.Retryable {
        // Transient: stay in pending_carrier; reconcile cron will retry.
        // Update last_attempt_at + carrier_error so we don't hammer.
        if err := s.recordTransientFailure(ctx, shipment.ID, res); err != nil {
            slog.Warn("shipments: record transient failure", "err", err)
        }
        return nil, fmt.Errorf("%w: %v", ErrCarrierTransient, res.Err)
    }
    // Permanent: mark failed, release wallet reserve, return error.
    return s.commitBookingFailure(ctx, shipment, res)
}

func (s *service) commitBookingSuccess(
    ctx context.Context, shipment *Shipment, br framework.BookResponse, quote *pricing.QuoteForBooking,
) (*Shipment, error) {
    var out *Shipment
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ShipmentMarkBooked(ctx, sqlcgen.ShipmentMarkBookedParams{
            ID:                  shipment.ID.UUID(),
            AWB:                 br.AWB,
            CarrierShipmentID:   br.CarrierShipmentID,
            EstimatedDeliveryAt: br.EstimatedDelivery,
        })
        if err != nil {
            // Race: shipment is no longer in pending_carrier.
            // This means reconcile got it first. Refetch to know status.
            if errors.Is(err, pgx.ErrNoRows) {
                refreshed, _ := s.q.ShipmentGet(ctx, shipment.ID.UUID())
                out = shipmentFromRow(refreshed)
                return nil
            }
            return err
        }
        out = shipmentFromRow(row)

        // Wallet confirm (debit committed)
        if err := s.wallet.ConfirmInTx(ctx, tx, wallet.ConfirmRequest{
            SellerID:    shipment.SellerID,
            RefType:     "shipment",
            RefID:       shipment.OrderID.String(),
            Direction:   wallet.DirectionDebit,
        }); err != nil {
            return err
        }

        // Order: MarkBooked
        if err := s.orders.MarkBookedInTx(ctx, tx, shipment.OrderID, orders.BookedRef{
            ShipmentID:  shipment.ID,
            AWB:         br.AWB,
            CarrierCode: shipment.CarrierCode,
        }); err != nil {
            return err
        }

        // State event
        if err := qtx.ShipmentStateEventInsert(ctx, sqlcgen.ShipmentStateEventInsertParams{
            ShipmentID: shipment.ID.UUID(),
            SellerID:   shipment.SellerID.UUID(),
            FromState:  string(StatePendingCarrier),
            ToState:    string(StateBooked),
            Reason:     pgxNullString("carrier_confirmed"),
            Payload:    jsonbFrom(map[string]any{"awb": br.AWB}),
        }); err != nil {
            return err
        }

        if err := s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.booked",
            Key:  string(shipment.ID),
            Payload: map[string]any{
                "shipment_id": shipment.ID, "awb": br.AWB,
                "carrier": shipment.CarrierCode, "order_id": shipment.OrderID,
            },
        }); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: shipment.SellerID,
            Action:   "shipment.booked",
            Object:   audit.ObjShipment(shipment.ID),
            Payload:  map[string]any{"awb": br.AWB, "carrier": shipment.CarrierCode},
        })
    })
    return out, err
}

func (s *service) commitBookingFailure(
    ctx context.Context, shipment *Shipment, res framework.Result[framework.BookResponse],
) (*Shipment, error) {
    var out *Shipment
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ShipmentMarkFailed(ctx, sqlcgen.ShipmentMarkFailedParams{
            ID:           shipment.ID.UUID(),
            FailedReason: errMsg(res),
        })
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                refreshed, _ := s.q.ShipmentGet(ctx, shipment.ID.UUID())
                out = shipmentFromRow(refreshed)
                return nil
            }
            return err
        }
        out = shipmentFromRow(row)

        // Wallet release: refund the reservation.
        if err := s.wallet.ReleaseInTx(ctx, tx, wallet.ReleaseRequest{
            SellerID:  shipment.SellerID,
            RefType:   "shipment",
            RefID:     shipment.OrderID.String(),
            Direction: wallet.DirectionDebit,
            Reason:    "shipment_booking_failed",
        }); err != nil {
            return err
        }

        // Order: revert to ready (so seller can retry / re-allocate)
        if err := s.orders.RevertToReadyInTx(ctx, tx, shipment.OrderID); err != nil {
            return err
        }

        if err := qtx.ShipmentStateEventInsert(ctx, sqlcgen.ShipmentStateEventInsertParams{
            ShipmentID: shipment.ID.UUID(),
            SellerID:   shipment.SellerID.UUID(),
            FromState:  string(StatePendingCarrier),
            ToState:    string(StateFailed),
            Reason:     pgxNullString(errMsg(res)),
        }); err != nil {
            return err
        }
        if err := s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.booking.failed",
            Key:  string(shipment.ID),
            Payload: map[string]any{
                "shipment_id": shipment.ID, "reason": errMsg(res),
                "carrier": shipment.CarrierCode,
            },
        }); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: shipment.SellerID,
            Action:   "shipment.booking.failed",
            Object:   audit.ObjShipment(shipment.ID),
            Payload:  map[string]any{"reason": errMsg(res)},
        })
    })
    if err != nil {
        return nil, err
    }
    return out, fmt.Errorf("%w: %v", ErrCarrierBookFailed, res.Err)
}

func (s *service) recordTransientFailure(ctx context.Context, id core.ShipmentID, res framework.Result[framework.BookResponse]) error {
    return s.q.ShipmentRecordAttempt(ctx, sqlcgen.ShipmentRecordAttemptParams{
        ID:                id.UUID(),
        LastCarrierError:  pgxNullString(errMsg(res)),
    })
}
```

### Generating `carrier_request_id`

Carrier request IDs are our **client-generated idempotency tokens** that some carriers (Delhivery's PWBN flow, e.g.) accept. Even when the carrier doesn't support an explicit idempotency header, we still generate and persist one — used by the reconcile path's "have I already booked this?" probe (e.g., GET /shipment by client_ref).

```go
func generateCarrierRequestID(orderID core.OrderID, carrier string) string {
    // Stable across retries for the SAME (order, carrier) pair, so
    // re-attempts after transient failure use the same token.
    h := sha256.Sum256([]byte(string(orderID) + "|" + carrier))
    return "PSP-" + hex.EncodeToString(h[:8])
}
```

## Reconcile Worker

The reconcile cron is the **safety net** for Phase B failures (process crash, network partition, ambiguous timeout). Runs every minute.

```go
package shipments

type ReconcileJob struct{ river.JobArgs }
func (ReconcileJob) Kind() string { return "shipment.reconcile" }

type ReconcileWorker struct {
    river.WorkerDefaults[ReconcileJob]
    svc *service
}

func (w *ReconcileWorker) Work(ctx context.Context, j *river.Job[ReconcileJob]) error {
    return w.svc.RunReconcileSweep(ctx)
}

func (s *service) RunReconcileSweep(ctx context.Context) error {
    // Exponential backoff: minimum 1 minute since last attempt.
    cutoff := s.clock.Now().Add(-1 * time.Minute)
    rows, err := s.q.ShipmentListPendingForReconcile(ctx, sqlcgen.ShipmentListPendingForReconcileParams{
        LastAttemptBefore: cutoff,
        Limit:             100,
    })
    if err != nil {
        return err
    }
    for _, r := range rows {
        if err := s.reconcileOne(ctx, shipmentFromRow(r)); err != nil {
            slog.Warn("reconcile: failed", "shipment_id", r.ID, "err", err)
        }
    }
    return nil
}

func (s *service) reconcileOne(ctx context.Context, ship *Shipment) error {
    // Bound: after MaxBookAttempts (default 6), declare failed.
    if ship.AttemptCount >= s.cfg.MaxBookAttempts {
        s.logger.Warn("reconcile: max attempts reached, failing", "shipment_id", ship.ID)
        _, err := s.commitBookingFailure(ctx, ship, framework.Result[framework.BookResponse]{
            Err: errors.New("max_attempts_exceeded"),
            ErrorClass: framework.ErrCarrierUnavailable,
        })
        return err
    }

    // Step 1: probe — has the carrier ALREADY booked this from a previous
    // attempt? If we sent the same carrier_request_id last time and it
    // succeeded carrier-side but our process died before commit, the
    // carrier may have a record we can reattach to.
    a, err := s.carriers.Get(ship.CarrierCode)
    if err != nil {
        return err
    }
    probe, ok := a.(framework.ProbeBook)
    if ok {
        res := probe.ProbeBook(ctx, ship.CarrierRequestID)
        if res.OK && res.Value.AWB != "" {
            // Carrier did book it. Reattach.
            _, err := s.commitBookingSuccess(ctx, ship, res.Value, nil /* no quote refresh */)
            return err
        }
    }

    // Step 2: re-book.
    decision, _ := s.allocation.GetDecision(ctx, ship.AllocationDecisionID)
    order, _ := s.orders.GetSystem(ctx, ship.OrderID)
    pickup, _ := s.catalog.GetPickupLocationSystem(ctx, ship.SellerID, ship.PickupLocationID)

    res := a.Book(ctx, framework.BookRequest{
        // ... same as Phase B above, with carrier_request_id same as before
    })
    if res.OK {
        _, err := s.commitBookingSuccess(ctx, ship, res.Value, nil)
        return err
    }
    if res.Retryable {
        return s.recordTransientFailure(ctx, ship.ID, res)
    }
    _, err = s.commitBookingFailure(ctx, ship, res)
    return err
}
```

The optional `framework.ProbeBook` interface lets carriers expose "look up by client ref" support; carriers that don't implement it skip the probe step (we just retry).

## Cancel

```go
func (s *service) Cancel(ctx context.Context, id core.ShipmentID, req CancelRequest) error {
    cur, err := s.q.ShipmentGet(ctx, id.UUID())
    if err != nil {
        return ErrNotFound
    }

    state := ShipmentState(cur.State)
    switch state {
    case StateBooked, StateInTransit:
        // Try carrier cancel first
    case StatePendingCarrier:
        // Tricky: carrier may or may not have booked. Strategy: probe;
        // if booked, cancel; if not booked, mark failed + release wallet.
        return s.cancelPending(ctx, shipmentFromRow(cur), req)
    default:
        return fmt.Errorf("%w: state=%s", ErrInvalidState, state)
    }

    a, err := s.carriers.Get(cur.CarrierCode)
    if err != nil {
        return err
    }
    res := a.Cancel(ctx, framework.CancelRequest{
        SellerID: core.SellerIDFromUUID(cur.SellerID),
        AWB:      cur.AWB.String,
        Reason:   req.Reason,
    })
    if !res.OK {
        if res.ErrorClass == framework.ErrCarrierRefused {
            return ErrCancelNotPermitted
        }
        return fmt.Errorf("shipments: cancel: %w", res.Err)
    }

    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ShipmentTransitionState(ctx, sqlcgen.ShipmentTransitionStateParams{
            ID:               id.UUID(),
            State:            string(StateCancelled),
            FromState:        string(state),
            CancelledAt:      pgxNullTimestamp(s.clock.Now()),
            CancelledReason:  pgxNullString(req.Reason),
        })
        if err != nil {
            return err
        }
        _ = row // ensure compile
        // Wallet refund (full charges, since carrier accepted cancel)
        if err := s.wallet.PostInTx(ctx, tx, wallet.PostRequest{
            SellerID:    core.SellerIDFromUUID(cur.SellerID),
            AmountPaise: core.Paise(cur.ChargesPaise),
            RefType:     "shipment_cancel",
            RefID:       id.String(),
            Direction:   wallet.DirectionCredit,
            Reason:      "shipment_cancelled",
        }); err != nil {
            return err
        }
        if err := qtx.ShipmentStateEventInsert(ctx, sqlcgen.ShipmentStateEventInsertParams{
            ShipmentID: id.UUID(),
            SellerID:   cur.SellerID,
            FromState:  string(state),
            ToState:    string(StateCancelled),
            Reason:     pgxNullString(req.Reason),
        }); err != nil {
            return err
        }
        if err := s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.cancelled",
            Key:  string(id),
            Payload: map[string]any{"shipment_id": id, "reason": req.Reason},
        }); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(cur.SellerID),
            Action:   "shipment.cancelled",
            Object:   audit.ObjShipment(id),
            Payload:  map[string]any{"reason": req.Reason},
        })
    })
}
```

## Labels

```go
func (s *service) FetchLabel(ctx context.Context, id core.ShipmentID, format string) (*Label, error) {
    if format == "" {
        format = "pdf-a4"
    }
    if cached, err := s.q.ShipmentLabelGet(ctx, sqlcgen.ShipmentLabelGetParams{
        ShipmentID: id.UUID(),
        Format:     format,
    }); err == nil {
        bytes, err := s.objstore.Read(ctx, cached.ObjectKey)
        if err == nil {
            return &Label{Format: format, Bytes: bytes, Cached: true}, nil
        }
    }
    cur, err := s.q.ShipmentGet(ctx, id.UUID())
    if err != nil {
        return nil, ErrNotFound
    }
    if cur.State != string(StateBooked) && cur.State != string(StateInTransit) && cur.State != string(StateDelivered) {
        return nil, ErrLabelUnavailable
    }
    a, _ := s.carriers.Get(cur.CarrierCode)
    res := a.FetchLabel(ctx, framework.LabelRequest{
        SellerID: core.SellerIDFromUUID(cur.SellerID),
        AWB:      cur.AWB.String,
        Format:   format,
    })
    if !res.OK {
        return nil, fmt.Errorf("shipments: fetch label: %w", res.Err)
    }
    var bytes []byte
    if len(res.Value.Bytes) > 0 {
        bytes = res.Value.Bytes
    } else {
        bytes, err = s.objstore.FetchURL(ctx, res.Value.URL)
        if err != nil {
            return nil, err
        }
    }
    objKey := s.objstore.LabelKey(id, format)
    if err := s.objstore.Write(ctx, objKey, bytes); err != nil {
        return nil, err
    }
    if err := s.q.ShipmentLabelUpsert(ctx, sqlcgen.ShipmentLabelUpsertParams{
        ShipmentID: id.UUID(),
        SellerID:   cur.SellerID,
        Format:     format,
        ObjectKey:  objKey,
        ExpiresAt:  pgxNullTimestamp(s.clock.Now().Add(7*24*time.Hour)),
    }); err != nil {
        return nil, err
    }
    return &Label{Format: format, Bytes: bytes, Cached: false}, nil
}
```

## Manifests

```go
func (s *service) BuildManifest(ctx context.Context, req ManifestRequest) (*Manifest, error) {
    date := req.Date
    if date.IsZero() {
        date = s.clock.Now().In(time.Local).Truncate(24 * time.Hour)
    }
    var manifest *Manifest
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ManifestUpsertOpen(ctx, sqlcgen.ManifestUpsertOpenParams{
            ID:               core.NewManifestID().UUID(),
            SellerID:         req.SellerID.UUID(),
            PickupLocationID: req.PickupLocationID.UUID(),
            CarrierCode:      req.CarrierCode,
            ManifestDate:     date,
        })
        if err != nil {
            return err
        }
        manifest = manifestFromRow(row)

        // Find unmanifested booked shipments for the (seller, pickup, carrier, date)
        ships, err := qtx.ManifestShipmentsForBuild(ctx, sqlcgen.ManifestShipmentsForBuildParams{
            SellerID:         req.SellerID.UUID(),
            PickupLocationID: req.PickupLocationID.UUID(),
            CarrierCode:      req.CarrierCode,
            BookedDate:       date,
        })
        if err != nil {
            return err
        }
        if len(ships) == 0 && manifest.ShipmentCount == 0 {
            return ErrManifestEmpty
        }
        for _, sh := range ships {
            if err := qtx.ManifestAddShipment(ctx, sqlcgen.ManifestAddShipmentParams{
                ManifestID: manifest.ID.UUID(),
                ShipmentID: sh.ID,
                SellerID:   req.SellerID.UUID(),
            }); err != nil {
                return err
            }
        }
        manifest.ShipmentCount += len(ships)
        return nil
    })
    if err != nil {
        return nil, err
    }

    // Generate PDF outside the tx (bytes can be large)
    pdfBytes, err := s.renderManifestPDF(ctx, manifest)
    if err != nil {
        return nil, err
    }
    objKey := s.objstore.ManifestKey(manifest.ID)
    if err := s.objstore.Write(ctx, objKey, pdfBytes); err != nil {
        return nil, err
    }
    closed, err := s.q.ManifestClose(ctx, sqlcgen.ManifestCloseParams{
        ID:                manifest.ID.UUID(),
        ObjectStorageKey:  objKey,
        ShipmentCount:     int32(manifest.ShipmentCount),
    })
    if err != nil {
        return nil, err
    }
    return manifestFromRow(closed), nil
}
```

## Outbox Event Payloads

```go
type BookingRequestedPayload struct {
    SchemaVersion int       `json:"schema_version"` // = 1
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    CarrierCode   string    `json:"carrier_code"`
    OccurredAt    time.Time `json:"occurred_at"`
}

type BookedPayload struct {
    SchemaVersion int       `json:"schema_version"`
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    AWB           string    `json:"awb"`
    CarrierCode   string    `json:"carrier_code"`
    EstimatedDelivery time.Time `json:"estimated_delivery_at,omitempty"`
    OccurredAt    time.Time `json:"occurred_at"`
}

type BookingFailedPayload struct {
    SchemaVersion int       `json:"schema_version"`
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    Reason        string    `json:"reason"`
    OccurredAt    time.Time `json:"occurred_at"`
}

type CancelledPayload struct {
    SchemaVersion int       `json:"schema_version"`
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    Reason        string    `json:"reason"`
    OccurredAt    time.Time `json:"occurred_at"`
}
```

Forwarder routes:
- `shipment.booked` → `notifications.SendBookingConfirmationJob`
- `shipment.booking.failed` → `notifications.SendBookingFailedJob`
- `shipment.cancelled` → `notifications.SendCancellationJob` + `recon.OnCancelJob`
- `shipment.booked` → `tracking.RegisterAWBJob` (so tracking starts polling/listens for webhook)

## Testing

### Unit Tests

- `TestCanTransition` — covers the matrix.
- `TestGenerateCarrierRequestID_Stable` — same input → same token.
- `TestCommitBookingSuccess_Race_PendingCarrierAlreadyMoved` — when reconcile gets there first.

### SLT (`service_slt_test.go`)

```go
func TestBook_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    seed := slt.SeedFullStackForBooking(t, pg) // seller, order(ready), allocation
    sandbox := slt.SandboxCarrier(t, pg, "sb")
    sandbox.SetBookResult(framework.Result[framework.BookResponse]{
        OK: true,
        Value: framework.BookResponse{AWB: "SB123", CarrierShipmentID: "CSID-1"},
    })

    sh, err := slt.Shipments(pg).Book(ctx, BookRequest{
        SellerID:             seed.SellerID,
        OrderID:              seed.OrderID,
        AllocationDecisionID: seed.DecisionID,
    })
    require.NoError(t, err)
    require.Equal(t, StateBooked, sh.State)
    require.Equal(t, "SB123", sh.AWB)

    // Order is booked; wallet is debited; outbox has events
    o, _ := slt.Orders(pg).Get(ctx, seed.OrderID)
    require.Equal(t, "booked", string(o.State))

    bal, _ := slt.Wallet(pg).Balance(ctx, seed.SellerID)
    require.Equal(t, seed.InitialBalance - sh.ChargesPaise, bal)

    require.True(t, slt.OutboxHas(t, pg, "shipment.booked", string(sh.ID)))
}

func TestBook_PermanentFailure_SLT(t *testing.T) {
    // Carrier returns ErrInvalidInput. Shipment becomes 'failed';
    // wallet reserve is RELEASED (not debited); order stays 'ready'.
    pg := slt.StartPG(t)
    seed := slt.SeedFullStackForBooking(t, pg)
    sandbox := slt.SandboxCarrier(t, pg, "sb")
    sandbox.SetBookResult(framework.Result[framework.BookResponse]{
        OK: false, Err: errors.New("bad data"), ErrorClass: framework.ErrInvalidInput,
        Retryable: false,
    })
    _, err := slt.Shipments(pg).Book(ctx, BookRequest{
        SellerID: seed.SellerID, OrderID: seed.OrderID,
        AllocationDecisionID: seed.DecisionID,
    })
    require.ErrorIs(t, err, ErrCarrierBookFailed)

    sh, _ := slt.Shipments(pg).Repo().GetByOrder(ctx, seed.OrderID)
    require.Equal(t, StateFailed, sh.State)
    bal, _ := slt.Wallet(pg).Balance(ctx, seed.SellerID)
    require.Equal(t, seed.InitialBalance, bal) // refunded
}

func TestBook_TransientFailure_StaysPending_SLT(t *testing.T) {
    // Carrier returns ErrTimeout. Shipment becomes 'pending_carrier'
    // and reconcile picks it up later.
    pg := slt.StartPG(t)
    seed := slt.SeedFullStackForBooking(t, pg)
    sandbox := slt.SandboxCarrier(t, pg, "sb")
    sandbox.SetBookResult(framework.Result[framework.BookResponse]{
        OK: false, Err: errors.New("timeout"), ErrorClass: framework.ErrTimeout,
        Retryable: true,
    })
    _, err := slt.Shipments(pg).Book(ctx, BookRequest{
        SellerID: seed.SellerID, OrderID: seed.OrderID,
        AllocationDecisionID: seed.DecisionID,
    })
    require.ErrorIs(t, err, ErrCarrierTransient)

    sh, _ := slt.Shipments(pg).Repo().GetByOrder(ctx, seed.OrderID)
    require.Equal(t, StatePendingCarrier, sh.State)
    require.Equal(t, 1, sh.AttemptCount)

    // Now make sandbox succeed and run reconcile.
    sandbox.SetBookResult(framework.Result[framework.BookResponse]{
        OK: true, Value: framework.BookResponse{AWB: "SB-RECON"},
    })
    slt.AdvanceClock(2 * time.Minute)
    require.NoError(t, slt.Shipments(pg).Service().(*service).RunReconcileSweep(ctx))

    sh2, _ := slt.Shipments(pg).Repo().GetByOrder(ctx, seed.OrderID)
    require.Equal(t, StateBooked, sh2.State)
    require.Equal(t, "SB-RECON", sh2.AWB)
}

func TestBook_Idempotent_SameOrderTwice_SLT(t *testing.T) { /* ... */ }
func TestCancel_Booked_SLT(t *testing.T) { /* ... */ }
func TestCancel_PendingCarrier_NoCarrierBooking_SLT(t *testing.T) { /* ... */ }
func TestFetchLabel_FirstThenCached_SLT(t *testing.T) { /* ... */ }
func TestBuildManifest_HappyPath_SLT(t *testing.T) { /* ... */ }
func TestBuildManifest_Idempotent_AddsNewShipmentsOnRebuild_SLT(t *testing.T) { /* ... */ }
func TestReconcile_MaxAttempts_FailsShipment_SLT(t *testing.T) { /* ... */ }
func TestReconcile_ProbeReattaches_SLT(t *testing.T) { /* ... */ }
```

### Microbenchmarks

```go
func BenchmarkGenerateCarrierRequestID(b *testing.B) {
    oid := core.NewOrderID()
    for i := 0; i < b.N; i++ {
        _ = generateCarrierRequestID(oid, "delhivery")
    }
}
// Target: < 500 ns, 1 alloc.

func BenchmarkCanTransitionShipment(b *testing.B) {
    for i := 0; i < b.N; i++ {
        _ = canTransition(StateBooked, StateInTransit)
    }
}
// Target: < 20 ns.
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Book` Phase A (no carrier call) | 10 ms | 30 ms | wallet reserve + insert + outbox + audit |
| `Book` Phase B success (sync) | + carrier RTT | + carrier RTT + 8 ms | carrier-bound; tx is small |
| `Book` end-to-end (Delhivery prod p50) | 1.2 s | 3.5 s | dominated by carrier API |
| `Cancel` (booked) | + carrier RTT + 12 ms | + RTT + 30 ms | wallet refund + state event |
| `FetchLabel` (cache hit) | 6 ms | 15 ms | object store read |
| `FetchLabel` (cache miss) | 800 ms | 2.5 s | carrier fetch + object store write |
| `BuildManifest` (50 shipments) | 250 ms | 800 ms | PDF render dominates |
| `Reconcile` per shipment | 1.5 s | 4 s | carrier probe + maybe re-book |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Process crash between Phase A and Phase B | Shipment in `pending_carrier` after restart | Reconcile cron picks up; probes carrier; commits success or retries. |
| Carrier returns success but our commit-success tx fails (DB outage) | Inserted shipment in `pending_carrier`; carrier has booking | Reconcile probes; carrier returns same AWB; commitBookingSuccess re-runs. |
| Carrier API slow (network partition) | Per-call timeout fires | Treated as `ErrCarrierUnavailable` (transient); shipment stays pending; reconcile retries. |
| Same `carrier_request_id` re-sent and carrier creates two bookings | Detected by reconcile probing → finds two AWBs | Adapter MUST surface this; we then call `Cancel` on the duplicate. (Adapter responsibility; framework provides hooks.) |
| Wallet has insufficient balance at Phase A | `wallet.ReserveInTx` returns `wallet.ErrInsufficient` | Tx rolls back; Book returns 402 Payment Required to caller. |
| Allocation decision is stale | Decision created > 30 min ago | Return `ErrAllocationStale`; caller re-allocates. |
| Two operators try to book the same order | UNIQUE constraint `shipment_one_per_order` | Second tx fails; second caller sees `ErrAlreadyBooked`. |
| Order cancelled mid-Phase-B | Order state is `cancelled` when Phase B commits | `MarkBookedInTx` fails (state guard); commit fails; we then run cancel-on-carrier flow to undo. |
| Reconcile picks up a shipment 6 times | `AttemptCount >= MaxBookAttempts` | Force-fail with reason `max_attempts_exceeded`; alert ops. |
| Label fetch from carrier returns non-PDF garbage | We don't verify content-type | Object store stores it anyway; future: add content-type check. |

## Open Questions

1. **Booking debounce.** Two adapters could race on the same order if a UI client clicks "Book" twice. UNIQUE on `order_id` handles correctness, but the second user sees `ErrAlreadyBooked` instead of "shipment booked". **Decision: surface 200 with the existing shipment** (idempotent semantics in the handler layer, not the service).
2. **Manifest editing.** Once closed, manifests cannot be edited. Sellers occasionally need to remove a shipment that didn't physically get picked up. **Decision:** for v0, generate a new manifest the next day; revisit if it becomes a complaint.
3. **Label format negotiation.** Carriers vary on format support. Today we ask for the format the seller's printer wants; if the carrier doesn't support it, we error. **Decision:** acceptable for v0; future: framework-level conversion (PDF→ZPL).
4. **Phase B retries within a single request.** Currently, on transient failure, we return `ErrCarrierTransient` and rely on reconcile. Alternative: retry inline a few times before giving up. **Decision:** rely on reconcile; user-facing latency stays bounded.
5. **Cross-carrier failover at booking time.** If primary carrier fails permanently, automatically retry with secondary? **Decision: no for v0**. Failed booking returns to `ready`; allocation re-runs; user sees a different carrier. Auto-failover requires careful product semantics on cost/SLA changes.

## References

- HLD §01-architecture/03-async-and-outbox: two-phase tx pattern.
- HLD §01-architecture/05-decisions/0006-booking-two-tx (ADR).
- LLD §03-services/05-wallet: ReserveInTx / ConfirmInTx / ReleaseInTx contract.
- LLD §03-services/06-pricing: QuoteForBooking.
- LLD §03-services/07-allocation: GetDecision used to fetch allocation decision.
- LLD §03-services/10-orders: MarkAllocating / MarkBooked / RevertToReady contracts.
- LLD §03-services/12-carriers-framework: Adapter, Result, breaker, ProbeBook.
- LLD §03-services/14-tracking: receives `shipment.booked` to start tracking.
- LLD §02-infrastructure/04-http-server: handler wiring + idempotency key.
