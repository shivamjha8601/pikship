# ADR 0005 — Postgres RLS for seller scoping

Date: 2026-04-30
Status: Accepted
Owner: Architect A (with Architect B input on report-role split)

## Context

We need bulletproof seller data isolation. A bug that returns one seller's order to another seller is an extinction-level event for trust.

Two implementation strategies:
1. **Application-layer enforcement.** Every query has `WHERE seller_id = ?`. Layer of repos / sqlc-generated wrappers ensures no query skips it.
2. **Database-layer enforcement (RLS).** Postgres Row Level Security ensures every query is filtered, regardless of what the application does.

## Decision

**Postgres RLS + per-transaction `app.seller_id` GUC.** Every seller-scoped table has an RLS policy. Every request transaction sets `SET LOCAL app.seller_id = $1` after auth resolves the seller.

Three Postgres roles:
- `pikshipp_app` — application role; RLS enforced; no `BYPASSRLS`.
- `pikshipp_reports` — analytics role; has `BYPASSRLS`; read-only on most tables.
- `pikshipp_admin` — Pikshipp ops elevated path; has `BYPASSRLS`; writes audit-logged.

(The reports/admin role split was added per Architect B's review.)

## Alternatives considered

### Application-layer enforcement
- Rejected: a single forgotten `WHERE seller_id` is a leak. Code review and tests would mostly catch it; RLS is an additional defense that catches the rest.
- Even with sqlc generating typed wrappers, an ad-hoc query in a migration or a maintenance script could bypass.

### Schema-per-tenant
- Rejected: doesn't scale to 10k+ sellers.

### Database-per-tenant
- Rejected: doesn't scale; not for SMB SaaS.

### RLS (chosen)
- Postgres-level guarantee; even a buggy query can't return wrong-seller data (just empty results, which fails-loud in tests).
- Standard pattern; well-understood operational profile.

## Consequences

### What this commits us to
- Tied to Postgres for seller scoping. (Already tied per ADR 0002.)
- Every seller-scoped table needs `seller_id` column + RLS policy + index. Boilerplate in migrations.
- Application must `SET LOCAL app.seller_id` per transaction. Middleware enforces.

### What it costs
- A small performance overhead per query (RLS predicate evaluation + index lookup). With proper `(seller_id, ...)` composite indexes, negligible. Benchmark gate at v1.
- Reports module needs `BYPASSRLS` role; coding discipline to use it correctly.
- Cross-seller analytics queries explicitly use the reports role; ops "view as" uses the admin role + audit emission.

### What it enables
- Defense in depth.
- Bug in handler = empty results, not data leak.
- Application code is simpler (no manual seller_id threading).
- DB dump/restore works without leaks.

## Operational notes

- Three connection pools, one per role.
- Application code uses the appropriate pool for the operation.
- RLS policies are migrated like tables; never hand-edited in prod.

## Test gate

- Unit test: every seller-scoped table has its RLS policy tested (ensure one seller can't see another's data even with crafted queries).
- SLT: cross-seller leak test with two sellers and parallel requests.
- Lint: SQL static analysis on sqlc-generated code flags missing `app.seller_id` GUC set in handlers.

## Open questions

- RLS performance at high concurrent load: benchmark at v1. If meaningful slowdown, we add per-policy hints or denormalize further.
- If we ever ditch Postgres for some module, we need a different scoping mechanism for that module. Architectural choice; would warrant its own ADR.
