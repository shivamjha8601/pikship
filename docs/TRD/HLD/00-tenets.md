# HLD Tenets

> **Status:** Locked v1.0 — committed after a 3-architect review (Architect A, B, C). Reversals from earlier conversations are explicitly noted.
>
> Tenets here are **load-bearing**. They shape every downstream design choice. Changing one requires writing an ADR and re-reviewing affected services.

## Audience

Any engineer touching the codebase. Read once on onboarding; refer back when in doubt.

---

## 1. The shape

### T-1.1 Single Go monolith
One Go module, one binary, deployed as a single artifact. Internally **modular** — each PRD bounded context is a Go package with a published interface and hidden internals. No microservices. No service mesh. No event bus beyond Postgres.

### T-1.2 One Postgres, one schema
A single AWS RDS Postgres handles OLTP, jobs, audit, and reports at v0/v1. **Multi-AZ from day 0** (reversed from earlier "single-AZ at v0" — see [`05-decisions/0008-multi-az-rds-from-v0.md`](./05-decisions/0008-multi-az-rds-from-v0.md)). One `public` schema; tables prefixed by domain (e.g., `wallet_ledger_entry`, `shipment_attempt`).

### T-1.3 No Redis. No Kafka. No SQS.
Postgres handles caching (LISTEN/NOTIFY + table-backed state), queues (river), and event distribution (outbox pattern). The simplification is intentional — fewer moving parts to monitor, deploy, debug.

### T-1.4 HTTP/JSON only, REST style
URL-versioned (`/v1/...`). No GraphQL, no gRPC. Every state-mutating endpoint accepts an `Idempotency-Key` header.

### T-1.5 Two operational modes in one binary
- `--role=api` — serves HTTP only.
- `--role=worker` — runs river jobs only.
- `--role=all` — both. Default.

At v0 we run one `--role=all`. At v1 we split: 2× `--role=api` + 1× `--role=worker`. **Same binary, same code.** No service split.

---

## 2. Code & language

### T-2.1 Go, stdlib first
`net/http`, `database/sql`, `log/slog`, `encoding/json`, `errors`, `context`. External deps only when stdlib is genuinely insufficient. Approved external deps:
- `chi` — HTTP router (lightweight, stdlib-compatible).
- `sqlc` — typed SQL.
- `golang-migrate/migrate` — schema migrations.
- `riverqueue/river` — Postgres job queue.
- `jackc/pgx/v5` — Postgres driver (stdlib `database/sql` compatible).
- `go-playground/validator/v10` — request validation.
- `golangci-lint` — lint.

Anything beyond this list requires an ADR.

### T-2.2 sqlc for DB access. No ORM.
Hand-write SQL in `query/*.sql`; sqlc generates type-safe Go. **No GORM, no Bun, no Ent.**

### T-2.3 golang-migrate for schema
Plain `.sql` migrations. Versioned. Migrations applied as a **CI step before deploy** — not on binary startup. Binary on boot verifies schema version is ≥ expected and refuses to start if not. *(Reversed from "migrations on startup" — see ADR 0009.)*

### T-2.4 Idiomatic Go
- No `init()` functions (explicit dependency wiring in `main`).
- No package-level mutable state.
- No `panic()` outside `main`.
- No `interface{}` / `any` in domain APIs (use concrete types).
- Errors via `errors.Is` / `errors.As`; no error string matching.
- Context first parameter on every domain method that does I/O.
- `clock.Clock` interface injected; never call `time.Now()` directly in domain code (testability).

### T-2.5 Strict lint config
`golangci-lint` with: `errcheck`, `gosec`, `gocyclo`, `gosimple`, `staticcheck`, `revive`, `bodyclose`, `sqlclosecheck`, `nilerr`. PRs blocked on lint.

---

## 3. Persistence & transactions

### T-3.1 Seller scoping via Postgres RLS + GUC
Every domain table has `seller_id` (or `sub_seller_id`) and an RLS policy:
```sql
CREATE POLICY x_seller ON foo USING (seller_id = current_setting('app.seller_id')::uuid);
```
Request-scoped transaction sets `SET LOCAL app.seller_id = $1` after auth resolves the seller. **The application has no path that bypasses RLS** except via the dedicated `pikshipp_reports` and `pikshipp_admin` Postgres roles, which carry `BYPASSRLS`. *(Reports role bypass added per Architect B's review — see ADR 0005.)*

### T-3.2 One DB transaction per mutating request
Domain operations write business state + outbox event in the same transaction. External I/O (carrier API, email send, S3 upload) happens **outside** the transaction.

### T-3.3 Booking is two transactions
Tx1: validate, reserve wallet, write `pending_carrier` shipment, write outbox `shipment.pending`. Carrier API call (no DB tx). Tx2: write AWB or release hold; write outbox `shipment.booked` or `shipment.booking_failed`. Reconciliation cron sweeps shipments stuck in `pending_carrier` for >5min. *(Spec'd explicitly per Architect B's review — see ADR 0006.)*

### T-3.4 Two-phase wallet operations
`wallet.Reserve(amount, ttl) → hold_id` / `Confirm(hold_id, ref) → ledger_entry` / `Release(hold_id)`. Holds expire automatically via a per-minute river cron. Wallet idempotency enforced via UNIQUE constraint on `(ref_type, ref_id, direction)`.

### T-3.5 Money in paise (int64)
No floats anywhere in money code. sqlc maps NUMERIC to a custom `Paise` int64 type.

---

## 4. Async, outbox, jobs

### T-4.1 Outbox pattern is mandatory for cross-module events
Domain operations write to `outbox_event` in the same DB transaction as their state change. A river worker consumes outbox rows with `SELECT ... FOR UPDATE SKIP LOCKED`. Required:
- Partial index `WHERE enqueued_at IS NULL`.
- River jobs use `unique_args = (outbox_event_id)` to prevent double-handling on the rare double-enqueue.
- Cleanup cron removes `enqueued_at IS NOT NULL AND created_at < now() - 7d`.

### T-4.2 river for all background work
Channel polling, carrier polling, NDR deadlines, COD reconciliation, wallet invariant check, audit chain verification, outbox forwarding — all river jobs. River's scheduler replaces cron.

### T-4.3 Per-seller ordering where it matters
Wallet ledger replays, audit chain writes — workers configured with `unique_args = seller_id` to serialize per seller. Notifications and tracking events parallelize freely.

---

## 5. Authn, authz, identity

### T-5.1 Server-side opaque sessions at v0 (interface-pluggable)
Authentication is a swappable implementation behind a single `auth.Authenticator` interface (see [`04-cross-cutting/01-authn-authz.md`](./04-cross-cutting/01-authn-authz.md)).

**v0 implementation:** `OpaqueSessionAuthenticator` — server-side `session` table, opaque token in HTTP-only secure cookie (dashboard) or `Authorization: Bearer <token>` header (API clients). Indexed lookup; immediately revocable.

**Future implementations** (interface-compatible, not built at v0): `JWTAuthenticator`, `OIDCAuthenticator` for SAML/OIDC SSO at enterprise tier.

*(Reversed from earlier "JWT only" preference — see ADR 0003 for full rationale.)*

### T-5.2 Google OAuth → session
Login flow: OAuth via Google → resolve Pikshipp user → create session row → set cookie/return token. No password authentication. Phone is added on profile (verified via SMS OTP through MSG91) but is **not** a login factor at v0.

### T-5.3 Roles within a seller
RBAC: Owner / Manager / Operator / Finance / ReadOnly. Stored in `seller_user` join table. Session carries `(user_id, seller_id, roles)`. Permission checks at handler layer via `auth.Require(roles...)`.

### T-5.4 No MFA at v0
TOTP added in v1 for Owner and Finance roles.

---

## 6. API conventions

### T-6.1 RESTful URL design
- `/v1/orders/{id}` — singular resource.
- `/v1/orders` — collection.
- POST creates; PATCH updates; DELETE deletes (rare; mostly we mark wound-down).

### T-6.2 Standardized error shape
```json
{ "error": { "type": "...", "code": "...", "message": "...", "param": "...", "request_id": "...", "doc_url": "..." } }
```

### T-6.3 Cursor-based pagination
`?starting_after=<id>&limit=N` with `limit ≤ 100`. Stable across paginated mutations.

### T-6.4 OpenAPI as source of truth for the dashboard contract
Hand-maintained `api/openapi.yaml`. Dashboard regenerates its TypeScript client from this. Spec-first, code follows.

### T-6.5 Idempotency-Key header
Every state-mutating endpoint accepts it. Server stores `(seller_id, idempotency_key) → response_body` for 24h. Replay returns cached response with `Idempotent-Replayed: true` response header.

---

## 7. Observability

### T-7.1 Structured JSON logs to stdout, shipped to CloudWatch via Vector
*(Adopted from Architect C's review.)* `log/slog` with JSON handler. Vector agent on the EC2 host ships to CloudWatch. Log fields: `request_id`, `seller_id` (when applicable), `user_id` (when applicable), `actor_kind`, `service`, `version`, `timestamp`, `level`, `msg`.

### T-7.2 Health endpoints
- `GET /healthz` — liveness, no external dependencies, returns 200 if process is responsive.
- `GET /readyz` — readiness: DB up + migrations to head + S3 reachable. Carrier APIs are NOT in readiness check.

### T-7.3 Queue depth alerting
A river cron checks queue depth every 5 min; emits `audit_event{kind: ops.queue_alert}` when threshold exceeded.

### T-7.4 No Sentry, no Datadog at v0
Errors are logged to stdout (with stack traces). Move to Sentry in v1.

### T-7.5 No distributed tracing at v0
Single binary makes tracing low-value. OpenTelemetry added in v1.

---

## 8. Resilience

### T-8.1 Carrier circuit breakers backed by DB state
Each carrier has a row in `carrier_health_state`. Process-level cache reads it with 5s TTL fallback (in addition to LISTEN/NOTIFY for low-latency invalidation). Multi-instance safe. *(Adopted from Architect B's review.)*

### T-8.2 Idempotency at every external boundary
Inbound webhooks (carriers, channels, payment gateway): unique on `(source, source_event_id)`. Outbound calls (carrier book, KYC, etc.): caller-supplied or deterministic idempotency key.

### T-8.3 Two-phase commits for booking
Wallet reserve → carrier call → wallet confirm. Reconcile cron handles partial failures.

### T-8.4 Per-seller in-process rate limiting
Token bucket per `(seller_id, endpoint_class)`. At multi-instance, total rate is N × per-instance limit; we double the limits in config when scaling out.

### T-8.5 Graceful shutdown
SIGTERM → stop accepting requests → drain in-flight HTTP → drain river jobs (configurable timeout) → exit. Targeting <30s drain at v1 traffic.

---

## 9. Testing

### T-9.1 SLTs (Service-Level Tests) using testcontainers
Every public domain interface has SLT coverage: bring up real Postgres + LocalStack (S3, SES, SQS-not-used-but-LS-provides), exercise the interface end-to-end, assert side effects in DB.

### T-9.2 Unit tests on domain logic
Pure-function logic (rate computation, allocation scoring, status normalization) has table-driven unit tests with high coverage targets (≥80%).

### T-9.3 Microbenchmarks on hot paths
`testing.B` benchmarks for: pricing engine quote, allocation engine pick, policy engine resolve, wallet ledger post, status normalization. Tracked over time; regressions block merge.

### T-9.4 Sandbox carrier adapter
A real `Adapter` implementation that simulates Delhivery (and others) deterministically. Used in SLTs and in the seller-facing sandbox environment. Same code path as production adapters.

---

## 10. Deployment & secrets (v0 simplified)

### T-10.1 EC2 + systemd, single instance at v0
`pikshipp.service` systemd unit. Logs to journald → Vector → CloudWatch. Multi-instance + ALB at v1. *Deploy outage at v0 is acceptable* — documented; deploys outside business hours.

### T-10.2 Multi-AZ RDS from day 0
Even at v0. *(Adopted from Architect C's review.)* Reduce instance size if cost is a concern; do not reduce HA.

### T-10.3 Secrets in env vars at v0 (note: revisit)
**Acknowledged technical debt.** Migrate to SSM Parameter Store before prod. Tracked in [`05-decisions/0010-secrets-as-env-at-v0.md`](./05-decisions/0010-secrets-as-env-at-v0.md).

### T-10.4 No KMS / encryption at rest at v0 (note: revisit)
TLS at the edge only. Application-level encryption deferred. Tracked as a v1 prerequisite.

### T-10.5 Migrations as a CI step
Run before binary deploy. Binary refuses to start if schema version < expected. *(Reversed from "migrations on startup" — see ADR 0009.)*

---

## 11. Multi-instance readiness (designed-for, even though we run N=1)

Per Architect C's review, every piece of in-process state is audited in [`01-architecture/04-multi-instance-readiness.md`](./01-architecture/04-multi-instance-readiness.md). All caches must be invalidatable cross-instance via LISTEN/NOTIFY *and* tolerate missed notifications via TTL.

---

## Reversals from earlier discussions (explicit)

| Earlier decision | Reversed to | Reason | ADR |
|---|---|---|---|
| JWT only, no sessions | Server-side opaque sessions (interface-pluggable) | 15-min revocation gap unacceptable on money platform; we needed a session table anyway for refresh; PG-on-index lookup is sub-ms | 0003 |
| Single-AZ RDS at v0 | Multi-AZ from day 0 | $30/mo cost-of-cowardice doesn't beat one weekend incident | 0008 |
| Migrations on startup | Migrations as CI step | Bad migration takes service down for rollback duration; CI-step gives observability + reversibility | 0009 |
| Logs to stdout-only at v0 | Vector → CloudWatch from day 0 | journald rotation loses old logs; first incident pays for the 30-min setup | 0011 |
| Circuit breaker in-memory only | DB-backed state + 5s TTL | LISTEN/NOTIFY drops on disconnect; multi-instance drift unacceptable | 0007 |

These reversals are *recent* — committed during the architectural review on 2026-04-30. Earlier-conversation tenets that *survived* the review are unchanged.

---

## What's NOT a tenet

To prevent over-codifying:
- File/folder layout — opinions in code, not tenets.
- Specific lint rules beyond the curated set above.
- Database connection pool sizes.
- HTTP timeouts (configurable, not hard-coded).
- Cache TTLs (configurable per cache).
- Specific carrier adapter implementations.

These are **conventions**, captured in `04-cross-cutting/` and per-service docs. They evolve more quickly than tenets.
