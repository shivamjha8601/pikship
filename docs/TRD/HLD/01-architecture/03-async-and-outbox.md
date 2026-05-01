# Async & outbox

> All async work runs in the same binary via `river` (Postgres-backed job queue). Cross-module events use the outbox pattern, atomic with the originating transaction.

## Why outbox + river instead of just river

Domain operations need to atomically (a) commit DB state and (b) emit a downstream event. River by itself doesn't give us atomicity with our domain transaction — a `river.Insert(job)` call would commit on its own connection.

**Outbox decouples**: in the domain transaction, we INSERT into `outbox_event`. A separate forwarder process reads outbox rows and enqueues river jobs. The forwarder is at-least-once with idempotency at the consumer.

```
[Domain tx]                          [Outbox forwarder]              [River]
  BEGIN                                                              
  ...domain mutations...                                              
  INSERT outbox_event ←─── atomic ───→ SELECT FOR UPDATE SKIP LOCKED 
  COMMIT                                                              
                                       INSERT river_job                
                                       UPDATE outbox.enqueued_at         
                                                                      job consumed
                                                                      handler runs
```

## Outbox table

```sql
CREATE TABLE outbox_event (
  id            BIGSERIAL PRIMARY KEY,
  seller_id     UUID,
  kind          TEXT NOT NULL,           -- 'shipment.booked', 'wallet.charged', ...
  payload       JSONB NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  enqueued_at   TIMESTAMPTZ              -- NULL until forwarder enqueues
);

CREATE INDEX outbox_event_pending_idx
  ON outbox_event (created_at)
  WHERE enqueued_at IS NULL;

CREATE INDEX outbox_event_seller_idx
  ON outbox_event (seller_id, created_at);
```

The partial index makes `WHERE enqueued_at IS NULL` cheap regardless of total table size.

## Forwarder (a single river job that runs forever)

```go
func ForwardOutbox(ctx context.Context, db DB, river *river.Client) error {
    for {
        select {
        case <-ctx.Done(): return nil
        default:
        }

        // claim batch
        rows, err := db.QueryContext(ctx, `
            SELECT id, kind, payload FROM outbox_event
            WHERE enqueued_at IS NULL
            ORDER BY id ASC
            LIMIT 100
            FOR UPDATE SKIP LOCKED
        `)
        if err != nil { return err }

        for rows.Next() {
            var id int64; var kind string; var payload []byte
            rows.Scan(&id, &kind, &payload)

            // map kind → river job type
            jobArgs := mapToJob(kind, payload)
            _, err := river.Insert(ctx, jobArgs, &river.InsertOpts{
                UniqueOpts: river.UniqueOpts{ByArgs: true}, // idempotency on outbox_id
            })
            if err != nil {
                // log + continue; we'll retry next loop
                continue
            }

            db.ExecContext(ctx, `UPDATE outbox_event SET enqueued_at=now() WHERE id=$1`, id)
        }

        if !rowsHadAny { time.Sleep(100 * time.Millisecond) }
    }
}
```

Two things to note:
1. `FOR UPDATE SKIP LOCKED` — multiple forwarder workers (multi-instance future) don't fight.
2. River's `UniqueOpts.ByArgs` — even if forwarder crashes after `INSERT river_job` but before `UPDATE outbox`, the next run will re-attempt the insert and river will deduplicate because the outbox_id is in the args.

## Per-seller ordering (when needed)

Most events don't need ordering. A few do:
- Wallet ledger replays — must apply in commit order per seller.
- Audit chain writes — must be linear per seller (already enforced by hash chain).
- Some channel events (e.g., Shopify order updates that supersede earlier events).

For ordered events, river's `unique_args` keys on `seller_id` (per kind) — at most one job for a (kind, seller_id) running at a time. The next event waits.

## River jobs we run

| Job | Kind | Schedule | Notes |
|---|---|---|---|
| `outbox.forward` | continuous | always | the forwarder; one per worker |
| `tracking.poll_carrier` | per-carrier scheduled | per carrier capability (5–15 min) | for non-webhook carriers |
| `channel.poll_orders` | per-(channel, seller) scheduled | per channel capability (5–60 min) | for non-webhook channels |
| `ndr.deadline_sweep` | every 15 min | cron | takes default action when seller doesn't respond |
| `cod.reconcile_remittance` | daily 03:00 IST | cron | matches carrier remittance file |
| `wallet.invariant_check` | daily 02:00 IST | cron | detects ledger drift |
| `audit.verify_chain` | weekly Sun 04:00 IST | cron | recomputes hash chain integrity |
| `shipment.pending_reconcile` | every 5 min | cron | sweeps stuck `pending_carrier` |
| `wallet.expire_holds` | every 1 min | cron | releases holds past TTL |
| `notifications.send_*` | event-driven | from outbox | one job kind per channel × event type |
| `carrier.book` | event-driven | from outbox (bulk only) | for bulk-book; single is sync |
| `weight_recon.process_carrier_file` | event-driven | from outbox | processes ingested reweigh file |

## Job characteristics

- **Idempotent.** Every job handler must tolerate re-execution. Store side-effect markers; check before re-applying.
- **Bounded retries.** Default 5 attempts with exponential backoff (1s, 5s, 30s, 5min, 1h). After max, dead-letter.
- **Dead-letter visibility.** River's failed jobs surface in admin console; ops investigates.
- **Bounded runtime.** Each job has a deadline (default 60s; long jobs override). Beyond that, killed; treated as failure for retry.
- **No long-held resources.** A river worker holds one DB connection while running. Long jobs that would tie up connections are split.

## Failure modes & recovery

### Outbox row written, forwarder never gets to it
Row stays `enqueued_at IS NULL`. Next forwarder run picks it up. Worst-case latency = forwarder loop interval (100ms) + processing time.

### Forwarder enqueued river job but crashed before marking enqueued_at
Next run re-enqueues; river deduplicates via `unique_args` on `outbox_event_id`. No double-handling.

### River job enqueued but worker never runs (process crash)
River persists job state in PG; on worker restart, claims unstarted jobs. No loss.

### River job ran, side effect partially applied, worker crashed
Job's idempotency check on next attempt detects partial application; either completes or rolls back as appropriate.

### Outbox table grows unbounded
Cleanup cron runs daily: `DELETE FROM outbox_event WHERE enqueued_at IS NOT NULL AND created_at < now() - INTERVAL '7 days'`.

### Poison-pill job (always fails)
After max attempts, dead-letter queue. Audit alert. Manual ops intervention.

## What does NOT use the outbox

- Operations within a single domain that don't cross module boundaries (in-memory, in-tx).
- Synchronous external calls (carrier book, payment gateway recharge initiate) — those are inline in the request handler.
- Pure queries.

The outbox is for **cross-module event distribution**, not a generic queue.

## Multi-instance considerations

At v1 with worker instances split from API:
- Each worker instance runs the forwarder loop. `FOR UPDATE SKIP LOCKED` ensures no double-claim.
- River's coordination is built-in.
- Outbox cleanup runs as a cron; only one instance per cron firing (river handles this).

## Observability

- Outbox depth: `SELECT count(*) FROM outbox_event WHERE enqueued_at IS NULL` — reported every 60s; alert if >1000.
- River queue depth: river's built-in metrics; alert if total pending >5000.
- Per-job failure rate: log every failure; alert on >5% failure rate over 5min for any kind.
- Per-job P95 latency: tracked.
