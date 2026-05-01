# ADR 0002 — Postgres-only persistence

Date: 2026-04-30
Status: Accepted
Owner: Architect A

## Context

Modern applications often grow organically into:
- Postgres for transactional data.
- Redis for cache.
- Kafka / SQS / RabbitMQ for queues.
- Elasticsearch for search.
- Memcached for sessions.
- Time-series DB for metrics.

Each addition is an operational tax. Pikshipp at v0 is one developer. Six pieces of infra to operate is unsustainable.

Postgres in 2026 can do most of this:
- Cache (with LISTEN/NOTIFY for invalidation).
- Queues (with `FOR UPDATE SKIP LOCKED`; libraries like `river` build on this).
- Pub/sub (LISTEN/NOTIFY).
- Full-text search (built-in tsvector; sufficient for our needs).
- Time-series-ish (with TimescaleDB extension; not v0).

The question is: where's the line?

## Decision

**Postgres-only persistence.** No Redis. No Kafka. No SQS. No Elasticsearch.

Specifically:
- **Caching** — in-process per instance; backed by DB; LISTEN/NOTIFY for invalidation; TTL fallback.
- **Queues** — `river` (Postgres-native job queue).
- **Pub/sub** — LISTEN/NOTIFY for short-lived signals; outbox table for durable events.
- **Search** — Postgres full-text + indexes; sufficient for v0–v2.
- **Sessions** — `session` table.

## Alternatives considered

### Add Redis for cache + sessions
- Pro: industry default; well-understood ops; very fast.
- Con: another service to deploy, monitor, secure, back up; another point of failure; another set of failure modes.
- For our scale, sub-millisecond Postgres lookups on indexed `session.token_hash` are *fast enough*. Redis would be a 10× speedup we don't need.

### Add SQS / Kafka for events
- Pro: durable, scalable, decoupled.
- Con: another service; not transactional with our DB (need outbox pattern anyway); inter-service contracts.
- River + outbox gives us durable event delivery from inside Postgres. Same guarantees, fewer moving parts.

### Add Elasticsearch for search
- Pro: powerful search.
- Con: ops burden; sync lag with primary DB.
- Postgres tsvector handles our needs (search by AWB, order ID, buyer phone) for v0–v2. Elasticsearch only justifies itself if our search needs grow significantly.

## Consequences

### What this commits us to
- Postgres performance is critical. We benchmark, we tune, we add indexes diligently.
- We don't have a fallback for cache misses to a faster cache layer; we have to keep cache miss paths fast.
- One DB instance carries the world; multi-AZ + backups are non-negotiable.

### What it costs
- We can't lean on Redis tricks (e.g., distributed locks, sorted sets).
- Postgres is more sensitive to schema design and index tuning than Redis.
- At very high scale (v3+), we may hit walls and need to revisit.

### What it enables
- Single deploy unit for all state.
- Atomic operations across queues, events, and domain state (outbox pattern works because everything is in one DB).
- Lower operational overhead.
- Cheaper at our scale.

## Open questions

- At what scale do we reconsider? Hard to predict; likely when we have multiple read-heavy services hitting the DB and read replicas alone don't cut it. We add Redis at that point if needed.
- Do we ever add Elasticsearch? If we get into log analytics or seller-facing analytics, maybe. Not v0/v1/v2.
