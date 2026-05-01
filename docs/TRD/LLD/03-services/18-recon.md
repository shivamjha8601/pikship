# Reconciliation Service (Weight)

## Purpose

Carriers periodically reweigh parcels at their hubs and issue a **weight discrepancy report** that adjusts the billing weight (and therefore the freight charge) for shipments where the seller-declared dimensions/weight differ from what the carrier measured. This is the second-largest revenue-impact area after COD remittance.

Responsibilities:

- Ingest carrier weight-discrepancy files (CSV/Excel/JSON).
- Match each row against a shipment by AWB.
- Quote the **adjustment amount** = (new charge) − (current charge), given the new billing weight.
- Open a **dispute window** (`dispute_open` state) during which the seller can contest with proof (photos, packing list).
- After window expires (or operator approves), apply the adjustment to the wallet ledger.
- Track per-seller weight-mismatch frequency for risk and rate-card-tuning purposes.

This service deliberately mirrors the COD service's structure (file ingest → batch → match → post) because the operations team will use both flows interchangeably.

Out of scope:

- Buyer/operator dispute UI — admin/ops (LLD §03-services/23).
- Image storage for proof — adapter (S3) shared with KYC.
- Pricing engine itself — pricing (LLD §03-services/06); we ask it to re-quote.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | money, IDs, errors. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/wallet` | post adjustments. |
| `internal/shipments` | shipment lookup by AWB; charges_paise. |
| `internal/pricing` | re-quote freight at carrier-reported weight. |
| `internal/policy` | dispute window days, auto-approve threshold. |
| `internal/audit`, `internal/outbox` | standard. |

## Package Layout

```
internal/recon/
├── service.go
├── service_impl.go
├── repo.go
├── types.go               // WeightDiscrepancy, ReconBatch, ReconLine
├── lifecycle.go
├── jobs.go                // IngestFileJob, AutoApproveJob, PostBatchJob
├── errors.go
├── events.go
├── service_test.go
└── service_slt_test.go
```

## Discrepancy Lifecycle

```
       Ingest carrier file
              │
              ▼
  ┌──────────────────────────┐
  │      raised              │  ← row matched to shipment; delta computed
  └────────┬─────────────────┘
           │  (auto if delta ≤ auto_approve_threshold)
           │  (or after dispute_window_days elapse with no contest)
           ▼
  ┌──────────────────────────┐
  │      approved            │  pending wallet post
  └────────┬─────────────────┘
           │  Post()
           ▼
  ┌──────────────────────────┐
  │      posted              │  wallet debited; closed
  └──────────────────────────┘

Side branches:
  raised → disputed   (seller contests within window)
  raised → rejected   (operator rejects; carrier eats it)
  disputed → approved (after operator review)
  disputed → rejected (after operator review)
```

```go
type DiscrepancyState string

const (
    StateRaised    DiscrepancyState = "raised"
    StateDisputed  DiscrepancyState = "disputed"
    StateApproved  DiscrepancyState = "approved"
    StatePosted    DiscrepancyState = "posted"
    StateRejected  DiscrepancyState = "rejected"
)
```

## Public API

```go
package recon

type Service interface {
    // IngestFile is invoked by an operator from the admin console.
    // Parses the file, creates a recon_batch, inserts recon_line rows,
    // attempts to match each line against a shipment.
    IngestFile(ctx context.Context, req IngestFileRequest) (*ReconBatch, error)

    // SubmitDispute is invoked from the seller dashboard.
    SubmitDispute(ctx context.Context, req DisputeRequest) (*WeightDiscrepancy, error)

    // ApproveDiscrepancy moves a single discrepancy to approved state
    // (operator override; bypasses dispute window).
    ApproveDiscrepancy(ctx context.Context, id core.DiscrepancyID, operatorID core.UserID) error

    // RejectDiscrepancy moves to rejected (carrier's claim is wrong;
    // we don't charge the seller).
    RejectDiscrepancy(ctx context.Context, id core.DiscrepancyID, operatorID core.UserID, reason string) error

    // RunAutoApproveSweep runs daily; promotes raised→approved when
    // dispute window has elapsed.
    RunAutoApproveSweep(ctx context.Context) error

    // PostBatch is the financial step. Posts all approved discrepancies
    // in a batch as wallet adjustments.
    PostBatch(ctx context.Context, batchID core.ReconBatchID) error

    // Reads
    Get(ctx context.Context, sellerID core.SellerID, id core.DiscrepancyID) (*WeightDiscrepancy, error)
    List(ctx context.Context, q ListQuery) (ListResult, error)
}
```

### Request / Response Types

```go
type IngestFileRequest struct {
    OperatorID  core.UserID
    CarrierCode string
    FileDate    time.Time
    UploadID    string         // S3 key
    SchemaName  string         // "delhivery_weight_v1" | ...
}

type DisputeRequest struct {
    SellerID         core.SellerID
    DiscrepancyID    core.DiscrepancyID
    OperatorID       core.UserID    // seller's user
    Reason           string
    EvidenceObjectKeys []string     // S3 keys for photos/etc.
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("recon: not found")
    ErrInvalidState          = errors.New("recon: invalid state")
    ErrDisputeWindowExpired  = errors.New("recon: dispute window has expired")
    ErrFileSchemaUnknown     = errors.New("recon: unknown file schema")
    ErrAlreadyPosted         = errors.New("recon: already posted")
)
```

## DB Schema

```sql
CREATE TABLE recon_batch (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_code    text        NOT NULL,
    file_date       date        NOT NULL,
    schema_name     text        NOT NULL,
    upload_id       text        NOT NULL,
    operator_id     uuid        NOT NULL REFERENCES app_user(id),

    state           text        NOT NULL CHECK (state IN ('parsed','posted','partially_posted','closed')),
    line_count      integer     NOT NULL DEFAULT 0,
    matched_count   integer     NOT NULL DEFAULT 0,
    unmatched_count integer     NOT NULL DEFAULT 0,
    posted_count    integer     NOT NULL DEFAULT 0,
    total_delta_paise        bigint NOT NULL DEFAULT 0,
    posted_delta_paise       bigint NOT NULL DEFAULT 0,
    parsed_at       timestamptz NOT NULL DEFAULT now(),
    posted_at       timestamptz,
    UNIQUE (carrier_code, file_date, upload_id)
);

CREATE TABLE weight_discrepancy (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    recon_batch_id     uuid        NOT NULL REFERENCES recon_batch(id),
    seller_id          uuid        REFERENCES seller(id),
    shipment_id        uuid        REFERENCES shipment(id),
    carrier_code       text        NOT NULL,
    awb                text        NOT NULL,

    -- Snapshot from booking
    declared_weight_g   integer    NOT NULL,
    declared_volumetric_g integer  NOT NULL,
    original_charge_paise bigint   NOT NULL,

    -- Carrier-reported
    new_weight_g        integer    NOT NULL,
    new_volumetric_g    integer    NOT NULL,
    new_charge_paise    bigint     NOT NULL,
    new_billing_weight_g integer   NOT NULL,

    delta_paise         bigint     NOT NULL,            -- new − original

    state               text       NOT NULL CHECK (state IN ('raised','disputed','approved','posted','rejected')),

    dispute_window_until timestamptz,
    disputed_at         timestamptz,
    dispute_reason      text,
    dispute_evidence    jsonb,
    decided_at          timestamptz,
    decided_by          uuid REFERENCES app_user(id),
    decision_reason     text,
    posted_at           timestamptz,

    raw_row             jsonb       NOT NULL,
    matched             boolean     NOT NULL DEFAULT false,
    match_state         text,                     -- "ok" | "unknown_awb" | "shipment_cancelled"

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX weight_discrepancy_seller_state_idx
    ON weight_discrepancy(seller_id, state)
    WHERE seller_id IS NOT NULL;
CREATE INDEX weight_discrepancy_batch_idx
    ON weight_discrepancy(recon_batch_id);
CREATE INDEX weight_discrepancy_dispute_window_idx
    ON weight_discrepancy(dispute_window_until)
    WHERE state = 'raised';

ALTER TABLE weight_discrepancy ENABLE ROW LEVEL SECURITY;
CREATE POLICY weight_disc_isolation ON weight_discrepancy
    USING (seller_id IS NULL OR seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON recon_batch, weight_discrepancy TO pikshipp_app;
GRANT SELECT ON recon_batch, weight_discrepancy TO pikshipp_reports;
```

## sqlc Queries

```sql
-- name: ReconBatchInsert :one
INSERT INTO recon_batch (
    id, carrier_code, file_date, schema_name, upload_id, operator_id, state
) VALUES ($1, $2, $3, $4, $5, $6, 'parsed')
RETURNING *;

-- name: WeightDiscrepancyInsert :one
INSERT INTO weight_discrepancy (
    id, recon_batch_id, seller_id, shipment_id, carrier_code, awb,
    declared_weight_g, declared_volumetric_g, original_charge_paise,
    new_weight_g, new_volumetric_g, new_charge_paise, new_billing_weight_g,
    delta_paise, state, dispute_window_until, raw_row, matched, match_state
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19
)
RETURNING *;

-- name: WeightDiscrepancyGet :one
SELECT * FROM weight_discrepancy WHERE id = $1;

-- name: WeightDiscrepancyMarkDisputed :one
UPDATE weight_discrepancy
SET state = 'disputed',
    disputed_at = now(),
    dispute_reason = $2,
    dispute_evidence = $3,
    updated_at = now()
WHERE id = $1 AND state = 'raised' AND dispute_window_until > now()
RETURNING *;

-- name: WeightDiscrepancyDecide :one
UPDATE weight_discrepancy
SET state = $2,
    decided_at = now(),
    decided_by = $3,
    decision_reason = $4,
    updated_at = now()
WHERE id = $1 AND state IN ('raised','disputed')
RETURNING *;

-- name: WeightDiscrepancyMarkPosted :one
UPDATE weight_discrepancy
SET state = 'posted', posted_at = now(), updated_at = now()
WHERE id = $1 AND state = 'approved'
RETURNING *;

-- name: WeightDiscrepancyAutoApproveDue :many
SELECT * FROM weight_discrepancy
WHERE state = 'raised' AND dispute_window_until <= $1
LIMIT $2;

-- name: WeightDiscrepancyApprovedNotPosted :many
SELECT * FROM weight_discrepancy
WHERE recon_batch_id = $1 AND state = 'approved';
```

## Implementation Highlights

### IngestFile

```go
func (s *service) IngestFile(ctx context.Context, req IngestFileRequest) (*ReconBatch, error) {
    schema, ok := schemas[req.SchemaName]
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

    autoApproveThresholdPaise, _ := s.policy.GetIntGlobal(ctx, "recon.auto_approve_threshold_paise")
    if autoApproveThresholdPaise == 0 {
        autoApproveThresholdPaise = 1000 // ≤ ₹10
    }
    disputeWindowDays, _ := s.policy.GetIntGlobal(ctx, "recon.dispute_window_days")
    if disputeWindowDays == 0 {
        disputeWindowDays = 7
    }
    deadline := s.clock.Now().AddDate(0, 0, disputeWindowDays)

    var batch *ReconBatch
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        b, err := qtx.ReconBatchInsert(ctx, sqlcgen.ReconBatchInsertParams{
            ID:          core.NewReconBatchID().UUID(),
            CarrierCode: req.CarrierCode,
            FileDate:    req.FileDate,
            SchemaName:  req.SchemaName,
            UploadID:    req.UploadID,
            OperatorID:  req.OperatorID.UUID(),
        })
        if err != nil {
            return err
        }
        batch = batchFromRow(b)

        var totalDelta int64
        var matched, unmatched int

        for _, p := range parsed {
            sh, err := s.shipments.GetByAWBSystem(ctx, p.AWB)
            matchState := "ok"
            if err != nil {
                matchState = "unknown_awb"
            } else if shipmentTerminalCancelled(sh) {
                matchState = "shipment_cancelled"
            }

            // Compute new charge using pricing engine.
            var newChargePaise int64
            var newBillingWeightG int
            if matchState == "ok" {
                quote, err := s.pricing.QuoteForRecon(ctx, pricing.QuoteForReconRequest{
                    SellerID:    sh.SellerID,
                    CarrierCode: p.CarrierCode,
                    OriginPin:   sh.PickupAddrSnapshot.Pincode,
                    DestPin:     sh.DropPincode,
                    WeightG:     p.WeightG,
                    LengthMM:    p.LengthMM, WidthMM: p.WidthMM, HeightMM: p.HeightMM,
                    ServiceType: sh.ServiceType,
                    PaymentMode: sh.PaymentMode(),
                })
                if err != nil {
                    return err
                }
                newChargePaise = int64(quote.TotalPaise)
                newBillingWeightG = quote.BillingWeightG
            } else {
                newChargePaise = int64(p.CarrierClaimedChargePaise) // trust file when we can't recompute
            }

            delta := newChargePaise - func() int64 {
                if sh != nil {
                    return sh.ChargesPaise
                }
                return 0
            }()

            // Auto-approve small deltas.
            initialState := DiscrepancyState(StateRaised)
            if matchState == "ok" && abs64(delta) <= int64(autoApproveThresholdPaise) {
                initialState = StateApproved
            }

            row, err := qtx.WeightDiscrepancyInsert(ctx, sqlcgen.WeightDiscrepancyInsertParams{
                ID:                  core.NewDiscrepancyID().UUID(),
                ReconBatchID:        batch.ID.UUID(),
                SellerID:            pgxNullUUIDFrom(sellerOf(sh)),
                ShipmentID:          pgxNullUUIDFrom(shipmentOf(sh)),
                CarrierCode:         p.CarrierCode,
                AWB:                 p.AWB,
                DeclaredWeightG:     int32(declaredWeightG(sh)),
                DeclaredVolumetricG: int32(declaredVolumetricG(sh)),
                OriginalChargePaise: chargesOf(sh),
                NewWeightG:          int32(p.WeightG),
                NewVolumetricG:      int32(p.VolumetricG),
                NewChargePaise:      newChargePaise,
                NewBillingWeightG:   int32(newBillingWeightG),
                DeltaPaise:          delta,
                State:               string(initialState),
                DisputeWindowUntil:  pgxNullTimestamp(deadline),
                RawRow:              jsonbFrom(p.Raw),
                Matched:             matchState == "ok",
                MatchState:          pgxNullString(matchState),
            })
            if err != nil {
                return err
            }
            _ = row

            if matchState == "ok" {
                matched++
                totalDelta += delta
            } else {
                unmatched++
            }
        }
        // Update batch
        // ... ReconBatchUpdateProgress

        return nil
    })
    return batch, err
}
```

### SubmitDispute

```go
func (s *service) SubmitDispute(ctx context.Context, req DisputeRequest) (*WeightDiscrepancy, error) {
    var out *WeightDiscrepancy
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.WeightDiscrepancyMarkDisputed(ctx, sqlcgen.WeightDiscrepancyMarkDisputedParams{
            ID:              req.DiscrepancyID.UUID(),
            DisputeReason:   pgxNullString(req.Reason),
            DisputeEvidence: jsonbFrom(map[string]any{"object_keys": req.EvidenceObjectKeys}),
        })
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrDisputeWindowExpired
            }
            return err
        }
        out = discrepancyFromRow(row)

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Actor:    audit.ActorUser(req.OperatorID),
            Action:   "recon.dispute.opened",
            Object:   audit.ObjDiscrepancy(req.DiscrepancyID),
            Payload:  map[string]any{"reason": req.Reason},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "recon.dispute.opened",
            Key:  string(req.DiscrepancyID),
            Payload: map[string]any{"discrepancy_id": req.DiscrepancyID, "reason": req.Reason},
        })
    })
    return out, err
}
```

### Approve / Reject

```go
func (s *service) ApproveDiscrepancy(ctx context.Context, id core.DiscrepancyID, operatorID core.UserID) error {
    return s.decide(ctx, id, operatorID, StateApproved, "operator_approved")
}

func (s *service) RejectDiscrepancy(ctx context.Context, id core.DiscrepancyID, operatorID core.UserID, reason string) error {
    return s.decide(ctx, id, operatorID, StateRejected, reason)
}

func (s *service) decide(ctx context.Context, id core.DiscrepancyID, operatorID core.UserID, target DiscrepancyState, reason string) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.WeightDiscrepancyDecide(ctx, sqlcgen.WeightDiscrepancyDecideParams{
            ID:             id.UUID(),
            State:          string(target),
            DecidedBy:      pgxNullUUID(&operatorID),
            DecisionReason: pgxNullString(reason),
        })
        if err != nil {
            return err
        }
        sellerID := core.SellerIDFromUUID(row.SellerID.UUID)
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: sellerID,
            Actor:    audit.ActorUser(operatorID),
            Action:   "recon." + string(target),
            Object:   audit.ObjDiscrepancy(id),
            Payload:  map[string]any{"reason": reason},
        })
    })
}
```

### RunAutoApproveSweep (daily)

```go
func (s *service) RunAutoApproveSweep(ctx context.Context) error {
    rows, err := s.q.WeightDiscrepancyAutoApproveDue(ctx, sqlcgen.WeightDiscrepancyAutoApproveDueParams{
        Now: s.clock.Now(), Limit: 500,
    })
    if err != nil {
        return err
    }
    for _, r := range rows {
        // Skip if not matched (we can't auto-approve unmatched lines)
        if !r.Matched {
            continue
        }
        if err := s.decide(ctx, core.DiscrepancyIDFromUUID(r.ID), s.systemActorID(), StateApproved, "auto_approved_after_window"); err != nil {
            slog.Warn("recon: auto-approve failed", "id", r.ID, "err", err)
        }
    }
    return nil
}
```

### PostBatch

```go
func (s *service) PostBatch(ctx context.Context, batchID core.ReconBatchID) error {
    rows, err := s.q.WeightDiscrepancyApprovedNotPosted(ctx, batchID.UUID())
    if err != nil {
        return err
    }
    var posted int
    var postedDelta int64
    for _, r := range rows {
        if err := s.postOne(ctx, r); err != nil {
            slog.Warn("recon: post failed", "id", r.ID, "err", err)
            continue
        }
        posted++
        postedDelta += r.DeltaPaise
    }
    // ... update batch progress
    return nil
}

func (s *service) postOne(ctx context.Context, row sqlcgen.WeightDiscrepancy) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        // Direction: positive delta → debit seller (we collected too little; carrier wants more)
        //            negative delta → credit seller (refund overcharge)
        direction := wallet.DirectionDebit
        amount := row.DeltaPaise
        if amount < 0 {
            direction = wallet.DirectionCredit
            amount = -amount
        }
        if amount > 0 {
            if err := s.wallet.PostInTx(ctx, tx, wallet.PostRequest{
                SellerID:    core.SellerIDFromUUID(row.SellerID.UUID),
                AmountPaise: core.Paise(amount),
                RefType:     "recon_adjustment",
                RefID:       row.ID.String(),
                Direction:   direction,
                Reason:      "weight_recon_adjustment",
            }); err != nil {
                return err
            }
        }
        if _, err := qtx.WeightDiscrepancyMarkPosted(ctx, row.ID); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(row.SellerID.UUID),
            Action:   "recon.posted",
            Object:   audit.ObjDiscrepancy(core.DiscrepancyIDFromUUID(row.ID)),
            Payload:  map[string]any{"delta_paise": row.DeltaPaise, "direction": direction},
        })
    })
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `IngestFile` per row | 6 ms | 18 ms | shipment lookup + pricing quote + INSERT |
| `IngestFile` 1k rows | 6 s | 18 s | scaling linearly |
| `SubmitDispute` | 5 ms | 15 ms | UPDATE + audit + outbox |
| `Approve/Reject` | 5 ms | 15 ms | UPDATE + audit |
| `PostBatch` per row | 8 ms | 22 ms | wallet post + UPDATE |
| `RunAutoApproveSweep` 100 rows | 1 s | 3 s | sequential decides |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| File row references unknown AWB | match fails | Stored with `match_state=unknown_awb`; ops investigates manually. |
| Same file uploaded twice | UNIQUE on `(carrier, file_date, upload_id)` | 409; ops confirms. |
| Pricing engine fails to quote | `QuoteForRecon` errors | Row stored with current delta = `new_charge - 0`; treated as match failed. Operator can re-run after fixing rate cards. |
| Wallet debit fails on posting (insufficient funds) | `wallet.ErrInsufficient` | Discrepancy stays `approved`; `posted` flag false; ops re-tries after seller tops up. **Decision:** at v0, defer; consider `seller_arrears` table at v1. |
| Auto-approve runs before file fully ingested | window-until is far in the future | Not possible — auto-approve sweep filters on `dispute_window_until <= now`. |
| Seller disputes after posting | window already expired; ApproveDiscrepancy/Decide blocked by state guard | Surface `ErrInvalidState`; sellers must contact support. |

## Testing

```go
func TestIngestFile_AutoApproveSmallDelta_SLT(t *testing.T) { /* ... */ }
func TestIngestFile_LargeDelta_StaysRaised_SLT(t *testing.T) { /* ... */ }
func TestSubmitDispute_WithinWindow_SLT(t *testing.T) { /* ... */ }
func TestSubmitDispute_AfterWindow_Rejected_SLT(t *testing.T) { /* ... */ }
func TestAutoApproveSweep_PromotesExpired_SLT(t *testing.T) { /* ... */ }
func TestPostBatch_Idempotent_SLT(t *testing.T) {
    // wallet UNIQUE prevents double-post.
}
func TestRLS_DiscrepancyIsolation_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Per-seller dispute caps.** Habitual disputers may abuse the window. **Decision:** add per-seller dispute_rate metric; visible to ops; introduce caps in v1 if abused.
2. **Bulk dispute via CSV.** Sellers may want to dispute 100 lines at once. **Decision:** add bulk-dispute endpoint when first requested.
3. **Charges to buyer (excess weight = buyer's fault?).** Some plans pass the charge to the buyer. Out of scope for v0.
4. **Photo evidence storage.** S3 is the canonical store. Each evidence object is referenced by `dispute_evidence` JSONB; cleaner than a separate table.
5. **Multi-carrier batch in one file.** Some sellers consolidate. **Decision:** schema must declare carrier; multi-carrier files require per-row carrier resolution → defer.

## References

- HLD §03-services/04-wallet-and-ledger: wallet adjustments here.
- HLD §03-services/02-pricing: re-quote for new weight.
- LLD §03-services/05-wallet: PostInTx idempotency.
- LLD §03-services/06-pricing: QuoteForRecon contract.
- LLD §03-services/13-shipments: shipment lookup by AWB (system role).
- LLD §03-services/16-cod: parallel pattern (file ingest → match → post).
- LLD §03-services/01-policy-engine: `recon.auto_approve_threshold_paise`, `recon.dispute_window_days`.
