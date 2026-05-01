# Business model

## How Pikshipp makes money

Pikshipp has **two revenue lines** in scope, plus a future-optional adjacency line. The product must support all three from day 1 (architecturally — even if v1 only monetizes line #1).

### 1. Direct shipping aggregation (per-shipment markup)

**Customer:** sellers transacting with Pikshipp.
**Mechanic:** we pay couriers our negotiated rate; we charge sellers a markup; the delta is our gross margin.
**Typical structure:**
- Free / pay-per-shipment tier at rack rates.
- Paid tier: monthly subscription (~₹999–2,999/mo) for lower rates and added features.
- Custom: enterprise rates and contracts.

**Revenue components per shipment:**
| Component | Margin source |
|---|---|
| Forward shipping markup | Negotiated courier rate vs. seller rate |
| COD handling fee | We charge seller % of COD value (typ. 1.5–2%); courier charges us less. *Whether this is shown as a line item or bundled into total cost is a UX choice — see Feature 12.* |
| RTO charge passthrough (sometimes with markup) | Reverse-leg charges debited from seller wallet |
| Weight reconciliation gain (optional) | When we win a weight dispute on the seller's behalf, we may keep a portion |

This is the dominant revenue line at v1 and v2.

### 2. Adjacent products (commission / float)

**Customer:** sellers (and indirectly their buyers).
**Mechanic:** value-added services that piggyback on the shipping data we already have.

| Product | Mechanic | Likely timing |
|---|---|---|
| Shipment insurance | Commission from insurance partner | v1 (optional, partner-led) |
| Wallet float | We hold prepaid balances; if invested, we earn float income (regulatory: see RBI PPI rules — defer investment until cleared) | v2+ |
| Working capital / advance against COD | Lend against expected COD remittance | v3+ (regulated, partner-led) |
| Buyer-side checkout COD-confirm widget | Per-checkout fee from sellers | v2+ |
| Returns-as-a-service (reverse pickup + QC + restock) | Per-return fee | v2+ |

These are **derivatives** of the shipping moat. They become possible because we have the seller relationship, the buyer endpoints, and the data.

### What is NOT a revenue line

- **No white-label / platform-licensing revenue.** We are not building a platform that other aggregators use under their brand. (See [`01-vision-and-mission.md`](./01-vision-and-mission.md).)
- **No public-API monetization** at v1 (the API itself is deferred to v2).
- **No first-party fleet revenue** until v3+, if ever.

## Unit economics (sketch)

Per-shipment economics in line #1, illustrative only (real numbers TBD by Finance):

```
Revenue
  Seller-paid forward charge:           ₹65.00
  COD handling (if COD, 50% of orders): ₹ 6.00 (avg)
  Insurance attach (10% of orders):     ₹ 0.50 (avg)
  Total revenue per shipment:           ₹71.50

Cost
  Courier-paid forward:                 -₹55.00
  COD courier fee:                       -₹3.00 (avg)
  Payment gateway (1.8% on wallet):      -₹1.20 (amortized)
  WhatsApp / SMS / email:                -₹0.50
  Card / KYC / fraud tools:              -₹0.30
  Total cost per shipment:              -₹60.00

Gross profit per shipment:               ₹11.50
Gross margin %:                          ~16%
```

Plus reduction from:
- RTO costs (carrier still charges return leg even on RTO; seller is invoiced via wallet debit; our markup is small)
- Weight discrepancies that go un-disputed (seller eats; we eat the trust loss)
- Refunds / disputes / chargebacks
- COD remittance delays / write-offs

Net contribution margin in line #1 is realistic at **8–12%** at scale.

## Pricing strategy

### Anchors
- **Shiprocket Lite** (free, ~₹35/0.5kg). The "free" anchor in the market. We must match.
- **Shiprocket Plus** (₹999/mo, lower rates). We must offer a comparable or better paid tier.
- **NimbusPost** undercuts Shiprocket on rack rates by 10–15%. We do not compete on price; we compete on reliability and support, but we cannot be more expensive than Shiprocket at parity volume.

### Plan structure (initial proposal — v1)

Plans are **bundles of configuration values** (see [`03-product-architecture/05-policy-engine.md`](../03-product-architecture/05-policy-engine.md)). Custom enterprise = override of any axis.

| Plan | Target | Price | Rates | What's bundled |
|---|---|---|---|---|
| **Pikshipp Free** | Hobbyist, < 100 shipments/mo | ₹0/mo | Rack | 1 channel, 1 pickup, prepaid only, COD off by default |
| **Pikshipp Grow** | SMB, 100–1,000 shipments/mo | ₹999/mo | -10% off rack | Up to 5 channels, 5 pickups, COD eligible after threshold, weight-dispute auto-handling |
| **Pikshipp Scale** | Mid-market, 1k–10k shipments/mo | ₹2,999/mo | Negotiated | Unlimited channels & pickups, faster COD remittance, priority support |
| **Pikshipp Enterprise** | 10k+/mo | Custom | Custom | Custom contract; per-axis overrides; SLA commitments |

### Pricing principles

1. **Free tier exists** — without it, we can't compete with Shiprocket Lite for the long tail.
2. **No per-channel fee** — punishes the cross-channel seller, who is our ideal customer.
3. **Subscription unlocks lower rates and operational defaults, not features** — features at the SMB tier should be table-stakes-equivalent to competitors. We don't paywall NDR or tracking.
4. **Weight reconciliation handling is included on Grow and above** — our differentiator, not a paywall.
5. **Custom Enterprise = config override**, not a different codebase.

## Customer acquisition

### Direct (line #1)
- **SEO + content** on shipping comparison, courier comparison, GST/COD/RTO guides.
- **Channel marketplaces** — Shopify App Store, WooCommerce.com, BigCommerce. The single highest-intent acquisition channel; sellers actively search there.
- **Performance marketing** to seller persona (G/Meta) — works at SMB tier; expensive but Shiprocket pays the same.
- **Marketplace partnerships** — Amazon/Flipkart seller training programs that recommend aggregators.
- **Referral economics** — sellers refer each other. Build into product (₹500 wallet credit per referral).

### Adjacent (line #2)
- Acquisition is free; these products attach to existing customers.
- Cross-sell motion in product (suggest insurance at booking; suggest wallet credit when balance low).

## CAC & LTV (placeholder)

Real targets are owned by Marketing/Finance and tracked in `01-vision-and-strategy/04-success-metrics.md`. Order-of-magnitude expectations for v1:

- **Direct (Free → Paid conversion):** target 12–18% in first 90 days; ARR per paid SMB seller ~₹15–25k including markup.
- **Direct (Enterprise):** sales cycle 30–90 days; ACV ~₹3–10L.
- **Payback period:** <12 months for direct SMB; <18 months for enterprise.

## Cost structure

Major cost buckets, in declining order (illustrative):
1. **Courier costs (passthrough)** — by far the largest, but margin-neutral.
2. **People** — engineering, ops, support. Largest *real* cost line.
3. **Cloud infra** — significant at scale; meaningful at v1 only if poorly architected. Budget aggressively.
4. **Payments** — wallet recharge gateway fees (~1.8–2.2%); structural, not optimizable except by negotiating with PG provider.
5. **WhatsApp / SMS** — every shipment generates 2–5 buyer messages.
6. **KYC / fraud tools** — Karza/Hyperverge per verification.
7. **CAC** — performance marketing, content, conferences.
8. **Customer support** — humans + tools (Freshdesk/Zendesk).
9. **Bad debt** — wallet refunds, COD write-offs.

## Capital strategy

Out of scope for the PRD beyond noting:
- v0/v1 are achievable on seed capital.
- v2 (channel breadth + scale) typically requires Series A logistics fundraise.
- v3 (adjacencies, possible first-party) is Series B+.

## What changes if Shiprocket cuts prices?

- **Free tier:** unaffected; both are at zero subscription.
- **Paid tiers:** we have ~10% pricing flexibility before our gross margin disappears; Shiprocket has more, but not infinite.
- **Real defense:** quality of NDR, COD, weight reconciliation, support, audit transparency, configurability for negotiated customers. A 5% rate war does not move SMB sellers if their reconciliation is broken elsewhere.

This is why we do not compete on price as a primary axis. The business model assumes parity pricing, not undercutting.
