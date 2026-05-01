# Cross-cutting: Observability

> What we instrument, where it goes, what we alert on. Adopted from Architect C's review: log shipping from day 0; structured everything; queue depth alerting.

## v0 baseline

- **Structured JSON logs** to stdout via `log/slog`.
- **Vector** agent on the EC2 host ships logs to **CloudWatch Logs**.
- **No Sentry, no Datadog at v0** — errors are logged with stack traces; reviewed via CloudWatch Logs Insights.
- **No distributed tracing at v0** — single binary makes it low-value. OpenTelemetry added in v1.
- **No metrics infrastructure at v0** — counters logged as structured events; queried via CloudWatch Logs Metric Filters.

This is enough for the first 5–10 sellers. v1 adds Sentry + (probably) CloudWatch Metrics or Grafana Cloud.

## Logging conventions

### Per-request fields

Every log line in a request context carries:

| Field | Always | Usage |
|---|---|---|
| `request_id` | yes | UUID generated at edge; correlates all logs for one request |
| `seller_id` | when applicable | UUID; lets us slice by tenant |
| `user_id` | when applicable | UUID |
| `actor_kind` | when applicable | `'seller_user' | 'pikshipp_admin' | 'system' | 'webhook'` |
| `service` | yes | `'pikshipp'` (single binary) |
| `version` | yes | Build version (git SHA) |
| `timestamp` | yes | RFC3339 |
| `level` | yes | `'debug' | 'info' | 'warn' | 'error'` |
| `msg` | yes | Human-readable message |

Module-specific fields appended; e.g., `wallet.amount_minor`, `shipment.awb`, `carrier.id`.

### Levels

- **debug**: development; off in prod.
- **info**: significant business events (booking, recharge, NDR action).
- **warn**: recoverable anomalies (carrier API timeout but retry succeeded).
- **error**: errors returned to user OR background failures.

### slog setup

```go
package observability

func InitLogger(version string) *slog.Logger {
    h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
        AddSource: true,
    })
    return slog.New(h).With(
        slog.String("service", "pikshipp"),
        slog.String("version", version),
    )
}
```

Logger flowed via context. Domain code calls `slog.InfoContext(ctx, "booked shipment", "awb", awb, "amount_minor", price)`.

## Log shipping

Vector agent on the EC2 host:
- Reads from journald.
- Tags with hostname, instance role.
- Ships to CloudWatch Logs with log group `/pikshipp/<env>`.
- Rate-limit: 10MB/min default.

Vector config in repo (`deploy/vector.toml`).

## CloudWatch Logs Insights queries

Standard queries documented in `runbooks/`:
- "errors in last 1h": `fields @timestamp, level, msg, request_id | filter level = "error" | sort @timestamp desc | limit 100`
- "slow requests": `fields @timestamp, request_id, latency_ms | filter latency_ms > 1000`
- "all logs for a request": `fields @timestamp, msg | filter request_id = "..." | sort @timestamp asc`
- "wallet operations for a seller": `fields @timestamp, msg, ref_type, amount_minor | filter seller_id = "..." and msg like "wallet"`

## Health endpoints

### `GET /healthz`
- Returns 200 if process is responsive.
- No external dependencies checked.
- Should never return non-200 unless the binary is broken.

### `GET /readyz`
- Returns 200 only if:
  - DB is reachable (ping with 1s timeout).
  - Migration version >= expected (read once at startup).
  - S3 reachable (HEAD on a known object; cached for 60s).
- Returns 503 otherwise.
- ALB / load balancer uses this as upstream health check.

Carrier API health is **not** in `/readyz` — they're domain dependencies, not platform.

## Queue depth monitoring

A river cron at every-5-minutes runs:

```sql
SELECT count(*) FROM river_job WHERE state = 'available';
SELECT count(*) FROM outbox_event WHERE enqueued_at IS NULL;
```

If either exceeds threshold:
- river queue depth > 5000 → audit event `ops.queue_alert.river`.
- outbox depth > 1000 → audit event `ops.queue_alert.outbox`.

At v0 these surface in CloudWatch Logs (filter on the audit event kind). At v1, page on-call.

Per-job-type metrics:
- Jobs run / sec.
- Jobs failed / sec.
- Job latency P95 by kind.

Logged; alertable.

## Metrics (without a metrics service at v0)

We log counters as structured events:
```go
slog.InfoContext(ctx, "metric.counter.shipment_booked", "carrier_id", carrierID, "value", 1)
```

CloudWatch Logs Metric Filters extract these into CloudWatch Metrics:
- `pikshipp.shipments.booked` by `carrier_id`.
- `pikshipp.wallet.recharges` count and total amount.
- `pikshipp.ndr.events` count.
- `pikshipp.tracking.webhook_received` count.
- ... etc.

This is **lo-fi** but works. v1 moves to native metrics with Prometheus.

## Errors

At v0:
- Errors logged at `error` level with stack trace (`AddSource: true` in slog).
- CloudWatch Logs Insights query for grep-style search.
- Manual review.

At v1:
- Add Sentry. SDK call on every `error`-level log automatically.

## Tracing

Skipped at v0. At v1 with multi-instance, OpenTelemetry SDK instruments HTTP server, DB queries, river jobs, external HTTP calls. Backend: AWS X-Ray or self-hosted Jaeger.

## Audit emission

Every privileged action emits an `audit_event` in addition to logging. Audit is for **business** observability (who-did-what); logs are for **technical** observability. They overlap intentionally — both have value.

## Alerting at v0

- "Queue depth alert" → CloudWatch Log filter → CloudWatch Alarm → email.
- "Error rate spike" → CloudWatch Log filter on `level=error` → Alarm → email.
- "Wallet invariant fail" → audit event of category `ops.invariant_fail` → CloudWatch Log filter → Alarm → page (PagerDuty integration deferred to v1).

At v0 with one developer, "page" = email. At v1 with on-call, PagerDuty.

## What we deliberately DON'T instrument at v0

- Per-DB-query latency (slow query log handles).
- Per-Go-routine count.
- GC pauses.
- Per-endpoint percentiles (CloudWatch derives from logged latency_ms).

Add when needed.

## Performance budgets

Tracked in service docs but rolled up here:

| Operation | P95 target |
|---|---|
| Auth middleware | <5ms |
| Policy resolve (cache hit) | <1ms |
| Pricing quote | <30ms |
| Allocation (8 carriers) | <200ms |
| Wallet reserve/confirm/release | <30ms each |
| Tracking webhook handler | <100ms |
| GET /v1/orders (list) | <500ms |
| POST /v1/shipments (book) | <2s end-to-end (carrier API permitting) |

Microbenchmarks (`testing.B`) track these in CI; regressions block merge.

## Runbooks

In `IMPL/runbooks/` (live folder). Each runbook is a CloudWatch Logs Insights query + a remediation playbook:
- Carrier outage detected.
- Stuck shipment.
- Wallet invariant fail.
- KYC backlog.
- Webhook flood.

Authored progressively as ops scenarios arise.
