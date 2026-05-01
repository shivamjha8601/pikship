# Tracking Service

## Purpose

The tracking service is the **status-update plane** for shipments: it ingests carrier events (via webhook **or** polling) and translates carrier-specific raw status strings into Pikshipp's normalized status model. It then drives shipment state transitions and broadcasts `shipment.tracking.updated` events to downstream consumers (notifications, NDR detection, COD reconciliation).

Responsibilities:

- Ingest webhook events (carrier-pushed) — verify signature, persist raw event, normalize.
- Poll for events (carrier-pulled) — schedule per-shipment polls based on capability.
- Maintain the **carrier status normalization map** (per-carrier → canonical status).
- Drive shipment state transitions (`booked → in_transit → delivered`, etc.).
- Detect NDR (failed delivery attempts) and emit `ndr.detected` for the NDR service.
- Provide a stable tracking-history read API for sellers and buyers.

Out of scope:

- Booking — shipments (LLD §03-services/13).
- NDR action handling — NDR (LLD §03-services/15).
- Buyer-facing tracking page UI — buyer-experience (LLD §03-services/21).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, clock. |
| `internal/db` | `pgxpool.Pool`, `WithTx`. |
| `internal/shipments` | shipment state writes via service callbacks. |
| `internal/carriers/framework` | `Adapter.FetchTrackingEvents`, signature helpers. |
| `internal/audit`, `internal/outbox`, `internal/idempotency` | standard. |
| `internal/policy` | poll intervals, NDR-attempt thresholds. |
| HTTP server | webhook endpoints. |

## Package Layout

```
internal/tracking/
├── service.go             // Service interface
├── service_impl.go        // Implementation
├── webhook.go             // Webhook handlers + signature verification
├── poller.go              // Polling worker
├── normalize.go           // Per-carrier status → canonical status mapping
├── repo.go
├── types.go               // Event, CanonicalStatus, NDRAttempt
├── errors.go
├── jobs.go                // PollShipmentJob, RegisterAWBJob
├── events.go
├── service_test.go
├── normalize_test.go
├── webhook_test.go
└── service_slt_test.go
```

## Canonical Status Model

This is the **stable** status enum that the rest of the system uses. Carrier-specific strings are mapped here at ingest time.

```go
package tracking

type CanonicalStatus string

const (
    StatusUnknown            CanonicalStatus = "unknown"

    // Pre-pickup
    StatusBookingConfirmed   CanonicalStatus = "booking_confirmed"
    StatusPickupScheduled    CanonicalStatus = "pickup_scheduled"
    StatusPickupAttempted    CanonicalStatus = "pickup_attempted"
    StatusPickupFailed       CanonicalStatus = "pickup_failed"
    StatusPickedUp           CanonicalStatus = "picked_up"

    // In transit
    StatusInTransit          CanonicalStatus = "in_transit"
    StatusOutForDelivery     CanonicalStatus = "out_for_delivery"
    StatusReachedDestHub     CanonicalStatus = "reached_dest_hub"

    // Terminal-success
    StatusDelivered          CanonicalStatus = "delivered"

    // NDR
    StatusDeliveryAttempted  CanonicalStatus = "delivery_attempted" // failed; will reattempt
    StatusUndeliverable      CanonicalStatus = "undeliverable"      // carrier giving up

    // RTO
    StatusRTOInitiated       CanonicalStatus = "rto_initiated"
    StatusRTOInTransit       CanonicalStatus = "rto_in_transit"
    StatusRTODelivered       CanonicalStatus = "rto_delivered"

    // Lost / damaged
    StatusLost               CanonicalStatus = "lost"
    StatusDamaged            CanonicalStatus = "damaged"

    // Cancelled
    StatusCancelled          CanonicalStatus = "cancelled"
)
```

### Status → Shipment-State Mapping

```go
var statusToShipmentState = map[CanonicalStatus]shipments.ShipmentState{
    StatusBookingConfirmed:  shipments.StateBooked,
    StatusPickupScheduled:   shipments.StateBooked,
    StatusPickupAttempted:   shipments.StateBooked,
    StatusPickupFailed:      shipments.StateBooked,
    StatusPickedUp:          shipments.StateInTransit,
    StatusInTransit:         shipments.StateInTransit,
    StatusOutForDelivery:    shipments.StateInTransit,
    StatusReachedDestHub:    shipments.StateInTransit,
    StatusDeliveryAttempted: shipments.StateInTransit, // still in transit per carrier
    StatusUndeliverable:     shipments.StateInTransit, // RTO will follow
    StatusDelivered:         shipments.StateDelivered,
    StatusRTOInitiated:      shipments.StateRTOInProgress,
    StatusRTOInTransit:      shipments.StateRTOInProgress,
    StatusRTODelivered:      shipments.StateRTOCompleted,
    StatusCancelled:         shipments.StateCancelled,
    StatusLost:              shipments.StateInTransit, // operator decides; we surface alert
    StatusDamaged:           shipments.StateInTransit, // ditto
}
```

## Public API

```go
package tracking

type Service interface {
    // RegisterAWB starts tracking for a newly-booked shipment.
    // For carriers with PullsTrackingEvents=true, schedules a poll.
    // For PushesWebhookEvents, records the AWB so webhook routing works.
    RegisterAWB(ctx context.Context, req RegisterAWBRequest) error

    // IngestWebhook is called by the HTTP webhook handler after
    // signature verification. Persists raw event, normalizes, and
    // applies state changes synchronously.
    IngestWebhook(ctx context.Context, carrierCode string, raw RawWebhookEvent) (IngestResult, error)

    // IngestPolled is called by the polling worker for one carrier event.
    // Same semantics as IngestWebhook but no signature step.
    IngestPolled(ctx context.Context, shipmentID core.ShipmentID, ev framework.TrackingEvent) (IngestResult, error)

    // GetHistory returns tracking events for a shipment in chronological order.
    GetHistory(ctx context.Context, shipmentID core.ShipmentID) ([]TrackingEvent, error)

    // GetByPublicToken returns a sanitized history for a buyer-facing
    // tracking page. The token is stable, unguessable per shipment.
    GetByPublicToken(ctx context.Context, token string) (*PublicTrackingView, error)
}

type IngestResult struct {
    Persisted     bool
    Duplicate     bool
    StateChanged  bool
    NewStatus     CanonicalStatus
}
```

### Sentinel Errors

```go
var (
    ErrNotFound          = errors.New("tracking: not found")
    ErrInvalidSignature  = errors.New("tracking: webhook signature invalid")
    ErrUnknownAWB        = errors.New("tracking: unknown AWB")
    ErrEventOutOfOrder   = errors.New("tracking: event older than current state")
    ErrUnsupportedCarrier = errors.New("tracking: carrier has no webhook or poll support")
)
```

## DB Schema

```sql
CREATE TABLE tracking_event (
    id              bigserial   PRIMARY KEY,
    shipment_id     uuid        NOT NULL REFERENCES shipment(id),
    seller_id       uuid        NOT NULL REFERENCES seller(id),

    carrier_code    text        NOT NULL,
    awb             text        NOT NULL,
    raw_status      text        NOT NULL,
    canonical_status text       NOT NULL,
    location        text,
    occurred_at     timestamptz NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now(),

    source          text        NOT NULL CHECK (source IN ('webhook','poll','manual')),
    raw_payload     jsonb       NOT NULL,

    -- Dedupe key: (carrier, awb, raw_status, occurred_at). Carriers
    -- frequently push the same event multiple times.
    dedupe_key      text        NOT NULL,
    UNIQUE (dedupe_key)
);

CREATE INDEX tracking_event_shipment_occurred_idx
    ON tracking_event(shipment_id, occurred_at);
CREATE INDEX tracking_event_awb_idx
    ON tracking_event(awb);
CREATE INDEX tracking_event_canonical_status_idx
    ON tracking_event(canonical_status, occurred_at);

ALTER TABLE tracking_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY tracking_event_isolation ON tracking_event
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Carrier webhook raw archive: kept for replay/debug. Append-only.
CREATE TABLE carrier_webhook_archive (
    id            bigserial   PRIMARY KEY,
    carrier_code  text        NOT NULL,
    received_at   timestamptz NOT NULL DEFAULT now(),
    headers       jsonb       NOT NULL,
    body          bytea       NOT NULL,
    signature_ok  boolean     NOT NULL,
    parsed_count  integer     NOT NULL DEFAULT 0
);

CREATE INDEX carrier_webhook_archive_received_idx
    ON carrier_webhook_archive(carrier_code, received_at DESC);

-- Polling schedule: one row per actively-tracked shipment for pull-only carriers.
CREATE TABLE tracking_poll_schedule (
    shipment_id    uuid        PRIMARY KEY REFERENCES shipment(id) ON DELETE CASCADE,
    seller_id      uuid        NOT NULL REFERENCES seller(id),
    carrier_code   text        NOT NULL,
    awb            text        NOT NULL,
    next_poll_at   timestamptz NOT NULL,
    last_poll_at   timestamptz,
    interval_sec   integer     NOT NULL,        -- starts at 600 (10 min); back off on no change
    last_status    text,
    consecutive_no_change_count integer NOT NULL DEFAULT 0,
    paused         boolean     NOT NULL DEFAULT false,
    paused_reason  text,
    UNIQUE (carrier_code, awb)
);

CREATE INDEX tracking_poll_due_idx
    ON tracking_poll_schedule(next_poll_at) WHERE paused = false;

ALTER TABLE tracking_poll_schedule ENABLE ROW LEVEL SECURITY;
CREATE POLICY poll_schedule_isolation ON tracking_poll_schedule
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Public tracking tokens (buyer-facing tracking pages)
CREATE TABLE tracking_public_token (
    token          text        PRIMARY KEY,
    shipment_id    uuid        NOT NULL REFERENCES shipment(id) ON DELETE CASCADE,
    seller_id      uuid        NOT NULL REFERENCES seller(id),
    expires_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tracking_public_token_shipment_idx
    ON tracking_public_token(shipment_id);
-- No RLS: tokens are explicitly buyer-facing; access controlled by token possession.

-- Grants
GRANT SELECT, INSERT, UPDATE ON
    tracking_event, carrier_webhook_archive, tracking_poll_schedule, tracking_public_token
TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE tracking_event_id_seq, carrier_webhook_archive_id_seq TO pikshipp_app;
GRANT SELECT ON tracking_event, tracking_poll_schedule TO pikshipp_reports;
```

## sqlc Queries

```sql
-- name: TrackingEventInsert :one
INSERT INTO tracking_event (
    shipment_id, seller_id, carrier_code, awb,
    raw_status, canonical_status, location, occurred_at,
    source, raw_payload, dedupe_key
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
ON CONFLICT (dedupe_key) DO NOTHING
RETURNING *;

-- name: TrackingEventListByShipment :many
SELECT * FROM tracking_event
WHERE shipment_id = $1
ORDER BY occurred_at, id;

-- name: TrackingEventLatestStatus :one
SELECT canonical_status, occurred_at FROM tracking_event
WHERE shipment_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT 1;

-- name: PollScheduleUpsert :exec
INSERT INTO tracking_poll_schedule (
    shipment_id, seller_id, carrier_code, awb,
    next_poll_at, interval_sec
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (shipment_id) DO UPDATE
    SET next_poll_at = EXCLUDED.next_poll_at,
        carrier_code = EXCLUDED.carrier_code,
        awb = EXCLUDED.awb,
        paused = false;

-- name: PollScheduleDue :many
SELECT * FROM tracking_poll_schedule
WHERE paused = false AND next_poll_at <= $1
ORDER BY next_poll_at
LIMIT $2;

-- name: PollScheduleRecordPoll :exec
UPDATE tracking_poll_schedule
SET last_poll_at = now(),
    last_status = $2,
    next_poll_at = $3,
    interval_sec = $4,
    consecutive_no_change_count = $5
WHERE shipment_id = $1;

-- name: PollSchedulePause :exec
UPDATE tracking_poll_schedule
SET paused = true, paused_reason = $2 WHERE shipment_id = $1;

-- name: WebhookArchiveInsert :one
INSERT INTO carrier_webhook_archive (carrier_code, headers, body, signature_ok, parsed_count)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: PublicTokenInsert :exec
INSERT INTO tracking_public_token (token, shipment_id, seller_id, expires_at)
VALUES ($1, $2, $3, $4);

-- name: PublicTokenLookup :one
SELECT * FROM tracking_public_token
WHERE token = $1 AND (expires_at IS NULL OR expires_at > now());
```

## Webhook Ingestion

Webhook is the **fast path**. Pseudo-flow:

```
HTTP POST /webhooks/{carrier}
   ↓
Webhook handler (per-carrier)
   ↓
[1] Read body (raw bytes; do NOT parse yet)
   ↓
[2] Verify signature via adapter (HMAC-SHA256 / IP / etc.)
   ↓
[3] Archive raw body in carrier_webhook_archive
   ↓
[4] Adapter.ParseWebhook(body) → []framework.TrackingEvent
   ↓
[5] For each event:
       service.IngestWebhook(carrier, event)
   ↓
[6] Reply 200 quickly (carrier-side timeout is short)
```

```go
package tracking

// WebhookHandler is the chi handler factory.
func WebhookHandler(svc Service, registry *framework.Registry) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        carrier := chi.URLParam(r, "carrier")
        a, err := registry.Get(carrier)
        if err != nil {
            http.Error(w, "unknown carrier", http.StatusNotFound)
            return
        }
        verifier, ok := a.(framework.WebhookVerifier)
        if !ok {
            http.Error(w, "carrier does not accept webhooks", http.StatusBadRequest)
            return
        }
        body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20)) // 4 MiB hard cap
        if err != nil {
            http.Error(w, "read", http.StatusBadRequest)
            return
        }
        sigOK := verifier.VerifyWebhook(r.Header, body)
        // Archive regardless of signature OK — useful for replaying
        // payloads after a key rotation incident.
        archiveID, _ := svc.(*service).archiveWebhook(r.Context(), carrier, r.Header, body, sigOK)
        if !sigOK {
            slog.Warn("tracking: bad signature", "carrier", carrier, "archive_id", archiveID)
            http.Error(w, "bad signature", http.StatusUnauthorized)
            return
        }
        events, err := verifier.ParseWebhook(body)
        if err != nil {
            slog.Warn("tracking: parse webhook", "carrier", carrier, "err", err)
            http.Error(w, "parse", http.StatusBadRequest)
            return
        }
        for _, ev := range events {
            if _, err := svc.IngestWebhook(r.Context(), carrier, RawWebhookEvent{
                AWB:        ev.AWB,
                Status:     ev.Status,
                Location:   ev.Location,
                OccurredAt: ev.OccurredAt,
                Raw:        ev.RawPayload,
            }); err != nil {
                slog.Warn("tracking: ingest", "carrier", carrier, "awb", ev.AWB, "err", err)
                // Continue with other events — partial success is fine.
            }
        }
        w.WriteHeader(http.StatusOK)
    })
}
```

### IngestWebhook (and IngestPolled)

```go
func (s *service) IngestWebhook(ctx context.Context, carrierCode string, raw RawWebhookEvent) (IngestResult, error) {
    return s.ingest(ctx, carrierCode, raw, "webhook")
}

func (s *service) IngestPolled(ctx context.Context, shipmentID core.ShipmentID, ev framework.TrackingEvent) (IngestResult, error) {
    // For polls we already know the shipment; convert to RawWebhookEvent shape.
    return s.ingest(ctx, ev.RawPayload["__carrier_code"].(string), RawWebhookEvent{
        AWB: ev.AWB, Status: ev.Status, Location: ev.Location, OccurredAt: ev.OccurredAt, Raw: ev.RawPayload,
    }, "poll")
}

func (s *service) ingest(ctx context.Context, carrierCode string, raw RawWebhookEvent, source string) (IngestResult, error) {
    // 1. Resolve shipment by AWB (cached map; bypasses RLS — system role).
    sh, err := s.shipmentByAWB(ctx, raw.AWB)
    if err != nil {
        return IngestResult{}, ErrUnknownAWB
    }

    // 2. Normalize status.
    canonical := s.normalizer.Normalize(carrierCode, raw.Status)
    if canonical == StatusUnknown {
        // Don't drop the event — persist with raw_status so the ops
        // team can investigate the missing mapping.
        s.logger.Warn("tracking: unmapped status",
            "carrier", carrierCode, "raw_status", raw.Status, "shipment_id", sh.ID)
    }

    dedupe := dedupeKey(carrierCode, raw.AWB, raw.Status, raw.OccurredAt)

    var result IngestResult
    err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)

        // 3. Insert (idempotent on dedupe_key).
        row, err := qtx.TrackingEventInsert(ctx, sqlcgen.TrackingEventInsertParams{
            ShipmentID:       sh.ID.UUID(),
            SellerID:         sh.SellerID.UUID(),
            CarrierCode:      carrierCode,
            AWB:              raw.AWB,
            RawStatus:        raw.Status,
            CanonicalStatus:  string(canonical),
            Location:         pgxNullString(raw.Location),
            OccurredAt:       raw.OccurredAt,
            Source:           source,
            RawPayload:       jsonbFrom(raw.Raw),
            DedupeKey:        dedupe,
        })
        if err != nil {
            return err
        }
        if row.ID == 0 { // ON CONFLICT DO NOTHING didn't insert
            result.Duplicate = true
            return nil
        }
        result.Persisted = true

        // 4. Apply state-change side effects (only if canonical mapping exists).
        if canonical == StatusUnknown {
            return nil
        }

        // 5. Determine new shipment state.
        targetState, hasMap := statusToShipmentState[canonical]
        if !hasMap {
            return nil
        }
        // Skip if same state.
        if targetState == sh.State {
            // Still emit tracking.updated for downstream consumers.
            return s.outb.Emit(ctx, tx, outbox.Event{
                Kind: "shipment.tracking.updated",
                Key:  string(sh.ID),
                Payload: map[string]any{
                    "shipment_id": sh.ID, "status": canonical, "occurred_at": raw.OccurredAt,
                },
            })
        }

        // 6. Apply transition. The shipments service exposes a callback
        //    so this layer doesn't directly mutate shipment rows.
        if err := s.shipments.ApplyTrackingStateInTx(ctx, tx, shipments.ApplyTrackingStateRequest{
            ShipmentID: sh.ID,
            FromState:  sh.State,
            ToState:    targetState,
            CanonicalStatus: string(canonical),
            Reason:     fmt.Sprintf("tracking_event:%s", canonical),
        }); err != nil {
            return err
        }
        result.StateChanged = true
        result.NewStatus = canonical

        // 7. Emit outbox event.
        if err := s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "shipment.tracking.updated",
            Key:  string(sh.ID),
            Payload: map[string]any{
                "shipment_id": sh.ID, "status": canonical, "from_state": sh.State,
                "to_state": targetState, "occurred_at": raw.OccurredAt,
            },
        }); err != nil {
            return err
        }

        // 8. Detect NDR.
        if canonical == StatusDeliveryAttempted {
            return s.outb.Emit(ctx, tx, outbox.Event{
                Kind: "ndr.detected",
                Key:  string(sh.ID),
                Payload: map[string]any{
                    "shipment_id": sh.ID, "occurred_at": raw.OccurredAt,
                    "location": raw.Location, "raw_reason": raw.Status,
                },
            })
        }
        return nil
    })
    return result, err
}
```

### Out-of-Order Events

Carriers occasionally push events out of chronological order (especially for hub-rejoin scenarios). Our model handles this gracefully:

- Events are stored with `occurred_at`; reads always sort by it.
- State transitions only apply if the **canonical status maps to a state we can transition to** from current shipment state. An older "in_transit" event arriving when the shipment is "delivered" is persisted (history) but does NOT regress state, because `delivered → in_transit` is not in the allowed transitions table.
- The shipments service's `ApplyTrackingStateInTx` enforces this monotonicity.

## Polling

For carriers without webhook support, we poll. Poll intervals are adaptive:

```go
package tracking

// nextPollDelay returns the interval until the next poll. Start at 10
// minutes; on N consecutive polls with no change, double up to 6 hours.
// On a change, reset to 10 minutes.

const (
    minPollInterval = 10 * time.Minute
    maxPollInterval = 6 * time.Hour
)

func nextPollDelay(prev time.Duration, statusChanged bool) time.Duration {
    if statusChanged {
        return minPollInterval
    }
    next := prev * 2
    if next < minPollInterval {
        return minPollInterval
    }
    if next > maxPollInterval {
        return maxPollInterval
    }
    return next
}
```

```go
type PollSweepJob struct{ river.JobArgs }
func (PollSweepJob) Kind() string { return "tracking.poll_sweep" }

type PollSweepWorker struct {
    river.WorkerDefaults[PollSweepJob]
    svc *service
}

func (w *PollSweepWorker) Work(ctx context.Context, j *river.Job[PollSweepJob]) error {
    rows, err := w.svc.q.PollScheduleDue(ctx, sqlcgen.PollScheduleDueParams{
        Now: w.svc.clock.Now(), Limit: 200,
    })
    if err != nil {
        return err
    }
    for _, row := range rows {
        if err := w.svc.pollOne(ctx, row); err != nil {
            slog.Warn("tracking: poll failed", "shipment_id", row.ShipmentID, "err", err)
        }
    }
    return nil
}

func (s *service) pollOne(ctx context.Context, row sqlcgen.TrackingPollSchedule) error {
    a, err := s.carriers.Get(row.CarrierCode)
    if err != nil {
        return err
    }
    cur, _ := s.q.TrackingEventLatestStatus(ctx, row.ShipmentID)
    since := cur.OccurredAt
    res := a.FetchTrackingEvents(ctx, row.AWB, since)
    if !res.OK {
        // Don't change interval on transient failure; the breaker handles
        // carrier-wide unhealthiness. Bump next_poll_at slightly.
        return s.q.PollScheduleRecordPoll(ctx, sqlcgen.PollScheduleRecordPollParams{
            ShipmentID:                row.ShipmentID,
            LastStatus:                pgxNullString(row.LastStatus.String),
            NextPollAt:                s.clock.Now().Add(time.Duration(row.IntervalSec) * time.Second),
            IntervalSec:               row.IntervalSec,
            ConsecutiveNoChangeCount:  row.ConsecutiveNoChangeCount + 1,
        })
    }
    statusChanged := false
    for _, ev := range res.Value {
        ev.RawPayload["__carrier_code"] = row.CarrierCode
        ir, _ := s.IngestPolled(ctx, core.ShipmentIDFromUUID(row.ShipmentID), ev)
        if ir.StateChanged {
            statusChanged = true
        }
    }
    var newInterval time.Duration
    if statusChanged {
        newInterval = nextPollDelay(0, true)
    } else {
        newInterval = nextPollDelay(time.Duration(row.IntervalSec)*time.Second, false)
    }

    // If shipment is terminal, pause polling.
    sh, _ := s.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(row.ShipmentID))
    if isTerminal(sh.State) {
        return s.q.PollSchedulePause(ctx, sqlcgen.PollSchedulePauseParams{
            ShipmentID: row.ShipmentID, PausedReason: pgxNullString("terminal"),
        })
    }

    return s.q.PollScheduleRecordPoll(ctx, sqlcgen.PollScheduleRecordPollParams{
        ShipmentID:               row.ShipmentID,
        LastStatus:               pgxNullString(string(latestCanonical(res.Value))),
        NextPollAt:               s.clock.Now().Add(newInterval),
        IntervalSec:              int32(newInterval.Seconds()),
        ConsecutiveNoChangeCount: incrementOrReset(row, statusChanged),
    })
}

func isTerminal(state shipments.ShipmentState) bool {
    switch state {
    case shipments.StateDelivered, shipments.StateRTOCompleted, shipments.StateCancelled, shipments.StateFailed:
        return true
    }
    return false
}
```

`PollSweepJob` is registered as a river periodic job firing every 60 seconds.

## Status Normalization

Each carrier ships a normalization map, registered with the tracking service at startup.

```go
package tracking

type Normalizer struct {
    perCarrier map[string]map[string]CanonicalStatus
    mu         sync.RWMutex
}

func NewNormalizer() *Normalizer {
    return &Normalizer{perCarrier: make(map[string]map[string]CanonicalStatus)}
}

func (n *Normalizer) Register(carrier string, m map[string]CanonicalStatus) {
    n.mu.Lock()
    defer n.mu.Unlock()
    n.perCarrier[strings.ToLower(carrier)] = m
}

func (n *Normalizer) Normalize(carrier string, raw string) CanonicalStatus {
    n.mu.RLock()
    defer n.mu.RUnlock()
    m, ok := n.perCarrier[strings.ToLower(carrier)]
    if !ok {
        return StatusUnknown
    }
    if c, ok := m[strings.ToLower(strings.TrimSpace(raw))]; ok {
        return c
    }
    return StatusUnknown
}
```

Example registration in the Delhivery adapter (LLD §04-adapters/01-delhivery):

```go
func (a *Adapter) RegisterStatusMappings(n *tracking.Normalizer) {
    n.Register("delhivery", map[string]tracking.CanonicalStatus{
        "manifested":           tracking.StatusBookingConfirmed,
        "in transit":           tracking.StatusInTransit,
        "out for delivery":     tracking.StatusOutForDelivery,
        "delivered":            tracking.StatusDelivered,
        "ndr":                  tracking.StatusDeliveryAttempted,
        "rto initiated":        tracking.StatusRTOInitiated,
        "rto in transit":       tracking.StatusRTOInTransit,
        "rto delivered":        tracking.StatusRTODelivered,
        "lost":                 tracking.StatusLost,
        "damaged":              tracking.StatusDamaged,
        // ... ~30 more
    })
}
```

### Unknown-status Alerting

Unmapped statuses generate a metric (`tracking_unknown_status_count{carrier=...}`) and a daily ops report. This catches carriers that introduced new statuses we haven't mapped yet.

## Public Buyer Tracking

Sellers expose a tracking page at `https://track.example.com/<token>`. The token is generated when shipment is booked.

```go
// In shipments.commitBookingSuccess, we also call:
//   tracking.IssuePublicToken(shipmentID, sellerID)
//
// Token format: 32 chars, base32-no-pad, urandom.

func (s *service) IssuePublicToken(ctx context.Context, shipmentID core.ShipmentID, sellerID core.SellerID) (string, error) {
    token := generateToken()
    expires := s.clock.Now().Add(120 * 24 * time.Hour) // 4 months
    if err := s.q.PublicTokenInsert(ctx, sqlcgen.PublicTokenInsertParams{
        Token:      token,
        ShipmentID: shipmentID.UUID(),
        SellerID:   sellerID.UUID(),
        ExpiresAt:  pgxNullTimestamp(expires),
    }); err != nil {
        return "", err
    }
    return token, nil
}

func (s *service) GetByPublicToken(ctx context.Context, token string) (*PublicTrackingView, error) {
    row, err := s.q.PublicTokenLookup(ctx, token)
    if err != nil {
        return nil, ErrNotFound
    }
    sh, err := s.shipments.GetSystem(ctx, core.ShipmentIDFromUUID(row.ShipmentID))
    if err != nil {
        return nil, err
    }
    events, err := s.q.TrackingEventListByShipmentSystem(ctx, row.ShipmentID)
    if err != nil {
        return nil, err
    }
    return sanitizeForBuyer(sh, events), nil
}
```

`sanitizeForBuyer`:
- removes raw payload
- removes seller-internal fields
- shows only canonical statuses (not raw carrier strings)
- masks origin pincode last 3 digits
- displays expected delivery + last update only

## Outbox Event Payloads

```go
type TrackingUpdatedPayload struct {
    SchemaVersion int       `json:"schema_version"` // = 1
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    SellerID      string    `json:"seller_id"`
    Status        string    `json:"status"`
    FromState     string    `json:"from_state,omitempty"`
    ToState       string    `json:"to_state,omitempty"`
    OccurredAt    time.Time `json:"occurred_at"`
}

type NDRDetectedPayload struct {
    SchemaVersion int       `json:"schema_version"`
    ShipmentID    string    `json:"shipment_id"`
    OrderID       string    `json:"order_id"`
    SellerID      string    `json:"seller_id"`
    Location      string    `json:"location"`
    RawReason     string    `json:"raw_reason"`
    OccurredAt    time.Time `json:"occurred_at"`
}
```

Forwarder routes:
- `shipment.tracking.updated` → `notifications.SendTrackingUpdateJob`
- `ndr.detected` → `ndr.OnDetectedJob` (NDR service handles attempt counting and buyer outreach)
- `shipment.tracking.updated (status=delivered)` → `cod.OnDeliveredJob` if shipment is COD
- `shipment.tracking.updated (status=rto_delivered)` → `recon.OnRTOJob`

## Testing

### Unit Tests

```go
func TestNormalize_KnownStatus(t *testing.T) {
    n := tracking.NewNormalizer()
    n.Register("delhivery", map[string]tracking.CanonicalStatus{
        "delivered": tracking.StatusDelivered,
    })
    require.Equal(t, tracking.StatusDelivered, n.Normalize("delhivery", "Delivered"))
    require.Equal(t, tracking.StatusUnknown, n.Normalize("delhivery", "wat"))
}

func TestNormalize_CaseInsensitive(t *testing.T) { /* ... */ }
func TestDedupeKey_Stable(t *testing.T) { /* same inputs → same key */ }
func TestNextPollDelay_DoublesUpToCap(t *testing.T) { /* ... */ }
func TestStatusToStateMapping_Coverage(t *testing.T) {
    // Every CanonicalStatus must have an entry in statusToShipmentState
    // (or be explicitly listed as "unmapped" in this test).
}
```

### SLT (`service_slt_test.go`)

```go
func TestIngest_Webhook_NewState_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedShipment(t, pg)
    svc := slt.Tracking(pg)

    res, err := svc.IngestWebhook(ctx, "sb", RawWebhookEvent{
        AWB: sh.AWB, Status: "in_transit", OccurredAt: slt.Now(),
    })
    require.NoError(t, err)
    require.True(t, res.Persisted)
    require.True(t, res.StateChanged)
    require.Equal(t, StatusInTransit, res.NewStatus)

    refreshed, _ := slt.Shipments(pg).GetSystem(ctx, sh.ID)
    require.Equal(t, shipments.StateInTransit, refreshed.State)
}

func TestIngest_DuplicateEvent_NoChange_SLT(t *testing.T) {
    // Send the same (carrier, awb, status, occurred_at) twice.
    // Second call returns Duplicate=true; no state event written.
    // ...
}

func TestIngest_OutOfOrder_DoesNotRegress_SLT(t *testing.T) {
    // Deliver event first, then a stale "in_transit" with earlier
    // occurred_at. Shipment must remain delivered.
    // ...
}

func TestPollSweep_AdaptiveInterval_SLT(t *testing.T) {
    // First poll: status changed → interval reset to 10 min.
    // Second poll: no change → 20 min.
    // Third poll: no change → 40 min.
    // ...
}

func TestWebhook_BadSignature_Rejected_SLT(t *testing.T) { /* ... */ }
func TestWebhook_DuplicateInBatch_AllPersistOnce_SLT(t *testing.T) { /* ... */ }

func TestNDRDetection_EmitsOutboxOnce_SLT(t *testing.T) {
    // delivery_attempted → ndr.detected emitted; second attempt also
    // emits a fresh ndr.detected (NDR service deduplicates).
    // ...
}

func TestPublicToken_GetReturnsSanitizedView_SLT(t *testing.T) {
    // Token lookup returns canonical statuses only; no raw payload;
    // origin pincode masked.
    // ...
}

func TestRLS_TrackingEvents_SLT(t *testing.T) { /* ... */ }
```

### Microbenchmarks

```go
func BenchmarkNormalize_Hit(b *testing.B) {
    n := tracking.NewNormalizer()
    n.Register("c", map[string]tracking.CanonicalStatus{"delivered": tracking.StatusDelivered})
    for i := 0; i < b.N; i++ {
        _ = n.Normalize("c", "delivered")
    }
}
// Target: < 80 ns, 0 allocs.

func BenchmarkDedupeKey(b *testing.B) {
    t := time.Now()
    for i := 0; i < b.N; i++ {
        _ = dedupeKey("delhivery", "AWB123", "in transit", t)
    }
}
// Target: < 200 ns, 1 alloc.
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `IngestWebhook` (one event, no state change) | 4 ms | 12 ms | INSERT + outbox |
| `IngestWebhook` (one event, state change) | 8 ms | 22 ms | + shipment update + state event + outbox |
| `IngestWebhook` (duplicate) | 1.5 ms | 5 ms | INSERT…ON CONFLICT DO NOTHING |
| Webhook handler end-to-end | 25 ms | 80 ms | parse + N events; cap 50 events/payload |
| `PollOne` | 600 ms | 2 s | bound by carrier RTT |
| `Normalize` | 60 ns | 150 ns | map lookup |
| `GetHistory` (50 events) | 3 ms | 10 ms | index on (shipment_id, occurred_at) |
| `GetByPublicToken` | 4 ms | 12 ms | token lookup + history fetch + sanitize |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Carrier sends webhook with bad signature | `verifier.VerifyWebhook` returns false | 401; archive (sigOK=false) for replay; alert if rate exceeds threshold. |
| Carrier sends event for unknown AWB | `shipmentByAWB` returns ErrNotFound | Log warning + drop (return 200 to carrier — webhook replay is more harmful than the missed event). Future: archive in `unknown_awb_events`. |
| Status not in normalization map | Normalize returns StatusUnknown | Persist event with raw_status; no state change; metric increments. |
| Out-of-order event | `ApplyTrackingStateInTx` rejects regression | Event persisted; state untouched. |
| Webhook batch contains 1k events | LimitReader caps body at 4 MB; we additionally bound to 50 events per webhook call | Return 200 + log; carrier will retry; framework processes in chunks. |
| Polling worker can't reach carrier | `FetchTrackingEvents` returns transient error | Don't change interval; circuit breaker decides global health. |
| Polling worker behind by hours | `next_poll_at` lag metric | Alert at p95 > 30 min lag. |
| `tracking_event` table size grows unbounded | partitioning at v1+ | Plan: monthly partition by `received_at` once over 50M rows. |
| Public token enumeration | 32-char random base32 | 160-bit entropy; brute force infeasible. Token expiration adds defense in depth. |

## Open Questions

1. **Late-arriving events past terminal state.** A "delivered" event followed days later by an "out_for_delivery" (clock skew or hub error). Today we persist both but only deliver-state survives. **Decision: keep current behavior**; ops can manually correct if needed.
2. **Per-shipment rather than per-carrier polling.** Some carriers offer batch tracking APIs (poll 100 AWBs at once). **Decision:** introduce as adapter capability when first carrier with batch support is integrated.
3. **Webhook deduplication scope.** Two carriers happen to use the same AWB format → conflict. **Decision:** dedupe key includes `carrier_code`. AWB uniqueness across carriers is not enforced (`shipment.awb` UNIQUE only within a single shipment row).
4. **Public token revocation.** Sellers may want to revoke a buyer's tracking link. **Decision:** add a `revoked_at` column at v1; today, sellers cannot revoke.
5. **PII in raw_payload.** Some carriers include buyer phone in webhook payloads. **Decision:** acceptable to store; the `tracking_event` table is RLS-protected; reports role can read but operators have audit trail.

## References

- HLD §03-services/05-tracking-and-status: status model + event flow.
- LLD §03-services/12-carriers-framework: `Adapter.FetchTrackingEvents`, `WebhookVerifier`.
- LLD §03-services/13-shipments: `ApplyTrackingStateInTx` callback contract.
- LLD §03-services/15-ndr: consumes `ndr.detected` outbox event.
- LLD §03-services/16-cod: consumes `delivered` for COD shipments.
- LLD §03-services/03-outbox: event routing.
- LLD §02-infrastructure/04-http-server: webhook handler wiring.
