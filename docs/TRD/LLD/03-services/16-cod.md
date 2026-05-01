# COD Service

## Purpose

In India, **cash-on-delivery (COD)** is the default payment method for the majority of e-commerce orders outside the top metros. The COD service owns:

- Tracking which shipments are COD and what amount the carrier collected on delivery.
- Reconciling carrier-reported collections against the platform's records.
- Maintaining the **per-seller COD remittance schedule** and producing remittance batches.
- Crediting wallets when COD is remitted (carrier → Pikshipp account → seller wallet).
- Flagging mismatches (over-collection, under-collection, missing remittance).

COD failures are the **highest-revenue-impact bugs** in any aggregator: a single off-by-one paisa in a remittance file repeated across 10,000 shipments turns into a real refund nightmare. The service is built around an **eventually-zero-delta invariant**: every COD shipment must reach `cod_settled` with a complete remittance trail.

Out of scope:

- The wallet ledger itself — wallet (LLD §03-services/05).
- Carrier remittance file ingestion adapter — recon (LLD §03-services/18) for weight files; we extend the same ingestion framework.
- Tax invoice generation on remitted COD — finance/recon territory.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | money, IDs, errors, clock. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/wallet` | `Post(direction=credit)` to settle COD into seller wallet. |
| `internal/shipments` | shipment lookup; ship state transitions are NOT done here (tracking owns them). |
| `internal/orders` | order lookup for COD amount. |
| `internal/carriers/framework` | (future) per-carrier remittance file fetching. |
| `internal/policy` | per-seller remittance frequency, hold-back days. |
| `internal/audit`, `internal/outbox` | standard. |

## Package Layout

```
internal/cod/
├── service.go             // Service interface
├── service_impl.go
├── repo.go
├── types.go               // CODShipment, RemittanceBatch, RemittanceLine
├── lifecycle.go           // COD shipment state machine
├── reconcile.go           // Carrier file ↔ our records
├── jobs.go                // OnDeliveredJob, RemittanceBatchJob, ReconcileFileJob
├── errors.go
├── events.go
├── service_test.go
└── service_slt_test.go
```

## COD Shipment Lifecycle

A shipment with `payment_method=cod` automatically gets a `cod_shipment` row at booking time. That row tracks the COD-specific lifecycle independent of the parent shipment:

```
       Book (cod=true)
            │
            ▼
   ┌──────────────────────┐
   │      pending          │  buyer hasn't paid yet (parcel in transit)
   └────────┬──────────────┘
            │  delivered event from tracking
            ▼
   ┌──────────────────────┐
   │      collected        │  carrier collected; we've not received remittance
   └────────┬──────────────┘
            │  remittance file ingested + matched
            ▼
   ┌──────────────────────┐
   │      remitted         │  carrier paid us
   └────────┬──────────────┘
            │  remittance batch posted to seller wallet
            ▼
   ┌──────────────────────┐
   │      settled          │  seller has the money in their wallet
   └──────────────────────┘

Side branches:
   pending → cancelled  (shipment cancelled before delivery)
   collected → disputed  (mismatch found during reconcile)
   any → mismatch_open  (carrier reported delivered but no remit; or remit > expected)
```

```go
type CODState string

const (
    CODStatePending      CODState = "pending"
    CODStateCollected    CODState = "collected"
    CODStateRemitted     CODState = "remitted"
    CODStateSettled      CODState = "settled"
    CODStateCancelled    CODState = "cancelled"
    CODStateDisputed     CODState = "disputed"
    CODStateMismatchOpen CODState = "mismatch_open"
)
```

## Public API

```go
package cod

type Service interface {
    // Register is invoked from shipments.Book when a COD shipment is
    // booked. Idempotent on (shipment_id).
    Register(ctx context.Context, req RegisterRequest) error

    // OnDelivered is invoked from the outbox forwarder when tracking
    // emits delivered for a COD shipment. Transitions COD pending→collected.
    OnDelivered(ctx context.Context, req OnDeliveredRequest) error

    // OnCancelled handles shipment cancellation while in pending. Marks
    // the cod_shipment cancelled.
    OnCancelled(ctx context.Context, shipmentID core.ShipmentID) error

    // IngestRemittanceFile parses a carrier-supplied remittance file
    // (CSV/Excel) and creates a RemittanceBatch with line items. Each
    // line attempts to match against a cod_shipment in 'collected' state.
    IngestRemittanceFile(ctx context.Context, req IngestRemittanceRequest) (*RemittanceBatch, error)

    // PostRemittanceBatch settles all matched lines: credits the seller
    // wallets and transitions cod_shipments to 'settled'. Mismatched
    // lines remain unsettled and require manual ops resolution.
    PostRemittanceBatch(ctx context.Context, batchID core.RemittanceBatchID) error

    // ListPendingRemittance is used by the seller dashboard's
    // "Money awaiting" widget.
    ListPendingRemittance(ctx context.Context, sellerID core.SellerID) (*PendingRemittanceSummary, error)

    // List/Get for the seller dashboard.
    GetCODShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (*CODShipment, error)
    ListMismatches(ctx context.Context, q ListMismatchQuery) ([]*CODShipment, error)
}
```

### Request / Response Types

```go
type RegisterRequest struct {
    SellerID         core.SellerID
    ShipmentID       core.ShipmentID
    OrderID          core.OrderID
    CODAmountPaise   core.Paise
    CarrierCode      string
    BookedAt         time.Time
}

type OnDeliveredRequest struct {
    SellerID    core.SellerID
    ShipmentID  core.ShipmentID
    DeliveredAt time.Time
    // CarrierReportedAmount is filled when tracking events include
    // a "collected_amount" field (rare). Otherwise we trust order.cod_amount.
    CarrierReportedAmountPaise *core.Paise
}

type IngestRemittanceRequest struct {
    OperatorID  core.UserID
    CarrierCode string
    UploadID    string         // S3 key
    SchemaName  string         // "delhivery_v1" | ...
    FileDate    time.Time
}

type PendingRemittanceSummary struct {
    CollectedAwaitingRemitPaise   core.Paise
    RemittedAwaitingSettlePaise   core.Paise
    SettledThisMonthPaise         core.Paise
    NextEstimatedSettlementDate   time.Time
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("cod: not found")
    ErrInvalidState          = errors.New("cod: invalid state for operation")
    ErrAmountMismatch        = errors.New("cod: amount mismatch")
    ErrAlreadyRegistered     = errors.New("cod: shipment already registered")
    ErrAlreadyDelivered      = errors.New("cod: shipment already marked collected")
    ErrAlreadyRemitted       = errors.New("cod: shipment already remitted")
    ErrFileSchemaUnknown     = errors.New("cod: unknown remittance file schema")
    ErrBatchEmpty            = errors.New("cod: remittance batch has no matching shipments")
)
```

## DB Schema

```sql
CREATE TABLE cod_shipment (
    shipment_id     uuid        PRIMARY KEY REFERENCES shipment(id),
    seller_id       uuid        NOT NULL REFERENCES seller(id),
    order_id        uuid        NOT NULL REFERENCES order_record(id),
    carrier_code    text        NOT NULL,

    state           text        NOT NULL CHECK (state IN
        ('pending','collected','remitted','settled','cancelled','disputed','mismatch_open')),

    expected_amount_paise         bigint  NOT NULL CHECK (expected_amount_paise > 0),
    carrier_reported_amount_paise bigint,                       -- when carrier confirms
    remitted_amount_paise         bigint,                       -- as appearing on remittance file
    settled_amount_paise          bigint,                       -- as credited to seller wallet

    delivered_at     timestamptz,
    remitted_at      timestamptz,
    settled_at       timestamptz,

    booked_at        timestamptz NOT NULL,
    cancelled_at     timestamptz,

    -- Tie-back fields
    remittance_batch_id  uuid REFERENCES cod_remittance_batch(id),
    remittance_line_id   bigint,

    -- For mismatches
    mismatch_reason  text,

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX cod_shipment_seller_state_idx
    ON cod_shipment(seller_id, state);
CREATE INDEX cod_shipment_carrier_state_idx
    ON cod_shipment(carrier_code, state)
    WHERE state IN ('collected','mismatch_open');
CREATE INDEX cod_shipment_delivered_idx
    ON cod_shipment(delivered_at)
    WHERE state = 'collected';

ALTER TABLE cod_shipment ENABLE ROW LEVEL SECURITY;
CREATE POLICY cod_shipment_isolation ON cod_shipment
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Remittance batches (one per file ingest)
CREATE TABLE cod_remittance_batch (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_code      text        NOT NULL,
    file_date         date        NOT NULL,
    upload_id         text        NOT NULL,
    schema_name       text        NOT NULL,
    operator_id       uuid        NOT NULL REFERENCES app_user(id),
    state             text        NOT NULL CHECK (state IN ('parsed','matched','posted','partially_posted','failed')),
    line_count        integer     NOT NULL DEFAULT 0,
    matched_count     integer     NOT NULL DEFAULT 0,
    unmatched_count   integer     NOT NULL DEFAULT 0,
    posted_count      integer     NOT NULL DEFAULT 0,
    total_amount_paise        bigint NOT NULL DEFAULT 0,
    matched_amount_paise      bigint NOT NULL DEFAULT 0,
    posted_amount_paise       bigint NOT NULL DEFAULT 0,
    parsed_at         timestamptz,
    posted_at         timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (carrier_code, file_date, upload_id)
);

-- No RLS: this is platform-internal (cross-seller).
GRANT SELECT, INSERT, UPDATE ON cod_remittance_batch TO pikshipp_app;
GRANT SELECT ON cod_remittance_batch TO pikshipp_reports;

CREATE TABLE cod_remittance_line (
    id                       bigserial   PRIMARY KEY,
    remittance_batch_id      uuid        NOT NULL REFERENCES cod_remittance_batch(id) ON DELETE CASCADE,
    carrier_code             text        NOT NULL,

    -- From the file:
    awb                      text        NOT NULL,
    carrier_amount_paise     bigint      NOT NULL,
    carrier_delivered_at     timestamptz,
    raw_row                  jsonb       NOT NULL,

    -- After matching:
    shipment_id              uuid REFERENCES shipment(id),
    seller_id                uuid REFERENCES seller(id),
    matched                  boolean     NOT NULL DEFAULT false,
    match_state              text,        -- "ok" | "amount_mismatch" | "unknown_awb" | "already_remitted"
    match_notes              text,

    posted                   boolean     NOT NULL DEFAULT false,
    posted_at                timestamptz
);

CREATE INDEX cod_remittance_line_batch_idx ON cod_remittance_line(remittance_batch_id);
CREATE INDEX cod_remittance_line_awb_idx ON cod_remittance_line(awb);

-- RLS policy lets sellers see only their own posted lines (for reports).
ALTER TABLE cod_remittance_line ENABLE ROW LEVEL SECURITY;
CREATE POLICY cod_remittance_line_isolation ON cod_remittance_line
    USING (seller_id IS NULL OR seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON cod_remittance_line TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE cod_remittance_line_id_seq TO pikshipp_app;
GRANT SELECT ON cod_remittance_line TO pikshipp_reports;
```

## sqlc Queries

```sql
-- name: CODShipmentInsert :one
INSERT INTO cod_shipment (
    shipment_id, seller_id, order_id, carrier_code,
    state, expected_amount_paise, booked_at
) VALUES (
    $1, $2, $3, $4, 'pending', $5, $6
)
ON CONFLICT (shipment_id) DO NOTHING
RETURNING *;

-- name: CODShipmentGet :one
SELECT * FROM cod_shipment WHERE shipment_id = $1;

-- name: CODShipmentMarkCollected :one
UPDATE cod_shipment
SET state = 'collected',
    delivered_at = $2,
    carrier_reported_amount_paise = $3,
    updated_at = now()
WHERE shipment_id = $1 AND state = 'pending'
RETURNING *;

-- name: CODShipmentMarkRemitted :one
UPDATE cod_shipment
SET state = 'remitted',
    remitted_amount_paise = $2,
    remittance_batch_id = $3,
    remittance_line_id = $4,
    remitted_at = now(),
    updated_at = now()
WHERE shipment_id = $1 AND state = 'collected'
RETURNING *;

-- name: CODShipmentMarkSettled :one
UPDATE cod_shipment
SET state = 'settled',
    settled_amount_paise = $2,
    settled_at = now(),
    updated_at = now()
WHERE shipment_id = $1 AND state = 'remitted'
RETURNING *;

-- name: CODShipmentMarkMismatch :one
UPDATE cod_shipment
SET state = 'mismatch_open',
    mismatch_reason = $2,
    updated_at = now()
WHERE shipment_id = $1
RETURNING *;

-- name: CODShipmentMarkCancelled :one
UPDATE cod_shipment
SET state = 'cancelled', cancelled_at = now(), updated_at = now()
WHERE shipment_id = $1 AND state IN ('pending')
RETURNING *;

-- name: CODShipmentLookupByAWB :one
SELECT cs.* FROM cod_shipment cs
JOIN shipment s ON s.id = cs.shipment_id
WHERE s.awb = $1;

-- name: CODPendingRemittanceSummary :one
SELECT
    SUM(CASE WHEN state = 'collected'  THEN expected_amount_paise ELSE 0 END) AS collected_pending,
    SUM(CASE WHEN state = 'remitted'   THEN remitted_amount_paise ELSE 0 END) AS remitted_pending,
    SUM(CASE WHEN state = 'settled' AND settled_at >= date_trunc('month', now()) THEN settled_amount_paise ELSE 0 END) AS settled_this_month
FROM cod_shipment
WHERE seller_id = $1;

-- Remittance batch
-- name: CODRemittanceBatchInsert :one
INSERT INTO cod_remittance_batch (
    id, carrier_code, file_date, upload_id, schema_name, operator_id, state, parsed_at
) VALUES ($1, $2, $3, $4, $5, $6, 'parsed', now())
RETURNING *;

-- name: CODRemittanceLineInsert :one
INSERT INTO cod_remittance_line (
    remittance_batch_id, carrier_code, awb, carrier_amount_paise, carrier_delivered_at, raw_row
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: CODRemittanceLineUpdateMatch :exec
UPDATE cod_remittance_line
SET shipment_id = $2, seller_id = $3, matched = $4, match_state = $5, match_notes = $6
WHERE id = $1;

-- name: CODRemittanceLineMarkPosted :exec
UPDATE cod_remittance_line
SET posted = true, posted_at = now()
WHERE id = $1;

-- name: CODRemittanceBatchUpdateProgress :exec
UPDATE cod_remittance_batch
SET state = $2,
    line_count = $3,
    matched_count = $4,
    unmatched_count = $5,
    posted_count = $6,
    total_amount_paise = $7,
    matched_amount_paise = $8,
    posted_amount_paise = $9,
    posted_at = COALESCE($10, posted_at)
WHERE id = $1;
```

## Implementation

### Register (called from shipments.commitBookingSuccess)

```go
package cod

func (s *service) Register(ctx context.Context, req RegisterRequest) error {
    if req.CODAmountPaise <= 0 {
        return fmt.Errorf("%w: amount must be > 0", ErrInvalidState)
    }
    _, err := s.q.CODShipmentInsert(ctx, sqlcgen.CODShipmentInsertParams{
        ShipmentID:           req.ShipmentID.UUID(),
        SellerID:             req.SellerID.UUID(),
        OrderID:              req.OrderID.UUID(),
        CarrierCode:          req.CarrierCode,
        ExpectedAmountPaise:  int64(req.CODAmountPaise),
        BookedAt:             req.BookedAt,
    })
    return err
}
```

### OnDelivered

```go
func (s *service) OnDelivered(ctx context.Context, req OnDeliveredRequest) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.CODShipmentGet(ctx, req.ShipmentID.UUID())
        if err != nil {
            return ErrNotFound
        }
        if CODState(cur.State) != CODStatePending {
            // Idempotent: collected/remitted/settled means already delivered.
            return nil
        }

        var reportedAmountSQL pgtype.Int8
        if req.CarrierReportedAmountPaise != nil {
            reportedAmountSQL = pgtype.Int8{Int64: int64(*req.CarrierReportedAmountPaise), Valid: true}
            // Compare against expected
            if int64(*req.CarrierReportedAmountPaise) != cur.ExpectedAmountPaise {
                if _, err := qtx.CODShipmentMarkMismatch(ctx, sqlcgen.CODShipmentMarkMismatchParams{
                    ShipmentID:     req.ShipmentID.UUID(),
                    MismatchReason: pgxNullString(fmt.Sprintf("delivered_amount_mismatch: expected=%d carrier=%d", cur.ExpectedAmountPaise, *req.CarrierReportedAmountPaise)),
                }); err != nil {
                    return err
                }
                return s.outb.Emit(ctx, tx, outbox.Event{
                    Kind: "cod.mismatch.opened",
                    Key:  string(req.ShipmentID),
                    Payload: map[string]any{"shipment_id": req.ShipmentID, "reason": "delivered_amount_mismatch"},
                })
            }
        }

        if _, err := qtx.CODShipmentMarkCollected(ctx, sqlcgen.CODShipmentMarkCollectedParams{
            ShipmentID:                 req.ShipmentID.UUID(),
            DeliveredAt:                req.DeliveredAt,
            CarrierReportedAmountPaise: reportedAmountSQL,
        }); err != nil {
            return err
        }
        if err := s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "cod.collected",
            Object:   audit.ObjShipment(req.ShipmentID),
            Payload:  map[string]any{"expected": cur.ExpectedAmountPaise},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "cod.collected",
            Key:  string(req.ShipmentID),
            Payload: map[string]any{"shipment_id": req.ShipmentID, "expected_amount_paise": cur.ExpectedAmountPaise},
        })
    })
}
```

### IngestRemittanceFile

```go
func (s *service) IngestRemittanceFile(ctx context.Context, req IngestRemittanceRequest) (*RemittanceBatch, error) {
    schema, ok := remittanceSchemas[req.SchemaName]
    if !ok {
        return nil, fmt.Errorf("%w: %s", ErrFileSchemaUnknown, req.SchemaName)
    }
    body, err := s.objstore.Read(ctx, req.UploadID)
    if err != nil {
        return nil, err
    }
    parsed, err := schema.Parse(body)
    if err != nil {
        return nil, err
    }

    batchID := core.NewRemittanceBatchID()
    var batch *RemittanceBatch
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        b, err := qtx.CODRemittanceBatchInsert(ctx, sqlcgen.CODRemittanceBatchInsertParams{
            ID:          batchID.UUID(),
            CarrierCode: req.CarrierCode,
            FileDate:    req.FileDate,
            UploadID:    req.UploadID,
            SchemaName:  req.SchemaName,
            OperatorID:  req.OperatorID.UUID(),
        })
        if err != nil {
            return err
        }
        batch = batchFromRow(b)

        var totalLineCount, matchedCount, unmatchedCount int
        var totalAmount, matchedAmount int64

        for _, p := range parsed {
            lineID, err := qtx.CODRemittanceLineInsert(ctx, sqlcgen.CODRemittanceLineInsertParams{
                RemittanceBatchID:    batchID.UUID(),
                CarrierCode:          req.CarrierCode,
                AWB:                  p.AWB,
                CarrierAmountPaise:   int64(p.AmountPaise),
                CarrierDeliveredAt:   pgxNullTimestamp(p.DeliveredAt),
                RawRow:               jsonbFrom(p.Raw),
            })
            if err != nil {
                return err
            }
            totalLineCount++
            totalAmount += int64(p.AmountPaise)

            // Try to match
            cs, err := qtx.CODShipmentLookupByAWB(ctx, p.AWB)
            if err != nil {
                if errors.Is(err, pgx.ErrNoRows) {
                    if err := qtx.CODRemittanceLineUpdateMatch(ctx, sqlcgen.CODRemittanceLineUpdateMatchParams{
                        ID:         lineID,
                        Matched:    false,
                        MatchState: pgxNullString("unknown_awb"),
                        MatchNotes: pgxNullString("no shipment for this AWB on this carrier"),
                    }); err != nil {
                        return err
                    }
                    unmatchedCount++
                    continue
                }
                return err
            }
            // Validate: must be in 'collected' state and amount matches.
            matchState := "ok"
            notes := ""
            switch CODState(cs.State) {
            case CODStateCollected:
                if cs.ExpectedAmountPaise != int64(p.AmountPaise) {
                    matchState = "amount_mismatch"
                    notes = fmt.Sprintf("expected=%d file=%d", cs.ExpectedAmountPaise, p.AmountPaise)
                }
            case CODStateRemitted, CODStateSettled:
                matchState = "already_remitted"
            default:
                matchState = "wrong_state:" + cs.State
            }
            matched := matchState == "ok"
            if err := qtx.CODRemittanceLineUpdateMatch(ctx, sqlcgen.CODRemittanceLineUpdateMatchParams{
                ID:          lineID,
                ShipmentID:  pgxNullUUIDFrom(cs.ShipmentID),
                SellerID:    pgxNullUUIDFrom(cs.SellerID),
                Matched:     matched,
                MatchState:  pgxNullString(matchState),
                MatchNotes:  pgxNullString(notes),
            }); err != nil {
                return err
            }
            if matched {
                matchedCount++
                matchedAmount += int64(p.AmountPaise)
            } else {
                unmatchedCount++
                if matchState == "amount_mismatch" {
                    if _, err := qtx.CODShipmentMarkMismatch(ctx, sqlcgen.CODShipmentMarkMismatchParams{
                        ShipmentID:     cs.ShipmentID,
                        MismatchReason: pgxNullString("remit_amount_mismatch:" + notes),
                    }); err != nil {
                        return err
                    }
                }
            }
        }

        nextState := "matched"
        if unmatchedCount == totalLineCount {
            nextState = "failed"
        }
        if err := qtx.CODRemittanceBatchUpdateProgress(ctx, sqlcgen.CODRemittanceBatchUpdateProgressParams{
            ID:                  batchID.UUID(),
            State:               nextState,
            LineCount:           int32(totalLineCount),
            MatchedCount:        int32(matchedCount),
            UnmatchedCount:      int32(unmatchedCount),
            PostedCount:         0,
            TotalAmountPaise:    totalAmount,
            MatchedAmountPaise:  matchedAmount,
        }); err != nil {
            return err
        }
        batch.LineCount = totalLineCount
        batch.MatchedCount = matchedCount
        batch.UnmatchedCount = unmatchedCount
        batch.TotalAmountPaise = core.Paise(totalAmount)
        batch.MatchedAmountPaise = core.Paise(matchedAmount)
        batch.State = nextState
        return nil
    })
    if err != nil {
        return nil, err
    }
    return batch, nil
}
```

### PostRemittanceBatch

```go
func (s *service) PostRemittanceBatch(ctx context.Context, batchID core.RemittanceBatchID) error {
    // Lock all matched lines + their cod_shipments. We post wallet
    // credits ONE LINE AT A TIME in separate small txs to avoid one
    // giant tx blocking everything else. Each line is independently
    // idempotent via wallet (ref_type=cod_remit, ref_id=line_id).

    lines, err := s.q.CODRemittanceLinesMatchedNotPosted(ctx, batchID.UUID())
    if err != nil {
        return err
    }
    if len(lines) == 0 {
        return ErrBatchEmpty
    }

    var posted int
    var postedAmount int64
    for _, line := range lines {
        if err := s.postOneLine(ctx, line); err != nil {
            slog.Warn("cod: post line failed", "batch_id", batchID, "line_id", line.ID, "err", err)
            continue
        }
        posted++
        postedAmount += line.CarrierAmountPaise
    }

    // Update batch summary
    state := "posted"
    if posted < len(lines) {
        state = "partially_posted"
    }
    if err := s.q.CODRemittanceBatchUpdateProgress(ctx, sqlcgen.CODRemittanceBatchUpdateProgressParams{
        ID:                  batchID.UUID(),
        State:               state,
        LineCount:           int32(len(lines)), // unchanged but required
        // ... other counts
        PostedCount:         int32(posted),
        PostedAmountPaise:   postedAmount,
        PostedAt:            pgxNullTimestamp(s.clock.Now()),
    }); err != nil {
        return err
    }
    return nil
}

func (s *service) postOneLine(ctx context.Context, line sqlcgen.CODRemittanceLine) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        // 1. Mark cod_shipment as remitted
        if _, err := qtx.CODShipmentMarkRemitted(ctx, sqlcgen.CODShipmentMarkRemittedParams{
            ShipmentID:           line.ShipmentID.UUID,
            RemittedAmountPaise:  line.CarrierAmountPaise,
            RemittanceBatchID:    pgxNullUUIDFrom(line.RemittanceBatchID),
            RemittanceLineID:     pgxNullInt8(line.ID),
        }); err != nil {
            return err
        }

        // 2. Wallet post (credit). Idempotent on (ref_type, ref_id, direction)
        //    UNIQUE (LLD §03-services/05-wallet).
        if err := s.wallet.PostInTx(ctx, tx, wallet.PostRequest{
            SellerID:    core.SellerIDFromUUID(line.SellerID.UUID),
            AmountPaise: core.Paise(line.CarrierAmountPaise),
            RefType:     "cod_remit",
            RefID:       fmt.Sprintf("%d", line.ID),
            Direction:   wallet.DirectionCredit,
            Reason:      "cod_remittance",
        }); err != nil {
            return err
        }

        // 3. Mark cod_shipment settled
        if _, err := qtx.CODShipmentMarkSettled(ctx, sqlcgen.CODShipmentMarkSettledParams{
            ShipmentID:           line.ShipmentID.UUID,
            SettledAmountPaise:   line.CarrierAmountPaise,
        }); err != nil {
            return err
        }

        // 4. Mark line posted
        if err := qtx.CODRemittanceLineMarkPosted(ctx, line.ID); err != nil {
            return err
        }

        // 5. Audit
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(line.SellerID.UUID),
            Action:   "cod.settled",
            Object:   audit.ObjShipment(core.ShipmentIDFromUUID(line.ShipmentID.UUID)),
            Payload:  map[string]any{"amount_paise": line.CarrierAmountPaise, "batch_id": line.RemittanceBatchID},
        }); err != nil {
            return err
        }

        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "cod.settled",
            Key:  fmt.Sprintf("%s|%d", line.ShipmentID.UUID, line.ID),
            Payload: map[string]any{
                "shipment_id": line.ShipmentID.UUID,
                "amount_paise": line.CarrierAmountPaise,
                "batch_id": line.RemittanceBatchID,
            },
        })
    })
}
```

### Carrier File Schemas

Like the orders CSV importer, each carrier's remittance file format is a plug-in:

```go
type RemittanceSchema interface {
    Name() string
    Parse(body []byte) ([]ParsedRemittanceLine, error)
}

type ParsedRemittanceLine struct {
    AWB         string
    AmountPaise core.Paise
    DeliveredAt time.Time
    Raw         map[string]any
}

var remittanceSchemas = map[string]RemittanceSchema{
    "delhivery_v1": &delhiveryV1Schema{},
    "bluedart_v1":  &bluedartV1Schema{},
}
```

## Outbox Routing

Forwarder routes:
- `shipment.tracking.updated (status=delivered, payment_method=cod)` → `cod.OnDeliveredJob`
- `shipment.cancelled (payment_method=cod)` → `cod.OnCancelledJob`
- `cod.collected` → `notifications.SellerCODCollectedJob`
- `cod.settled` → `notifications.SellerCODSettledJob`
- `cod.mismatch.opened` → `notifications.SellerOpsAlertJob` + ops dashboard

## Periodic Reconciliation

`CODReconcileSweep` runs daily and flags any cod_shipment that:
- Is `collected` for more than `policy.cod.max_collected_age_days` (default 14) without remittance.
- Is `remitted` for more than `policy.cod.max_remitted_age_days` (default 5) without settlement.
- Has been in `mismatch_open` for more than `policy.cod.max_mismatch_age_days` (default 7).

Each unhealthy row produces a single audit event + ops alert.

## Testing

### SLT (`service_slt_test.go`)

```go
func TestRegister_AndOnDelivered_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedCODShipment(t, pg, 50000) // 500 INR

    require.NoError(t, slt.COD(pg).OnDelivered(ctx, OnDeliveredRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID, DeliveredAt: slt.Now(),
    }))

    cs, _ := slt.COD(pg).GetCODShipment(ctx, sh.SellerID, sh.ID)
    require.Equal(t, CODStateCollected, cs.State)
    require.True(t, slt.OutboxHas(t, pg, "cod.collected", string(sh.ID)))
}

func TestOnDelivered_AmountMismatch_OpensCase_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedCODShipment(t, pg, 50000)
    paid := core.Paise(49900)
    require.NoError(t, slt.COD(pg).OnDelivered(ctx, OnDeliveredRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID, DeliveredAt: slt.Now(),
        CarrierReportedAmountPaise: &paid,
    }))
    cs, _ := slt.COD(pg).GetCODShipment(ctx, sh.SellerID, sh.ID)
    require.Equal(t, CODStateMismatchOpen, cs.State)
}

func TestIngestRemittanceFile_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    seller := slt.NewSeller(t, pg)
    sh1 := slt.NewBookedCODShipment(t, pg, 50000)
    sh2 := slt.NewBookedCODShipment(t, pg, 25000)
    slt.MarkCODCollected(t, pg, sh1.ID)
    slt.MarkCODCollected(t, pg, sh2.ID)

    fileBytes := slt.MakeDelhiveryRemittanceCSV([]slt.RemitRow{
        {AWB: sh1.AWB, AmountRupees: 500},
        {AWB: sh2.AWB, AmountRupees: 250},
    })
    objKey := slt.PutObject(t, fileBytes)

    batch, err := slt.COD(pg).IngestRemittanceFile(ctx, IngestRemittanceRequest{
        OperatorID: slt.OperatorUserID(t, pg),
        CarrierCode: "delhivery",
        UploadID: objKey, SchemaName: "delhivery_v1", FileDate: slt.Today(),
    })
    require.NoError(t, err)
    require.Equal(t, 2, batch.MatchedCount)
    require.Equal(t, 0, batch.UnmatchedCount)
    require.Equal(t, "matched", batch.State)

    require.NoError(t, slt.COD(pg).PostRemittanceBatch(ctx, batch.ID))

    bal, _ := slt.Wallet(pg).Balance(ctx, seller.ID)
    require.Equal(t, core.Paise(75000), bal)
}

func TestIngestRemittanceFile_AmountMismatch_LineNotPosted_SLT(t *testing.T) { /* ... */ }
func TestIngestRemittanceFile_UnknownAWB_LineNotPosted_SLT(t *testing.T) { /* ... */ }
func TestIngestRemittanceFile_AlreadyRemitted_LineSkipped_SLT(t *testing.T) { /* ... */ }
func TestPostRemittanceBatch_Idempotent_SLT(t *testing.T) {
    // Calling Post twice; wallet shows single credit (UNIQUE on ledger).
    // ...
}
func TestRLS_CODShipmentIsolation_SLT(t *testing.T) { /* ... */ }
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Register` | 3 ms | 8 ms | INSERT ON CONFLICT |
| `OnDelivered` | 5 ms | 14 ms | UPDATE + audit + outbox |
| `IngestRemittanceFile` (1k lines) | 4 s | 10 s | 1 batch tx + N line inserts; could chunk by 100 |
| `PostRemittanceBatch` per line | 8 ms | 22 ms | 4 UPDATEs + audit + outbox |
| `PostRemittanceBatch` (1k lines) | 9 s | 25 s | sequential by design (ledger ordering) |
| `ListPendingRemittance` | 4 ms | 12 ms | aggregate query on indexed cols |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Carrier delivers but never remits | daily reconcile sweep | Flag in `mismatch_open`; ops opens carrier ticket. |
| Remittance file double-uploaded | UNIQUE on `(carrier_code, file_date, upload_id)` | Insert fails; surface 409; operator confirms. |
| Same AWB on two remittance files | second line lands on already-remitted shipment; match_state=already_remitted | Line stays unposted; no double-credit (wallet UNIQUE also defends). |
| Wallet credit fails mid-batch | individual line fails; counter shows partial | Batch state = `partially_posted`; ops re-runs Post. |
| Carrier corrects an already-settled remittance | future event with same AWB different amount | Detected as `already_remitted`; ops manually creates correction adjustment. |
| File schema parses 95% of rows correctly but skips 5% | row-level errors recorded in `raw_row`+ `match_notes` | Operators investigate; can re-upload corrected file. |
| Multiple schemas registered for same carrier | one wins via map | Operators specify exact schema_name in upload form. |

## Open Questions

1. **Auto-pulled remittance vs manual upload.** Some carriers expose APIs to pull remittance files programmatically. **Decision: manual for v0**; add `cod.fetch_remittance` river job per carrier when adapters support it.
2. **Per-seller hold-back days.** Some sellers (low risk) get 3-day, others 7-day. Today the model assumes settlement happens on remit. **Decision: defer**; introduce a `settlement_delay_days` per seller via policy when first needed.
3. **Disputes flow.** When mismatch opens, who acts? Today it's ops via ad-hoc action. **Decision:** add a structured dispute workflow in v1 that lets sellers contest.
4. **Fee cut at remittance.** Some platforms keep a percentage. Out of scope for v0 (we charge separately at booking via wallet debit).
5. **Currency invariants.** All amounts are in paise. Floating-point and rupee-formatted carrier files are normalized at parse time; the schema is responsible for rejecting any row that doesn't round to a whole paisa.

## References

- HLD §03-services/04-wallet-and-ledger: wallet ledger underpins COD settlement.
- HLD §03-services/05-tracking-and-status: delivered events trigger COD.
- LLD §03-services/05-wallet: `PostInTx` contract; idempotency UNIQUE.
- LLD §03-services/13-shipments: COD attribute carried at booking.
- LLD §03-services/14-tracking: emits delivered for COD.
- LLD §03-services/18-recon: parallel pattern for weight reconciliation.
- LLD §03-services/01-policy-engine: `cod.max_collected_age_days`, etc.
