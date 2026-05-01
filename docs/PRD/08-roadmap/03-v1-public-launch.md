# v1 — Public launch

> Goal: a usable, monetizable, Shiprocket-class aggregator for Indian SMB-to-mid-market sellers. Paying customers, public marketing.

## What v1 includes

### Onboarding & identity
- Mobile + email + OTP login.
- Auto-KYC via Karza/Hyperverge (GSTIN + PAN + bank).
- Risk-tiered KYC depth (basic / standard / enhanced).
- Sandbox mode during KYC.
- Manual KYC fallback.
- Roles: Owner, Manager, Operator, Finance, Read-only.
- Multi-user invites.

### Seller organization & configuration
- Single-tenant data scoping (all rows seller_id-scoped).
- Sub-seller hierarchy support (basic).
- Lifecycle: provisioning / sandbox / active / suspended / wound-down / archived.
- Suspension semantics including wallet-debt-driven.

### Policy engine
- Full taxonomy of seller-config axes.
- Resolution: Pikshipp lock → seller override → seller-type default → global default.
- Settings UI in seller dashboard for self-serve axes.
- Settings UI in admin console for ops-managed axes.
- Audit on every override + change.

### Channels
- Manual order entry.
- CSV import.
- Shopify (OAuth + webhooks).
- WooCommerce (REST + webhooks).
- Amazon SP-API (subset).

### Catalog & warehouses
- Multi pickup locations.
- Optional product master.
- Bulk import.

### Carriers
- Delhivery, Bluedart, DTDC, Ekart, Xpressbees, Ecom Express, Shadowfax, India Post.
- Per-seller subset configurable.
- Carrier health dashboard internal.

### Allocation engine (Feature 25)
- Multi-objective scoring (cost, speed, reliability, seller-pref).
- Hard-constraint filtering.
- Auditable decisions.
- Recommended pick + ranked alternatives.
- Auto-mode (per rules) and interactive mode.

### Pricing engine (Feature 07)
- Local rate cards with per-seller overrides.
- Versioned, immutable on publish.
- Rate simulator.

### Booking
- Single + bulk.
- Two-phase wallet.
- Idempotency.
- Auto-fallback on carrier failure (opt-in).
- Labels: PDF A4 + 4×6 + ZPL.
- Manifests system + carrier-required formats.

### Tracking
- Webhook ingest + polling fallback.
- Status normalization.
- Buyer tracking page (per-seller branded; mobile-first).
- ETA + on-time tracking.

### NDR
- Detection from canonical events.
- Action set: reattempt, reattempt_with_slot, contact_buyer, rto.
- Buyer NDR feedback page.
- Auto-rules engine (basic).
- WhatsApp + SMS outreach.

### Returns / RTO
- RTO lifecycle to QC.
- Buyer-initiated returns portal (basic).
- Reverse pickup via carrier APIs.

### COD
- Verification (WhatsApp + SMS) — configurable per seller.
- Remittance per seller cycle (D+2 / D+3 / D+5 by seller-type).
- Reconciliation with carrier remittance.
- Mismatch alerting.

### Wallet & billing
- Double-entry ledger.
- Razorpay recharge (UPI + cards + netbanking).
- Auto-recharge.
- Credit line (manual approval; from contract).
- **Reverse-leg charging with grace cap** (RTO charges debit wallet; small negative grace before suspension).
- Monthly GST invoice.
- Wallet statement.

### Weight reconciliation
- Photo-at-pack capture.
- Carrier reweigh ingest (API + CSV).
- Auto-eval engine (basic).
- Carrier dispute submission.
- Wallet reversal on win.

### Reports
- 8 default seller reports.
- CSV export.
- Scheduled email delivery.

### Notifications
- WhatsApp + SMS + email.
- Multi-vendor with failover.
- Templates with seller overrides where allowed.
- DLT-registered SMS; Meta-approved WhatsApp templates.
- Same baseline for all sellers (no notification tier).

### Buyer experience
- Tracking page (per-seller branded).
- NDR feedback page.
- Returns portal.
- COD confirm page.
- English + Hindi.
- Custom domain provisioning (optional, per seller).

### Support & ticketing
- In-app + email + WhatsApp intake.
- Ticket linked to context.
- Knowledge base (Pikshipp-curated; per-seller-type variants).
- SLA tracking.
- Freshdesk/Zendesk under the hood.
- No dedicated CSM model (pooled support).

### Admin & ops console
- Seller management.
- KYC review queue.
- Shipment 360.
- Wallet ops with approvals.
- Carrier ops + health.
- Audit log.
- Impersonation under consent.

### Risk & fraud (Feature 26 — minimum)
- Rules-based intake screening.
- Pincode RTO history; per-seller buyer phone history.
- Behavioral seller monitoring.
- Ops review queue.

### Contracts & documents (Feature 27 — minimum)
- Encrypted document storage.
- E-sign integration (Leegality / Digio).
- Machine-readable contract terms feed → policy engine.
- Renewal alerts.

### Cross-cutting
- Security: TLS, secrets in vault, RBAC.
- Audit: every privileged action.
- Performance budgets per `05-cross-cutting/04`.
- Observability per `05-cross-cutting/05`.
- Accessibility per `05-cross-cutting/03`.
- Internationalization (English + Hindi for buyer pages; English for seller).

### Insurance (optional, partner-led)
- Per-shipment attach (manual or auto-rule).
- Multi-insurer routing.
- Claims workflow.

## Explicit non-goals at v1

- **Public API** — deferred to v2. Sellers use dashboard only.
- **Mobile app** (native).
- **White-label / reseller** — out of scope entirely.
- **Hyperlocal / same-day** couriers.
- **B2B / freight**.
- **ONDC channel**.
- **First-party fleet operation**.
- **Channels beyond Shopify / WC / Amazon / Manual / CSV**.
- **Custom report builder**.
- **AI/ML risk models** (rules-based v1; ML in v2).
- **2-way WhatsApp chatbot**.
- **International shipping**.
- **Notification tiering** — same baseline for all.
- **Dedicated CSM per seller**.

## v1 success metrics

(See `01-vision-and-strategy/04-success-metrics.md` for detail.)

- 500+ active sellers within 3 months of public launch.
- 50k+ shipments/month within 6 months.
- 99.9% booking + tracking endpoint uptime.
- NDR resolution rate ≥ 65%.
- 0 cross-seller data leak incidents.
- 100% audit completeness on privileged actions.

## Risks for v1

- Underestimated KYC SLA → seller churn at the gate.
- Carrier API instability undocumented → seller frustration spikes.
- WhatsApp template approval delay → outreach quality degraded.
- Float exposure on COD remittance grows faster than projections.
- Reverse-leg charge debt accumulates if grace-cap suspension not enforced.

## What we will deliberately ship rough at v1

- Custom report builder (use exports for v1).
- Sub-seller hierarchy (basic; advanced overrides v2).
- Mobile app (mobile-web is acceptable).
- 2-way WhatsApp (one-way templated is enough).
- AI-driven recommendations (rules-based is enough).
- ML risk models (rules are enough).

## Pre-launch checklist (gate for v1 → public)

- [ ] All v1 features green-rated by their feature owner.
- [ ] At least 100 sellers piloted in private beta with positive feedback.
- [ ] 99.9% uptime over 30-day pilot.
- [ ] 0 ledger discrepancies in pilot.
- [ ] Legal review complete (GST, DPDP, RBI PPI scope, IRDAI scope).
- [ ] Support team trained; SLA targets approved.
- [ ] Public docs / marketing site live.
- [ ] On-call rotation operational.
- [ ] Disaster recovery drill done.
- [ ] Audit-of-audit verification passing.
