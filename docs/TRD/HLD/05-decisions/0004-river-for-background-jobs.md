# ADR 0004 — `river` for background jobs

Date: 2026-04-30
Status: Accepted
Owner: Architect A

## Context

We need a background job system for:
- Outbox forwarding (continuous).
- Carrier polling (per-carrier scheduled).
- NDR deadline sweeper (every 15min).
- COD remittance reconciliation (daily).
- Wallet invariant check (daily).
- Audit chain verification (weekly).
- Pending-shipment reconcile (every 5min).
- Wallet hold expiry (every minute).
- Notification dispatch (event-driven from outbox).

Per [ADR 0002](./0002-postgres-only-persistence.md), this must be Postgres-backed (no Redis/SQS/Kafka).

Options:
1. Roll our own with `FOR UPDATE SKIP LOCKED` polling.
2. Use `riverqueue/river` (Postgres-native, modern Go library).
3. Use `gocraft/work` or similar (Redis-based, ruled out).
4. Use `asynq` (Redis-based, ruled out).

## Decision

**Use `riverqueue/river`** (https://riverqueue.com/). Postgres-native, modern, idiomatic Go API.

## Alternatives considered

### Roll our own
- Pro: no dependency.
- Con: surprisingly subtle. Cron scheduling, retries with backoff, leader election, DLQ, exactly-once-with-idempotency, observability — all need careful implementation. Six months of side-quest engineering before we have what `river` already provides.

### `gocraft/work` / `asynq`
- Pro: mature.
- Con: Redis-based. Violates ADR 0002.

### `pgmq` (Postgres extension)
- Pro: simple.
- Con: limited feature set; lacks scheduled jobs, retries with policies, transactional integration.

### `river`
- Pro: written for our exact use case; idiomatic Go; integrates with `pgxpool`; supports unique args, scheduled jobs, periodic jobs, retry policies, dead-letter; transactional `Insert` (writes to a job table inside an existing transaction).
- Con: relatively new (v0.x as of mid-2026). API still occasionally changes.

The acceptable risk: river's API churn is bounded; its core mechanism is correct (it's just `FOR UPDATE SKIP LOCKED`-style polling done well); we can fork or replace if it stalls.

## Consequences

### What this commits us to
- `river` as a dependency. Pinned version; manual upgrades.
- River's table set in our DB (~5 small tables managed by river itself).
- River's CLI for ops (`river-cli` for queue inspection).

### What it costs
- Library churn risk (mitigable).
- One more thing to operate (but minimal — it's just job tables).

### What it enables
- Outbox pattern atomic with our domain transactions (`river.Insert` accepts a `pgx.Tx`).
- Standard retry/DLQ/scheduling without custom code.
- Multi-instance worker coordination via Postgres.

## Operational notes

- `river` workers run in the `--role=worker` binary mode.
- One river client per process; configured at startup.
- Dead-letter inspection via river-cli; alert on queue depth via SLO.

## Open questions

- If river's API stabilizes (v1.0), we lock to it. If river stalls or its semantics change, we evaluate forking or migrating. The core machinery (Postgres + `FOR UPDATE SKIP LOCKED`) is replaceable; the integration code is small.
