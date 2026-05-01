# Vision & mission

## Vision

> **Every Indian seller, however small, ships with the same operational sophistication as a large enterprise — without hiring a logistics team.**

Logistics in Indian e-commerce is where the typical small seller spends the most time and loses the most money to errors that are not their fault: failed deliveries, weight disputes, COD remittance gaps, RTO costs. The vision is to make those problems either invisible (automated away) or trivial (one click in a dashboard).

We are not the cheapest. We are not the fastest. We are the seller's **operational nervous system** — the layer between their store and every courier, channel, and buyer.

## Mission

> **Build the most reliable, configurable, and auditable shipping platform in India.**

Three load-bearing words in that sentence:

- **Reliable** — uptime, accurate rates, working integrations, fast support. The bar most aggregators miss.
- **Configurable** — every behavioral default is a per-seller setting; large customers get negotiated overrides without us forking the codebase. Plans are bundles of config; not feature gates.
- **Auditable** — every state change, every override, every ops action is traceable. This is what enterprise customers eventually demand and what protects us against internal abuse.

## Long-form vision (5 years out)

By 2031, Pikshipp:

- **Is a default infrastructure choice** — when an Indian D2C founder asks "how should I ship?", Pikshipp is in the answer (the way Razorpay is the answer for "how should I take payments?").
- **Serves the long-tail SMB and the mid-market enterprise on the same platform** — what differs across them is configuration, not codebase.
- **Has expanded the surface beyond aggregation** — wallet/credit, insurance, returns-as-a-service, hyperlocal, B2B freight, and (perhaps) first-party last-mile in metros. Each adjacency is a derivative of the data and trust that aggregation creates.
- **Operates with single-digit ops cost per shipment** because the configurability + audit + automation make ops linear, not super-linear.

## Anti-vision (what we are explicitly NOT building)

Equally important — being explicit about what we won't pursue.

| Not us | Why not |
|---|---|
| A courier company first | We are the layer above couriers. First-party comes later, *if at all*, and plugs in as one carrier among many — never with a special path in the architecture. |
| A marketplace | We do not aggregate buyers; we serve sellers. |
| A storefront builder | Shopify, WooCommerce, Dukaan, Bikayi, etc. exist. We integrate, we don't replace. |
| A finance lender | We have wallet & credit-against-shipments, but we are not Razorpay Capital. We may partner. |
| A platform-maker for *other* aggregators | Building Shiprocket is hard; building a platform that builds Shiprocket is harder and serves a tiny market. We are not white-label. |
| International from day 1 | India is the prize. International is a v3+ extension when our home market is locked. |
| A B2C buyer-facing app | Our buyer surface is the tracking page and NDR feedback page only. We don't make a buyer app. |
| Per-seller custom development | If a seller needs something we don't offer, we either add it as a config axis (so all sellers benefit) or politely decline. We don't fork. |

## Principles

The principles below are tie-breakers when two product decisions look equally valid.

### 1. Configurability over hard-coded behavior
If a seller's needs differ on some dimension, that's a *new config axis*, not a *new feature surface*. Plans are bundles of config; enterprise customers are config overrides. Our codebase has one shape.

### 2. APIs internally, dashboard externally
Internally, every domain has a clean API. Externally — at least until v2 — sellers operate via dashboard only. We don't ship a public API as a v1 product. (See [`08-roadmap/03-v1-public-launch.md`](../08-roadmap/03-v1-public-launch.md).)

### 3. Boring tech for the boring 90%
Logistics is not a place to be clever. Use proven patterns: adapter pattern for carriers and channels, event sourcing for shipment state, idempotent webhooks. Save the cleverness for the allocation engine, NDR/RTO automation, and risk scoring.

### 4. Make the courier disappear
The seller should rarely need to know which courier carried a shipment unless they ask. Status normalization, label normalization, manifest normalization, error normalization — all paid for once, by us.

### 5. Money is sacred
Wallet, COD remittance, weight charges, refunds. Every rupee in or out is a ledger entry with a reference, a timestamp, and a counterparty. No "miscellaneous adjustments". No untraceable balance changes.

### 6. Audit everything
State changes, config overrides, ops actions, cross-tenant access. Tamper-evident. Per-seller exportable. Two-person approval above thresholds. No exceptions.

### 7. KYC for risk, not ritual
We do KYC because we want to know who is shipping what — to protect us, our carriers, and the system from fraud. We do not perform KYC theatre. Depth scales with risk and volume, not with bureaucratic checkbox compliance.

### 8. Operations is a product
The internal ops console (KYC review, dispute resolution, manual interventions) is a product, not a backoffice tool. It gets the same design rigor as the seller app, because operational efficiency is our margin.

### 9. Don't ship features without their operational counterpart
No "weight disputes UI" without auto-eval and an ops queue. No "NDR action center" without buyer outreach and audit. No "wallet recharge" without daily reconciliation. Every feature ships with the ops capability to run it.

## How vision shapes the rest of the PRD

Everything downstream of this page is in service of the vision:

- **Architecture** is built around a configurability framework (the policy engine) so adding axes is easy.
- **Carrier adapter framework** is structured so first-party (if ever) plugs in cleanly.
- **Roadmap phasing** prioritizes the boring middle (KYC, reconciliation, audit) over feature velocity.
- **Cross-cutting audit** treats traceability as a first-class invariant.

If you find yourself reading a feature doc that contradicts a principle on this page, the page wins. File a change against the principle, not against the feature.
