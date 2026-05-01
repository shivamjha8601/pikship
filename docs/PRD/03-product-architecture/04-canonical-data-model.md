# Canonical data model

> When a Shopify order, an Amazon order, a manual order, and a CSV-imported order all land in our system, what do they look like? Same shape. This document defines that shape.

## Why this matters

The aggregator's promise — "one dashboard, every channel" — is built on the canonical model. If the model leaks channel-specific shape, every downstream feature has to know about every channel. That's the failure mode of cheap aggregators. The canonical model absorbs the channel diversity once, and everything downstream assumes the canonical shape.

The same principle applies to carriers (one canonical Shipment shape, regardless of which carrier carries it).

## Canonical Order

```yaml
order:
  id: PSO-{seller_short}-{seq}            # internal, idempotent
  scope:
    seller_id: slr_xxx
    sub_seller_id: ssl_xxx | null

  source:
    channel_id: ch_xxx                    # null if manual or API direct
    channel_platform: shopify | woocommerce | magento | amazon | flipkart | meesho | ... | manual | csv | api
    channel_order_ref: "1003"             # platform's own number
    channel_order_url: "https://store.myshop.com/admin/orders/1003"  # back-reference
    placed_at: "2026-04-25T10:32:00+05:30"
    ingested_at: "2026-04-25T10:32:08+05:30"

  buyer:
    name: "Asha M"
    phone: "+91-9XXXXXXXXX"               # canonical E.164
    email: "asha@example.com" | null      # may be missing on COD orders
    secondary_phone: "+91-..." | null
    consent:
      whatsapp: true | false | unknown
      sms: true | false | unknown
      email: true | false | unknown

  ship_to:
    line1, line2, landmark
    city, state, pincode, country: IN
    contact_name, contact_phone
    location_type: home | office | shop | pickup_point
    geo: {lat, lng} | null

  bill_to: same shape as ship_to | null   # often == ship_to in B2C

  pickup_location_id: pl_xxx
  pickup_address: snapshot                # snapshotted at booking time

  payment:
    mode: prepaid | cod
    cod_amount: 0 if prepaid, else INR amount
    declared_value_inr: 1499              # for insurance / customs
    currency: INR

  package:
    declared_weight_g: 600
    dims_mm: { l: 250, w: 200, h: 50 }
    is_fragile: false
    is_dangerous_goods: false

  line_items:
    - sku: "ABC-001"
      product_id: prd_xxx | null          # null if no catalog
      name: "Earrings - Gold Plated"
      hsn_code: "7117"
      qty: 1
      unit_price: 1499
      tax: { gst_pct: 5, amount: 71.4 }

  meta:
    tags: ["meesho-priority", "fragile-handle"]
    custom_fields: { ... }
    seller_notes: "Pack in small box; gift wrap"
    buyer_notes: "Please don't ring bell"

  status: pending | ready_to_ship | on_hold | booked | partially_fulfilled | fulfilled | cancelled
```

## Canonical Shipment

```yaml
shipment:
  id: PSS-pik-{seller_short}-{seq}
  order_id: PSO-...
  scope: { ... same shape ... }

  carrier:
    carrier_id: crr_delhivery
    service_type: surface | air | express | hyperlocal | b2b
    awb: "DLV1234567890"
    barcode_data: "..."

  rate_quote_id: rq_xxx
  charge:
    declared_weight_g: 600
    chargeable_weight_g: 700                # max(declared, volumetric)
    volumetric_weight_g: 700                # L*W*H/5000 (or carrier-specific divisor)
    base_amount: 55.00
    cod_handling: 6.00
    fuel_surcharge: 4.20
    gst: 11.74
    total: 76.94

  status:
    canonical: created | booked | pickup_pending | picked_up | in_transit | out_for_delivery | delivered | ndr_n | rto_initiated | rto_in_transit | rto_delivered | cancelled | lost | damaged
    last_event_at: "2026-04-25T18:00:00+05:30"
    last_event_location: "Mumbai Sortation Hub"
    estimated_delivery_at: "2026-04-28"

  pickup:
    scheduled_for: "2026-04-26"
    picked_up_at: null
    manifest_id: mfst_xxx | null

  delivery:
    delivered_at: null
    pod_proof_ref: null                    # signature image / OTP / photo

  ndr:
    attempts: []                           # see NDREvent below
    open: false
    next_action: null
    next_action_deadline: null

  rto:
    initiated_at: null
    delivered_back_at: null
    qc_outcome: pending | passed | failed | partial

  cod:
    cod_amount: 1499.00
    remitted_at: null
    remittance_ref: null

  weight_dispute:
    raised: false
    carrier_reported_weight_g: null
    evidence_refs: []
    status: null
    resolved_amount: 0

  insurance:
    insured: false
    declared_value_inr: 1499
    premium: 0
    policy_ref: null

  audit:
    booked_by_user_id: u_xxx
    booked_at: "..."
    booked_via: ui | api | rule
    rule_ref: null
```

## Canonical TrackingEvent

```yaml
tracking_event:
  id: trk_xxx
  shipment_id: PSS-...
  carrier_event_code: "PUD"                # raw courier code
  carrier_event_label: "Picked Up at Origin Hub"  # raw courier text
  canonical_status: picked_up
  canonical_substatus: at_origin_hub | null
  location:
    text: "Mumbai Sortation Hub"
    pincode: "400001"
    geo: { lat, lng } | null
  occurred_at: "2026-04-26T10:14:00+05:30"
  recorded_at: "2026-04-26T10:14:08+05:30"
  source: webhook | poll | manual
  raw_payload_ref: stash://tracking/raw/abc123  # for debugging
```

## Canonical NDREvent

```yaml
ndr_event:
  id: ndr_xxx
  shipment_id: PSS-...
  attempt_no: 1
  reason_canonical: buyer_unavailable | wrong_address | refused | premises_locked | cod_not_ready | other
  reason_raw: "OUT - door locked"
  reported_at: "2026-04-27T13:00:00+05:30"
  action_taken: null | reattempt | rto | contact_buyer | hold
  action_at: null
  action_payload:
    new_slot: "tomorrow_evening"
    new_address_id: addr_xxx
    contact_message_id: msg_xxx
  deadline_at: "2026-04-29T13:00:00+05:30"
  resolution: pending | resolved_delivered | rto | expired
```

## Canonical Address (value type)

```yaml
address:
  line1: "Flat 12, Building A"
  line2: "Sector 4"
  landmark: "Near Big Bazaar" | null
  city: "Mumbai"
  state: "MH"                              # ISO 3166-2:IN
  pincode: "400070"
  country: "IN"
  contact_name: "Asha M"
  contact_phone: "+91-9XXXXXXXXX"
  alternate_phone: null
  location_type: home | office | shop | warehouse | pickup_point
  geo: { lat, lng } | null
  source: buyer_provided | seller_corrected | normalized | geocoded
  validation:
    pincode_serviceable: true
    pincode_zone_classification: metro | regional | rest_of_india | special
```

## Canonical RateQuote

```yaml
rate_quote:
  id: rq_xxx
  for_order_id: PSO-...
  carrier_id: crr_xxx
  service_type: surface | ...
  rate_card_id: rc_xxx
  rate_card_version: 7
  computed_at: "..."
  expires_at: "..."   # short — minutes
  inputs:
    chargeable_weight_g: 700
    zone: metro_metro
    payment_mode: cod
    declared_value_inr: 1499
  breakdown:
    base_first_slab: 35.00
    additional_weight: 20.00
    cod_handling: 6.00
    fuel_surcharge: 4.20
    gst: 11.74
    total: 76.94
  estimated_delivery_days: 2
  notes: ["First-slab is 0.5kg"]
```

## Canonical LedgerEntry

```yaml
ledger_entry:
  id: led_xxx
  wallet_account_id: wa_xxx
  amount: 76.94
  currency: INR
  direction: debit | credit
  ref_type: shipment_charge | recharge | refund | weight_dispute_adjustment | cod_remittance | manual_adjustment | invoice_adjustment
  ref_id: PSS-... | rcg_... | ...
  occurred_at: "..."
  posted_at: "..."
  actor:
    kind: user | system | reconciliation_job
    id: ...
  description: "Shipment charge: Delhivery surface, AWB DLV1234567890"
  reverses_ledger_id: null                # if this is a reversal
```

## Status normalization table (excerpt)

The mapping from courier status codes to canonical status. Real table lives in [`07-integrations/02-courier-adapter-framework.md`](../07-integrations/02-courier-adapter-framework.md). Sample:

| Courier | Courier code | Courier label | → Canonical |
|---|---|---|---|
| Delhivery | UD | Manifested | booked |
| Delhivery | PU | Pickup Done | picked_up |
| Delhivery | TR | In Transit | in_transit |
| Delhivery | OFD | Out for Delivery | out_for_delivery |
| Delhivery | DL | Delivered | delivered |
| Delhivery | RT | RTO Initiated | rto_initiated |
| Delhivery | RTD | RTO Delivered | rto_delivered |
| Delhivery | CR | Customer Refused | ndr (reason=refused) |
| Bluedart | OK | Delivered | delivered |
| Bluedart | NS | Not Served | ndr (reason=premises_locked) |
| Bluedart | LK | Door Locked | ndr (reason=premises_locked) |
| ... | ... | ... | ... |

Where the courier sends a code we have not seen, the normalizer falls back to `unknown` and raises an internal alert.

## Data identifiers and prefixes

A consistent prefix system makes IDs human-recognizable and helps support staff identify what they are looking at.

| Entity | Prefix | Example |
|---|---|---|
| Seller | `slr_` | `slr_lotusbeauty` |
| Sub-seller | `ssl_` | `ssl_lotus_mumbai` |
| User | `u_` | `u_a3kf81` |
| Channel | `ch_` | `ch_shopify_main` |
| Pickup location | `pl_` | `pl_mum_warehouse` |
| Product | `prd_` | `prd_abc001` |
| Buyer | `byr_` | `byr_9XXXXXXXXX_001` |
| Address | `addr_` | `addr_x9zk2` |
| Order | `PSO-` | `PSO-lotus-100123` |
| Shipment | `PSS-` | `PSS-lotus-100123-1` |
| Carrier | `crr_` | `crr_delhivery` |
| Rate card | `rc_` | `rc_lotus_v3` |
| Rate quote | `rq_` | `rq_a3lkj` |
| Allocation decision | `alc_` | `alc_a3lkj` |
| Manifest | `mfst_` | `mfst_2026_04_25_a` |
| Tracking event | `trk_` | `trk_aa11` |
| NDR event | `ndr_` | `ndr_p3` |
| Wallet account | `wa_` | `wa_lotus_main` |
| Ledger entry | `led_` | `led_a4b2c3` |
| Invoice | `inv_` | `inv_lotus_2026_04` |
| Weight dispute | `wd_` | `wd_p3` |
| COD remittance | `cod_` | `cod_2026_04_25_lotus` |
| Notification | `noti_` | `noti_aabb` |
| Ticket | `tck_` | `tck_2026_001` |
| Risk score | `rsk_` | `rsk_a3lkj` |
| Contract | `con_` | `con_lotus_2026` |
| Audit event | `aud_` | `aud_a3lkj` |

## Idempotency keys

Every write that originates from outside the platform (channel webhook, manual create, public API) carries an idempotency key:

- Channel webhook: `(channel_id, channel_event_id)`.
- Public API: `Idempotency-Key` header (caller-supplied, UUIDv4 recommended).
- Manual create: server-generated; UI prevents duplicate-submit.

Replays produce the same response without side-effects.

## Versioning

- The canonical model is versioned (`v1`, `v2`, ...). Webhooks emitted to sellers carry the model version they were generated under, so seller integrations don't break on minor model evolution.
- Internal storage is migrated forward on schema change; we do not run multiple versions of the canonical model simultaneously in production.

## What this enables downstream

Because the canonical model is stable:

- **Order management** is built once, regardless of channel.
- **Rate engine** consumes canonical Order, doesn't care if it came from Amazon or a CSV.
- **Shipment booking** produces canonical Shipment regardless of carrier.
- **Tracking & NDR** speak only canonical statuses.
- **Buyer-facing UI** renders from canonical Shipment, not carrier payloads.
- **Public API** exposes canonical model directly to seller integrations.
- **Reports** roll up canonical fields, not channel/carrier-specific fields.

This is *the* leverage point. Every hour spent here saves N hours downstream.
