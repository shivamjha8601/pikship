# Audit & change-log

> Cross-cutting. Every state change, every config override, every privileged operation, every cross-seller access is audit-logged. Tamper-evident. Per-seller exportable. This is the document that makes "audit everything" a reality, not a slogan.

## Why this is its own document

"Audit" is mentioned in many feature docs (KYC, wallet, ops console, policy engine, contracts). Without a single canonical document, each feature implements its own audit shape and they don't compose. With one, every feature emits to the same backbone, and we get queryable, complete history with no gaps.

## Goals

- **100% coverage** of privileged actions and state changes.
- **Tamper-evident** for high-value events (financial, KYC decisions, policy overrides).
- **Per-seller exportable** for DPDP rights, due diligence, ad-hoc forensics.
- **Searchable** across all dimensions (actor, action, target, time).
- **Actionable** — feeds into risk/fraud monitoring of internal anomalies.

## Non-goals

- Application logs (errors, warnings) — separate system.
- Performance tracing — observability stack handles.
- BI / analytics dashboards — separate system.

## What gets audited

### Always audited

| Domain | Events |
|---|---|
| **Identity** | Login, logout, MFA enrollment, password change, role grant/revoke |
| **Seller lifecycle** | Created, KYC decision, suspension, reactivation, wind-down, archive |
| **Policy engine** | Override set/removed, lock set/removed, seller-type defaults changed |
| **Wallet** | Every ledger entry, every recharge, every refund, every manual adjustment |
| **KYC** | Doc upload, auto-decision, manual decision, override |
| **Carriers** | Adapter config change, credential rotation, status change (active/disabled) |
| **Contracts** | Created, signed, amended, terminated, expired |
| **Ops actions** | Manual ledger adjustment, KYC override, seller suspension, view-as access, impersonation |
| **Cross-seller access** | Pikshipp staff viewing/modifying any seller data |
| **Security events** | Failed auth, rate limit hit, suspicious access pattern |
| **Allocation decisions** | Every allocation decision (already its own first-class object; also emits audit) |

### Selectively audited (sensitive)

| Domain | Events |
|---|---|
| Reads of sensitive policy keys (credit_limit, KYC depth) | Yes (read-audit) |
| Reads of KYC documents | Yes |
| Reads of signed contracts | Yes |

### Not audited (too noisy)

- Reads of public/derived data (tracking events, public order summary).
- Application traces (handled by observability).
- Read-only feature usage (which page a seller visited).

## The audit event shape

```yaml
audit_event:
  id: aud_xxx
  
  scope:
    seller_id              # the seller this event concerns
    sub_seller_id          # optional
  
  actor:
    kind: seller_user | pikshipp_admin | pikshipp_ops | pikshipp_support |
          system | scheduled_job | api_key | external_webhook
    user_id
    api_key_id
    impersonated_by         # if action via impersonation
  
  action:
    domain: identity | seller | policy | wallet | kyc | carrier | 
            contract | ops | allocation | security
    type: created | updated | deleted | approved | rejected | overridden |
          locked | suspended | viewed | exported | impersonated
    name: human readable     # e.g., "wallet.adjust_balance"
  
  target:
    kind: seller | order | shipment | wallet_account | ledger_entry |
          policy_setting | contract | document | carrier_config
    ref
  
  payload:
    before                   # snapshot before change
    after                    # snapshot after change
    diff                     # computed diff
    reason                   # why this happened (required for ops actions)
    metadata                 # action-specific
  
  context:
    ip_address
    user_agent
    request_id
    session_id
    geo_approx
  
  occurred_at
  recorded_at
  
  tamper:
    chain_hash               # hash linking to previous event in this seller's chain
    event_hash               # hash of this event content
```

## Tamper evidence

For high-value events (financial, KYC decisions, policy locks, ops manual actions, impersonation):

- Each event's hash is included in the next event's `chain_hash`.
- The hash chain is verifiable (recompute and compare).
- Periodic hash-chain checkpoints are signed and stored externally.
- Any tampering is detectable on verification.

For lower-stakes events (e.g., login), tamper-evidence is optional (cost-benefit).

## Read paths

### Per-seller audit log

In the seller's dashboard: every action affecting their account, including Pikshipp staff actions.

- Filterable by domain, actor kind, time range.
- Exportable (CSV, JSON).
- DPDP-ready (seller can request their full history).

### Pikshipp ops search

In the admin console: search across all sellers.

- Powerful filters.
- Privacy: PII access itself is audited.

### Tamper verification

Tooling to recompute hash chains for any seller's audit log.

- Run periodically.
- Run on demand for legal / forensic.
- Failure raises P0 alert.

## Write paths

Every domain emits via a common audit client:

```pseudo
audit.emit(
  scope=seller_ctx,
  actor=request_actor,
  action="wallet.adjust_balance",
  target={kind: "wallet_account", ref: wa_id},
  payload={before: {bal: 1000}, after: {bal: 1500}, reason: "Promotional credit"},
)
```

The client:
- Validates required fields.
- Captures context automatically.
- Computes hashes.
- Persists asynchronously (with synchronous validation).
- On failure of audit emit: action MAY be allowed to proceed (financial-grade events: NO; lower-stakes: yes with alert).

## Retention

| Category | Retention |
|---|---|
| Financial events | 7 years (legal) |
| KYC events | Active + 7 years post-wind-down |
| Policy overrides & locks | Active + 7 years |
| Ops actions | 7 years |
| Login / security | 1 year hot, 5 years cold |
| Other | 1 year hot, 3 years cold |

## DPDP rights integration

Sellers can:
- Request their full audit log (machine-readable export).
- Request deletion (subject to legal retention overrides — financial records cannot be deleted but can be marked).

Pikshipp staff PII in audit (e.g., who viewed) is retained as part of the chain; redacted on export.

## Use cases driven by audit

### Compliance / investigation
- Regulator requests history → export.
- Seller disputes a charge → full ledger + ops actions.
- Carrier disputes our records → tamper-evident chain proves authenticity.

### Internal risk (Feature 26)
- Anomaly detection on Pikshipp staff actions (one ops member with disproportionate adjustments).
- Suspicious patterns (impersonations without ticket linkage).

### Product
- Per-seller "you've changed this setting 3 times this month — want help?".
- Allocation transparency (already covered by allocation engine; audit cross-references).

## Implementation considerations (PRD-scope notes)

- **Append-only** storage — never UPDATE or DELETE on audit table.
- **High write throughput** — sized for peak (every shipment book = 5–10 audit events).
- **Schema evolution** — additive only; never remove fields without migration.
- **Privacy** — PII fields tagged so DPDP export can redact actor PII when sending to seller.

## Open questions

- **Q-AU1** — Should we open audit-stream subscription as a paid feature (sellers consume their audit in real-time)? Default: v3 consideration.
- **Q-AU2** — Cross-seller anonymous patterns (e.g., "ops member X has high adjustment count"): exposed to which roles? Default: Admin only.
- **Q-AU3** — Storage choice for audit (immutable cloud storage like S3 + Glacier vs append-only DB)? PRD-scope: prefer immutable storage; HLD details.
- **Q-AU4** — What's "high-value" for tamper-evidence? Default: any event affecting money, identity, KYC, ops, contract.
- **Q-AU5** — External anchor (timestamping with a public service like OpenTimestamps)? Default: v2 if compliance demands.

## Dependencies

- Every feature emits to audit. The audit feature is *consumed by* most others; it depends only on a storage primitive.

## Risks

| Risk | Mitigation |
|---|---|
| Audit volume overwhelms storage | Tiered storage; compression; archival policy |
| Audit gap from missed instrumentation | Coverage tests in CI; periodic audit-of-audit |
| Audit-emit failure silently breaks events | Synchronous validation + async persist; alerts on persist failure |
| Tamper from inside (someone with DB access) | Immutable storage; external checkpoint anchors; quarterly external verification |
| Privacy violation (PII over-exposure) | Automated PII tagging + redaction in exports |
