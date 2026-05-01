# Service: Tracking & status normalization

> **Module:** `internal/tracking/`
> **Maps to PRD:** [`PRD/04-features/09-tracking.md`](../../../PRD/04-features/09-tracking.md)
>
> Ingest carrier events; normalize to canonical status; drive downstream (NDR, COD, notifications); be the single tracking truth.

## Responsibility

- Receive carrier webhook events; verify; idempotent persist.
- Poll non-webhook carriers.
- Normalize per-carrier codes → canonical status set.
- Persist append-only event log + canonical status transitions.
- Emit outbox events for downstream consumers.
- Reconcile shipments stuck at canonical states.

## Public interface

```go
package tracking

type Service interface {
    // Ingest from webhook or poller
    Ingest(ctx context.Context, carrierID core.CarrierID, events []NormalizedEvent) error

    // Read
    GetShipmentEvents(ctx context.Context, shipmentID core.ShipmentID) ([]Event, error)
    GetCanonicalStatus(ctx context.Context, shipmentID core.ShipmentID) (CanonicalStatus, error)

    // Operational
    PollCarrierActiveShipments(ctx context.Context, carrierID core.CarrierID, batch int) error
    Reconcile(ctx context.Context) error
}

type NormalizedEvent struct {
    ShipmentID    core.ShipmentID
    CarrierEventID string             // for idempotency
    CarrierCode    string
    CanonicalStatus core.CanonicalStatus
    Substatus      string
    Reason         core.NDRReason     // if NDR
    Location       Location
    OccurredAt     time.Time
    RawPayloadRef  string             // S3
    Source         Source             // 'webhook' | 'poll'
}
```

## Data model

```sql
CREATE TABLE tracking_event (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id         UUID NOT NULL,                    -- denormalized for RLS
  shipment_id       UUID NOT NULL,
  carrier_id        UUID NOT NULL,
  carrier_event_id  TEXT NOT NULL,
  carrier_event_code TEXT NOT NULL,
  carrier_event_label TEXT,
  canonical_status  TEXT NOT NULL,
  substatus         TEXT,
  ndr_reason        TEXT,
  location_jsonb    JSONB,
  occurred_at       TIMESTAMPTZ NOT NULL,
  recorded_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  source            TEXT NOT NULL,
  raw_payload_ref   TEXT,                              -- S3 key
  UNIQUE (carrier_id, carrier_event_id)               -- idempotency
);

CREATE INDEX tracking_event_shipment_idx ON tracking_event (shipment_id, occurred_at);

CREATE TABLE shipment_status_history (
  id                UUID PRIMARY KEY,
  seller_id         UUID NOT NULL,
  shipment_id       UUID NOT NULL,
  prev_status       TEXT,
  new_status        TEXT NOT NULL,
  source_event_id   UUID NOT NULL REFERENCES tracking_event(id),
  transition_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX shipment_status_history_shipment_idx ON shipment_status_history (shipment_id, transition_at);
```

`tracking_event` is the firehose; `shipment_status_history` is the canonical state machine.

## Webhook ingestion

```go
func (h *DelhiveryWebhookHandler) ServeHTTP(w, r) {
    // 1. Verify HMAC (carrier-specific)
    if !verifyHMAC(r, h.secret) {
        http.Error(w, "unauthorized", 401)
        return
    }

    // 2. Parse payload
    payload := parse(r.Body)

    // 3. Insert idempotently
    err := h.db.Exec(`
        INSERT INTO carrier_event (carrier_id, carrier_event_id, payload, received_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (carrier_id, carrier_event_id) DO NOTHING
    `, ...)

    // 4. ACK fast (carrier expects sub-second)
    w.WriteHeader(200)

    // 5. Async: trigger river job to process
    h.river.Insert(ctx, ProcessEventJob{EventID: ...}, river.UniqueArgs)
}
```

The webhook handler does **only** verification + idempotent persist. Processing is async.

## Status normalization

Per-carrier mapping table in adapter Go code:

```go
package delhivery

var statusMap = map[string]CanonicalMapping{
    "UD":  {Canonical: core.CanonicalStatusBooked},
    "PU":  {Canonical: core.CanonicalStatusPickedUp},
    "TR":  {Canonical: core.CanonicalStatusInTransit},
    "OFD": {Canonical: core.CanonicalStatusOutForDelivery},
    "DL":  {Canonical: core.CanonicalStatusDelivered},
    "ND":  {Canonical: core.CanonicalStatusNDR, Reason: core.NDRReasonBuyerUnavailable},
    "CR":  {Canonical: core.CanonicalStatusNDR, Reason: core.NDRReasonRefused},
    // ... more
}

func Normalize(carrierCode string) (CanonicalMapping, bool) {
    m, ok := statusMap[carrierCode]
    return m, ok
}
```

If a code is missing: log + emit `unknown_carrier_code` audit event; keep `tracking_event` row but set `canonical_status='unknown'`. Ops adds the mapping; redeploy.

## State machine enforcement

```go
var allowedTransitions = map[CanonicalStatus][]CanonicalStatus{
    StatusBooked: {StatusPickupPending, StatusCancelled},
    StatusPickupPending: {StatusPickedUp, StatusCancelled},
    StatusPickedUp: {StatusInTransit},
    StatusInTransit: {StatusOutForDelivery, StatusLost, StatusDamaged},
    StatusOutForDelivery: {StatusDelivered, StatusNDR, StatusLost, StatusDamaged},
    StatusNDR: {StatusOutForDelivery, StatusRTOInitiated},  // reattempt or rto
    StatusRTOInitiated: {StatusRTOInTransit},
    StatusRTOInTransit: {StatusRTODelivered},
    // terminal: StatusDelivered, StatusRTODelivered, StatusCancelled, StatusLost, StatusDamaged
}

func canTransition(from, to CanonicalStatus) bool {
    allowed, ok := allowedTransitions[from]
    return ok && contains(allowed, to)
}
```

When ingesting an event:
- If `canTransition(current, normalized.Canonical)`: apply, write history row, emit outbox event.
- Else: persist `tracking_event` for audit, but **don't** transition canonical. Log warning; high-impact regressions (e.g., delivered → in_transit) emit ops alert.

## Polling

For carriers without webhooks, river cron `tracking.poll_carrier`:
```go
func PollCarrierActiveShipments(ctx, carrierID, batch=100) error {
    // Get shipments still in non-terminal state
    shipments := db.Query("SELECT awb FROM shipment WHERE carrier_id=$1 AND status IN ('booked','pickup_pending','picked_up','in_transit','out_for_delivery','ndr') LIMIT $2", carrierID, batch)

    // Call carrier track API
    events := carrier.Adapter.Track(ctx, awbs)

    // Normalize and ingest
    return Ingest(ctx, carrierID, events)
}
```

Cadence per carrier from capability flags (5–15 min).

## Outbox events

On canonical status transition, emit one of:
- `shipment.picked_up`, `shipment.in_transit`, `shipment.out_for_delivery`, `shipment.delivered`
- `shipment.ndr` (with reason)
- `shipment.rto_initiated`, `shipment.rto_delivered`
- `shipment.lost`, `shipment.damaged`

Consumers:
- `notifications` — buyer + seller notifications.
- `ndr` — NDR action engine creates ndr_event.
- `cod` — schedule COD remittance on `shipment.delivered`.
- `wallet` — RTO charge on `shipment.rto_initiated`.
- `recon` — weight reconciliation on `shipment.delivered` (when reweigh data arrives).

## Reconciliation

A river cron every 5 min:
- Find shipments stuck in `pickup_pending` for >24h → log; alert seller.
- Find shipments in `in_transit` with no events for >72h → ops queue ("stuck shipment").
- Find shipments in `pending_carrier` for >5min → either retry carrier book or mark `booking_failed`.

## Raw payload storage

S3 key: `tracking-raw/<carrier_id>/<carrier_event_id>` storing the original JSON/XML. Reference in `tracking_event.raw_payload_ref`. Lifecycle: 90 days hot; deletion thereafter.

Used for: debugging unknown codes; audit; ops investigation.

## Performance

- Webhook handler: P95 < 100ms (verify + idempotent insert).
- Async processing per event: P95 < 200ms.
- Reconciliation cron: completes in <5min.

## Failure modes

| Failure | Behavior |
|---|---|
| HMAC mismatch | 401 to carrier; log; alert if rate exceeds threshold |
| Duplicate `(carrier, event_id)` | Silently dedup |
| Out-of-order events | Persist; don't regress canonical |
| Unknown carrier code | Persist; mark unknown; alert |
| State machine violation (e.g., delivered → in_transit) | Persist event; don't transition; alert if material |
| S3 unreachable for raw payload | Persist event without raw_payload_ref; alert |
| Carrier polling fails | Retry with backoff; alert on sustained failure |

## Test coverage

- **Unit**: status normalization, state machine, idempotency keys.
- **SLT**: end-to-end webhook → tracking_event → status transition → outbox event verified consumed by notification module.
- **Bench**: per-event ingest, batch ingest of 1000 events.

## Observability

- Counter: events by carrier, by canonical status.
- Histogram: webhook handler latency, async processing latency.
- Counter: unknown codes (alert above threshold).
- Counter: state machine violations (alert above threshold).
- Histogram: time-to-status-update from carrier event timestamp to our recorded_at.

## Open questions

- **Q-TR-1.** What if carrier sends correction (delivered → not delivered) outside our state machine? Manual ops process. Not auto-handled.
- **Q-TR-2.** Should buyer-facing tracking page show every micro-event or just the canonical stepper? PRD says stepper + last 5 events.
