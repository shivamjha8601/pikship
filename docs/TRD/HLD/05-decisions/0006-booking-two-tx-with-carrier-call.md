# ADR 0006 — Booking is two transactions with carrier call between

Date: 2026-04-30
Status: Accepted
Owner: Architect A (with Architect B push-back)

## Context

Booking a shipment is a multi-step operation:
1. Validate the order.
2. Reserve wallet funds.
3. Call the carrier API to create AWB.
4. Persist the AWB and finalize the wallet charge.

The carrier API call takes ~1–3 seconds and can fail. We have to choose how to span transactions across this work.

Options:
- **Single transaction**: hold a DB tx through the carrier API call. Lock contention; tx-timeout risk. **Rejected.**
- **No transaction**: write everything optimistically, repair via async reconcile. Loses atomicity. **Rejected.**
- **Two transactions**: tx1 reserves and writes a `pending_carrier` shipment + outbox; carrier API call outside; tx2 finalizes (sets AWB, confirms wallet, emits success outbox) or rolls back (releases hold, marks failed, emits failure outbox). **Chosen.**

Architect B pushed on the explicit failure-path spec — added below.

## Decision

**Two transactions, with the carrier API call between them, plus a reconcile cron.**

```
Tx1 (idempotent):
  - load order; validate
  - pricing.Quote
  - allocation.Allocate (writes allocation_decision)
  - wallet.Reserve (writes wallet_hold)
  - shipments.Create(status='pending_carrier')
  - outbox.Emit('shipment.pending')
  - COMMIT

Carrier API call (no DB tx):
  - carrier.Adapter.Book(...)
  - 3s timeout

Tx2 (idempotent):
  - if success:
      shipments.SetAWB
      wallet.Confirm
      outbox.Emit('shipment.booked')
  - if fatal failure:
      shipments.MarkFailed
      wallet.Release
      outbox.Emit('shipment.booking_failed')
  - if retryable failure:
      do nothing in tx2; reconcile cron will handle
  - COMMIT

Reconcile cron (every 5 min):
  - SELECT shipments WHERE status='pending_carrier' AND created_at < now() - interval '5 min'
  - for each:
    - query carrier API by our internal idempotency key
    - if carrier has AWB: tx2 success path
    - if carrier rejected: tx2 fatal path
    - if carrier hasn't seen the request: re-attempt book
```

Both tx1 and tx2 are scoped under `app.seller_id` via RLS middleware.

## Alternatives considered

### Single transaction holding through carrier call
- Rejected: 1-3s lock; under load, this exhausts DB connections and serializes per-seller.

### Pre-allocated AWB pool
- Some carriers let us reserve AWBs in bulk. Sometimes useful; not all carriers support; out of scope for v0.

### Optimistic write with eventual reconciliation
- Rejected: the seller's wallet is debited; we owe them an AWB. Asynchrony in the user's response is bad UX.

## Consequences

### What this commits us to
- Reconcile cron must run reliably.
- Wallet operations are idempotent on `(ref_type, ref_id, direction)`.
- Carrier adapters must support idempotent book calls (most do; adapter abstracts where needed).
- Status `pending_carrier` is a real state, surfaced in admin console.

### What it costs
- Booking handler is ~200 lines longer than a "single tx" version.
- Two state transitions per booking (pending → booked/failed).
- Reconcile cron is one more thing to monitor.

### What it enables
- Robust failure handling.
- No long DB transactions.
- Clear ownership of partial-failure recovery.

## Failure modes addressed (per Architect B)

| Failure | Behavior |
|---|---|
| Tx1 commits, carrier API hangs forever | Hold expires (TTL), reconcile cron either retries carrier book or marks failed |
| Tx1 commits, carrier returns AWB, Tx2 fails (DB blip) | Reconcile cron queries carrier by our idempotency key, finds AWB, completes Tx2 |
| Tx1 commits, carrier returns retryable error | Tx2 not run; reconcile cron retries carrier; eventually success or fatal classification |
| Tx1 commits, network glitch returns "unknown" status | Reconcile cron queries carrier; treats as canonical reality |
| Tx2 partial success (AWB set, wallet confirm fails) | Idempotent retry of wallet confirm; UNIQUE constraint prevents double-charge |

## Performance

- Tx1: P95 < 100ms (small writes, no external).
- Carrier call: P95 budget 3s.
- Tx2: P95 < 100ms.
- Total booking: P95 < 4s end-to-end (carrier permitting).

Sync booking is for one-at-a-time. Bulk-book uses river jobs (different flow; same per-shipment logic).

## Open questions

- For carriers that don't support idempotent book by our key: how do we deduplicate? Some return our "request reference" we can search by. For carriers with no such mechanism, reconcile cron may double-book in catastrophic failure. Mitigation: for those carriers, the cron is more conservative (long delay before retry); we accept the small risk. Document per-carrier in adapter.
