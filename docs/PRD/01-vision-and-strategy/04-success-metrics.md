# Success metrics

> What we measure, what we target, and how each metric ties back to the business model.

## North-star metric

**Shipments processed per month, weighted by retention cohort.**

Why this and not GMV / revenue / sellers:
- **Shipments** is the unit of value we create — every shipment is a successful seller-buyer transaction we enabled.
- **Weighted by retention cohort** corrects for vanity: 100k shipments from 1-month sellers is worse than 80k shipments from 6-month sellers.
- It is **directly correlated with revenue** (we earn per shipment), but harder to game than revenue.

## Metric tree

```
                     ┌──────────────────────────────────┐
                     │   Shipments / month (weighted)   │
                     └─────────────┬────────────────────┘
                                   │
        ┌──────────────────────────┼──────────────────────────┐
        │                          │                          │
   ┌────▼────┐               ┌─────▼─────┐              ┌─────▼─────┐
   │ Sellers │               │ Shipments │              │ Retention │
   │ (paid + │               │ per active│              │ (90-day,  │
   │  free)  │               │  seller   │              │  365-day) │
   └────┬────┘               └─────┬─────┘              └─────┬─────┘
        │                          │                          │
   ┌────┴────┐                ┌────┴────┐                ┌────┴────┐
   │ Acquisi-│                │ Channel │                │ NDR /   │
   │ tion    │                │ breadth │                │ RTO /   │
   │ (CAC,   │                │ per     │                │ Weight  │
   │ source) │                │ seller  │                │ outcomes│
   └─────────┘                └─────────┘                └─────────┘
```

## Tier 1 — business KPIs (quarterly board metrics)

| Metric | Definition | v0 / Internal alpha | v1 / Public launch | v2 / Year 1 |
|---|---|---|---|---|
| Shipments / month | Total successful first-attempt bookings, ex-cancellations | <1k | 10k by month 3 → 50k by month 6 | 250k+ |
| Active sellers (MAS) | Sellers with ≥1 booking in last 30 days | <50 | 500 by month 3 → 2,000 by month 6 | 10,000+ |
| Paid seller % | Paid plan / total active | n/a | 10% by month 6 | 18%+ |
| Retention 90-day | % of cohort still active 90 days after first booking | — | 50% | 65%+ |
| Retention 365-day | Same, 365 days | — | n/a | 40%+ |
| Net revenue retention | (this cohort's revenue this month) / (their revenue 12 months ago) | — | n/a | 110%+ |
| Average revenue per active seller (ARPS) | Monthly revenue / active sellers | — | ₹1,500 | ₹2,500 |

## Tier 2 — product KPIs (weekly product reviews)

### Acquisition & onboarding
| Metric | Target |
|---|---|
| Time-to-first-shipment (signup → first AWB) | < 30 minutes (median) |
| KYC approval SLA (P95) | < 24h |
| Onboarding funnel completion (signup → first booking) | 60%+ |
| First-week activation rate (≥3 shipments in week 1) | 40%+ |

### Engagement
| Metric | Target |
|---|---|
| Average channels connected per active seller | 2.0+ by month 12 |
| Average couriers used per active seller | 2.0+ by month 12 |
| % shipments via auto-recommended courier | 70%+ |
| Bulk-action usage (sellers using bulk label / bulk book) | 50%+ of >50-shipments/day sellers |

### Operational quality (this is where seller P&L is made or lost)
| Metric | Target | Industry typical |
|---|---|---|
| Pikshipp-mediated NDR resolution rate (NDR → Delivered without RTO) | 65%+ | 45–55% |
| RTO % (RTO shipments / total) | <12% | 15–22% |
| Weight dispute auto-resolution rate (resolved without seller manual action) | 80%+ | <30% |
| Weight dispute net seller P&L impact | Reduced 70%+ vs. baseline | Baseline = seller-eats |
| First-attempt delivery rate | 78%+ | 70–75% |
| Tracking event lag (P95, courier event → visible in Pikshipp) | < 5 min | 30 min – 2 h |

### Reliability
| Metric | Target |
|---|---|
| Platform uptime (booking + tracking endpoints) | 99.9% |
| API P95 latency, rate fetch | < 500 ms |
| API P95 latency, booking | < 2 s |
| Courier API success rate (per partner) | 98%+ (alert below) |

### Allocation engine
| Metric | Target |
|---|---|
| Allocation decisions audited (% with full explain trail) | 100% |
| Recommended-carrier acceptance rate | 75%+ |
| Allocation latency P95 | < 500 ms |

### Support & seller health
| Metric | Target |
|---|---|
| Support ticket FRT (first response time) | < 30 min business hours |
| Support ticket resolution time (median) | < 4 h |
| Tickets per 1,000 shipments | < 8 |
| NPS (sellers, quarterly) | > 40 |

### Money & integrity
| Metric | Target |
|---|---|
| Wallet recharge success rate | 99.0%+ |
| COD remittance accuracy (vs. courier reconciliation) | 99.95%+ |
| COD remittance lag — Pikshipp → Seller, P95 | per seller config (D+2 / D+5) |
| Days-of-balance (working capital float) | tracked, not targeted |
| Bad-debt write-off as % revenue | < 0.3% |
| Reverse-leg charge recovery rate | > 99% within 30d |

### Audit & integrity
| Metric | Target |
|---|---|
| Audit-log completeness (privileged actions logged / total) | 100% |
| Audit chain integrity (tamper-evidence checks) | 100% pass |
| Cross-tenant access events with reason captured | 100% |
| Two-person approvals for above-threshold actions | 100% |

## Tier 3 — guardrail / leading indicators

These are early warning signs that a Tier 1/2 metric is about to move.

| Indicator | What it predicts | Action threshold |
|---|---|---|
| Channel adapter failure rate (per channel) | Order ingestion drop | > 1% in any 1-hour window |
| Courier booking failure rate (per courier) | Seller frustration / NDR proxies | > 2% in any 1-hour window |
| KYC backlog (pending > 48h) | Activation drop | > 20 sellers waiting |
| Wallet balance < ₹100 across active sellers | Recharge friction or churn | sustained 10%+ of base |
| Wallet negative-balance count (RTO debt) | Bad-debt risk | > 5% of active sellers |
| Support ticket category spike (e.g., "label not generating") | Hidden bug | 3× weekly average |
| Weight dispute volume (per seller, per week) | Courier malfunction or seller fraud | 5× seller's monthly average |
| Allocation engine override rate (seller picks non-recommended) | Recommendation degradation | > 30% trending |
| RTO rate spike per pincode | Buyer fraud or address quality issue | 2× zone baseline |

## Slicing requirement

Every Tier 1 / Tier 2 metric must be sliceable by:
- **Plan tier** (Free / Grow / Scale / Enterprise)
- **Seller-type / volume band**
- **Channel** (Shopify / Amazon / Manual / etc.)
- **Carrier**
- **Seller cohort** (signup month)
- **Region / pincode zone**

This requires the data warehouse to denormalize per-seller attributes on every fact table. Detailed in [`05-cross-cutting/05-observability.md`](../05-cross-cutting/05-observability.md).

## Anti-metrics (what we will NOT optimize)

| Anti-metric | Why we ignore it |
|---|---|
| Total registered sellers (cumulative) | Vanity. Active is what counts. |
| Total shipments lifetime | Same — vanity. |
| Feature ship velocity | We optimize for outcomes, not output. |
| Time spent in product | Sellers want to spend *less* time, not more. |
| Pageviews | Irrelevant to the business model. |

## Reporting cadence

| Cadence | Audience | Metrics covered |
|---|---|---|
| Daily | Eng + Ops on-call | Tier 3 guardrails |
| Weekly | Product + Ops leadership | Tier 2 product KPIs |
| Monthly | Exec / All-hands | Tier 1 + cohort retention curves |
| Quarterly | Board | Tier 1 + financial roll-up + per-tier slices |

## Owned-by-whom

Every metric has an owner who is accountable for the number, even if they do not personally implement what moves it.

| Metric family | Owner |
|---|---|
| Acquisition / activation | Growth PM |
| Engagement / channel breadth | Channels PM |
| Operational quality (NDR/RTO/weight) | Operations PM |
| Allocation engine quality | Allocation PM |
| Reliability | Eng / SRE lead |
| Money integrity | Finance + Eng (wallet team) |
| Audit / compliance | Security + Eng (platform team) |

The Open Questions register (`09-appendix/02-open-questions.md`) tracks any metric where the owner or target is undecided.
