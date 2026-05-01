# Channel adapter framework

> The integration framework that lets us add Shopify, Amazon, Meesho, ... adapters without churning the rest of the platform. Pairs with `04-features/03-channel-integrations.md` (the *what*); this doc is the *how*.

## Goal

A single, stable contract that every channel adapter implements. The OMS, ingestion, and downstream features depend only on the contract. New channels = new adapter implementations, same contract.

## The contract (logical)

```typescript
interface ChannelAdapter {
  // Identity
  readonly platform: PlatformId;
  readonly capabilities: Capabilities;

  // Connection
  authStart(seller: SellerCtx, params: ConnectParams): Promise<AuthInitResponse>;
  authComplete(seller: SellerCtx, callback: AuthCallback): Promise<ChannelCredentials>;
  authValidate(creds: ChannelCredentials): Promise<{ valid: boolean; reason?: string }>;
  authRefresh(creds: ChannelCredentials): Promise<ChannelCredentials>;

  // Webhook config
  registerWebhooks(creds: ChannelCredentials, urls: WebhookUrls): Promise<void>;
  verifyInbound(req: InboundReq): Promise<{ ok: boolean; eventType: ChannelEventType; eventId: string; payload: any }>;

  // Order ingest
  fetchOrders(creds: ChannelCredentials, since: Date, cursor?: string): AsyncIterable<RawOrder>;
  fetchOrder(creds: ChannelCredentials, channelOrderRef: string): Promise<RawOrder>;
  toCanonicalOrder(raw: RawOrder, ctx: SellerCtx): CanonicalOrder;

  // Outbound (status back to channel)
  markFulfilled(creds: ChannelCredentials, channelOrderRef: string, payload: FulfillmentPayload): Promise<void>;
  markDelivered?(creds: ChannelCredentials, channelOrderRef: string): Promise<void>;
  cancelOrder(creds: ChannelCredentials, channelOrderRef: string, reason?: string): Promise<void>;

  // Catalog (optional, v2)
  fetchProducts?(creds: ChannelCredentials): AsyncIterable<RawProduct>;
}
```

## Capabilities declaration

Every adapter declares what the underlying channel supports:

```yaml
capabilities:
  webhooks: order_create | order_update | order_cancel | refund_create
  polling_required: false
  fulfillment_modes: [fulfilled, delivered]
  cancel_order: true
  partial_fulfillment: true
  pii_redaction:
    buyer_email: never | sometimes | always   # Amazon = always
    buyer_phone: never | sometimes | always
  multi_store_per_seller: true
  rate_limits:
    requests_per_minute_max: 250
  schema_quirks:
    has_address_line2: true
    multi_currency_in_order: false
```

Downstream features read capabilities to adapt UI/policy ("show 'reconnect' button if `webhooks=false`", etc.).

## Common helpers across adapters

- **HMAC verification** utilities.
- **Rate limiter** (token bucket per channel).
- **Polling worker** with checkpoint state.
- **Idempotency** by `(channel_id, event_id)` and `(channel_id, channel_order_ref)`.
- **Schema validators** (per-channel JSON schemas).
- **Address normalizer** (pincode validation, state matching).

## Versioning

- Adapters are versioned (`shopify_v3`, `amazon_v1`).
- Multiple versions can coexist (canary).
- Tenant pin to a version supported.

## Onboarding a new channel — checklist

1. Adapter scaffolding (capabilities, methods).
2. Auth flow (OAuth or API key).
3. Webhook config (if available).
4. Polling worker (if not).
5. RawOrder → CanonicalOrder mapping.
6. Schema tests with sample fixtures.
7. Outbound fulfillment update.
8. Cancellation flow.
9. Sandbox / test environment integration.
10. Ops runbook for adapter-specific issues.

## Per-channel idiosyncrasies (excerpt)

| Channel | Idiosyncrasies |
|---|---|
| Shopify | GraphQL preferred for some queries; REST for webhooks; cost-based rate limiting |
| Amazon SP-API | LWA + IAM; SQS for events; PII redaction; marketplace-specific endpoints |
| WooCommerce | Self-hosted; HTTP basic vs JWT; older sites unreachable; plugin variance |
| Magento | OAuth 1.0a (legacy) / 2.0 (newer); REST + extension webhooks |
| Flipkart | Polling-heavy; "Smart Fulfillment" exclusion |
| Meesho | Younger API; some endpoints rate-limited tightly |
| Custom (API) | We expose Pikshipp public API as an inbound channel for sellers to push orders |
| Manual / CSV | Synthetic adapter — no external system |

## Testing

- Per-adapter test suite with golden fixtures (real anonymized payloads).
- Live sandbox tests where channel offers them.
- CI integration tests run on adapter changes.

## Observability

- Per-channel metrics: ingestion lag, error rate, auth-fail count, throughput.
- Surfaced in seller's channel health card and Pikshipp ops dashboard.

## Failure handling

- Auth failure → channel marked `auth_expired`; seller prompted; ingestion paused.
- Schema drift → adapter version bump; canary cutover.
- Channel API outage → backoff; resume on recovery.

## Open questions

- **Q-CAF1** — Adapter SDK in a separate repo (community contributions)? Default: monorepo v1.
- **Q-CAF2** — Per-tenant adapter overrides (e.g., one seller wants different webhook scopes)? Default: not v1.
