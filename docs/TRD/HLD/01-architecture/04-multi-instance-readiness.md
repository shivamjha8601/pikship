# Multi-instance readiness audit

> Per Architect C's review: an explicit table of every piece of in-process state, what happens at N>1, and what (if anything) needs to change. Today, prevention beats archaeology.
>
> **Today (v0): N=1 single instance.** **v1+: 2× API instances + 1× worker instance.**
> **Goal:** the architecture works correctly at N>1 today, even though we deploy at N=1.

## Audit table

| In-process state | Module | What it is | Behavior at N=1 | Behavior at N>1 (uncorrected) | Mitigation in current design |
|---|---|---|---|---|---|
| **HTTP server** | `api/http` | Router + handler goroutines | Trivially fine | Each instance serves independently; sticky sessions unnecessary | None needed (stateless). Sessions are server-stored in DB |
| **Session resolution cache** | `internal/auth` | Recent sessions cached for ~30s | Trivially fine | Eventual consistency on session revocation: 30s window where revoked sessions still validate on instances that haven't seen the invalidation | TTL is small + LISTEN/NOTIFY on `session_revoked` channel for sub-second invalidation |
| **Policy engine cache** | `internal/policy` | Per-seller resolved values | Trivially fine | Stale config on one instance vs another | LISTEN/NOTIFY on `policy_updated` channel; 5s TTL fallback if NOTIFY missed |
| **Carrier circuit breaker state** | `internal/carriers` | open/closed/half-open per carrier | Trivially fine | Each instance has its own opinion of carrier health → one instance routes to a carrier the others have circuit-broken | DB-backed `carrier_health_state` table; in-process cache reads it; 5s TTL + LISTEN/NOTIFY |
| **Per-seller rate limiter** | `internal/observability/ratelimit` | Token bucket per (seller, endpoint) | Trivially fine | At N instances, total rate = N × per-instance limit | Acceptable; we configure per-instance limit at `total_target / N`. Rebalancing is manual on scale-out |
| **In-memory metrics counters** | `internal/observability` | Counters for requests, errors, etc. | Trivially fine | Aggregation requires shipping all counters to a central store | Logged to stdout per-instance; CloudWatch aggregates. At v0 metrics are minimal |
| **River runner state** | `riverqueue/river` | Worker pool, leader election | Trivially fine | River's Postgres-coordinated leader election handles this | River library handles correctly |
| **Outbox forwarder state** | `internal/outbox` | Loop reading outbox | Trivially fine | Multiple forwarders compete for rows | `FOR UPDATE SKIP LOCKED` partitions cleanly |
| **Idempotency cache** | `internal/idempotency` | (seller, key) → response | NOT cached in-process; DB-only at v0 | DB serves all instances consistently | DB authoritative; no in-process cache (correctness > latency) |
| **Carrier serviceability lookup** | `internal/carriers` | Pincode-zone cache | Trivially fine | Stale on one instance after a refresh | LISTEN/NOTIFY on `carrier_serviceability_updated`; 60s TTL |
| **Reliability scores** | `internal/allocation` | Carrier × pincode-zone scores | Trivially fine | Stale on one instance after the daily computation | TTL of 60s; LISTEN/NOTIFY when daily compute completes |
| **Rate card cache** | `internal/pricing` | Active rate cards per (seller, carrier) | Trivially fine | Stale after publish | LISTEN/NOTIFY on `rate_card_published`; 5min TTL |
| **JWT secret / session secret** | `internal/auth` | HMAC key for session token signing | Static; loaded at startup | All instances must share secret | Loaded from same env var across instances |
| **Wallet hold state** | `internal/wallet` | NOT in-process; DB-only | Tx-locked | DB serves all instances consistently | Correct by design |
| **Audit chain hash** | `internal/audit` | NOT in-process; per-write read of prior hash | Trivially fine | DB read inside the writing transaction guarantees correctness | Correct by design |
| **River job leases** | `riverqueue/river` | Leased job → executing worker | Trivially fine | Library handles via Postgres advisory locks | Library handles correctly |
| **In-flight HTTP request count** | `api/http` | For graceful shutdown | Trivially fine | Each instance drains its own | Per-instance correct |
| **Allocation decision log buffer** | NOT in-process; DB-only | — | DB authoritative | — | Correct by design |
| **HTTP client pools** | `internal/carriers/*` | Connection pools to carriers | Trivially fine | Independent per instance | Correct |
| **PG connection pool** | `pgxpool` | Connections to Postgres | Trivially fine | Independent per instance; PG handles N×poolsize total | Pool size at v0 is 50; at v1 with N=3 instances, total 150 connections — within RDS limits |
| **Prometheus-style histograms** | (not used at v0) | — | — | Multi-instance aggregation requires Prometheus | Deferred; logged values at v0 |

## Inferred rules

From the audit:

1. **No domain state in process memory** without a DB-backed source-of-truth and an invalidation strategy.
2. **Caches are advisory only.** Correctness from DB; cache for performance.
3. **LISTEN/NOTIFY is best-effort.** Always paired with TTL fallback.
4. **Serialized resources** (wallet rows, audit chain head) are PG-locked, not in-process.
5. **Process-restart should leave no orphan state.** River jobs survive; outbox events survive; HTTP in-flight drains.

## What changes at v1 N>1 deployment

When we move from N=1 to N=3 (2× API + 1× worker):

| Change | Required |
|---|---|
| Provision an ALB | Yes |
| Sticky sessions on ALB | **No** — sessions are server-stored, not session-affinity-required |
| TLS cert in ACM | Yes |
| Per-instance rate limit halved (configured to `total_target / N`) | Yes |
| Outbox forwarder runs on **worker instance only** | Yes (config flag) |
| All cron jobs run on **worker instance only** | Yes (config flag) |
| Health check on `/readyz` from ALB | Yes |
| Connection pool tuning (RDS connection limit / N) | Yes |
| Increase RDS instance size? | Maybe — capacity planning at v1 readiness review |

These are **configuration changes**, not code changes. The architecture is multi-instance-correct today.

## What we must NOT do (to preserve this property)

- Add an in-process cache for any seller-scoped mutable data without LISTEN/NOTIFY + TTL.
- Use package-level `var` for runtime state.
- Use `sync.Map` for shared mutable state without considering invalidation.
- Run cron-style logic via Go's `time.Ticker` instead of river. (River has built-in coordination; ad-hoc tickers fire on every instance.)
- Make any decision based on "I know there's only one instance".

## CI gate

A static analysis check (`go vet` + custom linter) flags:
- Package-level non-const `var` in `internal/` (with allowlist for narrowly-scoped utilities).
- `time.Ticker` usage outside `internal/observability/` (where it's local).
- Missing `seller_id` GUC set in any handler that reads seller-scoped tables (linted via SQL static analysis on sqlc-generated code).

## Test gate

A "multi-instance simulation" SLT spins up two `--role=all` processes against the same testcontainer Postgres and verifies:
- A session created on instance A is valid on instance B.
- A session revoked on A is invalid on B within 5s.
- A wallet operation from A and a concurrent one from B don't conflict (FOR UPDATE serializes).
- An outbox event written on A is forwarded by B (or A) exactly once.
- A carrier circuit-breaker trip on A is observed by B within 5s.

This SLT runs in CI on every PR. If it fails, multi-instance correctness is broken.

## When we deploy at N>1

We don't need a code change. We need:
1. Update the systemd template to support `--role=api` and `--role=worker` flags.
2. Provision two API EC2 instances + one worker EC2 instance.
3. Set up ALB.
4. Reduce per-instance rate limits.
5. Run the multi-instance SLT one more time against staging.
6. Cut over.

Estimated work: 1–2 days of ops + 1 day of testing. Not a v0 distraction.
