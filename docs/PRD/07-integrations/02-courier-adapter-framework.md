# Courier adapter framework

> Pairs with `04-features/06-courier-network.md`. The *how* of carrier integration.

## Goal

The same uniform contract as the channel adapter, but for the courier side. Booking, tracking, NDR action, manifests — all behind one interface, regardless of whether the carrier is Delhivery, Bluedart, Pikshipp Express, or a tiny regional player.

## The contract (logical)

```typescript
interface CourierAdapter {
  // Identity
  readonly carrierKey: string;
  readonly capabilities: CourierCapabilities;

  // Serviceability
  serviceability(input: ServiceQuery): Promise<ServiceabilityResult[]>;

  // Rate (live, optional — most use local rate cards)
  liveRate?(input: RateQuery): Promise<RateBreakdown>;

  // Booking
  book(input: BookingRequest): Promise<BookingResult>;
  cancel(awb: string, reason?: string): Promise<CancelResult>;

  // Label / Manifest
  label(awb: string, format: 'pdf-a4' | 'pdf-4x6' | 'zpl' | 'epl'): Promise<LabelDoc>;
  manifest(input: ManifestRequest): Promise<ManifestDoc>;

  // Tracking
  track(awb: string): Promise<TrackingEvents>;
  registerWebhook?(url: string, events: string[]): Promise<void>;
  verifyInbound(req: InboundReq): Promise<NormalizedTrackingEvent[]>;

  // NDR action
  requestNDRAction(awb: string, action: NDRAction, payload?: any): Promise<NDRActionResult>;

  // Pickup registration
  registerPickup?(pickup: PickupLocation): Promise<{ ref: string }>;

  // Reverse / RTO / Returns
  bookReverse?(input: ReverseBookingRequest): Promise<BookingResult>;

  // COD remittance
  fetchRemittance?(period: DateRange): AsyncIterable<RemittanceRecord>;

  // Weight reconciliation
  fetchReweighs?(period: DateRange): AsyncIterable<ReweighRecord>;
  submitWeightDispute?(awb: string, evidence: Evidence[], requested: 'reverse' | 'partial'): Promise<DisputeResult>;
}
```

## Capabilities declaration

```yaml
capabilities:
  services: [surface, air, express, hyperlocal, b2b_ltl, b2b_ftl]
  cod_supported: true
  cancel_post_manifest: false
  cancel_pre_manifest: true
  partial_cancel: false
  webhook_supported: true
  webhook_signature: hmac_sha256
  poll_required_fallback: true
  poll_cadence_min: 5
  manifest_format: pdf
  label_formats: [pdf-a4, pdf-4x6, zpl]
  reverse_pickup_supported: true
  weight_dispute_api: true
  remittance_api: true
  ndr_actions: [reattempt, reattempt_with_slot, rto, hold_at_hub, contact_buyer]
  weight_bounds:
    surface: { min_g: 50, max_g: 30000 }
    air: { min_g: 50, max_g: 30000 }
  volumetric_divisor: { surface: 5000, air: 5000 }
  pickup_registration_required: true
  zone_classification:  # carrier's own zones
    metro_metro, regional, ...
```

## Status normalization

Each adapter ships a status code mapping table → canonical status. Excerpts in `03-product-architecture/04-canonical-data-model.md`. The mapping is **the** contract — downstream features only see canonical statuses.

## Common helpers

- **Rate limiter** per carrier per credential.
- **Circuit breaker** per carrier (auto-trip on error rate; auto-recover with probe).
- **Idempotency** for booking via `(carrier, idempotency_key)`.
- **Webhook receiver** with signature verification.
- **Polling worker** for tracking when no webhooks.
- **Reconciliation job** for booked-but-no-event shipments.

## First-party Pikshipp Express adapter

When we build first-party, the adapter:
- Follows the same interface.
- Calls our internal first-party service via HTTP/gRPC.
- Has `is_first_party = true` capability flag.
- Lives in the same code surface.
- No special-cased code in upstream features.

## Onboarding a new carrier — checklist

1. Adapter scaffolding (capabilities, methods).
2. Sandbox credentials & integration tests.
3. Status code mapping.
4. Rate card uploaded centrally.
5. Service zone matrix uploaded.
6. Webhook signature configured (if applicable).
7. Polling cadence configured (if needed).
8. Pickup registration test (if required by carrier).
9. Manifest format test.
10. NDR action methods tested.
11. Remittance & reweigh ingest tested (where API exists).
12. Production credentials.
13. Soft launch (opt-in subset of sellers).
14. Monitor; GA after 2 stable weeks.

## Per-carrier idiosyncrasies (will accumulate)

A living document maintained per adapter (e.g., `docs/integrations/carriers/delhivery.md` in the codebase, not in this PRD). Top-of-mind items:

| Carrier | Idiosyncrasy |
|---|---|
| Delhivery | Strong API; supports most actions; per-pincode caching helpful |
| Bluedart | Pickup registration is per-address; API stricter; AWB format premium |
| DTDC | Mixed API quality across services; surface vs air diverge |
| Ekart | Only available to certain sellers historically; auth quirks |
| Xpressbees | Modern API; supports webhooks well |
| Ecom Express | Stronger tier-2/3 coverage; reweigh data via portal historically |
| Shadowfax | Hyperlocal-shaped; live tracking stronger |
| India Post | Last-resort; web scrape historically; modern API limited |

## Testing

- Per-adapter golden tests (anonymized payloads).
- Sandbox booking tests in CI.
- Probe traffic in production (low volume) to detect drift.

## Observability

- Per-carrier metrics: booking success rate, API P95, webhook lag, NDR rate, RTO rate, dispute win rate, remittance lag.
- Per-adapter alerts on capabilities-mismatch (e.g., we asked for slot reattempt but carrier silently ignored).

## Open questions

- **Q-CRAF1** — Adapter SDK as code-generated or hand-written? Default: hand-written v1; codegen later if patterns repeat heavily.
- **Q-CRAF2** — Multi-region carrier credentials (e.g., per-state Delhivery accounts)? Default: support if carrier requires.
- **Q-CRAF3** — Adapter health expectations published to sellers (per-carrier SLA)? Default: aggregated only.
