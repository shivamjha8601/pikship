# NDR Service

## Purpose

NDR — **Non-Delivery Report** — is what carriers report when they fail to deliver a parcel. The NDR service owns the **state and workflow** around these failed delivery attempts: counting attempts, soliciting buyer/seller decisions (reattempt, change address, return-to-origin), forwarding those decisions to the carrier, and ultimately driving shipments toward terminal states (delivered or RTO).

NDR is the highest-leverage workflow in the system: India's average NDR-to-delivered conversion sits around 35-50% across carriers, and how quickly we engage the buyer determines the bulk of that delta. This service exists to keep that loop tight.

Responsibilities:

- Consume `ndr.detected` events from tracking and create `ndr_case` records.
- Track per-shipment **attempt count** and configurable **attempt-cap** policy.
- Drive **buyer outreach** (SMS / email / WhatsApp) to collect a decision.
- Drive **seller outreach** when buyer doesn't respond.
- Forward the chosen action to the carrier via `Adapter.RaiseNDRAction`.
- Auto-RTO when no decision arrives within the policy window.

Out of scope:

- The actual notification dispatch — notifications service (LLD §03-services/20).
- RTO mechanics post-action — RTO/returns service (LLD §03-services/17).
- Tracking ingest — tracking (LLD §03-services/14).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, clock. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/shipments` | shipment lookup + `ApplyTrackingStateInTx` for RTO state. |
| `internal/tracking` | `ndr.detected` consumption (via outbox forwarder). |
| `internal/carriers/framework` | `Adapter.RaiseNDRAction`. |
| `internal/notifications` | enqueue buyer/seller messages. |
| `internal/policy` | reattempt-cap, response-window, allowed-actions. |
| `internal/audit`, `internal/outbox`, `internal/idempotency` | standard. |

## Package Layout

```
internal/ndr/
├── service.go             // Service interface
├── service_impl.go        // Implementation
├── repo.go
├── types.go               // Case, Action, AttemptResult
├── lifecycle.go           // Case state machine
├── jobs.go                // OnDetectedJob, BuyerNudgeJob, AutoRTOJob
├── errors.go
├── events.go
├── service_test.go
└── service_slt_test.go
```

## NDR Case State Machine

```
                    ndr.detected event
                            │
                            ▼
            ┌──────────────────────────┐
            │       open               │  awaiting buyer decision
            └────────┬─────────────────┘
                     │
   ┌─────────────────┼──────────────────┐
   │                 │                  │
buyer chooses     seller chooses      timeout (no response)
"reattempt"       "change_address"     within policy window
   │                 │                  │
   ▼                 ▼                  ▼
┌──────────┐    ┌──────────┐    ┌──────────┐
│ requested│    │ requested│    │auto_rto  │
│_reattempt│    │_addr_chg │    │_pending  │
└────┬─────┘    └────┬─────┘    └────┬─────┘
     │  carrier accepts            │
     ▼                              ▼
┌──────────────┐          ┌──────────────┐
│ in_carrier   │          │ rto_in_progress (handed off to RTO svc)
│  _retrying   │          └──────────────┘
└────┬─────────┘
     │
   ┌─┴───────────────────────┐
   ▼                         ▼
delivered (case closed)   another NDR event
                          → back to open

Hard caps:
- 3 reattempts max (policy: ndr.max_reattempts)
- After 3rd attempt, case auto-progresses to auto_rto_pending
```

```go
type CaseState string

const (
    CaseStateOpen                 CaseState = "open"
    CaseStateRequestedReattempt   CaseState = "requested_reattempt"
    CaseStateRequestedAddrChange  CaseState = "requested_addr_change"
    CaseStateInCarrierRetrying    CaseState = "in_carrier_retrying"
    CaseStateAutoRTOPending       CaseState = "auto_rto_pending"
    CaseStateRTOInitiated         CaseState = "rto_initiated"
    CaseStateDeliveredOnReattempt CaseState = "delivered_on_reattempt"
    CaseStateClosed               CaseState = "closed"
)

type Action string

const (
    ActionReattempt    Action = "reattempt"
    ActionChangeAddress Action = "change_address"
    ActionRTO          Action = "rto"
    ActionContactBuyer Action = "contact_buyer" // soft action: just send another nudge
)
```

## Public API

```go
package ndr

type Service interface {
    // OnNDRDetected is the entry point invoked by the outbox forwarder
    // when tracking emits ndr.detected. Idempotent on (shipment_id, occurred_at).
    //
    // Creates or updates an open case, increments attempt counter, and
    // schedules buyer outreach.
    OnNDRDetected(ctx context.Context, req OnNDRDetectedRequest) (*Case, error)

    // SubmitBuyerDecision is called from the buyer-experience service
    // when a buyer responds to a tracking-page action prompt or SMS.
    SubmitBuyerDecision(ctx context.Context, req BuyerDecisionRequest) (*Case, error)

    // SubmitSellerDecision is called from the seller dashboard.
    SubmitSellerDecision(ctx context.Context, req SellerDecisionRequest) (*Case, error)

    // GetByShipment returns the open or most recent case for a shipment.
    GetByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (*Case, error)

    // List drives the seller dashboard NDR queue.
    List(ctx context.Context, q ListQuery) (ListResult, error)

    // RunAutoRTOSweep is a periodic job that promotes cases that have
    // exceeded the response window or attempt cap to RTO.
    RunAutoRTOSweep(ctx context.Context) error
}
```

### Request / Response Types

```go
type OnNDRDetectedRequest struct {
    SellerID    core.SellerID
    ShipmentID  core.ShipmentID
    OrderID     core.OrderID
    OccurredAt  time.Time
    Location    string
    RawReason   string
}

type BuyerDecisionRequest struct {
    PublicToken string         // tracking page token; resolves to shipment
    Action      Action         // reattempt | change_address | rto
    NewAddress  *Address       // required if Action == change_address
    Notes       string
}

type SellerDecisionRequest struct {
    SellerID   core.SellerID
    CaseID     core.NDRCaseID
    Action     Action
    NewAddress *Address
    OperatorID core.UserID
    Notes      string
}

type ListQuery struct {
    SellerID core.SellerID
    States   []CaseState
    Page     Page
}
```

### Sentinel Errors

```go
var (
    ErrNotFound              = errors.New("ndr: case not found")
    ErrInvalidAction         = errors.New("ndr: action not allowed in current state")
    ErrInvalidActionForCarrier = errors.New("ndr: action unsupported by carrier")
    ErrAttemptCapExceeded    = errors.New("ndr: max reattempts exceeded")
    ErrAddressRequired       = errors.New("ndr: change_address requires new address")
    ErrNoOpenCase            = errors.New("ndr: no open case for shipment")
)
```

## DB Schema

```sql
CREATE TABLE ndr_case (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id      uuid        NOT NULL REFERENCES seller(id),
    shipment_id    uuid        NOT NULL REFERENCES shipment(id),
    order_id       uuid        NOT NULL REFERENCES order_record(id),

    state          text        NOT NULL CHECK (state IN
        ('open','requested_reattempt','requested_addr_change',
         'in_carrier_retrying','auto_rto_pending','rto_initiated',
         'delivered_on_reattempt','closed')),

    -- Attempt tracking: every distinct ndr.detected for this shipment
    -- bumps attempt_count.
    attempt_count       integer NOT NULL DEFAULT 0,
    last_attempt_at     timestamptz,
    last_attempt_reason text,
    last_attempt_location text,

    -- Buyer/seller engagement
    buyer_nudges_sent     integer NOT NULL DEFAULT 0,
    last_buyer_nudge_at   timestamptz,
    seller_nudges_sent    integer NOT NULL DEFAULT 0,
    last_seller_nudge_at  timestamptz,

    -- Decision
    decision_action       text,    -- "reattempt" | "change_address" | "rto"
    decision_by           text,    -- "buyer" | "seller" | "auto"
    decision_actor_id     uuid,    -- user_id for seller decisions
    decision_at           timestamptz,
    new_address           jsonb,   -- only for change_address decisions

    -- Carrier interaction
    carrier_action_sent_at timestamptz,
    carrier_action_response jsonb,

    -- Auto-progression deadline: when a buyer hasn't responded by then,
    -- the auto-RTO sweep promotes the case.
    response_window_until timestamptz,

    closed_at      timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- One open case per shipment at a time
CREATE UNIQUE INDEX ndr_case_one_open_per_shipment
    ON ndr_case (shipment_id)
    WHERE state IN ('open','requested_reattempt','requested_addr_change',
                    'in_carrier_retrying','auto_rto_pending');

CREATE INDEX ndr_case_seller_state_idx ON ndr_case(seller_id, state);
CREATE INDEX ndr_case_response_window_idx
    ON ndr_case(response_window_until)
    WHERE state = 'open';

ALTER TABLE ndr_case ENABLE ROW LEVEL SECURITY;
CREATE POLICY ndr_case_isolation ON ndr_case
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Attempt log: append-only audit of each NDR event
CREATE TABLE ndr_attempt_event (
    id            bigserial   PRIMARY KEY,
    ndr_case_id   uuid        NOT NULL REFERENCES ndr_case(id),
    seller_id     uuid        NOT NULL REFERENCES seller(id),
    occurred_at   timestamptz NOT NULL,
    location      text,
    raw_reason    text,
    raw_payload   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ndr_attempt_case_idx ON ndr_attempt_event(ndr_case_id, occurred_at);

ALTER TABLE ndr_attempt_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY ndr_attempt_isolation ON ndr_attempt_event
    USING (seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON ndr_case, ndr_attempt_event TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE ndr_attempt_event_id_seq TO pikshipp_app;
GRANT SELECT ON ndr_case, ndr_attempt_event TO pikshipp_reports;
```

## sqlc Queries

```sql
-- name: NDRCaseGetOpenByShipment :one
SELECT * FROM ndr_case
WHERE shipment_id = $1
  AND state IN ('open','requested_reattempt','requested_addr_change',
                'in_carrier_retrying','auto_rto_pending')
ORDER BY created_at DESC
LIMIT 1;

-- name: NDRCaseGetByID :one
SELECT * FROM ndr_case WHERE id = $1;

-- name: NDRCaseInsert :one
INSERT INTO ndr_case (
    id, seller_id, shipment_id, order_id, state,
    attempt_count, last_attempt_at, last_attempt_reason, last_attempt_location,
    response_window_until
) VALUES (
    $1, $2, $3, $4, 'open',
    1, $5, $6, $7,
    $8
)
RETURNING *;

-- name: NDRCaseRecordAttempt :one
UPDATE ndr_case
SET attempt_count = attempt_count + 1,
    last_attempt_at = $2,
    last_attempt_reason = $3,
    last_attempt_location = $4,
    state = 'open',                       -- back to open if it was retrying
    decision_action = NULL,
    decision_by = NULL,
    decision_actor_id = NULL,
    decision_at = NULL,
    response_window_until = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: NDRAttemptEventInsert :exec
INSERT INTO ndr_attempt_event (ndr_case_id, seller_id, occurred_at, location, raw_reason, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: NDRCaseRecordDecision :one
UPDATE ndr_case
SET state = $2,
    decision_action = $3,
    decision_by = $4,
    decision_actor_id = $5,
    decision_at = now(),
    new_address = $6,
    response_window_until = NULL,
    updated_at = now()
WHERE id = $1 AND state IN ('open','auto_rto_pending')
RETURNING *;

-- name: NDRCaseRecordCarrierResponse :one
UPDATE ndr_case
SET state = $2,
    carrier_action_sent_at = now(),
    carrier_action_response = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: NDRCaseClose :one
UPDATE ndr_case
SET state = $2, closed_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: NDRCaseListAutoRTODue :many
SELECT * FROM ndr_case
WHERE state = 'open' AND response_window_until <= $1
ORDER BY response_window_until
LIMIT $2;

-- name: NDRCaseBumpBuyerNudge :exec
UPDATE ndr_case
SET buyer_nudges_sent = buyer_nudges_sent + 1,
    last_buyer_nudge_at = now()
WHERE id = $1;
```

## Implementation

### OnNDRDetected

```go
package ndr

func (s *service) OnNDRDetected(ctx context.Context, req OnNDRDetectedRequest) (*Case, error) {
    // Read attempt cap and response window from policy.
    cap, _ := s.policy.GetInt(ctx, req.SellerID, "ndr.max_reattempts")
    if cap == 0 {
        cap = 3 // default
    }
    windowHours, _ := s.policy.GetInt(ctx, req.SellerID, "ndr.response_window_hours")
    if windowHours == 0 {
        windowHours = 24
    }
    deadline := req.OccurredAt.Add(time.Duration(windowHours) * time.Hour)

    var out *Case
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        // Look up existing open case
        existing, err := qtx.NDRCaseGetOpenByShipment(ctx, req.ShipmentID.UUID())
        if err != nil && !errors.Is(err, pgx.ErrNoRows) {
            return err
        }

        if errors.Is(err, pgx.ErrNoRows) {
            // First-ever NDR for this shipment → new case.
            row, err := qtx.NDRCaseInsert(ctx, sqlcgen.NDRCaseInsertParams{
                ID:                  core.NewNDRCaseID().UUID(),
                SellerID:            req.SellerID.UUID(),
                ShipmentID:          req.ShipmentID.UUID(),
                OrderID:             req.OrderID.UUID(),
                LastAttemptAt:       pgxNullTimestamp(req.OccurredAt),
                LastAttemptReason:   pgxNullString(req.RawReason),
                LastAttemptLocation: pgxNullString(req.Location),
                ResponseWindowUntil: pgxNullTimestamp(deadline),
            })
            if err != nil {
                var pgErr *pgconn.PgError
                if errors.As(err, &pgErr) && pgErr.ConstraintName == "ndr_case_one_open_per_shipment" {
                    // Race: another process inserted concurrently. Re-read.
                    row, err = qtx.NDRCaseGetOpenByShipment(ctx, req.ShipmentID.UUID())
                    if err != nil {
                        return err
                    }
                } else {
                    return err
                }
            }
            out = caseFromRow(row)
        } else {
            // Existing open case: bump attempt count.
            row, err := qtx.NDRCaseRecordAttempt(ctx, sqlcgen.NDRCaseRecordAttemptParams{
                ID:                  existing.ID,
                LastAttemptAt:       req.OccurredAt,
                LastAttemptReason:   pgxNullString(req.RawReason),
                LastAttemptLocation: pgxNullString(req.Location),
                ResponseWindowUntil: pgxNullTimestamp(deadline),
            })
            if err != nil {
                return err
            }
            out = caseFromRow(row)
        }

        if err := qtx.NDRAttemptEventInsert(ctx, sqlcgen.NDRAttemptEventInsertParams{
            NDRCaseID:  out.ID.UUID(),
            SellerID:   req.SellerID.UUID(),
            OccurredAt: req.OccurredAt,
            Location:   pgxNullString(req.Location),
            RawReason:  pgxNullString(req.RawReason),
            RawPayload: jsonbFrom(map[string]any{}),
        }); err != nil {
            return err
        }

        // Audit
        if err := s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Action:   "ndr.attempt.recorded",
            Object:   audit.ObjShipment(req.ShipmentID),
            Payload:  map[string]any{"attempt": out.AttemptCount, "reason": req.RawReason},
        }); err != nil {
            return err
        }

        // If we've hit the cap, jump straight to auto_rto_pending.
        if out.AttemptCount >= cap {
            row, err := qtx.NDRCaseRecordDecision(ctx, sqlcgen.NDRCaseRecordDecisionParams{
                ID:        out.ID.UUID(),
                State:     string(CaseStateAutoRTOPending),
                DecisionAction: pgxNullString(string(ActionRTO)),
                DecisionBy:     pgxNullString("auto"),
            })
            if err != nil {
                return err
            }
            out = caseFromRow(row)
            return s.outb.Emit(ctx, tx, outbox.Event{
                Kind: "ndr.attempt_cap_reached",
                Key:  string(out.ID),
                Payload: map[string]any{"case_id": out.ID, "shipment_id": req.ShipmentID, "attempts": out.AttemptCount},
            })
        }

        // Otherwise, schedule buyer nudge job.
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "ndr.buyer_nudge_due",
            Key:  string(out.ID),
            Payload: map[string]any{"case_id": out.ID, "shipment_id": req.ShipmentID},
        })
    })
    return out, err
}
```

### SubmitBuyerDecision

```go
func (s *service) SubmitBuyerDecision(ctx context.Context, req BuyerDecisionRequest) (*Case, error) {
    // Resolve shipment via tracking public token (this is how the buyer
    // identifies themselves on the public tracking page).
    sh, err := s.tracking.GetShipmentByPublicToken(ctx, req.PublicToken)
    if err != nil {
        return nil, ErrNotFound
    }
    if req.Action == ActionChangeAddress && req.NewAddress == nil {
        return nil, ErrAddressRequired
    }
    return s.recordDecision(ctx, recordDecisionInput{
        SellerID:   sh.SellerID,
        ShipmentID: sh.ID,
        Action:     req.Action,
        NewAddress: req.NewAddress,
        DecisionBy: "buyer",
        Notes:      req.Notes,
    })
}

func (s *service) SubmitSellerDecision(ctx context.Context, req SellerDecisionRequest) (*Case, error) {
    if req.Action == ActionChangeAddress && req.NewAddress == nil {
        return nil, ErrAddressRequired
    }
    return s.recordDecision(ctx, recordDecisionInput{
        SellerID:    req.SellerID,
        CaseID:      &req.CaseID,
        Action:      req.Action,
        NewAddress:  req.NewAddress,
        DecisionBy:  "seller",
        DecisionActorID: &req.OperatorID,
        Notes:       req.Notes,
    })
}

func (s *service) recordDecision(ctx context.Context, in recordDecisionInput) (*Case, error) {
    // Resolve case
    var caseRow sqlcgen.NDRCase
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        var err error
        if in.CaseID != nil {
            caseRow, err = qtx.NDRCaseGetByID(ctx, in.CaseID.UUID())
        } else {
            caseRow, err = qtx.NDRCaseGetOpenByShipment(ctx, in.ShipmentID.UUID())
        }
        if err != nil {
            return ErrNoOpenCase
        }
        if caseRow.SellerID != in.SellerID.UUID() {
            return ErrNotFound // RLS would also catch this; defensive
        }

        // Validate carrier supports the action.
        sh, err := s.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(caseRow.ShipmentID))
        if err != nil {
            return err
        }
        a, err := s.carriers.Get(sh.CarrierCode)
        if err != nil {
            return err
        }
        caps := a.Capabilities()
        if !carrierSupportsAction(caps, in.Action) {
            return fmt.Errorf("%w: carrier=%s action=%s", ErrInvalidActionForCarrier, sh.CarrierCode, in.Action)
        }

        // Determine new state.
        newState := stateFromAction(in.Action)

        var newAddrJSON []byte
        if in.NewAddress != nil {
            newAddrJSON, _ = json.Marshal(in.NewAddress)
        }
        row, err := qtx.NDRCaseRecordDecision(ctx, sqlcgen.NDRCaseRecordDecisionParams{
            ID:               caseRow.ID,
            State:            string(newState),
            DecisionAction:   pgxNullString(string(in.Action)),
            DecisionBy:       pgxNullString(in.DecisionBy),
            DecisionActorID:  pgxNullUUID(in.DecisionActorID),
            NewAddress:       pgxNullJSON(newAddrJSON),
        })
        if err != nil {
            return err
        }
        caseRow = row

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: in.SellerID,
            Action:   fmt.Sprintf("ndr.decision.%s", in.Action),
            Object:   audit.ObjShipment(core.ShipmentIDFromUUID(caseRow.ShipmentID)),
            Payload:  map[string]any{"action": in.Action, "by": in.DecisionBy},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "ndr.action.requested",
            Key:  string(caseRow.ID),
            Payload: map[string]any{
                "case_id":     caseRow.ID,
                "shipment_id": caseRow.ShipmentID,
                "action":      in.Action,
                "new_address": in.NewAddress,
            },
        })
    })
    if err != nil {
        return nil, err
    }
    return caseFromRow(caseRow), nil
}

func stateFromAction(a Action) CaseState {
    switch a {
    case ActionReattempt:    return CaseStateRequestedReattempt
    case ActionChangeAddress: return CaseStateRequestedAddrChange
    case ActionRTO:          return CaseStateAutoRTOPending
    default:                 return CaseStateOpen
    }
}

func carrierSupportsAction(caps framework.Capabilities, a Action) bool {
    // Simple capability check; expand as carriers expose more nuance.
    switch a {
    case ActionRTO:           return true        // every carrier supports RTO
    case ActionReattempt:     return true
    case ActionChangeAddress: return caps.SupportsAddressChange
    case ActionContactBuyer:  return true
    }
    return false
}
```

### Carrier Action Forwarder

The decision is recorded synchronously but **forwarding to the carrier** happens via a river job (so the user-facing API doesn't wait on the carrier and we can retry on transient failure).

```go
type ForwardCarrierActionJob struct {
    river.JobArgs
    CaseID core.NDRCaseID
}
func (ForwardCarrierActionJob) Kind() string { return "ndr.forward_carrier_action" }

type ForwardCarrierActionWorker struct {
    river.WorkerDefaults[ForwardCarrierActionJob]
    svc *service
}

func (w *ForwardCarrierActionWorker) Work(ctx context.Context, j *river.Job[ForwardCarrierActionJob]) error {
    return w.svc.forwardCarrierAction(ctx, j.Args.CaseID)
}

func (s *service) forwardCarrierAction(ctx context.Context, caseID core.NDRCaseID) error {
    row, err := s.q.NDRCaseGetByID(ctx, caseID.UUID())
    if err != nil {
        return err
    }
    sh, _ := s.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(row.ShipmentID))
    a, err := s.carriers.Get(sh.CarrierCode)
    if err != nil {
        return err
    }

    var newAddr *Address
    if len(row.NewAddress) > 0 {
        var addr Address
        _ = json.Unmarshal(row.NewAddress, &addr)
        newAddr = &addr
    }

    res := a.RaiseNDRAction(ctx, framework.NDRActionRequest{
        SellerID:   core.SellerIDFromUUID(row.SellerID),
        AWB:        sh.AWB,
        Action:     framework.NDRAction(row.DecisionAction.String),
        Notes:      "via pikshipp",
        NewAddress: newAddr,
    })
    if !res.OK {
        if res.Retryable {
            return fmt.Errorf("ndr: carrier transient: %w", res.Err) // river retries
        }
        // Permanent: record the failure; case stays in requested state
        // and ops decides next.
        if _, err := s.q.NDRCaseRecordCarrierResponse(ctx, sqlcgen.NDRCaseRecordCarrierResponseParams{
            ID:                    caseID.UUID(),
            State:                 row.State, // unchanged
            CarrierActionResponse: jsonbFrom(map[string]any{"err": errMsg(res), "permanent": true}),
        }); err != nil {
            return err
        }
        return nil
    }

    nextState := CaseStateInCarrierRetrying
    if Action(row.DecisionAction.String) == ActionRTO {
        nextState = CaseStateRTOInitiated
    }
    if _, err := s.q.NDRCaseRecordCarrierResponse(ctx, sqlcgen.NDRCaseRecordCarrierResponseParams{
        ID:                    caseID.UUID(),
        State:                 string(nextState),
        CarrierActionResponse: jsonbFrom(map[string]any{"ok": true, "next_event_estimate": res.Value.NextEventEstimate}),
    }); err != nil {
        return err
    }

    // For RTO: emit event so RTO service starts tracking the inbound shipment.
    if Action(row.DecisionAction.String) == ActionRTO {
        return s.outb.Emit(ctx, nil /* outside-tx ok for non-critical */, outbox.Event{
            Kind: "shipment.rto.initiated",
            Key:  string(caseID),
            Payload: map[string]any{
                "case_id": caseID, "shipment_id": row.ShipmentID, "by": row.DecisionBy.String,
            },
        })
    }
    return nil
}
```

### Auto-RTO Sweep

```go
type AutoRTOSweepJob struct{ river.JobArgs }
func (AutoRTOSweepJob) Kind() string { return "ndr.auto_rto_sweep" }

func (s *service) RunAutoRTOSweep(ctx context.Context) error {
    rows, err := s.q.NDRCaseListAutoRTODue(ctx, sqlcgen.NDRCaseListAutoRTODueParams{
        Now: s.clock.Now(), Limit: 200,
    })
    if err != nil {
        return err
    }
    for _, r := range rows {
        if err := s.autoPromoteToRTO(ctx, core.NDRCaseIDFromUUID(r.ID)); err != nil {
            slog.Warn("ndr: auto-rto failed", "case_id", r.ID, "err", err)
        }
    }
    return nil
}

func (s *service) autoPromoteToRTO(ctx context.Context, caseID core.NDRCaseID) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.NDRCaseRecordDecision(ctx, sqlcgen.NDRCaseRecordDecisionParams{
            ID:             caseID.UUID(),
            State:          string(CaseStateAutoRTOPending),
            DecisionAction: pgxNullString(string(ActionRTO)),
            DecisionBy:     pgxNullString("auto"),
        })
        if err != nil {
            return err
        }
        _ = row
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(row.SellerID),
            Action:   "ndr.auto_rto",
            Object:   audit.ObjShipment(core.ShipmentIDFromUUID(row.ShipmentID)),
            Payload:  map[string]any{"reason": "no_buyer_response_within_window"},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "ndr.action.requested",
            Key:  string(caseID),
            Payload: map[string]any{
                "case_id": caseID, "shipment_id": row.ShipmentID, "action": ActionRTO,
            },
        })
    })
}
```

`AutoRTOSweepJob` is registered as a river periodic job firing every 5 minutes.

### Buyer Nudge Job

```go
type BuyerNudgeJob struct {
    river.JobArgs
    CaseID core.NDRCaseID
}
func (BuyerNudgeJob) Kind() string { return "ndr.buyer_nudge" }

type BuyerNudgeWorker struct {
    river.WorkerDefaults[BuyerNudgeJob]
    svc *service
}

func (w *BuyerNudgeWorker) Work(ctx context.Context, j *river.Job[BuyerNudgeJob]) error {
    row, err := w.svc.q.NDRCaseGetByID(ctx, j.Args.CaseID.UUID())
    if err != nil {
        return err
    }
    if CaseState(row.State) != CaseStateOpen {
        return nil // case progressed; skip nudge
    }
    sh, _ := w.svc.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(row.ShipmentID))
    order, _ := w.svc.orders.GetSystem(ctx, core.OrderIDFromUUID(row.OrderID))
    token, _ := w.svc.tracking.GetOrCreatePublicTokenSystem(ctx, sh.ID, sh.SellerID)

    if err := w.svc.notif.SendNDRBuyerNudge(ctx, notifications.NDRBuyerNudgeRequest{
        SellerID:   sh.SellerID,
        OrderID:    sh.OrderID,
        ShipmentID: sh.ID,
        BuyerPhone: order.BuyerPhone,
        BuyerEmail: order.BuyerEmail,
        TrackingURL: w.svc.cfg.TrackingPublicBaseURL + "/" + token,
        AttemptCount: int(row.AttemptCount),
    }); err != nil {
        return err
    }
    return w.svc.q.NDRCaseBumpBuyerNudge(ctx, j.Args.CaseID.UUID())
}
```

## Outbox Routing

Forwarder routes:
- `ndr.detected` (from tracking) → `ndr.OnNDRDetectedJob`
- `ndr.buyer_nudge_due` → `ndr.BuyerNudgeJob`
- `ndr.action.requested` → `ndr.ForwardCarrierActionJob`
- `ndr.attempt_cap_reached` → `notifications.SellerOpsAlertJob`
- `shipment.rto.initiated` → consumed by RTO service (LLD §03-services/17)

## Testing

### Unit Tests

- `TestStateFromAction` — action → next state.
- `TestCarrierSupportsAction_AddressChange` — caps gating.

### SLT (`service_slt_test.go`)

```go
func TestOnNDRDetected_FirstAttempt_CreatesOpenCase_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedShipment(t, pg)
    svc := slt.NDR(pg)
    c, err := svc.OnNDRDetected(ctx, OnNDRDetectedRequest{
        SellerID: sh.SellerID, ShipmentID: sh.ID, OrderID: sh.OrderID,
        OccurredAt: slt.Now(), Location: "Mumbai", RawReason: "address_not_found",
    })
    require.NoError(t, err)
    require.Equal(t, CaseStateOpen, c.State)
    require.Equal(t, 1, c.AttemptCount)
    require.True(t, slt.OutboxHas(t, pg, "ndr.buyer_nudge_due", string(c.ID)))
}

func TestOnNDRDetected_SecondAttempt_BumpsCount_SLT(t *testing.T) {
    // Same shipment; second event bumps attempt_count to 2; same case row.
    // ...
}

func TestOnNDRDetected_HitsCap_PromotesAutoRTO_SLT(t *testing.T) {
    // Set ndr.max_reattempts=2 in policy. Third event auto-promotes.
    // ...
}

func TestSubmitBuyerDecision_Reattempt_SLT(t *testing.T) {
    // Buyer chose reattempt → state=requested_reattempt, outbox has ndr.action.requested.
    // ...
}

func TestSubmitBuyerDecision_ChangeAddress_RequiresAddress_SLT(t *testing.T) { /* ... */ }

func TestForwardCarrierAction_Success_SLT(t *testing.T) {
    // sandbox carrier accepts; case transitions to in_carrier_retrying.
    // ...
}

func TestForwardCarrierAction_PermanentFail_StaysInRequested_SLT(t *testing.T) { /* ... */ }

func TestAutoRTOSweep_PromotesExpired_SLT(t *testing.T) {
    // Case open with response_window_until in the past; sweep moves to auto_rto_pending.
    // ...
}

func TestRLS_NDRCaseIsolation_SLT(t *testing.T) { /* ... */ }
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `OnNDRDetected` (existing case) | 6 ms | 18 ms | UPDATE + INSERT attempt + outbox |
| `OnNDRDetected` (new case) | 8 ms | 22 ms | INSERT + INSERT + outbox |
| `SubmitBuyerDecision` | 7 ms | 20 ms | UPDATE + audit + outbox |
| `ForwardCarrierAction` | + carrier RTT + 8 ms | dominated by carrier API |
| `RunAutoRTOSweep` per case | 5 ms | 15 ms | UPDATE + outbox + audit |
| `BuyerNudgeJob` | + SMS API RTT + 4 ms | dominated by notifications backend |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Carrier doesn't support `change_address` | capability check fails | Surface `ErrInvalidActionForCarrier` to seller; offer reattempt or RTO. |
| Two `ndr.detected` events fire concurrently for same shipment | UNIQUE on `ndr_case_one_open_per_shipment` | Second insert fails; we re-read existing case; idempotent. |
| Carrier rejects forwarded action | Permanent error class | Case stays in `requested_*` state; logged; ops manually intervenes. |
| Auto-RTO sweep doesn't run (river down) | response_window_until rows accumulate | Lag metric alarms; ops triggers manual sweep. |
| Buyer responds AFTER auto-RTO promoted | Decision attempt rejects (state=auto_rto_pending) | Buyer-experience UI surfaces "RTO already initiated"; offers contact-support flow. |
| Buyer nudge SMS rate-limited | notifications service surfaces error | River retries with backoff; nudge counter not bumped on failure. |
| Address-change decision sent but carrier later refuses (after `RaiseNDRAction` returned ok) | New NDR event with reason="address_invalid" | Treated as another attempt; bumps counter; eventually auto-RTO. |

## Open Questions

1. **Buyer self-service via WhatsApp.** WhatsApp Business API is not in v0. SMS + tracking-page works but conversion is lower. **Decision: defer**; integrate when notifications service supports it.
2. **Per-pincode reattempt success rates.** Some pincodes have systematically poor delivery; carriers won't reattempt productively. **Decision:** out of scope for v0; could feed allocation reliability scoring later.
3. **Operator-initiated RTO from ops console.** Today only seller can RTO via dashboard; operators have to log in as the seller. **Decision: add operator endpoint with explicit audit at v0.5**; minor.
4. **Correlate NDR with carrier's "remarks" field.** Carriers stuff freeform text in tracking events; sometimes that contains buyer phone-said-X. **Decision:** persist in raw_payload; surface as a UI hint; don't parse.
5. **Case re-open after closure.** If a "delivered" event comes in 30 days later (carrier portal correction), the case is closed. **Decision:** don't re-open; treat as informational; alert ops.

## References

- HLD §03-services/05-tracking-and-status: NDR detection signal.
- LLD §03-services/14-tracking: emits `ndr.detected`.
- LLD §03-services/17-rto-returns: consumes `shipment.rto.initiated`.
- LLD §03-services/12-carriers-framework: `Adapter.RaiseNDRAction`, capability matrix.
- LLD §03-services/20-notifications: buyer/seller nudge dispatch.
- LLD §03-services/01-policy-engine: `ndr.max_reattempts`, `ndr.response_window_hours`.
- LLD §03-services/13-shipments: shipment lookup (system role).
