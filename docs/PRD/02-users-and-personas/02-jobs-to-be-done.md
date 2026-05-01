# Jobs-to-be-done

> JTBD framing forces us to talk about the *progress* a user is trying to make, not the *features* we want to build. Personas tell us *who*; JTBDs tell us *why now*.

Each job is structured as: **When [situation], I want to [motivation], so I can [outcome]** — followed by current alternatives, our intended differentiation, and the features that serve the job.

---

## Seller-side jobs

### JTBD-S1 — "Get every order shipped without thinking about which courier"
**When** a new order arrives on any of my channels,
**I want** the system to pick the best courier and book it for me,
**So I can** focus on selling, not logistics.

- Today: seller logs in to courier portals one-by-one or uses a single courier (suboptimal rates, single point of failure).
- Pikshipp: allocation engine + recommendation; bulk auto-book; rules-based routing.
- Features: [`07-rate-engine`](../04-features/07-rate-engine.md), [`25-allocation-engine`](../04-features/25-allocation-engine.md), [`08-shipment-booking`](../04-features/08-shipment-booking.md).

### JTBD-S2 — "Know where every shipment is, without chasing the courier"
**When** a buyer asks "where's my order?",
**I want** to answer in 10 seconds with the latest status,
**So I can** keep the buyer trust and avoid refund demands.

- Today: log into 3 courier portals; copy AWB; refresh.
- Pikshipp: unified tracking; status normalized across couriers; webhook-driven freshness; WhatsApp templates.
- Features: [`09-tracking`](../04-features/09-tracking.md), [`16-notifications`](../04-features/16-notifications.md), [`17-buyer-experience`](../04-features/17-buyer-experience.md).

### JTBD-S3 — "Recover a failed delivery before it becomes a return"
**When** a buyer is not available and the courier marks NDR,
**I want** to talk to the buyer, reschedule, and save the sale,
**So I can** avoid the RTO cost and preserve the order.

- Today: courier sends an SMS to the buyer; if no answer, attempts twice more, then RTO. Seller has no agency.
- Pikshipp: NDR action center, buyer-facing reschedule page, WhatsApp escalation, in-product reattempt request to courier.
- Features: [`10-ndr-management`](../04-features/10-ndr-management.md).

### JTBD-S4 — "Stop losing money to weight disputes"
**When** a courier reweighs my parcel and charges me more,
**I want** to challenge it with photo evidence,
**So I can** stop overpaying for shipments I packed correctly.

- Today: most sellers do not even know they were charged more; the rest have to email support.
- Pikshipp: automated dispute workflow with photo upload at packing time; auto-resolution rules; dispute history in finance ledger.
- Features: [`14-weight-reconciliation`](../04-features/14-weight-reconciliation.md).

### JTBD-S5 — "Know my COD will arrive on time, every time"
**When** my COD shipment is delivered,
**I want** the cash to be in my wallet within my contracted cycle,
**So I can** plan working capital.

- Today: COD remittance cycles vary by courier (D+4 to D+8); reconciliation gaps are common.
- Pikshipp: per-seller-configurable cycle (D+2 premium, D+5 default); transparent ledger; alerts on delays.
- Features: [`12-cod-management`](../04-features/12-cod-management.md), [`13-wallet-and-billing`](../04-features/13-wallet-and-billing.md).

### JTBD-S6 — "Reconcile the entire month in 30 minutes"
**When** the month ends,
**I want** to download a single report that ties shipments, charges, COD, weight disputes, and refunds,
**So I can** close my books without spreadsheet pain.

- Today: 5 different reports from 5 different couriers + the aggregator's own report.
- Pikshipp: single monthly statement; GST-compliant; CA-friendly export.
- Features: [`13-wallet-and-billing`](../04-features/13-wallet-and-billing.md), [`15-reports-and-analytics`](../04-features/15-reports-and-analytics.md).

### JTBD-S7 — "Add a new sales channel without re-doing my logistics"
**When** I expand from Shopify to also selling on Amazon and Meesho,
**I want** my shipping setup to "just work" with the new channel,
**So I can** grow distribution without operations rework.

- Pikshipp: one-click channel connect; orders flow into the same dashboard.
- Features: [`03-channel-integrations`](../04-features/03-channel-integrations.md), [`04-order-management`](../04-features/04-order-management.md).

### JTBD-S8 — "Negotiate better rates as I scale"
**When** my shipment volume grows,
**I want** automatic access to lower courier rates (or to negotiate custom rates with my account manager),
**So I can** improve my unit economics without hiring a logistics person.

- Pikshipp: tier-based plan defaults with per-seller rate-card overrides; transparent rate simulator.
- Features: [`07-rate-engine`](../04-features/07-rate-engine.md), [`13-wallet-and-billing`](../04-features/13-wallet-and-billing.md).

### JTBD-S9 — "Run a returns experience that doesn't kill the brand"
**When** a buyer wants to return a product,
**I want** them to initiate the return from a branded portal,
**So I can** preserve brand trust and minimize manual coordination.

- Pikshipp: branded return portal; QC photo on pickup; refund trigger in seller's wallet.
- Features: [`11-returns-and-rto`](../04-features/11-returns-and-rto.md), [`17-buyer-experience`](../04-features/17-buyer-experience.md).

### JTBD-S10 — "Know which buyers will probably RTO before I ship"
**When** I'm about to book a COD shipment,
**I want** to see a risk score on the buyer/address,
**So I can** ask for prepaid, restrict serviceability, or accept the risk eyes-open.

- Pikshipp: COD risk model based on pincode history, repeat buyer signals, address quality.
- Features: [`12-cod-management`](../04-features/12-cod-management.md), [`26-risk-and-fraud`](../04-features/26-risk-and-fraud.md).

### JTBD-S11 — "Show my brand to my buyers"
**When** a buyer receives a shipment,
**I want** them to see my brand throughout — tracking page, NDR notifications, returns portal,
**So I can** build my brand and not be just an Amazon seller.

- Pikshipp: per-seller buyer-experience branding (logo, colors, optional custom domain).
- Features: [`17-buyer-experience`](../04-features/17-buyer-experience.md).

---

## Internal jobs

### JTBD-I1 — "Resolve a stuck shipment in under 5 minutes"
**When** a seller raises a ticket about a stuck shipment,
**I want** to see all relevant context (order, courier events, prior tickets, wallet, NDR history) in one screen,
**So I can** resolve without 7 tabs and 3 portals.

- Pikshipp: unified ops console with shipment 360-degree view + carrier action shortcuts.
- Features: [`19-admin-and-ops`](../04-features/19-admin-and-ops.md).

### JTBD-I2 — "Know that a courier is degrading before sellers complain"
**When** a courier's API or operations degrade,
**I want** an automated alert with the data to triage,
**So I can** route around it (allocation engine biases away) before sellers feel pain.

- Pikshipp: real-time courier health dashboard, alerting on success rate, latency, NDR rate spikes.
- Features: [`05-cross-cutting/05-observability`](../05-cross-cutting/05-observability.md), [`06-courier-network`](../04-features/06-courier-network.md).

### JTBD-I3 — "Reconcile courier invoices in days, not weeks"
**When** a courier sends a monthly invoice,
**I want** to match line items to my own records automatically,
**So I can** identify discrepancies and pay/dispute.

- Pikshipp: courier invoice ingestion + auto-match + discrepancy queue.
- Features: [`19-admin-and-ops`](../04-features/19-admin-and-ops.md), [`14-weight-reconciliation`](../04-features/14-weight-reconciliation.md).

### JTBD-I4 — "Understand why a shipment was routed to a particular carrier"
**When** a seller asks "why did this go DTDC instead of Delhivery?",
**I want** the audit trail of the allocation decision,
**So I can** explain (or fix) it immediately.

- Pikshipp: allocation decisions are first-class auditable; every pick stored with weights, filters, scores.
- Features: [`25-allocation-engine`](../04-features/25-allocation-engine.md), [`05-cross-cutting/06-audit-and-change-log`](../05-cross-cutting/06-audit-and-change-log.md).

### JTBD-I5 — "Detect fraudulent sellers before they cost us"
**When** a seller exhibits abnormal patterns (sudden high-RTO, fake addresses, weight under-declaration),
**I want** an automated risk signal,
**So I can** intervene before financial loss.

- Pikshipp: behavioral risk scoring; automated holds; ops review queue.
- Features: [`26-risk-and-fraud`](../04-features/26-risk-and-fraud.md).

---

## Buyer jobs (yes, the buyer is also a user)

### JTBD-B1 — "Know when my package will arrive — without downloading an app"
**When** a seller ships me something,
**I want** a clear, mobile-friendly tracking page,
**So I can** plan my day around the delivery.

- Pikshipp: zero-login tracking page (URL via WhatsApp/SMS); branded to seller; estimated delivery window.
- Features: [`17-buyer-experience`](../04-features/17-buyer-experience.md), [`16-notifications`](../04-features/16-notifications.md).

### JTBD-B2 — "Reschedule a missed delivery without a phone call"
**When** I missed the delivery,
**I want** to pick a new time slot or address from a link,
**So I can** avoid playing phone tag with the courier.

- Pikshipp: NDR feedback page; updates flow back to the courier as a reattempt request.
- Features: [`10-ndr-management`](../04-features/10-ndr-management.md), [`17-buyer-experience`](../04-features/17-buyer-experience.md).

### JTBD-B3 — "Confirm or cancel a COD before the courier arrives"
**When** I placed a COD order I no longer want,
**I want** to confirm or cancel before the package ships,
**So I can** avoid the courier showing up with cash demand.

- Pikshipp: COD confirmation page sent to buyer at booking; cancellation flows back to seller.
- Features: [`12-cod-management`](../04-features/12-cod-management.md).

---

## How JTBDs map to outcomes

Each job maps to a measurable outcome (the metric that says we're succeeding at the job):

| Job | Primary outcome metric |
|---|---|
| JTBD-S1 | Time-to-ship per order; auto-recommended courier acceptance rate |
| JTBD-S2 | Tracking event lag P95; seller "where's my order" tickets per 1k shipments |
| JTBD-S3 | NDR resolution rate (NDR → Delivered without RTO) |
| JTBD-S4 | Weight dispute auto-resolution rate; seller P&L impact |
| JTBD-S5 | COD remittance lag P95; remittance accuracy |
| JTBD-S6 | Time to close monthly books (self-reported); reports per seller per month |
| JTBD-S7 | Channels per active seller |
| JTBD-S8 | % sellers on >Free plan; ARPS by tier |
| JTBD-S9 | Returns initiated per 1k shipments; return cycle time |
| JTBD-S10 | RTO % for predicted-low-risk vs predicted-high-risk shipments |
| JTBD-S11 | % sellers using custom branding; buyer engagement on tracking pages |
| JTBD-I1 | Median resolution time per ticket |
| JTBD-I2 | Mean time to detect (MTTD) for courier degradation |
| JTBD-I3 | Days to close monthly courier reconciliation |
| JTBD-I4 | Allocation audit completeness; seller "why this carrier" tickets resolved fast |
| JTBD-I5 | Risk model precision/recall; fraud loss avoided |
| JTBD-B1 | Tracking page bounce rate; "Where's my order" call-volume to seller |
| JTBD-B2 | Buyer NDR feedback page usage; rescheduled delivery success |
| JTBD-B3 | COD confirmation page usage; pre-shipment cancellation rate |

These outcomes appear in `01-vision-and-strategy/04-success-metrics.md`. JTBDs and metrics must agree.
