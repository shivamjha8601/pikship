# Architecture Decision Records (ADRs)

> Each ADR captures a single load-bearing decision: the problem, the decision, the alternatives considered, the rationale, the consequences. Short, dated, owner-attributed. New ADRs accumulate here as architecture evolves.

## Format

```
# ADR NNNN — title

Date: YYYY-MM-DD
Status: Proposed | Accepted | Superseded | Deprecated
Owner: <person>
Supersedes: ADR XXXX (if applicable)

## Context
What's the problem; what's the constraint?

## Decision
What we picked.

## Alternatives considered
What else was on the table; why we rejected.

## Consequences
What this commits us to. What it costs. What it enables.

## Open questions
What we still don't know.
```

## Accepted ADRs (this set)

| # | Title | Status |
|---|---|---|
| 0001 | Modular monolith, not microservices | Accepted |
| 0002 | Postgres-only persistence (no Redis, Kafka, SQS) | Accepted |
| 0003 | Server-side opaque sessions, not JWT (interface-pluggable) | Accepted |
| 0004 | river for background jobs | Accepted |
| 0005 | Postgres RLS for seller scoping | Accepted |
| 0006 | Booking is two transactions + carrier call between | Accepted |
| 0007 | Carrier circuit breaker is DB-backed with TTL cache | Accepted |
| 0008 | RDS multi-AZ from day 0 | Accepted |
| 0009 | Migrations as a CI step, not on startup | Accepted |
| 0010 | Secrets in env vars at v0 (technical debt) | Accepted |
| 0011 | Vector → CloudWatch from day 0 | Accepted |

## Pending / future

ADRs to be written as we make decisions:
- v1 multi-instance + ALB cutover plan.
- Sentry adoption for v1.
- Carrier adapter SDK packaging strategy (in-tree vs separate module).
- Mobile app architecture (v2).
- Multi-region (v3+).
- First-party fleet (Pikshipp Express) integration (v3+, conditional).
