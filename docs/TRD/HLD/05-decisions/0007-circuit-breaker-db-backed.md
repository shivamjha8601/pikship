# ADR 0007 — Carrier circuit breaker is DB-backed with TTL cache

Date: 2026-04-30
Status: Accepted
Owner: Architect A (after Architect B push-back)

## Context

Carrier APIs go down. When that happens, the allocation engine should route around the down carrier. The circuit-breaker pattern is standard.

Original proposal: in-process state (per-instance map of `carrier → state`), updated by counters as we hit/miss the carrier.

Architect B objected: at multi-instance, each instance has its own opinion. Instance A circuit-breaks Delhivery; Instance B keeps routing to it. Drift is a real correctness problem on a shared resource.

LISTEN/NOTIFY can broadcast state changes, but it's best-effort. Network blips, GC pauses, listener disconnects can cause missed events. After a missed event, instances drift indefinitely until the next reset.

## Decision

**DB-backed circuit-breaker state with in-process cache + 5s TTL fallback.**

Schema:
```sql
CREATE TABLE carrier_health_state (
  carrier_id          UUID PRIMARY KEY,
  status              TEXT NOT NULL,           -- 'closed' | 'open' | 'half_open'
  failures_in_window  INT NOT NULL DEFAULT 0,
  successes_in_window INT NOT NULL DEFAULT 0,
  window_started_at   TIMESTAMPTZ NOT NULL,
  opened_at           TIMESTAMPTZ,
  half_opened_at      TIMESTAMPTZ,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Update path:
- After every carrier API call, increment counters in DB (single row UPDATE, ~1ms).
- If `failures / (failures + successes) > threshold`: transition to `open`; emit NOTIFY.
- After `cooldown_sec`: transition to `half_open`; emit NOTIFY.
- After `half_open_test_count` successful test calls: transition to `closed`; emit NOTIFY.

Read path:
- In-process cache reads `carrier_health_state` row.
- Cache TTL: 5s.
- LISTEN/NOTIFY on `carrier_health_changed` invalidates cache immediately when message received.
- If LISTEN missed (disconnect, GC pause, etc.), the 5s TTL ensures eventual freshness.

## Alternatives considered

### Pure in-process state
- Rejected per Architect B's review: multi-instance drift.

### LISTEN/NOTIFY-only invalidation (no TTL)
- Rejected: NOTIFY is best-effort; misses cause indefinite staleness.

### Frequent polling (no cache, every request reads DB)
- Adds ~1ms per allocation. Acceptable but unnecessary; cache is cheap.

### Distributed locking on the carrier (e.g., Postgres advisory lock)
- Overkill; we don't need exclusivity, just visibility.

### Heartbeat + lease pattern
- Fancier; same outcome at higher complexity.

## Consequences

### What this commits us to
- One row UPDATE per carrier API call. Volume: at v1's 50k shipments/month, that's ~70 calls/min = trivial.
- Per-instance cache that's a few KB.
- LISTEN connection per worker process.

### What it costs
- Slight write contention on `carrier_health_state` rows during high-traffic periods. Mitigated: only ~10 carriers at v1, each row is its own lock target.

### What it enables
- Multi-instance correctness.
- Recovery from missed NOTIFYs.
- Simple ops view (one DB query shows all circuit states).
- Works at N=1 (no behavior change), and at N>1 (correct).

## Configuration

Per carrier (in `policy_setting_definition`):
- `circuit.failure_threshold_pct` — default 30%.
- `circuit.failure_window_sec` — default 60.
- `circuit.cooldown_sec` — default 60.
- `circuit.half_open_test_count` — default 5.

Adjustable per carrier without code change.

## Open questions

- Does the cache miss rate (forcing the 5s TTL fallback) ever become high enough to matter? Monitor; alert at >5% TTL fallback rate (suggests NOTIFY plumbing is broken).
- Per-pincode-zone circuit breaking? E.g., a carrier is healthy globally but down in Mumbai. Not v1; v2 if we observe the pattern.
