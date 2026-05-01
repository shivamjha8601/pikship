# User journeys

> Five representative end-to-end journeys, each covering many features. These are the canonical narratives we test against any product decision: *"does this still make sense in journey J3?"*

The journeys are detailed sequence diagrams in [`06-flows/`](../06-flows/). This file is the *narrative summary* — what happens, in plain English, end-to-end, from each persona's perspective.

---

## Journey 1 — A new SMB seller's first 24 hours

**Persona:** P6 (Owner) of a 2-month-old D2C jewelry brand on Shopify.

**The arc:**

1. **Discovery (10 min before signup):** Searches "Shiprocket alternative" on Google. Lands on Pikshipp blog → comparison page → signup CTA.
2. **Signup (3 min):** Mobile + OTP → business profile (name, GSTIN, address). Lands on dashboard in *sandbox mode* (KYC pending).
3. **Sandbox exploration (15 min):** Connects Shopify (OAuth). Sees 12 historical orders pulled in (read-only). Adds a pickup address. Browses rate calculator.
4. **KYC submission (5 min):** Uploads PAN + GSTIN + cancelled cheque. KYC enters review queue (auto + manual where needed).
5. **First wallet recharge (3 min):** Adds ₹5,000 via UPI. Wallet credited within 60 sec.
6. **Wait for KYC (median 4h):** Receives WhatsApp + email when approved.
7. **First booking (90 sec):** Picks an order, allocation engine recommends a courier with explanation, books. AWB generated, label downloads.
8. **First pickup (next-day):** Courier picks up; status updates appear; buyer gets WhatsApp tracking link.
9. **First delivered (3 days later):** Status auto-updated; buyer optionally rates the experience; seller sees delivered analytics.
10. **First payday (D+5 from delivery, default plan):** First COD remitted to wallet.

**What we measure:** Time-to-first-shipment; KYC SLA; first-week activation (≥3 shipments); first-month retention.

**Where it can go wrong:**
- KYC stuck (>24h) → seller churns to a competitor.
- Shopify OAuth fails for a particular plan → seller can't even start.
- First-courier API failure on the first booking → frustration is disproportionately damaging.

**Per-seller-config angle:** This new SMB is on Pikshipp Free; COD is off by default; allocation engine prioritizes cost over premium speed; carrier set is the base 4 carriers. All of this is policy-engine-resolved, not hard-coded.

---

## Journey 2 — A scaled D2C brand on a "normal" weekday

**Persona:** P7 (Operator) at a brand doing 800 orders/day across Shopify + Amazon + Meesho. P6 (Owner) reviews weekly.

**The day:**

- **9:00 AM** Operator opens dashboard. 320 orders in "ready to ship" queue (overnight).
- **9:05 AM** Bulk action: select all → allocate → bulk-book. 308 booked successfully; 12 fail (4 unserviceable pincodes, 8 weight-limit issues). Operator handles exceptions one-by-one.
- **9:30 AM** Bulk download labels (4×6 ZPL for thermal printer). Prints 308 labels in one queue.
- **10:30 AM** Pickup truck arrives. Operator scans manifest barcode at pickup; courier confirms pickup count.
- **2:00 PM** New 240 orders arrive (intra-day batch). Operator repeats bulk-book → labels.
- **4:00 PM** First NDR notifications come in (yesterday's shipments). 18 NDRs. Operator triages: 12 → request reattempt with corrected addresses; 4 → buyer didn't have COD ready, send WhatsApp; 2 → escalate to RTO.
- **5:30 PM** Wallet balance alert at 20% threshold. Operator messages Owner.
- **6:00 PM** Owner adds ₹50,000 via NEFT.
- **End of day:** ~530 shipments booked, 18 NDRs handled, 0 missed pickups.

**What we measure:** Shipments per active operator-day; bulk-action latency; exception ratio; NDR action ratio.

**Where it can go wrong:**
- Bulk-book 320 orders blocks for 4 minutes → operator workflow stalls.
- ZPL labels render incorrectly → reprints, disputes with courier on barcode.
- Pickup count mismatch between manifest and truck driver count → reconciliation hell.

**Per-seller-config angle:** This brand is on Pikshipp Scale; COD is on with D+2 remittance; allocation engine weighted toward reliability for high-value orders, cost for low-value; access to 6 carriers including premium air; auto-rules cover most NDR triage.

---

## Journey 3 — An NDR being saved

**Persona:** P7 (seller op) + P9 (buyer).

**The narrative:**

1. Buyer is a professional in tier-2 city; gets a delivery attempt at 11 AM on a workday — no one home.
2. Courier marks "NDR — buyer unavailable" at 11:30 AM.
3. Webhook fires to Pikshipp. We:
   - Mark order NDR in seller dashboard.
   - Send WhatsApp to buyer: *"Hi, your [Brand] order missed delivery today. Reschedule here: [link]"* (sender: seller's branded WhatsApp).
   - Notify seller via email.
4. Buyer clicks the link at lunch; chooses "tomorrow 6–9 PM"; submits.
5. Pikshipp sends a reattempt request to courier API (NDR action `reattempt` with slot `tomorrow_evening`).
6. Courier acknowledges; second-attempt scheduled.
7. Next day 7 PM: delivered. Webhook updates status. Buyer gets confirmation.
8. Seller dashboard shows: NDR resolved, 1 reattempt, no RTO cost. Audit trail captures every step.

**What we measure:** NDR resolution rate; time from NDR → buyer click; reattempt API success rate.

**Where it can go wrong:**
- WhatsApp delivery fails silently → buyer never sees the link → RTO.
- Courier API does not support per-slot reattempt → we send a generic reattempt → courier picks any slot → still missed.
- Buyer gets multiple WhatsApp messages from courier *and* Pikshipp → confusion.

**Per-seller-config angle:** Max 3 reattempts (configured for this seller's plan); buyer outreach uses seller's branded sender; auto-RTO would fire on attempt 4 if reached.

---

## Journey 4 — A weight dispute, resolved correctly

**Persona:** P8 (Seller Finance), P7 (Operator), P2 (Pikshipp Ops), P10 (Courier hub agent — indirect).

**The narrative:**

1. Operator packs a 600g parcel; weighs at 0.6 kg on packing scale; takes a photo of parcel + scale (camera tile in dashboard).
2. Books shipment at 0.5kg slab (declared dead weight 0.6kg → 0.5–1kg slab).
3. Courier weighs at hub at 0.85 kg (still 0.5–1kg slab). No dispute.
4. **OR** courier weighs at 1.05 kg (1–2kg slab). Charge difference: ₹35.
5. Two weeks later, courier sends a "weight reconciliation" file to Pikshipp.
6. Pikshipp ingests, sees the discrepancy, raises an in-app dispute on behalf of seller.
7. Auto-resolution flow:
   - Compare seller-declared weight with courier-claimed weight.
   - Compare seller's photo evidence with courier's reweigh photo (if courier sent one).
   - If photo evidence supports seller, auto-dispute with courier.
8. Courier accepts well-evidenced disputes (target ~70% acceptance). Charge reversed in seller's wallet.
9. Seller Finance sees: monthly statement shows the original charge, the reversal, net zero impact. Audit trail explains every decision.

**What we measure:** Dispute auto-resolution rate; net seller P&L impact from weight discrepancies (target: trending to zero); ops review queue size.

**Where it can go wrong:**
- Operator forgets to upload photo → no evidence → dispute weak.
- Courier sends reweigh data weeks late → past the dispute window.
- Courier reweigh process is itself opaque (no photo) → he-said-she-said.

---

## Journey 5 — A buyer interacting with the post-purchase experience

**Persona:** P9 (Buyer) — a 32-year-old in a tier-3 city; ordered cosmetics on a Shopify D2C brand that uses Pikshipp.

**The arc:**

1. Order placed Tuesday evening. Brand confirms via email (Shopify's default email).
2. Wednesday 11 AM: shipment booked. Buyer gets WhatsApp from "[Brand]": *"Your order is on its way. Track here."* Link goes to `track.[brand].com/abc123` (the seller's branded tracking page).
3. Wednesday–Friday: buyer checks tracking page 3 times. Sees status timeline, ETA, courier name.
4. Saturday: out-for-delivery. WhatsApp + SMS notify.
5. Saturday afternoon: delivery attempted, NDR (buyer in a meeting). WhatsApp: *"We missed you. Reschedule here."*
6. Buyer rescheduled for Monday morning.
7. Monday: delivered. WhatsApp: *"Delivered. Loved your order? Reply STAR to rate."*
8. Buyer replies 5 stars. Reply is captured by us (visible to seller) and used as a buyer satisfaction signal.

**What we measure:** Buyer engagement with tracking page; NDR feedback page conversion; buyer satisfaction.

**Where it can go wrong:**
- WhatsApp template not approved → fall back to SMS (lower engagement).
- Tracking page slow on 3G → bounce.
- Buyer thinks the WhatsApp is spam (sender unknown) → ignores → NDR not resolved.

**Per-seller-config angle:** This brand has set a custom tracking domain and uploaded their branding. WhatsApp sender is the seller's branded WhatsApp business number (set up via Pikshipp's Meta-BSP integration on the seller's behalf).

---

## Journeys NOT covered (and where they will be added later)

| Journey | Reason it's separate | Where it lives |
|---|---|---|
| Hyperlocal same-day pickup-to-delivery | v3 surface; very different timing | `04-features/23-hyperlocal-and-same-day.md` |
| B2B freight (LTL/FTL) | v3 surface; different unit of work (truck-load) | `04-features/24-b2b-shipping.md` |
| International outbound | Out of scope until v3+ | — |
| Seller dispute escalating to courier head-office | Edge case; lives in `19-admin-and-ops.md` |
