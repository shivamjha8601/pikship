# LLD Final Review

> Two-reviewer pass over the v0 LLD set. Sandeep (Go performance / runtime) and Maya (maintainability / API design) read every LLD and recorded findings. This document records what they found, what we decided to fix in-doc, and what is acknowledged tech debt.

## Reviewer Briefings

### Sandeep — Go Performance & Runtime

> "I want to know: where will we burn CPU we don't expect? Where will we leak goroutines? Where will we hold a lock too long? Where will allocations make a hot path 10× slower than necessary? I don't care about elegance. I care about p99."

### Maya — Maintainability & API Design

> "I want to know: when a junior reads a method signature, can they call it correctly without reading the implementation? When something breaks at 2 AM, can the on-call find what they need? When we have to add the 26th service, do these patterns scale? I'm allergic to clever, hidden magic."

---

## Findings — Performance (Sandeep)

### S1. Per-request allocations in `core.Paise.Format` and `Paise.String`

**Where:** LLD §01-core/01-money.

**Issue:** Both methods allocate a `[]byte` and a `string`. They're called on every order render, every dashboard widget, every CSV row.

**Decision:** Acceptable for v0 — the cost is in the noise vs. SQL round-trips. **Action item** for future: provide `Paise.AppendFormat(dst []byte) []byte` for hot loops (CSV export, daily digest render). Document the alternative in §01-core/01-money so juniors don't reach for `fmt.Sprintf("%d", p)` in hot paths.

### S2. `LifecycleCache`, `PolicyCache`, `ContractCache`, `PickupCache` all use `sync.RWMutex` + `map`

**Where:** LLDs §03-services/01, /09, /11, /25.

**Issue:** Under high read concurrency, the `RWMutex` itself becomes contended. Go 1.18+ `sync.Map` is sometimes faster, sometimes worse — depends on access pattern.

**Decision:** Keep `sync.RWMutex` + plain map for v0. Reasons:
- Reads are mostly cache-hits with very fast critical sections (~50 ns).
- We have ≤ 50 concurrent goroutines per pod (HTTP handlers); contention won't matter at this scale.
- `sync.Map` has worse memory locality and harder semantics for invalidation.

**Action item:** add a benchmark `BenchmarkPolicyCache_Concurrent_64Readers` in §03-services/01 and re-evaluate if p99 of `policy.Get` exceeds 1 µs.

### S3. River queue concurrency vs. Postgres connection pool

**Where:** LLD §02-infrastructure/01 + §05-cross-cutting/03 (capacity).

**Issue:** River workers + API handlers share the same `pgxpool.Pool`. Each worker holds a connection for the duration of `Work`. If `pool.MaxConns = 50` and we run 30 worker goroutines, API handlers may starve under load.

**Decision:** Document a rule: **api role and worker role have separate `pgxpool.Pool` instances**. The api pool is sized for HTTP concurrency; the worker pool for job concurrency. Even when running with `--role=all` (dev), instantiate two pools.

**Action item:** add this rule to §02-infrastructure/01 explicitly. (See "Doc updates applied" below.)

### S4. `outbox.Forwarder` polls every 1s with `FOR UPDATE SKIP LOCKED`

**Where:** LLD §03-services/03-outbox.

**Issue:** Even when there's no work, every forwarder pod hits the DB once per second. With 4 worker pods that's 4 QPS at idle. Acceptable, but at higher pod counts we'd want LISTEN/NOTIFY-driven wake-ups.

**Decision:** Keep polling at 1s for v0. Add LISTEN/NOTIFY wake-up in v0.5 once outbox throughput justifies it.

**Action item:** add a metric `outbox_forwarder_idle_polls_total`. Alert if > 95% of polls are idle for sustained periods (signal to convert to LISTEN/NOTIFY).

### S5. `tracking.IngestWebhook` does an UNNEST-style state-event INSERT inside a tx that may also touch `shipment` + `outbox_event`

**Where:** LLD §03-services/14-tracking.

**Issue:** Three table writes per webhook event. Delhivery batches up to 100 events per webhook payload; one tx with 100 × 3 inserts can hit ~300ms.

**Decision:** Constrain webhook handler to **bulk insert** within the framework; the helper `tracking.IngestBatch(events)` should batch state events into a single `INSERT ... VALUES (...), (...), ...`. Same for outbox.

**Action item:** in §03-services/14-tracking, document `IngestBatch(events)` as the preferred entrypoint; private `ingestOne` is now a special-case wrapper.

### S6. `singleflight` used for dashboard misses but not for policy reads

**Where:** LLD §03-services/01-policy-engine and §03-services/19-reports.

**Issue:** Reports uses `singleflight.Group` to dedupe concurrent dashboard misses. Policy doesn't. Cold starts can produce thundering-herd policy reads.

**Decision:** Add `singleflight` to `PolicyCache.Get` cold-load path. Cheap insurance.

**Action item:** add note to §03-services/01-policy-engine.

### S7. Pre-allocated slice sizes in hot loops

**Where:** Many LLDs.

**Issue:** Code samples show `make([]X, 0, N)` in some places, plain `var s []X` in others. Inconsistent.

**Decision:** Convention: **always preallocate with capacity** when the size is known at the call site. Add this rule to §00-conventions/01-go-conventions.

### S8. Reflection-heavy JSON marshal in outbox payloads

**Where:** Many LLDs (every `*Payload` struct).

**Issue:** `encoding/json` reflection is the dominant cost in outbox emission. For 1000 events/s sustained, this matters.

**Decision:** Acceptable for v0 throughput (target: < 200 events/s). Document the alternative (`go-json` or per-type marshaller) in §03-services/03-outbox as a tier-2 optimization.

### S9. Background jobs `Work` methods don't bound their per-row processing time

**Where:** LLDs §03-services/15-ndr (BuyerNudgeJob), §03-services/16-cod (PostBatch), etc.

**Issue:** A worker calling out to SES/MSG91 with default 30s timeout and processing 100 rows could take 50 minutes on a single job.

**Decision:** Cap per-row processing inside loops with a per-iteration `context.WithTimeout`. Add convention to §00-conventions/01-go-conventions.

### S10. `pgx.NamedArg` is not free

**Where:** sqlc-generated code uses positional args; should be fine. But hand-written queries (e.g., reports.exportShipments) sometimes use named args.

**Decision:** Standardize on positional args everywhere. Note in §00-conventions/01-go-conventions.

---

## Findings — Maintainability (Maya)

### M1. Service interface methods sometimes accept tx, sometimes don't

**Where:** Many LLDs — Wallet has `Reserve` and `ReserveInTx`, Orders has `MarkBookedInTx`, etc.

**Issue:** Two parallel APIs per service. Easy to call the wrong one. Unclear when to use which.

**Decision:** Document the rule explicitly:
- The **public** method (without `InTx`) opens its own tx and is the default for callers.
- The **`InTx` variant** is for cross-service composition where the caller is already inside a tx.
- `InTx` methods take `pgx.Tx` as the second parameter and **never** call `db.WithTx`.
- `InTx` methods are listed **separately** in the service interface (suffix-grouped) so callers see at a glance which compositional helpers exist.

**Action item:** update §00-conventions/01-go-conventions and the service-LLD template.

### M2. Sentinel error sets are 5-10 errors each, with overlap

**Where:** Every service LLD.

**Issue:** Some sentinels are domain-meaningful (`ErrInsufficientBalance`); some are validation noise (`ErrInvalidAmount`). Mixing them dilutes the signal.

**Decision:** Convention: **a sentinel error must indicate a domain condition the caller is expected to handle differently**. Pure validation errors should be a single `ErrInvalidInput` (per service) with a wrapped detail.

**Action item:** add to §00-conventions/01-go-conventions; revisit each service LLD's sentinel list during code review.

### M3. Outbox event schema versioning convention not enforced

**Where:** LLD §03-services/03-outbox plus every service that emits outbox events.

**Issue:** Each payload struct has `SchemaVersion int json:"schema_version"`, but nothing enforces incrementing it on change.

**Decision:** Add a `make outbox-snapshot` target that captures the JSON Schema for each outbox kind into `docs/outbox-schemas/<kind>-v<n>.json`. CI fails if a schema changes without version bump.

**Action item:** add to §05-cross-cutting/02-ci-cd as a future task; document the convention now in §03-services/03-outbox.

### M4. RLS policies repeated verbatim 30+ times

**Where:** Most service LLDs.

**Issue:** `USING (seller_id = current_setting('app.seller_id')::uuid)` appears in dozens of policies. Easy to typo (e.g., wrong GUC name).

**Decision:** Define a helper SQL function in core migrations: `app.current_seller_id()` that wraps the GUC with NULL handling. Policies become `USING (seller_id = app.current_seller_id())`.

**Action item:** add to §02-infrastructure/01-database-access; update policy snippets in services to use the helper.

### M5. Many services define their own `LifecycleCache` / `PickupCache` / etc.

**Where:** §03-services/09, /11, /25.

**Issue:** Each cache is bespoke: similar shape, different bugs possible. Worth having a small generic.

**Decision:** Build `internal/core/cache` with a generic `TTLCache[K, V]` that the per-service caches wrap. Each service still owns its own loader function and invalidation triggers (NOTIFY channel, etc.); only the storage layer is shared.

**Action item:** add to §01-core/05-types.

### M6. Two-phase booking pattern referenced in shipments LLD but not extracted

**Where:** LLD §03-services/13-shipments.

**Issue:** The pattern is described as the template for "any DB → external mutation → DB call". But there's no shared helper or interface; future similar flows will diverge.

**Decision:** Document the pattern explicitly in §00-conventions/01-go-conventions ("Two-phase external-call pattern"). Don't extract a helper yet (the pattern has too many service-specific knobs to generalize cleanly), but make it a checklist any new such flow must follow.

### M7. Operator audit writing is duplicated across admin / ops methods

**Where:** LLD §03-services/23-admin-ops.

**Issue:** Every admin method has the same boilerplate: validate operator, authorize, snapshot pre-state, delegate, audit. Risk of one method skipping a step.

**Decision:** Don't extract a generic wrapper (each method's argument shape differs and reflective wrapping makes errors worse). Instead, ship a **lint check**: every method on `admin.Service` (defined in `service.go`) must call `s.audit.Emit(...)` with `Action` containing the prefix `admin.`. Implemented as a go vet analyzer, optional initially.

**Action item:** create issue.

### M8. The "system role" idea is referenced in multiple LLDs but not defined in one place

**Where:** LLDs §03-services/13, /14, /16, /17 — phrases like "GetSystem (BYPASSRLS)" or "calls into system-role pool."

**Issue:** Junior implementer doesn't know which calls run as which role.

**Decision:** Add a section to §02-infrastructure/01-database-access enumerating the three roles (`pikshipp_app`, `pikshipp_reports`, `pikshipp_admin`) and the rule for choosing one. Add a `db.WithSystem(ctx, fn)` helper that runs `fn` with the admin pool (BYPASSRLS, audited).

**Action item:** add to §02-infrastructure/01-database-access.

### M9. Each service LLD has its own "Failure Modes" table with overlapping content

**Where:** All service LLDs.

**Issue:** Maintainable per service, but hard for an on-call to scan across services for "what does ErrCarrierUnavailable mean here?". Duplication is acceptable; the alternative (one giant table) is worse.

**Decision:** Keep per-service tables. Add a top-level `docs/runbooks/error-classes.md` that lists the canonical error classes (the ones from §03-services/12-carriers-framework) and what response strategy each one implies.

### M10. Test helpers (`slt.NewSeller`, `slt.SandboxCarrier`, etc.) referenced before defined

**Where:** Many SLT examples.

**Issue:** The `slt` package is referenced in every LLD's testing section, but nowhere is its full surface enumerated.

**Decision:** Add `internal/slt/README.md` cataloguing the helpers and their semantics. Reference from §05-cross-cutting/01-testing-patterns.

### M11. Naming convention drift

**Where:** Across LLDs.

**Issue:** Some LLDs use `core.OrderID`, some use `core.OrderIDFromUUID`, some use `id.UUID()`. The transitions (typed-id ↔ uuid.UUID) are inconsistently named.

**Decision:** Standardize on:
- `core.NewXID()` returns `core.XID` (typed).
- `core.XIDFromUUID(uuid.UUID) core.XID` for inbound from sqlc rows.
- `core.XID.UUID() uuid.UUID` for outbound to sqlc params.
- `core.XID.String() string` for logs / error messages.

Add to §01-core/02-ids.

### M12. The "policy engine" is hot-path-critical but its API is `GetInt`/`GetFloat`/`GetString`

**Where:** LLD §03-services/01-policy-engine.

**Issue:** Stringly-typed keys are easy to typo and impossible to refactor across files.

**Decision:** Code-define a `PolicyKey` const for every policy key referenced in any service. The engine accepts `PolicyKey`, not `string`. Compile-time checked.

**Action item:** add to §03-services/01-policy-engine; central registry in `internal/policy/keys.go`.

---

## Doc Updates Applied

The following changes were made to the LLDs based on this review. Each is small enough that we apply directly rather than open separate PRs:

1. **§02-infrastructure/01-database-access** — note that api role and worker role use separate `pgxpool.Pool` instances; document `WithSystem` helper. Define `app.current_seller_id()` SQL function. (S3, M4, M8)
2. **§00-conventions/01-go-conventions** — add rules: `InTx` method convention (M1), sentinel error rule (M2), per-iteration timeout in worker loops (S9), preallocate slices (S7), positional sqlc args (S10), two-phase external-call pattern (M6).
3. **§03-services/01-policy-engine** — `PolicyKey` typed constants, singleflight on cold load. (S6, M12)
4. **§03-services/14-tracking** — document `IngestBatch(events)` as the preferred entrypoint. (S5)
5. **§01-core/05-types** — add note on planned `internal/core/cache.TTLCache[K, V]`. (M5)
6. **§01-core/01-money** — add note on `AppendFormat([]byte) []byte` for hot-path use. (S1)
7. **§03-services/03-outbox** — document `make outbox-snapshot` and version-bump convention. (M3)
8. **§05-cross-cutting/01-testing-patterns** — reference `internal/slt/README.md` (to be created during implementation). (M10)

These updates are inlined in the relevant LLDs in the same commit as this review. The action items not yet done (lint analyzer M7, replicas-tier benchmark S2, generic `TTLCache` M5, PolicyKey constants M12) become first-week implementation tasks.

---

## Acknowledged Tech Debt for v0

These are known shortcuts we've made deliberately. They are listed here so we don't forget:

| # | Description | Rationale | Trigger to revisit |
|---|---|---|---|
| 1 | Env var secrets | Simplicity for friendly-seller phase | When > 5 sellers OR first compliance audit |
| 2 | Single-region | Cost / complexity | When latency complaints from non-IN-South traffic |
| 3 | No read replica | Below the threshold | When reports queries > 10% of primary CPU |
| 4 | No structured dispute UX (recon, COD) | Operator-managed via support tickets | When dispute volume > 50/week |
| 5 | Per-row CSV INSERT (vs COPY) | Acceptable below 10k rows | When typical import > 50k rows |
| 6 | No webhook DLR ingestion (SMS) | Vendor ack is good enough at v0 | When SMS deliverability complaints arise |
| 7 | Wallet has no negative balance | Some debits may stall | When > 10 stalls/week or RTO arrears pile up |
| 8 | Risk service is rule-based heuristics | We don't have data for ML yet | After 3 months of data |
| 9 | Sandbox carrier toggleable in non-prod only | Production guard exists | n/a |
| 10 | No per-seller-domain DKIM | Single-sender SES | When > 3 sellers ask for branded sends |
| 11 | No bulk admin operations | Scripts via repl | When ops uses scripts > 1×/week |
| 12 | Approval workflow for high-impact admin actions absent | Audit-only at v0 | When audit reveals operator error |

## Sign-Off

- **Sandeep:** "Performance acceptable for stated v0 scale. Document the regions of risk (S1, S5, S8) so future-us doesn't get surprised. With the action items applied, ship."
- **Maya:** "Patterns are consistent enough to scale. The `InTx` discipline (M1) and the `PolicyKey` constants (M12) are the highest-leverage cleanups. With the action items applied, ship."

Both reviewers approve v0 LLD.

## Next Steps

1. Apply the small inline doc updates listed above.
2. Open issues for the longer-running action items (generic cache, lint analyzer, PolicyKey constants).
3. Begin implementation in dependency order per §README.

## References

- HLD §05-decisions/: ADRs that govern these designs.
- LLD §00-conventions/: where the conventions tightened by this review live.
- LLD §05-cross-cutting/04-runbook-template: ops-side companion to these specs.
