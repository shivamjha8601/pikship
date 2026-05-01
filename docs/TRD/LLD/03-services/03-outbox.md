# Service: Outbox (`internal/outbox`)

> Atomic-with-tx event emission, forwarder loop into river, idempotency-key dedup, cleanup cron.

## Purpose

- `Emit(ctx, tx, event)` — write an event row in the same DB transaction as domain state.
- Forwarder loop reads outbox rows, enqueues river jobs, marks `enqueued_at`.
- Cleanup cron deletes processed rows after 7 days.

## Dependencies

- `internal/core`
- `internal/observability/dbtx`
- `internal/observability` (logger, SafeGo)
- `github.com/jackc/pgx/v5/pgxpool`
- `github.com/riverqueue/river`

## Package layout

```
internal/outbox/
├── doc.go
├── service.go         ← Emitter interface
├── service_impl.go
├── repo.go
├── types.go           ← Event
├── forwarder.go       ← background loop
├── jobs.go            ← cleanup cron
├── consumer_router.go ← maps event kind → river job arg type
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package outbox provides atomic event emission alongside DB writes.
//
// The pattern: domain operations write to outbox_event in the SAME transaction
// as their state change. A forwarder loop reads outbox rows with FOR UPDATE
// SKIP LOCKED, enqueues river jobs, marks enqueued_at. River guarantees
// at-least-once execution; consumer dedup makes the system effectively
// exactly-once at the application layer.
package outbox

import (
    "context"
    "encoding/json"
    "errors"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Emitter is the public API.
type Emitter interface {
    // Emit writes the event in the provided transaction.
    //
    // The transaction must be the SAME one writing the domain state — that's
    // the whole point. Caller is responsible for commit/rollback.
    Emit(ctx context.Context, tx pgx.Tx, event Event) error
}

// Event is an outbox row.
type Event struct {
    ID         uuid.UUID         // generated if zero
    SellerID   *core.SellerID    // nil for platform events
    Kind       string            // 'shipment.booked', 'wallet.charged', etc.
    Version    int               // schema version; default 1
    Payload    json.RawMessage   // event-specific payload
    OccurredAt time.Time         // defaults to now
}

// Sentinel errors.
var (
    ErrInvalidEvent = errors.New("outbox: invalid event")
)
```

## Implementation

```go
// internal/outbox/service_impl.go
package outbox

import (
    "context"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/db"
)

type emitterImpl struct {
    queries *db.Queries
    clock   core.Clock
}

// New constructs an outbox.Emitter.
//
// The pool is unused at emit time (we use the caller's tx); kept for tests
// and future operations.
func New(clock core.Clock) Emitter {
    return &emitterImpl{
        queries: db.New(nil),  // will be re-bound per call via WithTx
        clock:   clock,
    }
}

func (e *emitterImpl) Emit(ctx context.Context, tx pgx.Tx, event Event) error {
    if event.Kind == "" {
        return fmt.Errorf("outbox.Emit: empty kind: %w", ErrInvalidEvent)
    }
    if len(event.Payload) == 0 {
        event.Payload = json.RawMessage("{}")
    }
    if event.ID == uuid.Nil {
        event.ID = uuid.New()
    }
    if event.Version == 0 {
        event.Version = 1
    }
    if event.OccurredAt.IsZero() {
        event.OccurredAt = e.clock.Now()
    }

    var sellerIDArg *uuid.UUID
    if event.SellerID != nil && !event.SellerID.IsZero() {
        u := uuid.UUID(*event.SellerID)
        sellerIDArg = &u
    }

    q := e.queries.WithTx(tx)
    return q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
        ID:         event.ID,
        SellerID:   sellerIDArg,
        Kind:       event.Kind,
        Version:    int32(event.Version),
        Payload:    event.Payload,
        OccurredAt: event.OccurredAt,
    })
}
```

## DB schema

```sql
-- migrations/00NN_create_outbox.up.sql

CREATE TABLE outbox_event (
    id            UUID PRIMARY KEY,
    seller_id     UUID,                              -- NULL for platform
    kind          TEXT NOT NULL,
    version       INT NOT NULL DEFAULT 1,
    payload       JSONB NOT NULL DEFAULT '{}',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    enqueued_at   TIMESTAMPTZ
);

-- Hot lookup: pending rows
CREATE INDEX outbox_event_pending_idx ON outbox_event (created_at)
    WHERE enqueued_at IS NULL;

-- Per-seller listing
CREATE INDEX outbox_event_seller_idx ON outbox_event (seller_id, created_at)
    WHERE enqueued_at IS NOT NULL;

-- Outbox does NOT use RLS — it's a platform concern, written by domain
-- code using the same tx, so seller scoping is enforced upstream.
GRANT SELECT, INSERT, UPDATE, DELETE ON outbox_event TO pikshipp_app;
GRANT SELECT ON outbox_event TO pikshipp_admin;
```

## SQL queries

```sql
-- query/outbox.sql

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_event (id, seller_id, kind, version, payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ClaimPendingOutboxEvents :many
-- Used by the forwarder. SKIP LOCKED prevents multiple workers from claiming the same row.
SELECT id, seller_id, kind, version, payload, occurred_at
FROM outbox_event
WHERE enqueued_at IS NULL
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEnqueued :exec
UPDATE outbox_event
SET enqueued_at = now()
WHERE id = $1;

-- name: CleanupOldOutboxEvents :execrows
DELETE FROM outbox_event
WHERE enqueued_at IS NOT NULL
  AND created_at < now() - INTERVAL '7 days';

-- name: PendingOutboxDepth :one
SELECT count(*) AS depth FROM outbox_event WHERE enqueued_at IS NULL;
```

## Forwarder

```go
// internal/outbox/forwarder.go
package outbox

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/riverqueue/river"

    "github.com/pikshipp/pikshipp/internal/db"
)

// Forwarder reads outbox rows and enqueues river jobs.
//
// One Forwarder per worker process is enough; SKIP LOCKED makes multi-instance
// safe. River's UniqueOpts.ByArgs prevents double-enqueue on rare races.
type Forwarder struct {
    pool          *pgxpool.Pool
    queries       *db.Queries
    river         *river.Client[pgx.Tx]
    router        ConsumerRouter
    batchSize     int
    pollInterval  time.Duration
    log           *slog.Logger
}

// ConsumerRouter maps an outbox event Kind to a river job-args type.
//
// One row per event kind. Looked up by kind on each forward.
type ConsumerRouter interface {
    Route(kind string) (river.JobArgs, error)
}

func NewForwarder(pool *pgxpool.Pool, riverClient *river.Client[pgx.Tx], router ConsumerRouter, log *slog.Logger) *Forwarder {
    return &Forwarder{
        pool:         pool,
        queries:      db.New(pool),
        river:        riverClient,
        router:       router,
        batchSize:    100,
        pollInterval: 100 * time.Millisecond,
        log:          log,
    }
}

// Run loops until ctx is cancelled. Each iteration:
//   1. Claim up to batchSize pending events.
//   2. For each: route → river.Insert with unique args by outbox ID.
//   3. Mark enqueued_at.
//   4. Sleep pollInterval if no work.
//
// Errors during processing of one row are logged and the row is NOT marked;
// it'll be retried next iteration. Persistent failures eventually mean the
// pending depth alert fires.
func (f *Forwarder) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return nil
        default:
        }

        n, err := f.processBatch(ctx)
        if err != nil {
            f.log.ErrorContext(ctx, "outbox forwarder error", slog.Any("error", err))
        }

        if n == 0 {
            time.Sleep(f.pollInterval)
        }
    }
}

func (f *Forwarder) processBatch(ctx context.Context) (int, error) {
    tx, err := f.pool.Begin(ctx)
    if err != nil { return 0, fmt.Errorf("begin: %w", err) }
    defer tx.Rollback(ctx)

    q := f.queries.WithTx(tx)
    rows, err := q.ClaimPendingOutboxEvents(ctx, int32(f.batchSize))
    if err != nil { return 0, fmt.Errorf("claim: %w", err) }

    if len(rows) == 0 {
        return 0, tx.Commit(ctx)
    }

    for _, row := range rows {
        if err := f.forwardOne(ctx, tx, row); err != nil {
            f.log.WarnContext(ctx, "outbox forward failed",
                slog.String("event_id", row.ID.String()),
                slog.String("kind", row.Kind),
                slog.Any("error", err))
            // Do not mark enqueued; row stays pending; retry next batch.
            continue
        }
        if err := q.MarkOutboxEnqueued(ctx, row.ID); err != nil {
            return 0, fmt.Errorf("mark enqueued %s: %w", row.ID, err)
        }
    }

    return len(rows), tx.Commit(ctx)
}

func (f *Forwarder) forwardOne(ctx context.Context, tx pgx.Tx, row db.ClaimPendingOutboxEventsRow) error {
    args, err := f.router.Route(row.Kind)
    if err != nil { return fmt.Errorf("route %s: %w", row.Kind, err) }

    // Inflate the args from payload via JSON unmarshal.
    if u, ok := args.(payloadUnmarshaler); ok {
        if err := u.UnmarshalPayload(row.Payload); err != nil {
            return fmt.Errorf("unmarshal payload: %w", err)
        }
    }

    // Insert into river WITH the outbox event_id as unique key.
    _, err = f.river.InsertTx(ctx, tx, args, &river.InsertOpts{
        UniqueOpts: river.UniqueOpts{
            ByArgs: true,
        },
        Tags: []string{"outbox", "event_id:" + row.ID.String()},
    })
    return err
}

// payloadUnmarshaler is implemented by river job args that need JSON inflation.
type payloadUnmarshaler interface {
    UnmarshalPayload(json.RawMessage) error
}
```

## Consumer router

```go
// internal/outbox/consumer_router.go
package outbox

import (
    "errors"
    "fmt"

    "github.com/riverqueue/river"
)

// staticRouter dispatches by kind via a registered table.
type staticRouter struct {
    factories map[string]func() river.JobArgs
}

func NewStaticRouter() *staticRouter {
    return &staticRouter{factories: make(map[string]func() river.JobArgs)}
}

// Register adds a factory for the given event kind.
//
// Each call site (in the consuming module's wiring code) registers its own.
// Example:
//   router.Register("shipment.booked", func() river.JobArgs { return &notifications.NotifyBuyerArgs{} })
func (r *staticRouter) Register(kind string, factory func() river.JobArgs) {
    if _, exists := r.factories[kind]; exists {
        panic(fmt.Sprintf("outbox.Router: kind %q registered twice", kind))
    }
    r.factories[kind] = factory
}

func (r *staticRouter) Route(kind string) (river.JobArgs, error) {
    f, ok := r.factories[kind]
    if !ok {
        return nil, fmt.Errorf("no consumer for kind %q", kind)
    }
    return f(), nil
}
```

Wiring (in `cmd/pikshipp/main.go`):
```go
router := outbox.NewStaticRouter()
router.Register("shipment.booked",  func() river.JobArgs { return &notifications.BuyerTrackingLinkArgs{} })
router.Register("shipment.delivered", func() river.JobArgs { return &cod.ScheduleRemittanceArgs{} })
router.Register("audit.write", func() river.JobArgs { return &audit.AuditWriteArgs{} })
// ... etc
```

## Cleanup cron

```go
// internal/outbox/jobs.go
package outbox

import (
    "context"

    "github.com/riverqueue/river"
)

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "outbox.cleanup" }

type CleanupWorker struct {
    river.WorkerDefaults[CleanupArgs]
    queries *db.Queries
    log     *slog.Logger
}

func (w *CleanupWorker) Work(ctx context.Context, j *river.Job[CleanupArgs]) error {
    n, err := w.queries.CleanupOldOutboxEvents(ctx)
    if err != nil { return err }
    w.log.InfoContext(ctx, "outbox cleanup", slog.Int64("deleted", n))
    return nil
}

// Register: scheduled hourly via river PeriodicJobs.
```

## Depth monitoring

```go
type DepthAlertArgs struct{}

func (DepthAlertArgs) Kind() string { return "outbox.depth_alert" }

type DepthAlertWorker struct {
    river.WorkerDefaults[DepthAlertArgs]
    queries *db.Queries
    audit   audit.Emitter
    log     *slog.Logger
}

func (w *DepthAlertWorker) Work(ctx context.Context, j *river.Job[DepthAlertArgs]) error {
    depth, err := w.queries.PendingOutboxDepth(ctx)
    if err != nil { return err }

    const threshold = 1000
    if depth > threshold {
        w.audit.EmitAsync(ctx, audit.Event{
            Action: "ops.queue_alert.outbox",
            Actor:  audit.Actor{Kind: audit.ActorScheduledJob, Ref: "outbox.depth_alert"},
            Target: audit.Target{Kind: "outbox", Ref: "global"},
            Payload: map[string]any{
                "depth":     depth,
                "threshold": threshold,
            },
        })
        w.log.ErrorContext(ctx, "outbox depth above threshold",
            slog.Int64("depth", depth),
            slog.Int("threshold", threshold))
    }
    return nil
}

// Schedule: every 5 min via river PeriodicJobs.
```

## Testing

```go
func TestEmit_Atomic_SLT(t *testing.T) {
    p := testdb.New(t)
    e := outbox.New(core.NewFakeClock(time.Now()))

    sid := core.NewSellerID()

    // Successful tx: outbox row + domain row both committed
    err := dbtx.WithSellerTx(context.Background(), p.App, sid, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx, "INSERT INTO foo (seller_id, val) VALUES ($1, 'bar')", sid.String())
        if err != nil { return err }
        return e.Emit(ctx, tx, outbox.Event{
            SellerID: &sid,
            Kind:     "test.event",
            Payload:  json.RawMessage(`{"x":1}`),
        })
    })
    require.NoError(t, err)
    require.Equal(t, 1, countOutboxEvents(t, p.App))

    // Failed tx: neither row exists
    err = dbtx.WithSellerTx(context.Background(), p.App, sid, func(ctx context.Context, tx pgx.Tx) error {
        e.Emit(ctx, tx, outbox.Event{SellerID: &sid, Kind: "test.event"})
        return errors.New("simulated failure")
    })
    require.Error(t, err)
    require.Equal(t, 1, countOutboxEvents(t, p.App))  // still just the first one
}

func TestForwarder_AdvancesEnqueuedAt_SLT(t *testing.T) {
    // Insert outbox events
    // Run forwarder.processBatch
    // Verify all enqueued_at populated
    // Verify river jobs created
}

func TestForwarder_SkipsLocked_SLT(t *testing.T) {
    // Two forwarder instances against same DB
    // Insert 100 events
    // Each runs processBatch concurrently
    // Verify no event processed twice (count of unique river job inserts == 100)
}

func TestForwarder_RecoverFromPartialCrash_SLT(t *testing.T) {
    // Simulate: forwarder claims rows but crashes before MarkOutboxEnqueued.
    // After restart: same rows are re-claimed, re-enqueued.
    // River dedup (UniqueOpts.ByArgs on outbox event ID) prevents double-handling at the consumer.
}

func TestCleanup_DeletesOnlyOldEnqueued_SLT(t *testing.T) {
    // Insert: 1 old enqueued, 1 new enqueued, 1 old pending, 1 new pending
    // Run cleanup
    // Expect: only the old enqueued is gone
}

func BenchmarkEmit(b *testing.B) {
    // ... setup ...
    for i := 0; i < b.N; i++ {
        _ = dbtx.WithSellerTx(ctx, pool, sid, func(ctx context.Context, tx pgx.Tx) error {
            return e.Emit(ctx, tx, sampleEvent)
        })
    }
}

func BenchmarkForwarderProcessBatch(b *testing.B) {
    // Pre-fill outbox with 100 pending events
    // Measure forwarder.processBatch
}
```

## Performance

- `Emit`: 1 INSERT inside the caller's tx; ~1ms.
- `processBatch(100)`: 1 SELECT FOR UPDATE SKIP LOCKED + 100 river inserts + 100 UPDATEs; ~50ms.
- Forwarder loop steady-state: ~2000 events/sec one instance.
- Cleanup: hourly; ~1s for 100k rows.

## Failure modes

| Failure | Behavior |
|---|---|
| Domain tx commits, outbox tx commits, forwarder never runs (process down) | Next forwarder run picks up the row |
| Forwarder claims row, river insert fails, mark fails | Row stays pending; retry next iteration |
| Forwarder claims row, river insert succeeds, mark fails | Row remains pending; next run re-inserts to river; river dedup via UniqueOpts.ByArgs |
| Outbox table grows unbounded | Cleanup cron + depth alert |
| Two forwarders on same DB | SKIP LOCKED partitions cleanly |

## Open questions

- **Out-of-order delivery**: river jobs may run in any order. For events that need ordering per seller (e.g., wallet ledger replays), consumer sets river `unique_args` keyed on (seller_id, kind) to serialize. Document per-consumer.
- **Outbox compaction**: do we ever rewrite (e.g., for GDPR PII removal)? Currently no — outbox is append-only. PII removal happens at consumer side.
- **DLQ visibility**: river handles dead-letter; we don't need a separate outbox DLQ.

## References

- HLD `01-architecture/03-async-and-outbox.md`.
- HLD `01-architecture/05-domain-event-catalog.md`.
- ADR 0004 (river).
