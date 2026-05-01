# Glossary

> Authoritative list of terms used in this PRD. When a term is used in any feature/flow doc, it means *exactly* the definition here. If a feature doc seems to use a term differently, that's a bug — fix the doc.

## Identity & customers

- **Pikshipp** — Us — the platform operator and brand. Singular; there is no other aggregator brand on this platform.
- **Seller** — Our customer; a business that ships goods. Has multiple users (Owner / Operator / Finance / etc.) and a configuration vector.
- **Sub-seller** — Optional child organization of a seller (branch / subsidiary).
- **Buyer** — Recipient of a shipment. Not a tenant; not an account holder.
- **User** — A human or service acting within one seller's context.

## Configuration & policy

- **Policy engine** — The runtime system that resolves "what's the rule for this seller × this setting?" by walking Pikshipp lock → seller override → seller-type default → global default.
- **Seller config vector** — The ~30 axes of behavior a seller can be configured on (wallet posture, COD eligibility, allowed carriers, etc.).
- **Seller-type** — A bundled set of default config values (e.g., `small_smb` / `mid_market` / `enterprise`).
- **Plan** — Customer-facing label for a seller-type bundle.
- **Override** — A seller-specific value that supersedes the seller-type default.
- **Lock** — A Pikshipp-pinned value that cannot be overridden by sellers.

## Order & shipment

- **Channel** — Connection from a seller to a sales platform (Shopify, Amazon, Meesho, etc.).
- **Channel order ref** — The platform's order number; unique per (channel, seller).
- **Order** — Pikshipp canonical order; one per channel order.
- **Line item** — SKU + qty + price within an order.
- **Shipment** — A single physical parcel with one AWB.
- **AWB (Airway Bill)** — Courier-issued shipment number.
- **Manifest** — Pickup-time document listing AWBs.
- **POD (Proof of Delivery)** — Signed receipt of delivery.
- **Pickup location** — Physical address from which a seller ships.
- **Pickup window** — Time-of-day range when courier picks up.

## Carrier

- **Carrier / Courier** — Third-party shipping provider (Pikshipp Express in v3+, if ever).
- **Service type** — surface, air, express, hyperlocal, B2B-LTL, B2B-FTL.
- **Adapter** — Code that translates Pikshipp ↔ a specific carrier's API.
- **Zone** — (origin, destination) classification used in pricing.
- **Volumetric weight** — `(L × W × H) / divisor` (typ. 5000); chargeable weight = max(declared, volumetric).
- **Chargeable weight** — What we actually bill on.

## Allocation & pricing

- **Allocation engine** — System that picks which carrier/service to use for a given shipment.
- **Allocation decision** — A first-class record of why a particular carrier was chosen (candidates, scores, filters).
- **Pricing engine** — Computes price for a (carrier × service × order × seller); subsystem of allocation.
- **Rate card** — Multi-dimensional pricing structure; versioned.
- **Rate quote** — Short-lived snapshot of "what would shipping this Order via this Carrier cost right now".

## Status (canonical)

- `Created → Booked → Pickup_Pending → Picked_Up → In_Transit → Out_For_Delivery → Delivered`
- Branches: `NDR` → `RTO_Initiated → RTO_In_Transit → RTO_Delivered`, `Cancelled`, `Lost`, `Damaged`.

## NDR / RTO / Returns

- **NDR (Non-Delivery Report)** — Courier failed to deliver.
- **RTO (Return to Origin)** — Failed-forward shipment returning to seller.
- **Reattempt** — Request another delivery try.
- **Hold at hub** — Buyer collects from courier hub.
- **Reverse pickup** — Buyer-initiated return; courier picks up from buyer.
- **QC (Quality Check)** — Seller's check on returned/RTO'd item.

## Payment & money

- **COD (Cash on Delivery)** — Buyer pays at delivery.
- **Prepaid** — Buyer paid online before shipment.
- **Wallet** — Seller's prepaid (and optionally credit-line) balance with Pikshipp.
- **Ledger** — Append-only record of all wallet movements.
- **Hold / Reserve** — Tentative wallet decrement before booking confirms.
- **Confirm / Release** — Finalize or cancel a hold.
- **Float** — Money sitting in wallets / between us and carriers.
- **Remittance** — Money flowing from courier → us → seller.
- **Reverse-leg charge** — Charge for return shipping (RTO or buyer-initiated).
- **Grace cap** — Small negative-balance allowance on the wallet to absorb RTO charges before suspension.
- **Reconciliation** — Matching expected money flows with actual.
- **Weight discrepancy / dispute** — Courier's reweigh charge differs from declared weight.

## Compliance / legal

- **GST** — Goods and Services Tax (India).
- **GSTIN** — GST Identification Number.
- **HSN/SAC** — Harmonized/Service Accounting Codes for tax.
- **DPDP Act** — Digital Personal Data Protection Act 2023.
- **PPI** — Prepaid Payment Instrument (RBI category).
- **DLT** — Distributed Ledger Technology registration for SMS templates (TRAI).
- **IRDAI** — Insurance Regulatory and Development Authority of India.
- **E-way bill** — GST e-document for inter-state goods movement above ₹50k.
- **Place of supply** — GST jurisdiction rules for invoicing.

## Operational

- **SLA** — Service Level Agreement.
- **SLO** — Service Level Objective.
- **MTTD/MTTR** — Mean Time to Detect / Resolve.
- **DLQ** — Dead Letter Queue.
- **HMAC** — Hash-based Message Authentication Code (signature).
- **Idempotency key** — Caller-supplied key to deduplicate retries.
- **Audit-everything** — Cross-cutting principle: every privileged action is logged.
- **Tamper-evidence** — Hash-chain protection on high-value audit events.

## Seller lifecycle states

- **Provisioning** — Seller created but not transacting.
- **Sandbox** — KYC pending; sandbox-only.
- **Active** — Fully operational.
- **Suspended** — Reads OK; no new bookings.
- **Wound down** — Shutting down with data export.
- **Archived** — Terminated; data in cold storage.

## Channel-specific (selected)

- **SP-API** — Amazon Selling Partner API.
- **EventBridge / SQS** — Amazon's webhook delivery mechanism.
- **MPHP** — Myntra's seller portal.
- **Webhook HMAC** — Shopify, Bluedart, etc., sign with HMAC.
- **OAuth 1.0a / 2.0** — Auth standards used by various platforms.
- **ONDC** — Open Network for Digital Commerce; v3 channel option.

## What is NOT in this PRD

- **Reseller** — Not a concept here. We are the only aggregator.
- **White-label tenant** — Not a feature. Removed (see `09-appendix/04-deferred-features.md`).
- **Tenant tree / tenancy hierarchy** — Not a thing. Single layer: Pikshipp + seller.
- **Cross-tenant operations** — Reframed as "cross-seller operations" (Pikshipp staff acting on multiple sellers, with audit).

## Acronym index

AWB, BSP, CAC, COD, DLT, DPDP, EOD, ERP, ETA, FBA, FCM, FTL, GST, GSTIN, HMAC, HSN, IT, JTBD, KPI, KYC, LTV, LTL, MFA, MSME, NDR, NEFT, NPS, OCR, OFD, OIDC, OMS, OTP, PAN, PG, PII, POD, PPI, RBI, RBAC, RTGS, RTO, SAC, SAML, SES, SLA, SLO, SMS, SOC, SP-API, SQS, SSL, SSO, TLS, TRAI, UPI, WC, WCAG, WMS, ZPL.
