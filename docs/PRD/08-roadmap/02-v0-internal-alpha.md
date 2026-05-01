# v0 — Internal alpha

> Goal: prove the architecture and the core happy path with internal team + 5–10 friendly sellers. Not for revenue. Not for press.

## Definition of done for v0

A friendly seller can:
1. Sign up, complete KYC, and receive sandbox approval.
2. Add a pickup address.
3. Connect Shopify (or use manual entry).
4. Recharge wallet via Razorpay UPI.
5. Book a real shipment via Delhivery (or 1 other carrier).
6. Print a label.
7. Receive tracking events; see normalized status.
8. Buyer receives a tracking link (SMS or email; WhatsApp deferred).
9. Mark the shipment as delivered.
10. See the wallet ledger reflecting the charge.

Internal staff can:
1. Operate the admin console (read-only is OK for v0).
2. Approve KYC manually (auto-KYC stub).
3. Investigate a stuck shipment.
4. View audit log.

We do not yet have:
- Allocation engine multi-objective scoring (single-carrier picking is fine).
- NDR action loop (we observe NDRs but don't auto-handle).
- COD remittance (no COD shipments at v0).
- Weight reconciliation (manual ops if it happens).
- Returns / RTO automation.
- Risk / fraud signals.
- Contract feature (manual contract handling).

## Foundations (must-have v0)

| Area | Scope at v0 |
|---|---|
| Seller scoping & policy engine | Full shape implemented; few keys exercised |
| Identity & onboarding | Manual KYC OK; auto-KYC stub |
| Canonical data model | Full shape |
| Channel adapter framework | Shopify + manual + CSV |
| Carrier adapter framework | Delhivery + 1 other |
| Tracking + status normalization | Webhook + polling |
| Wallet | Double-entry ledger; recharge; debit on book |
| Notifications | SMS + email; WhatsApp deferred |
| Buyer tracking page | Minimal, neutral default branding |
| Admin console | Basic CRUD + investigation |
| Observability | Logs + metrics + alerting basics |
| Audit | Append-only events from every feature |
| Security | Seller scoping at data layer; secrets in vault |

## Explicit non-goals at v0

- Full allocation engine (single-carrier per seller is fine).
- COD verification & remittance flows.
- Weight reconciliation auto-flow.
- Insurance.
- Auto-KYC (manual is OK).
- Bulk operations beyond CSV import.
- Reports beyond a single shipment-summary page.
- Public API.
- Risk & fraud signals beyond manual ops review.
- Contract storage beyond shared drive.

## v0 success metrics

- 5 friendly sellers shipped at least 10 shipments each.
- 0 ledger inconsistencies.
- 0 cross-seller data exposure incidents.
- < 24h for an internal stuck-shipment to be resolved.
- All v0 foundations passing chaos tests (carrier API outage simulated; KYC vendor down simulated).

## Risks at v0

- Over-scoping. The list above is already large. Cut harder if needed.
- Eng building features instead of foundations. Discipline: "is this on the foundation list?"
- Skipping the canonical model in early commits. Big mistake.
- Skipping the policy engine. Even bigger mistake — refactoring out hard-coded `if plan == X` later is painful.
- Audit gap from missed instrumentation. Run coverage tests in CI.
