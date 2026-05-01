# ADR 0008 — RDS multi-AZ from day 0

Date: 2026-04-30
Status: Accepted
Owner: Architect A (after Architect C push-back)
Supersedes: prior conversational decision "single-AZ at v0"

## Context

Original decision: single-AZ RDS at v0 to save cost; multi-AZ from v1.

Architect C objected: a logistics platform handles money. Single-AZ means an AZ failure or a maintenance window or DB corruption causes hours of downtime. The cost delta (~$30/month for `db.t4g.medium`) is rounding error against one weekend incident.

We have ~5 friendly sellers at v0. They're friendly, but they're transacting real money. A single-AZ failure during their working hours is a trust catastrophe even at v0 scale.

## Decision

**RDS multi-AZ from day 0.** Even at v0 with low traffic.

If cost is a concern, reduce instance size (`db.t4g.small` if needed) but **do not reduce HA**.

## Alternatives considered

### Single-AZ at v0 + multi-AZ at v1
- Rejected: a single AZ outage in the v0 window is cheap to prevent and expensive to suffer.

### Single-AZ + nightly restore drill
- Rejected: doesn't address availability during the window between failure and manual restore.

## Consequences

### What this commits us to
- Multi-AZ pricing: ~2× single-AZ. At `db.t4g.medium` that's ~$60/month vs ~$30. Acceptable.
- Failover happens automatically on AZ failure. RTO ~30s, RPO ~5min (RDS automated backup interval).
- Maintenance windows can be zero-downtime via reader promotion.

### What it costs
- ~$30/month additional infrastructure cost at v0.
- Multi-AZ syncing has minor write latency increase (negligible at our load).

### What it enables
- Tolerance to AZ failure.
- Tolerance to RDS maintenance.
- Trust with friendly sellers.
- One less "v1 prerequisite" to remember.

## Operational notes

- RPO target: 5 minutes (RDS automated backup interval).
- RTO target: 30 minutes (failover + DNS propagation + app reconnect).
- Backup retention: 7 days automated; manual snapshot before each major release.
- Quarterly restore drill.

## Open questions

- When do we need a read replica? At v2 if reports module starts straining the primary. Not v1.
- Cross-region DR: deferred to v3+ when we have customers needing it.
