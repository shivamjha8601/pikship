# RTO & Returns Service

## Purpose

This service owns the **inbound parcel flow**: parcels coming back to the seller from the buyer side, regardless of why. Two distinct flows live here:

1. **RTO (return-to-origin)** — the buyer never accepted the parcel. Trigger: NDR auto-RTO, buyer chose RTO, or carrier could not deliver. The parcel is in motion back to the pickup location.
2. **Return** — the buyer accepted the parcel but later sent it back (refund flow, customer initiated). Trigger: seller raises a return shipment from a delivered shipment.

Both share the same **inbound shipment lifecycle**: a separate AWB (often), a separate tracking-event stream, and a final reconciliation step that returns money to the buyer / debits seller / disposes of the parcel correctly.

Out of scope:

- The carrier-side reverse logistics (carrier's job).
- Refund payment to the buyer's card/UPI (sellers handle externally; we expose the data).
- Quality inspection of returned items (seller's warehouse decides what to do with the parcel after it arrives).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | money, IDs, errors, clock. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/shipments` | original shipment lookup; create return shipments. |
| `internal/tracking` | inbound shipment tracking events. |
| `internal/wallet` | refund-side credits/debits. |
| `internal/carriers/framework` | `Adapter.Book` for return shipments. |
| `internal/orders` | order lookup. |
| `internal/policy` | return window days, RTO charge policy. |
| `internal/audit`, `internal/outbox` | standard. |

## Package Layout

```
internal/returns/
├── service.go             // Service interface
├── service_impl.go
├── repo.go
├── types.go               // RTOTracking, ReturnShipment
├── lifecycle.go
├── jobs.go                // OnRTOInitiatedJob, ReturnReconcileJob
├── errors.go
├── events.go
├── service_test.go
└── service_slt_test.go
```

## Two Flows, Two State Machines

### RTO Lifecycle (initiated by carrier/system)

```
       shipment.rto.initiated event (from NDR or carrier-direct)
                       │
                       ▼
            ┌──────────────────────────┐
            │      rto_initiated       │
            └────────┬─────────────────┘
                     │  tracking event: rto_in_transit
                     ▼
            ┌──────────────────────────┐
            │     rto_in_transit       │
            └────────┬─────────────────┘
                     │  tracking event: rto_delivered (back at origin)
                     ▼
            ┌──────────────────────────┐
            │     rto_delivered        │
            └────────┬─────────────────┘
                     │  finance reconciliation
                     ▼
            ┌──────────────────────────┐
            │     rto_settled          │  charges adjusted; closed
            └──────────────────────────┘
```

### Return Lifecycle (seller-initiated)

```
   POST /returns      (from seller dashboard)
        │
        ▼
   ┌──────────────────────────┐
   │    return_requested      │  unbooked
   └────────┬─────────────────┘
            │  Book() with carrier
            ▼
   ┌──────────────────────────┐
   │    return_booked         │  reverse AWB issued
   └────────┬─────────────────┘
            │  buyer hands parcel; tracking events flow
            ▼
   ┌──────────────────────────┐
   │  return_in_transit       │
   └────────┬─────────────────┘
            │  delivered to seller pickup location
            ▼
   ┌──────────────────────────┐
   │  return_received         │
   └────────┬─────────────────┘
            │  seller closes (accept | reject)
            ▼
   ┌──────────────────────────┐
   │  return_closed           │
   └──────────────────────────┘
```

## Public API

```go
package returns

type Service interface {
    // RTO flow

    // OnRTOInitiated is invoked from the outbox forwarder when NDR
    // promotes a case to RTO, or when tracking observes a carrier-direct
    // RTO event. Idempotent on (shipment_id).
    OnRTOInitiated(ctx context.Context, req RTOInitiatedRequest) (*RTOTracking, error)

    // OnRTOTrackingUpdate is invoked when tracking emits an rto_*
    // canonical status. Updates the rto_tracking row.
    OnRTOTrackingUpdate(ctx context.Context, req RTOTrackingUpdateRequest) error

    // SettleRTO is the finance step. Determines whether seller is
    // charged for the return leg, refunds COD-collected if any, etc.
    // Triggered automatically on rto_delivered + a 24h grace.
    SettleRTO(ctx context.Context, shipmentID core.ShipmentID) error

    // Return flow (seller-initiated)
    CreateReturn(ctx context.Context, req CreateReturnRequest) (*ReturnShipment, error)
    BookReturn(ctx context.Context, returnID core.ReturnID) (*ReturnShipment, error)
    ReceiveReturn(ctx context.Context, returnID core.ReturnID) (*ReturnShipment, error)
    CloseReturn(ctx context.Context, returnID core.ReturnID, decision CloseDecision) (*ReturnShipment, error)

    // Reads
    GetRTO(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (*RTOTracking, error)
    GetReturn(ctx context.Context, sellerID core.SellerID, returnID core.ReturnID) (*ReturnShipment, error)
    ListReturns(ctx context.Context, q ListReturnsQuery) (ListReturnsResult, error)
}
```

### Request / Response Types

```go
type RTOInitiatedRequest struct {
    SellerID    core.SellerID
    ShipmentID  core.ShipmentID
    OrderID     core.OrderID
    Reason      string         // "ndr_auto" | "buyer_rto" | "carrier_undeliverable"
    OccurredAt  time.Time
}

type RTOTrackingUpdateRequest struct {
    SellerID    core.SellerID
    ShipmentID  core.ShipmentID
    Status      tracking.CanonicalStatus // rto_in_transit | rto_delivered
    OccurredAt  time.Time
    Location    string
}

type CreateReturnRequest struct {
    SellerID         core.SellerID
    OperatorID       core.UserID
    OriginalShipmentID core.ShipmentID
    Reason           string         // "buyer_request" | "wrong_item" | "damaged" | ...
    PickupAddress    Address        // buyer's address (defaults to original drop)
    DropAddress      Address        // seller's pickup location (defaults to original pickup)
    PreferredCarrier string         // optional; otherwise allocation runs
    Lines            []ReturnLine   // partial returns supported
    Notes            string
}

type ReturnLine struct {
    SKU      string
    Quantity int
    AmountToRefundPaise core.Paise
}

type CloseDecision struct {
    Outcome   string         // "accepted" | "rejected"
    RefundPaise core.Paise   // 0 if rejected; else what was refunded to buyer (for records)
    Notes     string
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("returns: not found")
    ErrInvalidState          = errors.New("returns: invalid state for operation")
    ErrAlreadyRTO            = errors.New("returns: shipment already in RTO")
    ErrReturnWindowExpired   = errors.New("returns: return window has expired")
    ErrOriginalNotDelivered  = errors.New("returns: original shipment was not delivered")
    ErrPartialReturnExceeds  = errors.New("returns: partial return quantity exceeds shipped quantity")
)
```

## DB Schema

```sql
-- Mirrors RTO state for an outbound shipment that came back.
CREATE TABLE rto_tracking (
    shipment_id          uuid        PRIMARY KEY REFERENCES shipment(id),
    seller_id            uuid        NOT NULL REFERENCES seller(id),
    order_id             uuid        NOT NULL REFERENCES order_record(id),

    state                text        NOT NULL CHECK (state IN
        ('rto_initiated','rto_in_transit','rto_delivered','rto_settled')),

    reason               text        NOT NULL,        -- ndr_auto | buyer_rto | carrier_undeliverable
    initiated_at         timestamptz NOT NULL,
    in_transit_at        timestamptz,
    delivered_at         timestamptz,
    settled_at           timestamptz,

    -- Charges to seller for the RTO leg (often half of forward charge)
    rto_charge_paise     bigint      NOT NULL DEFAULT 0,
    cod_refund_paise     bigint      NOT NULL DEFAULT 0,

    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX rto_tracking_seller_state_idx ON rto_tracking(seller_id, state);

ALTER TABLE rto_tracking ENABLE ROW LEVEL SECURITY;
CREATE POLICY rto_tracking_isolation ON rto_tracking
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Seller-initiated returns: a parallel inbound shipment.
CREATE TABLE return_shipment (
    id                    uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id             uuid        NOT NULL REFERENCES seller(id),
    order_id              uuid        NOT NULL REFERENCES order_record(id),
    original_shipment_id  uuid        NOT NULL REFERENCES shipment(id),

    state                 text        NOT NULL CHECK (state IN
        ('return_requested','return_booked','return_in_transit','return_received','return_closed')),

    reason                text        NOT NULL,
    notes                 text,

    -- Inbound shipment record once booked
    return_carrier_code   text,
    return_awb            text,
    booked_at             timestamptz,

    pickup_address        jsonb       NOT NULL,
    drop_address          jsonb       NOT NULL,
    drop_pickup_location_id uuid,

    -- Lines (denormalized JSON; small enough)
    lines                 jsonb       NOT NULL,
    refund_total_paise    bigint      NOT NULL DEFAULT 0,

    -- Closure
    close_outcome         text,         -- accepted | rejected
    closed_at             timestamptz,
    refunded_paise        bigint        DEFAULT 0,

    created_by            uuid        NOT NULL REFERENCES app_user(id),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX return_shipment_awb_unique ON return_shipment(return_awb)
    WHERE return_awb IS NOT NULL;
CREATE INDEX return_shipment_seller_state_idx ON return_shipment(seller_id, state, created_at DESC);
CREATE INDEX return_shipment_original_idx ON return_shipment(original_shipment_id);

ALTER TABLE return_shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY return_shipment_isolation ON return_shipment
    USING (seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON rto_tracking, return_shipment TO pikshipp_app;
GRANT SELECT ON rto_tracking, return_shipment TO pikshipp_reports;
```

## Implementation Highlights

### OnRTOInitiated

```go
func (s *service) OnRTOInitiated(ctx context.Context, req RTOInitiatedRequest) (*RTOTracking, error) {
    var out *RTOTracking
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        existing, err := qtx.RTOTrackingGet(ctx, req.ShipmentID.UUID())
        if err == nil {
            out = rtoFromRow(existing)
            return nil // idempotent
        }
        if !errors.Is(err, pgx.ErrNoRows) {
            return err
        }

        // Compute RTO charge from policy
        ratio, _ := s.policy.GetFloat(ctx, req.SellerID, "rto.charge_ratio_of_forward")
        if ratio == 0 {
            ratio = 0.5
        }
        sh, _ := s.shipments.GetSystemInTx(ctx, tx, req.ShipmentID)
        rtoCharge := core.Paise(float64(sh.ChargesPaise) * ratio)

        row, err := qtx.RTOTrackingInsert(ctx, sqlcgen.RTOTrackingInsertParams{
            ShipmentID:        req.ShipmentID.UUID(),
            SellerID:          req.SellerID.UUID(),
            OrderID:           req.OrderID.UUID(),
            Reason:            req.Reason,
            InitiatedAt:       req.OccurredAt,
            RTOChargePaise:    int64(rtoCharge),
            // COD refund: if the COD shipment is still in pending (not collected),
            // there's nothing to refund. If collected/remitted/settled, that's
            // a separate dispute (ops handles).
        })
        if err != nil {
            return err
        }
        out = rtoFromRow(row)

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "shipment.rto.initiated",
            Object:   audit.ObjShipment(req.ShipmentID),
            Payload:  map[string]any{"reason": req.Reason, "rto_charge_paise": rtoCharge},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.rto.tracking_created",
            Key:  string(req.ShipmentID),
            Payload: map[string]any{"shipment_id": req.ShipmentID, "reason": req.Reason},
        })
    })
    return out, err
}
```

### SettleRTO

```go
func (s *service) SettleRTO(ctx context.Context, shipmentID core.ShipmentID) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.RTOTrackingGet(ctx, shipmentID.UUID())
        if err != nil {
            return ErrNotFound
        }
        if RTOState(cur.State) != RTOStateRTODelivered {
            return fmt.Errorf("%w: state=%s", ErrInvalidState, cur.State)
        }

        // Debit seller wallet for RTO charge.
        if cur.RTOChargePaise > 0 {
            if err := s.wallet.PostInTx(ctx, tx, wallet.PostRequest{
                SellerID:    core.SellerIDFromUUID(cur.SellerID),
                AmountPaise: core.Paise(cur.RTOChargePaise),
                RefType:     "rto_charge",
                RefID:       shipmentID.String(),
                Direction:   wallet.DirectionDebit,
                Reason:      "rto_charge",
            }); err != nil {
                return err
            }
        }

        if _, err := qtx.RTOTrackingMarkSettled(ctx, shipmentID.UUID()); err != nil {
            return err
        }

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(cur.SellerID),
            Action:   "shipment.rto.settled",
            Object:   audit.ObjShipment(shipmentID),
            Payload:  map[string]any{"rto_charge_paise": cur.RTOChargePaise},
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.rto.settled",
            Key:  string(shipmentID),
            Payload: map[string]any{"shipment_id": shipmentID, "charge_paise": cur.RTOChargePaise},
        })
    })
}
```

### CreateReturn (seller-initiated)

```go
func (s *service) CreateReturn(ctx context.Context, req CreateReturnRequest) (*ReturnShipment, error) {
    // Validate original shipment is delivered + within return window.
    orig, err := s.shipments.Get(ctx, req.OriginalShipmentID)
    if err != nil {
        return nil, err
    }
    if orig.State != shipments.StateDelivered {
        return nil, ErrOriginalNotDelivered
    }
    windowDays, _ := s.policy.GetInt(ctx, req.SellerID, "returns.window_days")
    if windowDays == 0 {
        windowDays = 7
    }
    if s.clock.Now().After(orig.DeliveredAt.AddDate(0, 0, windowDays)) {
        return nil, ErrReturnWindowExpired
    }

    // Validate partial-return quantities don't exceed original.
    order, _ := s.orders.Get(ctx, orig.OrderID)
    if err := validatePartialReturn(order, req.Lines); err != nil {
        return nil, err
    }

    var totalRefund core.Paise
    for _, l := range req.Lines {
        totalRefund = totalRefund.Add(l.AmountToRefundPaise)
    }

    var out *ReturnShipment
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ReturnShipmentInsert(ctx, sqlcgen.ReturnShipmentInsertParams{
            ID:                  core.NewReturnID().UUID(),
            SellerID:            req.SellerID.UUID(),
            OrderID:             orig.OrderID.UUID(),
            OriginalShipmentID:  req.OriginalShipmentID.UUID(),
            Reason:              req.Reason,
            PickupAddress:       jsonbFromAddress(req.PickupAddress),
            DropAddress:         jsonbFromAddress(req.DropAddress),
            DropPickupLocationID: pgxNullUUIDFrom(orig.PickupLocationID.UUID()),
            Lines:               jsonbFrom(req.Lines),
            RefundTotalPaise:    int64(totalRefund),
            CreatedBy:           req.OperatorID.UUID(),
        })
        if err != nil {
            return err
        }
        out = returnFromRow(row)

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "return.requested",
            Object:   audit.ObjReturn(out.ID),
            Payload:  map[string]any{"original_shipment_id": req.OriginalShipmentID, "reason": req.Reason},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "return.requested",
            Key:  string(out.ID),
            Payload: map[string]any{"return_id": out.ID, "original_shipment_id": req.OriginalShipmentID},
        })
    })
    return out, err
}
```

### BookReturn

```go
func (s *service) BookReturn(ctx context.Context, returnID core.ReturnID) (*ReturnShipment, error) {
    // Read return
    row, err := s.q.ReturnShipmentGet(ctx, returnID.UUID())
    if err != nil {
        return nil, ErrNotFound
    }
    if ReturnState(row.State) != ReturnStateRequested {
        return nil, fmt.Errorf("%w: state=%s", ErrInvalidState, row.State)
    }

    // Resolve carrier (use original carrier or run a mini-allocation)
    carrierCode := row.ReturnCarrierCode.String
    if carrierCode == "" {
        // For v0, default to original carrier — most carriers offer reverse pickup.
        orig, _ := s.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(row.OriginalShipmentID))
        carrierCode = orig.CarrierCode
    }
    a, err := s.carriers.Get(carrierCode)
    if err != nil {
        return nil, err
    }
    if !slices.Contains(a.Capabilities().Services, framework.ServiceReverse) {
        return nil, fmt.Errorf("returns: carrier %q does not support reverse", carrierCode)
    }

    // Book reverse with carrier (uses framework.Call → breaker). We're
    // NOT inside a tx here — same two-phase pattern as forward booking.
    var pickupAddr, dropAddr Address
    _ = json.Unmarshal(row.PickupAddress, &pickupAddr)
    _ = json.Unmarshal(row.DropAddress, &dropAddr)

    res := a.Book(ctx, framework.BookRequest{
        SellerID:      core.SellerIDFromUUID(row.SellerID),
        ShipmentID:    core.ShipmentIDFromUUID(row.ID), // re-using uuid form
        PickupAddress: pickupAddr,
        DropAddress:   dropAddr,
        ServiceType:   framework.ServiceReverse,
        // No COD on returns; declared value mirrors refund total
        DeclaredValuePaise: core.Paise(row.RefundTotalPaise),
    })
    if !res.OK {
        return nil, fmt.Errorf("returns: book failed: %w", res.Err)
    }

    var out *ReturnShipment
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        updated, err := qtx.ReturnShipmentMarkBooked(ctx, sqlcgen.ReturnShipmentMarkBookedParams{
            ID:                returnID.UUID(),
            ReturnCarrierCode: pgxNullString(carrierCode),
            ReturnAWB:         pgxNullString(res.Value.AWB),
        })
        if err != nil {
            return err
        }
        out = returnFromRow(updated)

        // Register inbound tracking
        if err := s.tracking.RegisterReturnAWBInTx(ctx, tx, tracking.RegisterAWBRequest{
            // For returns we use a separate logical channel; tracking stores
            // it in tracking_event with dedupe per (return_awb, status, occurred_at).
            ReturnID:    out.ID,
            CarrierCode: carrierCode,
            AWB:         res.Value.AWB,
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "return.booked",
            Key:  string(out.ID),
            Payload: map[string]any{"return_id": out.ID, "awb": res.Value.AWB, "carrier": carrierCode},
        })
    })
    return out, err
}
```

### ReceiveReturn / CloseReturn

```go
// ReceiveReturn is invoked when the inbound shipment is delivered to the
// seller pickup location. Triggered automatically by tracking events.
func (s *service) ReceiveReturn(ctx context.Context, returnID core.ReturnID) (*ReturnShipment, error) {
    var out *ReturnShipment
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ReturnShipmentTransitionState(ctx, sqlcgen.ReturnShipmentTransitionStateParams{
            ID:        returnID.UUID(),
            State:     string(ReturnStateReceived),
            FromState: string(ReturnStateInTransit),
        })
        if err != nil {
            return err
        }
        out = returnFromRow(row)
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "return.received",
            Key:  string(out.ID),
            Payload: map[string]any{"return_id": out.ID},
        })
    })
    return out, err
}

func (s *service) CloseReturn(ctx context.Context, returnID core.ReturnID, decision CloseDecision) (*ReturnShipment, error) {
    var out *ReturnShipment
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.ReturnShipmentClose(ctx, sqlcgen.ReturnShipmentCloseParams{
            ID:             returnID.UUID(),
            CloseOutcome:   pgxNullString(decision.Outcome),
            RefundedPaise:  pgxNullInt8(int64(decision.RefundPaise)),
        })
        if err != nil {
            return err
        }
        out = returnFromRow(row)

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(row.SellerID),
            Action:   "return.closed",
            Object:   audit.ObjReturn(out.ID),
            Payload:  map[string]any{"outcome": decision.Outcome, "refund_paise": decision.RefundPaise},
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "return.closed",
            Key:  string(out.ID),
            Payload: map[string]any{"return_id": out.ID, "outcome": decision.Outcome},
        })
    })
    return out, err
}
```

### Validation: Partial Returns

```go
func validatePartialReturn(order *orders.Order, lines []ReturnLine) error {
    skuQty := make(map[string]int, len(order.Lines))
    for _, l := range order.Lines {
        skuQty[l.SKU] += l.Quantity
    }
    for _, rl := range lines {
        if rl.Quantity <= 0 {
            return fmt.Errorf("%w: sku=%s qty=%d", ErrPartialReturnExceeds, rl.SKU, rl.Quantity)
        }
        if rl.Quantity > skuQty[rl.SKU] {
            return fmt.Errorf("%w: sku=%s requested=%d shipped=%d",
                ErrPartialReturnExceeds, rl.SKU, rl.Quantity, skuQty[rl.SKU])
        }
        skuQty[rl.SKU] -= rl.Quantity
    }
    return nil
}
```

## Outbox Routing

- `shipment.rto.initiated` (from NDR) → `returns.OnRTOInitiatedJob`
- `shipment.tracking.updated (status=rto_in_transit | rto_delivered)` → `returns.OnRTOTrackingUpdateJob`
- `returns.OnRTODeliveredJob` → after 24h grace → `returns.SettleRTOJob`
- `return.requested` → `returns.BookReturnJob` (operator can also click "Book" manually)
- `return.received` (from tracking) → seller-dashboard surfaces "Close return" prompt
- `return.closed` → ops + finance reports

## Testing

### SLT (`service_slt_test.go`)

```go
func TestRTO_FullFlow_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedShipment(t, pg)
    svc := slt.Returns(pg)

    _, err := svc.OnRTOInitiated(ctx, RTOInitiatedRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID, OrderID: sh.OrderID,
        Reason: "ndr_auto", OccurredAt: slt.Now(),
    })
    require.NoError(t, err)

    require.NoError(t, svc.OnRTOTrackingUpdate(ctx, RTOTrackingUpdateRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID,
        Status: tracking.StatusRTOInTransit, OccurredAt: slt.Now(),
    }))
    require.NoError(t, svc.OnRTOTrackingUpdate(ctx, RTOTrackingUpdateRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID,
        Status: tracking.StatusRTODelivered, OccurredAt: slt.Now(),
    }))
    require.NoError(t, svc.SettleRTO(ctx, sh.ID))

    rto, _ := svc.GetRTO(ctx, sh.SellerID, sh.ID)
    require.Equal(t, RTOStateRTOSettled, rto.State)

    bal, _ := slt.Wallet(pg).Balance(ctx, sh.SellerID)
    require.Equal(t, sh.InitialBalance - sh.ChargesPaise - rto.RTOChargePaise, bal)
}

func TestRTO_Idempotent_OnRTOInitiated_SLT(t *testing.T) { /* ... */ }

func TestReturn_FullFlow_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewDeliveredShipment(t, pg)
    svc := slt.Returns(pg)

    r, err := svc.CreateReturn(ctx, CreateReturnRequest{
        SellerID: sh.SellerID, OperatorID: slt.OperatorUserID(t, pg),
        OriginalShipmentID: sh.ID, Reason: "buyer_request",
        PickupAddress: sh.DropAddr, DropAddress: sh.PickupAddr,
        Lines: []ReturnLine{{SKU: "X", Quantity: 1, AmountToRefundPaise: 10000}},
    })
    require.NoError(t, err)

    sandbox := slt.SandboxCarrier(t, pg, "sb")
    sandbox.SetBookResult(framework.Result[framework.BookResponse]{
        OK: true, Value: framework.BookResponse{AWB: "RET123"},
    })
    r2, err := svc.BookReturn(ctx, r.ID)
    require.NoError(t, err)
    require.Equal(t, ReturnStateBooked, r2.State)
    require.Equal(t, "RET123", r2.ReturnAWB.String)

    require.NoError(t, svc.ReceiveReturn(ctx, r.ID))
    r3, err := svc.CloseReturn(ctx, r.ID, CloseDecision{
        Outcome: "accepted", RefundPaise: 10000,
    })
    require.NoError(t, err)
    require.Equal(t, ReturnStateClosed, r3.State)
}

func TestReturn_OutsideWindow_Rejected_SLT(t *testing.T) { /* ... */ }
func TestReturn_PartialQuantityValidation_SLT(t *testing.T) { /* ... */ }
func TestRLS_RTOAndReturnIsolation_SLT(t *testing.T) { /* ... */ }
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `OnRTOInitiated` | 6 ms | 18 ms | INSERT + audit + outbox |
| `OnRTOTrackingUpdate` | 4 ms | 12 ms | UPDATE + outbox |
| `SettleRTO` | 8 ms | 24 ms | wallet debit + UPDATE + audit + outbox |
| `CreateReturn` | 7 ms | 22 ms | INSERT + validate + outbox |
| `BookReturn` | + carrier RTT + 12 ms | + RTT + 35 ms | similar to forward booking |
| `CloseReturn` | 5 ms | 16 ms | UPDATE + audit + outbox |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| RTO initiated twice (NDR + carrier-direct) | Idempotent INSERT | Second call is no-op. |
| `SettleRTO` called before `rto_delivered` | State guard | `ErrInvalidState`. |
| Return book fails permanently | `ErrCarrierBookFailed` | Return stays `requested`; seller can change carrier and retry. |
| Carrier doesn't support reverse | capability check | Surface to seller; suggest alternate carrier or self-pickup. |
| Partial return quantity exceeds shipped | validation rejects | `ErrPartialReturnExceeds`. |
| Wallet debit for RTO charge fails (insufficient balance) | `wallet.ErrInsufficient` | Per policy: either grace-cap suspend or move to disputed. **Decision:** at v0, RTO charge always succeeds (wallet allows negative? no — it doesn't). Currently we attempt, fail, and leave RTO in `rto_delivered`; ops resolves. |
| Tracking event for return AWB before `BookReturn` records it | Race | Tracking falls back to logging unknown AWB; once book commits, future events match. |

## Open Questions

1. **Negative wallet balance for RTO charges.** v0 wallet does not allow negative. RTO charge failures pile up. **Decision: introduce a `seller_arrears` table at v1**; charge debits land there if wallet doesn't have funds, and we drain them on next credit.
2. **Auto-disposition of received returns.** Some sellers want returns to auto-credit buyer cards if accepted. **Decision:** out of scope; sellers refund externally.
3. **Multi-package returns.** Today one return = one inbound shipment. Multi-package out of scope.
4. **Return label generation.** Carrier issues label on book; same flow as forward labels.
5. **RTO-vs-return charges difference.** RTO is system-driven (failed delivery); return is buyer-driven (changed mind). Both result in inbound parcels but pricing models differ. **Decision:** policy `rto.charge_ratio` and `returns.charge_ratio` separately; defaults different (0.5 vs full).

## References

- HLD §03-services/02-tracking-and-status: emits `rto_*` canonical statuses.
- LLD §03-services/13-shipments: original shipment state guards.
- LLD §03-services/14-tracking: emits status updates that this service consumes.
- LLD §03-services/15-ndr: emits `shipment.rto.initiated`.
- LLD §03-services/05-wallet: PostInTx for charges/refunds.
- LLD §03-services/01-policy-engine: `rto.charge_ratio_of_forward`, `returns.window_days`.
- LLD §03-services/12-carriers-framework: `Adapter.Book` for reverse service.
