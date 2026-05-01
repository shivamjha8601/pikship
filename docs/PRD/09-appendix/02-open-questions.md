# Open questions register

> Decisions still pending. Each is owned, tagged, and tracks a target version. As decisions land, they move to the **Closed** section with a one-line outcome.

## Format

**Q-ID** — short question — owner — target — current default (if any)

---

## OPEN

### Identity & onboarding (Feature 01)
- **Q-O1** — Tiered KYC volume cap: cumulative GMV vs shipment count? Owner: Risk PM. Target: pre-v1.
- **Q-O2** — Aadhaar storage: hashed-only vs masked image? Owner: Legal. Target: pre-v1.
- **Q-O3** — KYC document retention period (90d post-rejection / indefinite post-approval / 7y DPDP-aligned). Owner: Legal.
- **Q-O4** — When auto-tier-upgrade triggers, does shipping pause until upgrade completes? Default: no — soft-flag with grace.

### Seller org & config (Feature 02)
- **Q-T1** — Sub-seller wallet: parent-shared (default v1) vs independent (v2 option). Owner: Product.
- **Q-T2** — Cross-seller user (one user serving multiple sellers, e.g., a CA): not v1; v2+.
- **Q-T3** — Bulk plan upgrade tooling for ops: nice-to-have v2.

### Channels (Feature 03)
- **Q-C1** — Amazon redacted PII ingestion: ingest with flag (default).
- **Q-C2** — Channel migration tool: not v1.
- **Q-C3** — Inventory sync to channels: not v1.
- **Q-C4** — Backfill aggressiveness on connect: 30 days default; up to 180.

### Order management (Feature 04)
- **Q-OM1** — Custom seller order states: tags only.
- **Q-OM2** — Auto-manifest on threshold: on-demand v1.
- **Q-OM3** — Rule-based auto-cancellation: no.

### Catalog / warehouse (Feature 05)
- **Q-CW1** — Geo-fenced pickup routing: v2.
- **Q-CW2** — Pooled / fulfillment-as-a-service: v3+.
- **Q-CW3** — Channel catalog sync: on-demand v2.

### Carrier network (Feature 06)
- **Q-CN1** — Serviceability replication: top carriers replicated; tail on-demand.
- **Q-CN2** — Carrier ranking weights: tunable.
- **Q-CN3** — SLA on carrier outage: bias-and-badge; no refunds for carrier-side.

### Pricing engine (Feature 07)
- **Q-RT1** — Reliability affecting price vs only recommendation: only recommendation.
- **Q-RT2** — "vs Shiprocket" savings displayed: marketing-only.
- **Q-RT3** — Rate display during carrier outage: show with badge.
- **Q-RT4** — Rate quote vs carrier book reconciliation: tighter spec needed.

### Booking (Feature 08)
- **Q-BK1** — Scheduled bookings: v2.
- **Q-BK2** — Bulk-book transactional: best-effort.
- **Q-BK3** — Server- vs client-side label rendering: server v1; client templates v2.

### Tracking (Feature 09)
- **Q-TR1** — Buyer page event detail: stepper + last 5.
- **Q-TR2** — Tracking event raw payload retention: 90 days.
- **Q-TR3** — Outbound webhook delivery semantics (when API launches v2): at-least-once.
- **Q-TR4** — Buyer feedback collection vs pass to seller: ours.

### NDR (Feature 10)
- **Q-NDR1** — Notify seller on auto-rule fire: only material.
- **Q-NDR2** — Slot vocabulary mapping per carrier: maintain mapping table.
- **Q-NDR3** — Cross-seller outreach copy A/B: tenant-private.
- **Q-NDR4** — Buyer responds post-RTO: terminal.

### Returns / RTO (Feature 11)
- **Q-RTN1** — No-RTO insurance product: v2.
- **Q-RTN2** — Pre-paid return labels: v2.
- **Q-RTN3** — Open-box-on-delivery window: per carrier.
- **Q-RTN4** — RTO with no pickup address: ops escalation.

### COD (Feature 12)
- **Q-COD1** — Same-day remittance for premium: v3.
- **Q-COD2** — Cross-seller hashed buyer for risk: v2; legal review.
- **Q-COD3** — Buyer late-cancel race: best-effort cancel.
- **Q-COD4** — COD verification opt-in vs default-on: configured per seller-type.

### Wallet & billing (Feature 13)
- **Q-WB1** — GST on COD handling: separate line.
- **Q-WB2** — Auto-recharge cap: ₹10k per trigger; 3/day. Tune.
- **Q-WB3** — TDS reports: yes.
- **Q-WB4** — Float interest / investment: no v1; legal v2.
- **Q-WB5** — Reverse-leg charge timing (RTO event vs delivery): at delivery.
- **Q-WB6** — Grace cap value: per seller-type via policy engine.

### Weight reconciliation (Feature 14)
- **Q-WR1** — Win-fee on recovered amounts: no v1.
- **Q-WR2** — Auto-dispute aggressiveness: tunable per carrier.
- **Q-WR3** — Image-comparison ML: vendor v2.
- **Q-WR4** — Publish carrier-level discrepancy stats: internal only.

### Reports (Feature 15)
- **Q-RP1** — Pikshipp carrier rankings to sellers: aggregated only with per-seller-zone scores.
- **Q-RP2** — AI narrative summaries: v2.
- **Q-RP3** — Real-time vs daily refresh: today real-time, historical pre-aggregated.

### Notifications (Feature 16)
- **Q-NT1** — 2-way WhatsApp: v2.
- **Q-NT2** — Per-seller WhatsApp business sender (feature flag): opt-in for `mid_market`+.
- **Q-NT3** — Vendor rotation: latency + delivery-rate based.
- **Q-NT4** — DLT registration tooling for hundreds of templates × sellers: ops-managed; tooling v2.

### Buyer experience (Feature 17)
- **Q-BX1** — Buyer-side delivery photo upload: v2.
- **Q-BX2** — Buyer chatbot: v2.
- **Q-BX3** — Buyer review syndication: v3.

### Support (Feature 18)
- **Q-SP1** — Ticketing in-house vs Freshdesk/Zendesk: integrate v1.
- **Q-SP2** — AI KB summarization: v2.
- **Q-SP3** — Agent assist with AI: v2.

### Admin & ops (Feature 19)
- **Q-AO1** — One console for all roles vs separate apps: one codebase, role-driven.
- **Q-AO2** — RBAC vs ABAC: RBAC v1; ABAC v2.
- **Q-AO3** — Self-serve runbook authoring: eng-curated v1.

### Public API (Feature 21)
- **Q-API1** — GraphQL public API: no v1.
- **Q-API2** — Webhook payload shape: compact event.
- **Q-API3** — Sandbox COD timeline simulation: yes.
- **Q-API4** — Public bug bounty: v2.

### Insurance (Feature 22)
- **Q-INS1** — Self-insure low-value high-volume: no v1.
- **Q-INS2** — Buyer-side claim portal: no.
- **Q-INS3** — Insurance for RTO leg: per-policy.

### Hyperlocal (Feature 23) [v3]
- **Q-HL1** — Integrated rate engine vs separate "Quick" tab: integrated.
- **Q-HL2** — Live driver phone exposure: proxy.
- **Q-HL3** — Multi-stop in one booking: not v3.

### B2B (Feature 24) [v3]
- **Q-B2B1** — In-house B2B rate engine: passthrough v3 launch; cards in v3+.
- **Q-B2B2** — Door-to-door insurance: per-carrier.
- **Q-B2B3** — Truck-loading photo capture: v3+.

### Allocation engine (Feature 25)
- **Q-AL1** — Real-time carrier bidding (dynamic pricing): static v1; dynamic v3+.
- **Q-AL2** — Reliability scores exposed to sellers: aggregated v1; per-zone v2.
- **Q-AL3** — Multi-shipment optimization (split a multi-pkg order across carriers): no v1.
- **Q-AL4** — ML-driven scoring as v2 enhancement: which signal first?

### Risk & fraud (Feature 26)
- **Q-RF1** — How conservative should v1 thresholds be? Very conservative; weekly tuning.
- **Q-RF2** — Cross-seller hashing key custody. Owner: Security.
- **Q-RF3** — Risk vs allocation interaction: high-risk → more reliable carrier in v2.
- **Q-RF4** — Notify customer when flagged: silent advisory default.
- **Q-RF5** — Vendor augmentation timing for sanctions/PEP: in-house v1; vendor v2.

### Contracts & documents (Feature 27)
- **Q-CT1** — Single e-sign vendor or multi: single v1; multi v2.
- **Q-CT2** — Click-wrap vs e-sign for plan T&C: click-wrap for Free/Grow.
- **Q-CT3** — Contract template versioning: new template + grandfather existing.
- **Q-CT4** — Vendor contracts in same system: yes; access-controlled.
- **Q-CT5** — Time-bounded vs perpetual term mapping: per-term effective range supported.

### Architecture
- **Q-A1** — Reconciliation context: one or three? Eng decision.
- **Q-A2** — Catalog context required at v1: optional via inline weight.
- **Q-A3** — Allocation calls pricing (default) or vice versa.
- **Q-A4** — Channel adapter coupling to OMS schema. Eng.

### Cross-cutting
- **Q-SC1** — RBI PPI applicability — wallet model classification. Owner: Legal.
- **Q-SC2** — Insurance intermediary model — referrer vs corporate agent. Owner: Legal.
- **Q-SC3** — DPO appointment for DPDP. Owner: Legal.
- **Q-SC4** — Bug bounty timing: v2.
- **Q-SC5** — SOC 2 target: end of year 2.
- **Q-I18N1** — Per-channel locale heuristic: auto-detect with confidence threshold.
- **Q-I18N2** — Carrier label language: English.
- **Q-I18N3** — RTL support: defer to v3.
- **Q-A11Y1** — Voice navigation for buyers: v3.
- **Q-A11Y2** — 508 / govt compliance: defer.
- **Q-PR1** — Multi-region timing: v2 if needed.
- **Q-PR2** — Tighter RPO/RTO: depends on enterprise commitments.
- **Q-PR3** — Real-time vs batch tracking: real-time.
- **Q-OB1** — Self-hosted vs vendor: vendor v1.
- **Q-OB2** — Tenant-scoped Grafana exposure: curated dashboards.
- **Q-OB3** — PII in traces: redacted.
- **Q-AU1** — Audit-stream subscription as paid feature: v3 consideration.
- **Q-AU2** — Cross-seller anonymous patterns visibility: Admin only.
- **Q-AU3** — Storage choice for audit: HLD-scope.
- **Q-AU4** — What's "high-value" for tamper-evidence: financial, identity, KYC, ops, contract.
- **Q-AU5** — External anchor (timestamping): v2 if compliance demands.

### Integrations
- **Q-CAF1** — Adapter SDK separate repo: monorepo v1.
- **Q-CAF2** — Per-tenant adapter overrides: no v1.
- **Q-CRAF1** — Carrier adapter SDK codegen: hand-written v1.
- **Q-CRAF2** — Multi-region carrier credentials: support if needed.
- **Q-CRAF3** — Per-carrier SLA published: aggregated only.
- **Q-PG1** — Native UPI vs PG-mediated: PG v1.
- **Q-PG2** — NACH/e-NACH for credit-line: v2.
- **Q-PG3** — Multi-currency: v3.
- **Q-CMP1** — Voice/IVR: v3.
- **Q-CMP2** — RCS adoption: watch.
- **Q-CMP3** — Web push pre-mobile: v2.
- **Q-KYC1** — DigiLocker primary: backup.
- **Q-KYC2** — Aadhaar storage: hashed reference.
- **Q-KYC3** — Video KYC: v3.
- **Q-KYC4** — Continuous KYC: yes for GSTIN annually.

### Policy engine (architecture)
- **Q-PE1** — Audit reads on sensitive keys: financial + risk yes; behavioral no.
- **Q-PE2** — Cache invalidation: event-driven; TTL fallback.
- **Q-PE3** — Contract-driven settings: require amendment to change; ops bypass with two-person.
- **Q-PE4** — Sub-seller policy 5th layer: no v1.

---

## CLOSED — decisions made during PRD drafting

Decisions resolved during the v0 → v1.0 PRD development. These are no longer open; the rationale is captured for posterity.

- **CLOSED — Reseller / white-label tier?** **No.** Single-aggregator model. (See [`09-appendix/04-deferred-features.md`](./04-deferred-features.md).)
- **CLOSED — Public API at v1?** **No, deferred to v2.** Sellers operate via dashboard. Internal APIs designed cleanly for v2 productization.
- **CLOSED — Notification tier?** **No.** Same baseline (WhatsApp + SMS + email) for all sellers.
- **CLOSED — Dedicated CSM-per-seller?** **No.** Pooled support; SLA tier varies by plan.
- **CLOSED — Pikshipp own fleet at v1/v2?** **No.** v3+ if at all; would plug in as carrier adapter.
- **CLOSED — KYC framing?** **Risk tool, not compliance ritual.** Depth scales with risk and volume.
- **CLOSED — Cross-tenant tree (sellers under resellers)?** **No.** Single layer: Pikshipp + sellers; sub-sellers as internal hierarchy only.
- **CLOSED — Reverse-leg charging mechanism?** **Post-event wallet debit with grace cap; suspension beyond cap; invoice path for credit-line customers.** (See Feature 13.)
- **CLOSED — Configurability strategy?** **Policy engine** as cross-feature substrate; plans are bundles of config; enterprise = overrides. (See `03-product-architecture/05-policy-engine.md`.)
- **CLOSED — Audit policy?** **Audit-everything as cross-cutting principle.** Tamper-evident on high-value events. (See `05-cross-cutting/06-audit-and-change-log.md`.)
- **CLOSED — Allocation as a feature?** **Yes, first-class with auditable explanations.** (See Feature 25.)
- **CLOSED — Risk/fraud as a feature?** **Yes, phased. v1 minimum (rules + behavioral).** (See Feature 26.)
- **CLOSED — Contracts as a feature?** **Yes, encrypted storage + e-sign + machine-readable terms feeding policy engine.** (See Feature 27.)
- **CLOSED — Per-seller buyer-experience branding?** **Yes** — logo, colors, optional custom domain. Covered in Feature 17. *Not* white-label; just per-seller branding.
- **CLOSED — Tracking source-of-truth?** **Carrier.** Pikshipp is the unification layer.
- **CLOSED — Insurance default at v1?** **Optional, opt-in, partner-led.** No forced attach.
- **CLOSED — Mobile app at v1?** **No.** Mobile-web at v1; native app v2.
- **CLOSED — COD remittance leg-1 cycle (Pikshipp → seller)?** **Configurable per seller-type via policy engine** (D+2 / D+3 / D+5 typical).
- **CLOSED — Hyperlocal & B2B at v1?** **No, v3.**
