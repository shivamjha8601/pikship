# Domain event catalog

> Every event our outbox emits, with its payload schema and consumers. Single source of truth — code review checks references against this list.

## Conventions

- Event kinds are dotted: `<aggregate>.<event_name>`.
- Payload is JSONB; schema versioned as `v` field.
- Every event carries `seller_id`, `occurred_at`, `event_id` (UUID).
- Consumers identified by their river job kind (`<consumer>.<job>`).
- Events are **append-only**. We never modify or delete an event from the outbox.

## Catalog

### Identity & seller lifecycle

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `seller.created` | Tx that creates a seller record | `{seller_id, primary_user_id}` | `audit.write`, `notifications.welcome_seller` |
| `seller.kyc_approved` | KYC application moves to approved | `{seller_id, kyc_application_id, approved_by, tier}` | `audit.write`, `notifications.kyc_outcome`, `seller.activate` |
| `seller.kyc_rejected` | KYC application moves to rejected | `{seller_id, kyc_application_id, reason}` | `audit.write`, `notifications.kyc_outcome` |
| `seller.suspended` | Status transitions to suspended | `{seller_id, reason, suspended_by}` | `audit.write`, `notifications.seller_status_change` |
| `seller.reactivated` | Status transitions to active from suspended | `{seller_id, reactivated_by}` | `audit.write`, `notifications.seller_status_change` |
| `user.invited` | Owner invites a user to seller | `{seller_id, user_id, email, role}` | `notifications.invite_email` |
| `session.revoked` | Session marked revoked (PG NOTIFY only, not outbox) | n/a | in-process cache invalidation |

### Channels

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `channel.connected` | Seller connects a channel | `{seller_id, channel_id, platform}` | `audit.write`, `channel.backfill_orders` |
| `channel.auth_expired` | Adapter detects 401 | `{seller_id, channel_id, platform}` | `audit.write`, `notifications.channel_reconnect` |
| `channel.disconnected` | Seller or platform disconnects | `{seller_id, channel_id, reason}` | `audit.write`, `notifications.channel_disconnected` |

### Orders

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `order.ingested` | Channel adapter writes a canonical order | `{seller_id, order_id, channel_id, channel_order_ref}` | `audit.write`, `risk.score_order` |
| `order.validated` | Validation pipeline runs | `{seller_id, order_id, blocks_count, warnings_count}` | `audit.write` |
| `order.held` | Order placed in `on_hold` (KYC/wallet/policy) | `{seller_id, order_id, reason}` | `audit.write` |
| `order.cancelled` | Pre-booking cancel | `{seller_id, order_id, by, reason}` | `audit.write`, `notifications.order_cancelled` |

### Shipments

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `shipment.pending` | Tx1 of booking commits | `{seller_id, order_id, shipment_id, carrier_id, hold_id}` | `audit.write` (only) |
| `shipment.booked` | Tx2 of booking commits with AWB | `{seller_id, order_id, shipment_id, carrier_id, awb, ledger_entry_id}` | `audit.write`, `notifications.buyer_tracking_link`, `notifications.seller_booking_confirmed`, `reports.aggregate_shipment` |
| `shipment.booking_failed` | Tx2 of booking commits with failure | `{seller_id, order_id, shipment_id, error_class, hold_id}` | `audit.write`, `notifications.seller_booking_failed` |
| `shipment.cancelled` | Pre-pickup cancel (carrier-side) | `{seller_id, shipment_id, reason}` | `audit.write`, `notifications.shipment_cancelled` |
| `shipment.picked_up` | Canonical status transition | `{seller_id, shipment_id, occurred_at, location}` | `audit.write`, `notifications.buyer_picked_up` |
| `shipment.in_transit` | Canonical status transition | `{seller_id, shipment_id, occurred_at, location}` | `audit.write` |
| `shipment.out_for_delivery` | Canonical status transition | `{seller_id, shipment_id, occurred_at}` | `audit.write`, `notifications.buyer_ofd` |
| `shipment.delivered` | Canonical status transition | `{seller_id, shipment_id, occurred_at, delivered_at}` | `audit.write`, `notifications.buyer_delivered`, `notifications.seller_delivered`, `cod.schedule_remittance`, `recon.fetch_reweigh`, `reports.aggregate_shipment` |
| `shipment.ndr` | Canonical status transition to NDR | `{seller_id, shipment_id, attempt_no, reason, occurred_at}` | `audit.write`, `ndr.create_event`, `notifications.buyer_ndr_outreach`, `notifications.seller_ndr` |
| `shipment.rto_initiated` | Canonical status transition | `{seller_id, shipment_id, reason, initiated_by}` | `audit.write`, `notifications.seller_rto`, `wallet.charge_rto` |
| `shipment.rto_in_transit` | Canonical status transition | `{seller_id, shipment_id, occurred_at}` | `audit.write` |
| `shipment.rto_delivered` | RTO arrives back at seller | `{seller_id, shipment_id, occurred_at}` | `audit.write`, `notifications.seller_rto_delivered`, `rto.create_qc_pending` |
| `shipment.lost` | Tracking declares lost | `{seller_id, shipment_id, last_known_location}` | `audit.write`, `notifications.seller_lost`, `insurance.evaluate_claim` |
| `shipment.damaged` | Tracking declares damaged | `{seller_id, shipment_id}` | `audit.write`, `notifications.seller_damaged`, `insurance.evaluate_claim` |

### NDR

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `ndr.action_taken` | Seller, buyer, or rule submits an action | `{seller_id, ndr_event_id, action_type, initiated_by}` | `audit.write`, `carrier.request_ndr_action` (river job) |
| `ndr.deadline_expired` | Sweeper found NDR past deadline; auto-action fired | `{seller_id, ndr_event_id, action_taken}` | `audit.write` |
| `ndr.buyer_responded` | Buyer used reschedule page | `{seller_id, ndr_event_id, action_chosen, slot}` | `audit.write`, `carrier.request_ndr_action` |

### Wallet

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `wallet.recharged` | PG webhook commits a credit | `{seller_id, wallet_id, amount_minor, ledger_entry_id, gateway, gateway_ref}` | `audit.write`, `notifications.wallet_recharged` |
| `wallet.charged` | Direct or two-phase confirm posts a debit | `{seller_id, wallet_id, amount_minor, ref_type, ref_id, ledger_entry_id}` | `audit.write` |
| `wallet.refunded` | Reversal entry posted | `{seller_id, wallet_id, amount_minor, reverses_id, ledger_entry_id}` | `audit.write`, `notifications.wallet_refunded` |
| `wallet.adjusted` | Manual ops adjustment | `{seller_id, wallet_id, amount_minor, direction, reason, by}` | `audit.write` (high-value), `notifications.wallet_adjusted` |
| `wallet.low_balance_alert` | Balance drops below threshold | `{seller_id, balance_minor, threshold_minor}` | `notifications.wallet_low_balance` |
| `wallet.grace_cap_breached` | Reverse-leg debit exceeds grace | `{seller_id, attempted_amount_minor, current_balance_minor, grace_cap_minor}` | `audit.write`, `seller.suspend` (signals downstream) |
| `wallet.invariant_check_failed` | Daily invariant cron found drift | `{seller_id, wallet_id, computed_minor, cached_minor, diff_minor}` | `audit.write`, `ops.alert_critical` |

### COD

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `cod.verification_sent` | Buyer outreach for confirm initiated | `{seller_id, order_id, channel}` | `audit.write` |
| `cod.verification_confirmed` | Buyer confirmed | `{seller_id, order_id}` | `audit.write` |
| `cod.verification_cancelled` | Buyer cancelled | `{seller_id, order_id}` | `audit.write`, `order.cancel` |
| `cod.remittance_due` | Scheduled at delivery | `{seller_id, shipment_id, amount_minor, due_at}` | `wallet.post_cod_remit` (river job at due time) |
| `cod.remittance_paid` | Wallet credit applied | `{seller_id, shipment_id, ledger_entry_id}` | `audit.write` |
| `cod.remittance_mismatch` | Carrier file disagrees with our records | `{seller_id, shipment_id, expected_minor, received_minor}` | `audit.write`, `ops.alert_warning` |

### Weight reconciliation

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `weight.discrepancy_detected` | Carrier reweigh > declared by tolerance | `{seller_id, shipment_id, declared_g, carrier_g, variance_pct}` | `audit.write` |
| `weight.dispute_raised` | We submitted dispute to carrier | `{seller_id, shipment_id, carrier_dispute_ref}` | `audit.write` |
| `weight.dispute_resolved` | Carrier ruled | `{seller_id, shipment_id, outcome, refund_minor}` | `audit.write`, `wallet.post_dispute_reversal` (if won) |

### Insurance (v1+)

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `insurance.attached` | Policy created at booking | `{seller_id, shipment_id, policy_id, premium_minor}` | `audit.write` |
| `insurance.claim_filed` | Seller files a claim | `{seller_id, shipment_id, policy_id, reason}` | `audit.write` |
| `insurance.claim_resolved` | Insurer rules | `{seller_id, shipment_id, outcome, payout_minor}` | `audit.write`, `wallet.post_insurance_payout` (if approved) |

### Risk

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `risk.flagged` | Risk score above threshold; action=hold | `{seller_id, scope, scope_ref, score, bucket}` | `audit.write` |
| `risk.behavioral_anomaly` | Daily seller monitoring detected outlier | `{seller_id, anomaly_kind, value, baseline}` | `audit.write`, `ops.alert_warning` |

### Operational

| Event | When emitted | Payload | Consumers |
|---|---|---|---|
| `ops.queue_alert.river` | Queue depth exceeded | `{depth, threshold, by_kind: {...}}` | log only at v0; PD at v1 |
| `ops.queue_alert.outbox` | Outbox pending count exceeded | `{depth, threshold}` | log only at v0; PD at v1 |
| `ops.alert_critical` | High-priority operational issue | `{kind, message, severity}` | log + email at v0; PD at v1 |
| `ops.alert_warning` | Lower-priority operational issue | `{kind, message}` | log at v0 |

## How events get emitted

```go
package outbox

type Event struct {
    ID         uuid.UUID
    SellerID   *core.SellerID  // nullable for platform events
    Kind       string           // dotted, lowercase
    Version    int              // schema version; default 1
    Payload    json.RawMessage  // event-specific
    OccurredAt time.Time
}

// Always called inside an existing transaction.
func Emit(ctx context.Context, tx *pgx.Tx, e Event) error
```

A handler in `internal/<module>/events.go` defines the typed payload struct + helper:

```go
package wallet

type RechargedPayload struct {
    SellerID       core.SellerID  `json:"seller_id"`
    WalletID       uuid.UUID      `json:"wallet_id"`
    AmountMinor    core.Paise     `json:"amount_minor"`
    LedgerEntryID  uuid.UUID      `json:"ledger_entry_id"`
    Gateway        string         `json:"gateway"`
    GatewayRef     string         `json:"gateway_ref"`
}

func EmitRecharged(ctx context.Context, tx *pgx.Tx, p RechargedPayload) error {
    payload, _ := json.Marshal(p)
    return outbox.Emit(ctx, tx, outbox.Event{
        ID: uuid.New(),
        SellerID: &p.SellerID,
        Kind: "wallet.recharged",
        Version: 1,
        Payload: payload,
        OccurredAt: time.Now(),
    })
}
```

## Adding a new event

1. Pick a name following the `<aggregate>.<event_name>` convention.
2. Add it to this catalog with: emit point, payload, consumers.
3. Add the typed payload struct + helper in `internal/<module>/events.go`.
4. Add the consumer river job(s) in the consuming module's `jobs.go`.
5. Wire the consumer in `cmd/pikshipp/main.go`.
6. Test SLT covering emit + consume.

## Removing or evolving an event

- **Adding fields**: backwards-compatible. Bump `Version`.
- **Removing fields**: breaking. Bump `Version`; old consumers must continue to work for the deprecation cycle.
- **Renaming**: emit both for one release; remove old after.
- **Outright removing**: only after no consumer references it for ≥1 release.

The events table schema only expects `kind` and JSON payload; consumers do the schema validation.
