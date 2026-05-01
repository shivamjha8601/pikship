# Executive summary

> One page. If you read nothing else, read this.

## What we're building

**Pikshipp** is a multi-courier shipping aggregator for the Indian e-commerce ecosystem. It is the seller's single control plane to:

1. **Pull orders** from every channel they sell on — their own storefront (Shopify, WooCommerce, Magento, custom), marketplaces (Amazon, Flipkart, Meesho, Myntra, Ajio), and offline (manual, CSV).
2. **Allocate the right courier** for every shipment — comparing 8+ carriers (Delhivery, Bluedart, DTDC, Ekart, Xpressbees, Ecom Express, Shadowfax, India Post, more later) on cost, speed, reliability, and seller-configured constraints.
3. **Book, label, manifest, and track** every shipment, with normalized status across heterogeneous courier APIs.
4. **Resolve the messy stuff** — failed deliveries (NDR), returns (RTO), weight disputes, COD reconciliation, fraud — through a workflow engine, not email threads.
5. **Reconcile money** — wallet, prepaid recharges, COD remittance ledger, weight-discrepancy adjustments, GST invoicing — all auditable.

## Why this exists

Indian e-commerce sellers operate across an average of **3–5 channels** and ship through **2–4 couriers**. The status quo is:

- **N×M operational chaos** — every channel has its own dashboard, every courier has its own portal. Sellers reconcile state by hand.
- **Opaque pricing** — rack rates are public; real rates are negotiated, vary by zone/weight/volume, and change quarterly.
- **Money lost in the gaps** — undisputed weight charges, mis-remitted COD, unrefunded returns. Sellers below ~₹50L GMV/month cannot afford a finance hire.
- **NDR is where margins die** — 25–40% of COD orders generate at least one NDR; without action, they become RTOs at full cost to the seller.

A shipping aggregator solves all of this with one integration. **Shiprocket has proven the demand**. The question is no longer *"is this a market?"* — it is *"how do you carve share inside it?"*

## Why us, and why now

We are not betting on a single wedge. We are betting on **executing the boring stuff better than the incumbent** along three axes:

1. **Better tech foundations** — configurable per-seller behavior across every dimension; clean carrier and channel adapter frameworks; an explicit allocation engine with audited decisions.
2. **Better NDR & COD outcomes** — the two operations that determine seller P&L. We invest disproportionately here.
3. **Better support, weight reconciliation, audit transparency** — the boring middle that incumbents under-deliver.

## Architectural model in one diagram

```
                       ┌─────────────┐
                       │   Pikshipp  │  (us — the aggregator and brand)
                       └──────┬──────┘
                              │
                  ┌───────────┴───────────┐
                  │                       │
             ┌────▼────┐             ┌────▼────┐
             │ Seller  │   ...       │ Seller  │   (our customers — businesses
             └────┬────┘             └────┬────┘    that ship goods)
                  │                       │
            ┌─────▼─────┐           ┌─────▼─────┐
            │  Buyers   │           │  Buyers   │  (sellers' customers —
            └───────────┘           └───────────┘   recipients; not accounts)
```

Carriers are pluggable adapters under one interface, so:

```
Shipment.book() ──► CarrierAdapter
                      ├─ Delhivery
                      ├─ Bluedart
                      ├─ DTDC
                      ├─ Ekart
                      ├─ Xpressbees
                      ├─ Ecom Express
                      ├─ Shadowfax
                      ├─ India Post
                      └─ ... (Pikshipp's own first-party network in v3+,
                              if ever — same interface, no platform fork)
```

Channels are pluggable adapters under one interface (Shopify / WooCommerce / Amazon / etc.), feeding into one canonical Order model.

## How sellers vary (the configuration vector)

A seller is not a flat account. A seller is a **vector of configuration values** across many independent axes — wallet posture (prepaid / hybrid / credit), COD eligibility & cycle, allowed carriers, max delivery attempts, pricing rate-card, KYC depth, restricted goods, buyer-experience branding, feature flags, and ~20 more. Every behavioral default is resolved at runtime through a **policy engine** that layers: Pikshipp defaults → seller-type defaults → seller-specific overrides → Pikshipp locks.

This means:
- A small SMB seller and a mid-market seller use the same product but get different defaults — different COD posture, different remittance cycle, different carrier set.
- Large customers get negotiated overrides without us forking the codebase.
- Adding a new behavior axis (a new restriction, a new pricing dimension) is a config change, not a feature.

Detailed in [`03-product-architecture/05-policy-engine.md`](./03-product-architecture/05-policy-engine.md).

## Scope of v1

A complete picture in [`08-roadmap/03-v1-public-launch.md`](./08-roadmap/03-v1-public-launch.md). The five-bullet version:

- Onboarding with risk-tiered KYC; sandbox mode; Pikshipp-Ops review queue.
- Channel integrations: manual + CSV + Shopify + WooCommerce + Amazon SP-API.
- Courier network with 8 carriers, allocation engine with cost+speed+reliability scoring, booking, labels, manifests.
- Tracking with status normalization, branded buyer tracking page, NDR action loop.
- Wallet (prepaid + grace-cap negative balance for RTO), COD remittance ledger, weight disputes, GST invoicing, basic reports.
- Audit-everything baseline.

Out of v1 scope (deferred to v2/v3, with reasons documented in roadmap):
- **No public API** — sellers operate via dashboard only.
- **No mobile app**.
- **No white-label / reseller layer** — this is a Shiprocket-class product, not a Shiprocket-platform-maker.
- **No insurance attach as default product** — optional partner-led offering.
- **No hyperlocal / B2B / international**.
- **No first-party fleet operation**.

## Top risks

| Risk | Why it matters | Mitigation |
|---|---|---|
| **Courier API instability** | We are downstream of 8+ third-party APIs; their outages are our outages. | Adapter pattern with circuit breakers; allocation engine biases away from degraded carriers; status normalization layer. |
| **COD float exposure** | We pay sellers their COD before carriers remit to us. ₹crore in float at all times. | Configurable per-seller cycle (D+2 premium, D+5 default, D+ longer for risk); carrier remittance-lag monitoring; per-seller float caps. |
| **Weight & COD fraud** | Both kill seller margins and our trust. | Photo evidence at packing for weight; pre-pickup buyer COD verification (configurable per seller); RTO risk model on intake (rules at v1, ML later). |
| **Reverse-leg charging gap** | If we don't recover RTO charges from sellers cleanly, we eat them. | Post-event wallet debit with grace-cap; suspension semantics on persistent negative; invoicing path for credit-line customers. |
| **Pricing-engine misconfiguration** | Real seller contracts have 8+ axes. Wrong rate = we eat the diff. | Versioned, structured rate cards with simulator; never hand-rolled rate logic; everything auditable. |
| **Audit gaps in operational actions** | Without audit on manual ledger adjustments, KYC overrides, etc., internal abuse is invisible. | Audit-everything as cross-cutting principle; tamper-evident; two-person approvals above thresholds. |

## What this PRD will produce next

- **HLA / HLD** (High-Level Architecture / Design) — derived from this PRD; defines services, schemas, infra.
- **Implementation LLDs** — per-component low-level designs.
- **Implementation plan** — phased build aligned to `08-roadmap/`.

## Reading guide

> If you have **15 minutes** — read this page and `01-vision-and-strategy/02-market-and-competitive-landscape.md`.
> If you have **1 hour** — also read `03-product-architecture/01-system-overview.md`, `02-multi-tenancy-model.md`, `05-policy-engine.md`, and `08-roadmap/01-phasing-strategy.md`.
> If you have **a day** — read everything in order.
