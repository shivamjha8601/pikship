# Cross-cutting: Capacity planning

> Concrete numbers — not handwaving. Updated per release as we observe real load.

## Reference loads

| Phase | Sellers | Shipments/month | Peak (festive) | Concurrent users |
|---|---|---|---|---|
| v0 alpha | 5–10 | < 1k | < 2k | < 20 |
| v1 launch (month 3) | 500 | 10k | 20k | 100 |
| v1 month 6 | 2,000 | 50k | 150k | 400 |
| v2 year 1 | 10,000 | 250k | 700k | 2,000 |

Festive multiplier is ~3–5×; we size for festive peak with 30% headroom.

## Per-shipment cost (computational)

A single booking does roughly:
- 1 HTTP request in
- ~5 DB reads (order, policy, allocation, wallet, idempotency)
- ~5 DB writes (allocation_decision, hold, shipment, outbox, idempotency_key)
- 1 carrier API call
- ~3 DB writes (Tx2: AWB update, ledger, outbox)
- ~3 outbox events emitted

Total: ~13 DB ops + 1 external call. Bounded.

A tracking webhook does:
- HMAC verify
- 1 idempotent insert
- async: 1 read, 1–2 writes, 1 outbox event

A wallet recharge does:
- PG webhook in
- 1 idempotent insert (recharge_event)
- 1 ledger insert + 1 wallet_account update
- 1 outbox

## DB sizing

### Connections

```
v0   (1 instance × pool 50)               = 50 connections to RDS
v1   (3 instances × pool 50 + worker 30)  = 180
v2   (5 instances × pool 50 + worker 50)  = 300
```

RDS connection limits by instance class (PG default `max_connections`):
- `db.t4g.medium` (4GB): ~225
- `db.m6g.large` (8GB): ~450
- `db.m6g.xlarge` (16GB): ~900

**v0**: `db.t4g.medium` is fine.
**v1**: still fits `db.t4g.medium`; if we hit limits, upgrade to `m6g.large`.
**v2**: `m6g.xlarge` likely.

### Storage

Estimated row growth at v1:
- `tracking_event`: ~10 events per shipment × 50k shipments/month = 500k rows/month, ~150MB/month JSON
- `wallet_ledger_entry`: ~5 per shipment × 50k = 250k rows/month, ~50MB/month
- `outbox_event`: ~10 per shipment × 50k = 500k rows/month (cleaned up at 7d → ~115k retained), ~30MB/month effective
- `audit_event`: ~15 per shipment × 50k = 750k rows/month, ~250MB/month
- `allocation_decision`: ~50k rows/month, ~100MB
- All other tables combined: ~100MB/month

**Total**: ~700MB/month at v1 mid-cycle. RDS storage starts at 100GB gp3, auto-scales. Good for 10+ years at v1 rates.

At v2 rates (5× v1): ~3.5GB/month. Still trivial.

### Read/write IOPS

Conservatively at v1:
- Read IOPS: ~500 average, ~2,500 peak (festive booking burst).
- Write IOPS: ~200 average, ~1,000 peak.
- gp3 baseline 3,000 IOPS is sufficient.

### CPU / RAM

`db.t4g.medium` is 2 vCPU / 4GB. Postgres in this class can handle our v0/v1 mixed workload comfortably.

`buffer_pool` should be ~70% of available RAM = ~2.8GB.

Upgrade trigger: sustained CPU > 70% for >1 hour during normal hours.

## Application sizing

### EC2 instance

`t4g.medium` (4GB / 2 vCPU) at v0 / v1 month 1.

Resource consumption per instance under v1 month-3 load (estimated):
- CPU: 30% average
- RAM: ~1.5GB (Go runtime + connection pools + working set)
- Network: ~10 Mbps in/out

Upgrade trigger: CPU > 60% sustained.

### Goroutine count

- ~1 goroutine per active HTTP request.
- ~1 goroutine per active river job.
- ~10 goroutines for system tasks (Vector pump, listeners, etc.).

Estimated peak: ~500 concurrent goroutines at v1 festive. Go handles this without breaking a sweat (Go's scheduler scales to 100k+ goroutines).

### HTTP server

Default `net/http` server. No special tuning at v0:
- `ReadTimeout`: 10s
- `WriteTimeout`: 30s (long for booking)
- `IdleTimeout`: 120s
- `MaxHeaderBytes`: 1MB

### Connection pool tuning

`pgxpool.Config`:
- `MaxConns`: 50 (per instance)
- `MinConns`: 5
- `MaxConnLifetime`: 60min
- `MaxConnIdleTime`: 30min
- `HealthCheckPeriod`: 1min

## Outbox & queue depth budgets

### Outbox

Steady state: outbox forwarder processes events at ~100/sec. Max acceptable backlog: 1000 rows (≈10 seconds of processing). Alert above.

Cleanup cron deletes `enqueued_at < now() - 7 days`. Retained outbox rows stay <1MB.

### River queue

Steady state: ~100 jobs/sec at v1 month 6. Max acceptable backlog: 5000 jobs (~50 seconds of processing). Alert above.

Dead-letter inspection daily.

## S3 sizing

### Per-shipment storage
- Raw tracking payload: ~5KB × ~10 events = 50KB per shipment (90-day retention)
- Label PDF: ~50KB (90-day post-terminal)
- Photo evidence (when seller uploads): ~500KB (per dispute)

### v1 monthly:
50k shipments × 100KB ≈ 5GB/month, of which ~1.5GB long-term retained.
Cost: ~$0.50/month on standard S3.

## Capacity planning gates per release

At each version:
1. Run `pgbench` against staging DB at projected load; verify P95 latency.
2. Run microbenchmarks; compare to previous version.
3. Run multi-instance simulation at projected concurrency.
4. Estimate DB size at projected month-12 load; verify storage scaling.
5. Estimate connection pool needs at projected instance count.

If any number breaches budget: upgrade infra **before** the release, not after.

## What we're NOT planning for

- 10× growth shocks. We monitor; if they happen, we add an instance and an RDS upgrade. Not pre-provisioned.
- Multi-region. Mumbai only.
- Cross-region DR. Backup-restore + manual failover only at v0/v1.
- DDoS-scale traffic. ALB + CloudFront handle modest waves; major DDoS needs dedicated mitigation (deferred).
