# PIKSHIPP — Product Requirements Document

> **A multi-courier shipping aggregator for India.**
>
> Sellers — D2C brands, marketplace sellers, SMB, and mid-market — use Pikshipp to manage, compare, book, track, and reconcile shipments across every Indian courier from a single dashboard. Pikshipp itself is the operator and brand; sellers are our customers; their buyers are recipients.

---

## How this PRD is organized

This is **not** a single document. The platform is too large to specify in one file without losing fidelity. The PRD is split into layers, in increasing depth:

| Layer | Purpose | Audience |
|---|---|---|
| **Strategy** | Why this product exists, who it serves, how it makes money | Founders, exec sponsors, investors |
| **Architecture** | The product's shape — domains, configurability, data scoping | All eng & product, before any feature work |
| **Features** | Per-feature problem, requirements, flows, industry options, edge cases | Product managers, engineers, designers |
| **Flows** | End-to-end journeys cutting across many features | Anyone reasoning about real-world scenarios |

Two orthogonal documents sit alongside the layers:

- **Cross-cutting concerns** — security, audit, performance, observability — apply to every feature.
- **Roadmap & phasing** — re-cuts the feature catalogue into v0 / v1 / v2 / v3 from a Product Owner's perspective. **The feature docs are written for completeness; the roadmap is written for shipping.**

> **Two reading orders.**
> *To understand what we're building:* read `00 → 01 → 02 → 03` in order, then sample features.
> *To plan the build:* read `00 → 03 → 08-roadmap`, then dive into v0/v1 features only.

---

## Table of contents

### 00 — Executive summary
- [00-executive-summary.md](./00-executive-summary.md) — one-page TL;DR

### 01 — Vision & strategy
- [01 / Vision & mission](./01-vision-and-strategy/01-vision-and-mission.md)
- [02 / Market & competitive landscape](./01-vision-and-strategy/02-market-and-competitive-landscape.md)
- [03 / Business model](./01-vision-and-strategy/03-business-model.md)
- [04 / Success metrics](./01-vision-and-strategy/04-success-metrics.md)

### 02 — Users & personas
- [01 / Personas](./02-users-and-personas/01-personas.md)
- [02 / Jobs-to-be-done](./02-users-and-personas/02-jobs-to-be-done.md)
- [03 / User journeys](./02-users-and-personas/03-user-journeys.md)

### 03 — Product architecture (logical, not technical)
- [01 / System overview](./03-product-architecture/01-system-overview.md)
- [02 / Seller configuration & data scoping](./03-product-architecture/02-multi-tenancy-model.md)
- [03 / Domain model](./03-product-architecture/03-domain-model.md)
- [04 / Canonical data model](./03-product-architecture/04-canonical-data-model.md)
- [05 / Policy engine](./03-product-architecture/05-policy-engine.md)

### 04 — Features (the completeness catalogue)

> Every feature doc follows the same template: **Problem → Goals/Non-goals → Industry patterns → Functional requirements → User stories → Flows → Configuration axes → Data model → Edge cases → Open questions → Dependencies → Risks**.

| # | Feature | Status (v1?) |
|---|---|---|
| 01 | [Identity & onboarding](./04-features/01-identity-and-onboarding.md) | ✅ v1 |
| 02 | [Seller organization & configuration](./04-features/02-tenant-and-organization.md) | ✅ v1 |
| 03 | [Channel integrations](./04-features/03-channel-integrations.md) | ✅ v1 |
| 04 | [Order management](./04-features/04-order-management.md) | ✅ v1 |
| 05 | [Catalog & warehouse](./04-features/05-catalog-and-warehouse.md) | ✅ v1 |
| 06 | [Courier network](./04-features/06-courier-network.md) | ✅ v1 |
| 07 | [Pricing engine](./04-features/07-rate-engine.md) | ✅ v1 (subsystem of 25) |
| 08 | [Shipment booking & manifests](./04-features/08-shipment-booking.md) | ✅ v1 |
| 09 | [Tracking & status normalization](./04-features/09-tracking.md) | ✅ v1 |
| 10 | [NDR management](./04-features/10-ndr-management.md) | ✅ v1 |
| 11 | [Returns & RTO](./04-features/11-returns-and-rto.md) | ✅ v1 |
| 12 | [COD management & remittance](./04-features/12-cod-management.md) | ✅ v1 |
| 13 | [Wallet, billing, invoicing](./04-features/13-wallet-and-billing.md) | ✅ v1 |
| 14 | [Weight reconciliation](./04-features/14-weight-reconciliation.md) | ✅ v1 |
| 15 | [Reports & analytics](./04-features/15-reports-and-analytics.md) | ✅ v1 |
| 16 | [Notifications](./04-features/16-notifications.md) | ✅ v1 |
| 17 | [Buyer experience (incl. seller branding)](./04-features/17-buyer-experience.md) | ✅ v1 |
| 18 | [Support & ticketing](./04-features/18-support-and-tickets.md) | ✅ v1 |
| 19 | [Admin & ops console](./04-features/19-admin-and-ops.md) | ✅ v1 |
| 20 | — *(removed; was white-label — see [`09-appendix/04-deferred-features.md`](./09-appendix/04-deferred-features.md))* | ❌ |
| 21 | [Public API & webhooks](./04-features/21-public-api-and-webhooks.md) | ⏳ v2 |
| 22 | [Shipment insurance](./04-features/22-insurance.md) | ⚠️ v1 (optional, partner-led) |
| 23 | [Hyperlocal & same-day](./04-features/23-hyperlocal-and-same-day.md) | ⏳ v3 |
| 24 | [B2B / heavy / freight](./04-features/24-b2b-shipping.md) | ⏳ v3 |
| 25 | [Allocation engine](./04-features/25-allocation-engine.md) | ✅ v1 |
| 26 | [Risk & fraud](./04-features/26-risk-and-fraud.md) | ⚠️ v1 minimal; v2/v3 deepens |
| 27 | [Contracts & documents](./04-features/27-contracts-and-documents.md) | ⚠️ v1 minimal; v2 productized |

### 05 — Cross-cutting concerns
- [01 / Security & compliance](./05-cross-cutting/01-security-and-compliance.md)
- [02 / Internationalization](./05-cross-cutting/02-internationalization.md)
- [03 / Accessibility](./05-cross-cutting/03-accessibility.md)
- [04 / Performance & reliability](./05-cross-cutting/04-performance-and-reliability.md)
- [05 / Observability](./05-cross-cutting/05-observability.md)
- [06 / Audit & change-log](./05-cross-cutting/06-audit-and-change-log.md)

### 06 — End-to-end flows
- [01 / Seller onboarding](./06-flows/01-seller-onboarding-flow.md)
- [02 / Order-to-delivery](./06-flows/02-order-to-delivery-flow.md)
- [03 / NDR action loop](./06-flows/03-ndr-flow.md)
- [04 / COD remittance](./06-flows/04-cod-remittance-flow.md)
- [05 / RTO handling](./06-flows/05-rto-flow.md)
- [06 / Weight dispute](./06-flows/06-weight-dispute-flow.md)

### 07 — Integration framework
- [01 / Channel adapter framework](./07-integrations/01-channel-adapter-framework.md)
- [02 / Courier adapter framework](./07-integrations/02-courier-adapter-framework.md)
- [03 / Payment gateways](./07-integrations/03-payment-gateways.md)
- [04 / Communication providers](./07-integrations/04-communication-providers.md)
- [05 / KYC & verification](./07-integrations/05-kyc-and-verification.md)

### 08 — Roadmap & phasing
- [01 / Phasing strategy](./08-roadmap/01-phasing-strategy.md)
- [02 / v0 — Internal alpha](./08-roadmap/02-v0-internal-alpha.md)
- [03 / v1 — Public launch](./08-roadmap/03-v1-public-launch.md)
- [04 / v2 — Scale & channel breadth](./08-roadmap/04-v2-scale.md)
- [05 / v3 — Adjacencies & advanced](./08-roadmap/05-v3-platform.md)

### 09 — Appendix
- [01 / Glossary](./09-appendix/01-glossary.md)
- [02 / Open questions register](./09-appendix/02-open-questions.md)
- [03 / References](./09-appendix/03-references.md)
- [04 / Deferred features](./09-appendix/04-deferred-features.md)

---

## Diagrams

All diagrams live in [`./diagrams/`](./diagrams/) as both `.mmd` (Mermaid source) and `.png` (rendered at scale 4).

To regenerate any diagram after editing the source:

```bash
mmdc -i docs/PRD/diagrams/<name>.mmd -o docs/PRD/diagrams/<name>.png --scale 4
```

To regenerate all diagrams:

```bash
for f in docs/PRD/diagrams/*.mmd; do
  mmdc -i "$f" -o "${f%.mmd}.png" --scale 4
done
```

Markdown files reference the rendered PNG; the `.mmd` source is the editable artifact.

---

## Conventions

- **MUST / SHOULD / MAY** — RFC 2119 keywords. *Must* is non-negotiable for the version it appears in; *should* is strong default; *may* is optional.
- **Industry patterns** — every feature lists how *Shiprocket, Shipway, NimbusPost, ClickPost, iThink Logistics, Easyship (intl)* solve the same problem, plus build/buy options.
- **Configurability is a first-class principle.** Almost every behavioral default in this PRD is *configurable per seller*. Plans are pre-bundled config vectors. The runtime resolution rules live in [`03-product-architecture/05-policy-engine.md`](./03-product-architecture/05-policy-engine.md).
- **Audit-everything is a first-class principle.** Every state change, every config override, every ops action, every cross-tenant access is audit-logged. See [`05-cross-cutting/06-audit-and-change-log.md`](./05-cross-cutting/06-audit-and-change-log.md).
- **Open questions** are tagged with stable IDs (Q-O1, Q-T5, etc.), captured per feature, and rolled up in [`09-appendix/02-open-questions.md`](./09-appendix/02-open-questions.md).

## Vocabulary used in this PRD

| Term | Meaning |
|---|---|
| **Pikshipp** | Us — the platform operator and brand. |
| **Seller** | Our customer — a business that ships goods to buyers via Pikshipp. |
| **Sub-seller** | A child unit of a seller (branch / subsidiary), inheriting most config from the parent. |
| **Buyer** | The recipient of a shipment. Not an account holder. |
| **Carrier / Courier** | Third-party (or, in v3+, first-party) shipping provider integrated via an adapter. |
| **Channel** | A connection between a seller and a sales platform (Shopify, Amazon, Meesho, etc.). |
| **Policy engine** | The runtime system that resolves "what's the rule for *this* seller × *this* setting?". |
| **Allocation engine** | The system that picks which carrier/service to use for a given shipment. |

There is **no reseller / white-label / multi-tenant-aggregator concept** in this PRD. Pikshipp is the only aggregator. Sellers' data is scoped per-seller (standard SaaS data isolation), not under a "tenant tree".

---

## Status

| Section | Status |
|---|---|
| Index | ✅ Complete (v1.0) |
| Executive summary | ✅ v1.0 |
| Strategy (4 docs) | ✅ v1.0 |
| Users & personas (3 docs) | ✅ v1.0 |
| Architecture (5 docs incl. policy engine) | ✅ v1.0 |
| Features — completeness catalogue (26 active docs) | ✅ v1.0 |
| Cross-cutting (6 docs incl. audit) | ✅ v1.0 |
| Flows (6 docs) | ✅ v1.0 |
| Integration framework (5 docs) | ✅ v1.0 |
| Roadmap & phasing (5 docs) | ✅ v1.0 |
| Appendix (4 docs incl. deferred features) | ✅ v1.0 |
| Diagrams | ~20 landmark `.mmd` + rendered `.png` |

**PRD version: 1.0** — first complete polished draft. All v1 product decisions locked. Open questions remaining are listed in `09-appendix/02-open-questions.md`. This is the document HLD/HLA, then implementation LLDs, will derive from.

Last updated: 2026-04-30.
