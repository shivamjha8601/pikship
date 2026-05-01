# Cross-cutting: Resilience

> Patterns we apply uniformly: idempotency, circuit breakers, retries, transactions, graceful degradation. Authored to be a single reference for engineers; implementations live in their domain modules.

## Idempotency

### At every external boundary

| Boundary | Idempotency mechanism |
|---|---|
| Inbound HTTP from clients | `Idempotency-Key` header → `(seller_id, key)` → cached response 24h |
| Inbound webhooks (carriers) | `(carrier_id, carrier_event_id)` UNIQUE → ON CONFLICT DO NOTHING |
| Inbound webhooks (channels) | `(channel_id, channel_event_id)` UNIQUE |
| Inbound webhooks (Razorpay) | `pg_event_id` UNIQUE |
| Outbound carrier API calls (book) | Carrier-supplied idempotency key (use carriers' native; some accept) |
| Outbound payment refunds | Razorpay's idempotency key |
| Outbox event consumption | `unique_args = outbox_event_id` on river job |

### Wallet idempotency

Built into the schema:
```sql
UNIQUE (ref_type, ref_id, direction) on wallet_ledger_entry
```

Posting the same `(shipment_charge, shipment_id, debit)` twice → second is a no-op (no UNIQUE violation panic; sqlc handles via ON CONFLICT).

### Booking idempotency

- Caller supplies `Idempotency-Key` header.
- Server stores `(seller_id, key) → response` for 24h.
- Replay returns cached response with `Idempotent-Replayed: true` header.

### Idempotency key cache

```sql
CREATE TABLE idempotency_key (
  seller_id   UUID NOT NULL,
  key         TEXT NOT NULL,
  response    JSONB NOT NULL,
  status_code INT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (seller_id, key)
);

CREATE INDEX idempotency_key_cleanup_idx ON idempotency_key (created_at);
```

Cleanup cron: delete rows where `created_at < now() - 24h`.

## Circuit breakers

### Carriers

Per-carrier breaker with state in `carrier_health_state` table. State machine:

```
Closed → (failure rate > X% in window) → Open
Open → (after cooldown) → Half-Open
Half-Open → (test request succeeds) → Closed
Half-Open → (test request fails) → Open
```

Configuration per carrier:
- `failure_threshold_pct`: e.g., 30%.
- `failure_window_sec`: e.g., 60.
- `cooldown_sec`: e.g., 60.
- `half_open_test_count`: e.g., 5.

```sql
CREATE TABLE carrier_health_state (
  carrier_id        UUID PRIMARY KEY,
  status            TEXT NOT NULL,        -- 'closed' | 'open' | 'half_open'
  failures_in_window INT NOT NULL DEFAULT 0,
  successes_in_window INT NOT NULL DEFAULT 0,
  window_started_at TIMESTAMPTZ NOT NULL,
  opened_at         TIMESTAMPTZ,
  half_opened_at    TIMESTAMPTZ,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### Carrier integration

Allocation engine reads breaker state via in-process cache (5s TTL + LISTEN/NOTIFY). If `open`: filter the carrier out with reason `circuit_breaker_open`.

### Other circuit breakers

- KYC vendor (Karza): same pattern.
- Payment gateway (Razorpay): same pattern.
- SMS vendor (MSG91): same pattern, with secondary vendor as fallback.
- Email (SES): same pattern.

## Retries

### When to retry

| Error class | Retry? | Strategy |
|---|---|---|
| Network timeout | Yes | Exponential: 1s, 5s, 30s, 5min, 1h |
| 5xx from external | Yes | Same |
| 4xx from external (validation, auth) | No | Fatal; fail-fast |
| `context.DeadlineExceeded` from caller | No | The caller gave up |
| Postgres `serialization_failure` | Yes | Tx retry, max 3 attempts |
| Idempotent op already done (UNIQUE violation) | No | Treat as success |

### Where retries live

- Synchronous request retries: NOT in the request handler (would extend latency); in the caller's error handling for genuinely transient failures.
- Async retries: river handles via job retry policy.
- Carrier API retries: in the carrier adapter, bounded.

### Bounded retries

Every retry loop has a maximum attempt count (default 5) and a maximum wall-clock budget (default 1h). Beyond that → dead-letter or error.

## Transactions

### Patterns documented in [`02-data-and-transactions.md`](../01-architecture/02-data-and-transactions.md)

- One DB transaction per mutating request.
- External calls **outside** transactions (no carrier API call inside a tx).
- Two-phase commits for booking.
- `FOR UPDATE` for wallet ops.
- `SET LOCAL app.seller_id` per transaction for RLS.

### What never happens in a transaction

- HTTP calls to external services.
- File uploads to S3.
- Sleeps.
- Anything taking > 100ms.

## Graceful degradation

### When carrier API is down

- Allocation engine biases away (circuit breaker).
- Sellers see "carrier degraded" badge in dashboard.
- Auto-fallback (if seller opted in) tries next carrier.
- Booking that explicitly chose the down carrier returns error with alternative suggestions.

### When KYC vendor is down

- Onboarding falls back to manual review queue.
- Sellers see "KYC review delayed" message.
- Ops sees backlog growing (audit alert).

### When payment gateway is down

- Recharge UI shows error; suggests alternative methods (NEFT).
- No new wallet credits via PG; existing credits unaffected.

### When SMS vendor is down

- Multi-vendor failover (MSG91 → Twilio fallback).
- If both fail: log; OTP retried; buyer outreach falls back to email.
- Critical operations (login OTP) might require manual intervention.

### When DB is down

- Cannot do almost anything.
- Health endpoint returns 503.
- ALB routes traffic away from this instance (multi-instance only).
- v0 single instance: full outage. RPO 5min, RTO 30min target via RDS multi-AZ failover.

## Graceful shutdown

```
SIGTERM received
  ├─► Stop accepting new HTTP connections (close listener)
  ├─► Wait for in-flight HTTP to complete (timeout 25s)
  ├─► Cancel context for river runner
  ├─► Wait for river jobs to drain (timeout 30s)
  ├─► Close DB pool
  ├─► Flush logs (Vector buffers)
  └─► Exit 0
```

Total drain budget: 30s. After timeout, force-exit (SIGKILL).

## Health checks (recap)

- `/healthz` — process responsive; no external dependency check.
- `/readyz` — DB up + migration version OK + S3 reachable.

ALB uses `/readyz` for upstream health.

## Rate limiting

### Per-seller rate limiter

In-process token bucket per `(seller_id, endpoint_class)`:
- `endpoint_class`: `read` (high limit), `write` (medium), `bulk` (low), `webhook` (very high).
- Limits configurable via policy engine.

### Per-IP rate limiter

For unauth endpoints (buyer tracking, OAuth start) and webhooks. In-process.

### Multi-instance considerations

At N>1 instances, total rate = N × per-instance limit. We re-tune limits when scaling out.

### Distributed rate limit (deferred)

If per-IP rate limiting becomes critical to enforce globally (e.g., to prevent abuse), we'd add a Postgres-backed counter. Not needed at v0/v1.

## Concurrency primitives

### Goroutine pools for bulk ops

`internal/observability/workerpool` provides a bounded pool. Used by:
- Bulk allocation.
- Bulk booking.
- Bulk label generation.

Pool size: `min(NumCPU, 16)` by default. Configurable per call site.

### Context cancellation

Every domain method takes `context.Context`. Honors cancellation. On HTTP handler context done → cancel propagates to all downstream calls (DB queries, external HTTP).

### Avoiding deadlocks

- Always acquire locks in a consistent order: `wallet_account` → `wallet_hold` (never the reverse).
- Don't hold a tx while making external calls.
- `FOR UPDATE` queries are explicit and named.

## Failure cascades — what we prevent

| Cascade | Prevention |
|---|---|
| Carrier API slow → tx held → DB connections exhausted | External calls outside tx |
| One slow seller → all sellers slow | Per-seller wallet locks (FOR UPDATE per seller_id) |
| One bad webhook → backlog → tracking-event ingest stalls | Webhook handler is fast (verify + persist); processing is async |
| Job poison-pill → river stuck | Bounded retries → dead-letter |
| Unbounded outbox growth → DB bloat | Cleanup cron + alerts |
| Cache miss storm → DB overload | TTL + jittered request coalescing |

## Test coverage

- Unit: idempotency key, circuit breaker state machine, retry policies.
- SLT: simulated carrier outage; verify allocation routes around; verify circuit-breaker recovery.
- Multi-instance sim: circuit breaker state visible across instances.

## Observability

- Counter: idempotency replays.
- Counter: retries by source.
- Gauge: open circuit count.
- Counter: dead-letter queue depth (alert).
- Histogram: retry-loop wall-clock time.
