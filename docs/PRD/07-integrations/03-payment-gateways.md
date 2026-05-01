# Payment gateways

> Used for **wallet recharge** (seller pays Pikshipp). Not for buyer-side checkout (channels handle).

## Vendors

| PG | Strengths | Notes |
|---|---|---|
| **Razorpay** | UPI-first; broad coverage; good DX; UPI Autopay; DLT-aware | Default v1 |
| **Cashfree** | Competitive on UPI; payouts API | Secondary; payouts to seller bank if needed |
| **PayU** | Established; good cards | Secondary |
| **Stripe India** | Cards-led; less UPI optimized | Tertiary |

## Methods supported

- UPI (collect + intent + autopay).
- Cards (debit / credit, OTP per RBI mandates).
- Netbanking.
- Wallets (Paytm, PhonePe — via PG).
- NEFT / RTGS / IMPS (manual reconciliation; large recharges).

## Integration model

- Use PG's hosted checkout for v1 (less PCI scope).
- Standard order flow:
  1. Create order on PG.
  2. Redirect or open SDK widget.
  3. PG handles auth + 2FA.
  4. PG webhook on success.
  5. Verify signature.
  6. Post wallet credit (idempotent).

## Auto-recharge (UPI Autopay)

- Mandate created at first opt-in.
- Triggered when balance crosses configured threshold.
- Per-day / per-trigger caps.
- Retry on failure with backoff.

## Webhooks & idempotency

- Verify signature.
- Idempotent on `(pg_event_id)`.
- Reconciliation job for missed webhooks.

## Reseller-side

- Pikshipp invoices reseller monthly via NEFT typically; PG used optionally.
- Resellers may also offer their own seller-side recharge via the same PG (same vendor, separate sub-merchant).

## Failure modes

- PG outage → multi-vendor failover (manual switch v1; auto v2).
- Disputed charge → chargeback flow with PG.
- Webhook retry storm → idempotency + rate limit.

## Compliance

- PCI DSS scope minimized (hosted checkout = SAQ-A).
- RBI mandates for cards (additional auth) honored.
- Local data storage requirements (PG handles).

## Open questions

- **Q-PG1** — Native UPI vs PG-mediated UPI for cost? PG default v1.
- **Q-PG2** — Direct NACH / e-NACH for credit-line collections? v2.
- **Q-PG3** — Multi-currency for international expansion (v3+).
