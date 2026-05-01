# HLD Overview — system narrative

> The bridge from PRD product architecture to the technical realization. Read after [`00-tenets.md`](./00-tenets.md).

## What we're building, technically

A **Go monolith** running on a **single EC2 instance** at v0 (multi-instance + ALB at v1), backed by a **multi-AZ AWS RDS Postgres**, serving a **Next.js dashboard** over an **HTTP/JSON API**, with all background work running **inside the same binary** via `river`. The system handles seller signup → KYC → channel ingestion → order management → carrier allocation → shipment booking → tracking → NDR/RTO → COD → wallet → reports.

No queues. No Redis. No Kafka. No microservices. No service mesh. The simplest set of moving parts that can sustain v1's 50k shipments/month and grow to v2's 250k.

## The shape

```
                        ┌─────────────────────────────┐
                        │  Next.js dashboard (S3+CF)  │
                        │  + buyer pages              │
                        └──────────────┬──────────────┘
                                       │ HTTPS
                            ┌──────────▼──────────┐
                            │   ALB (TLS term)    │
                            └──────────┬──────────┘
                                       │
              ┌────────────────────────▼────────────────────────┐
              │            Pikshipp monolith (EC2)              │
              │                                                 │
              │  HTTP API ─┐                                    │
              │  Webhooks ─┼─► chi router                       │
              │            │                                    │
              │            ▼                                    │
              │   Auth (sessions) → Seller scope → Domain svc   │
              │                                                 │
              │  River runner (in same process at v0)           │
              │   ├ outbox forwarder                            │
              │   ├ tracking polling                            │
              │   ├ NDR deadline sweeper                        │
              │   ├ COD recon                                   │
              │   ├ wallet invariant check                      │
              │   └ audit chain verifier                        │
              └─────────────────┬───────────────────────────────┘
                                │
                  ┌─────────────┴───────────────┐
                  │                             │
         ┌────────▼─────────┐         ┌─────────▼─────────┐
         │   AWS RDS PG     │         │   S3 (Mumbai)     │
         │  (multi-AZ)      │         │   labels, photos, │
         │                  │         │   raw payloads    │
         └──────────────────┘         └───────────────────┘

External:  Google OAuth · MSG91 (SMS) · SES (email) · Razorpay (PG) ·
           Karza (KYC, deferred to v1) · Carrier APIs (Delhivery first)
```

## How a request flows through

### Flow 1 — seller books a shipment (sync API)

```
POST /v1/shipments + Idempotency-Key
       │
       ▼
[Auth middleware] resolve session → set ctx{user_id, seller_id, roles}
       │
       ▼
[Idempotency middleware] lookup (seller_id, key) → if cached, return
       │
       ▼
[Seller scope middleware] BEGIN TX; SET LOCAL app.seller_id = ...
       │
       ▼
[Handler] orders.Get → policy.Resolve(rate_card, allowed_carriers) →
          allocation.Allocate → wallet.Reserve →
          shipments.Create(pending_carrier) →
          outbox.Emit("shipment.pending") → COMMIT
       │
       ▼
[Handler] carriers.<X>.Book(...)  ← external API call, NO TX
       │
       ▼
[Handler] BEGIN TX; SET LOCAL app.seller_id = ...
          shipments.SetAWB(...) → wallet.Confirm(hold) →
          outbox.Emit("shipment.booked") → COMMIT
       │
       ▼
[Idempotency middleware] cache response
       │
       ▼
Return 201 { shipment_id, awb, label_url }

Asynchronously (driven by outbox):
  notifications.SendBuyerTrackingLink, audit.Emit, ...
```

### Flow 2 — carrier sends a tracking webhook (async)

```
POST /webhooks/delhivery (HMAC signed)
       │
       ▼
[Webhook handler] verify HMAC; classify event_id;
                  INSERT carrier_event ON CONFLICT idempotency UNIQUE → DO NOTHING
       │
       ▼
return 200 immediately (carrier expects fast ack)
       │
       ▼
Asynchronously (river job triggered):
  parse → tracking.Ingest →
  IF state transition is canonical-monotonic:
    UPDATE shipment.status, INSERT shipment_status_history, INSERT outbox_event
       │
       ▼
Outbox forwarder picks up event → spawns notification job, NDR job, etc.
```

### Flow 3 — wallet recharge (PG webhook)

```
POST /webhooks/razorpay/payment.captured (signed)
       │
       ▼
verify; idempotent UPSERT into recharge_event
       │
       ▼
BEGIN TX; wallet.Post(credit, ref="recharge:<pg_event_id>");
          outbox.Emit("wallet.recharged") → COMMIT
```

## Module map (Go packages)

```
internal/
├── core/              ← pure types: Money, Pincode, Address, Order, ...
│
├── auth/              ← Authenticator interface + OpaqueSessionAuth impl
├── policy/            ← settings resolution
├── audit/             ← audit emit + hash chain verifier
├── idempotency/       ← request idempotency middleware
├── outbox/            ← outbox emit + forwarder
│
├── identity/          ← users, OAuth, sessions
├── seller/            ← seller orgs, sub-sellers, lifecycle
├── channels/          ← framework + per-platform adapters
│   ├── shopify/
│   ├── woocommerce/
│   ├── amazon/
│   ├── manual/
│   └── csv/
├── catalog/           ← products, pickup locations
├── orders/            ← canonical orders, validation, routing rules
├── carriers/          ← framework + per-carrier adapters
│   ├── delhivery/
│   ├── bluedart/      (v1+)
│   └── sandbox/       (test fixture)
├── pricing/           ← rate cards, quotes
├── allocation/        ← carrier picking
├── shipments/         ← booking, manifests, labels
├── tracking/          ← status normalization, webhooks, polling
├── ndr/               ← NDR action engine
├── rto/               ← returns + RTO
├── cod/               ← COD verification + remittance
├── wallet/            ← ledger, two-phase ops
├── recon/             ← weight reconciliation
├── reports/           ← aggregations (uses BYPASSRLS role)
├── notifications/     ← multi-vendor sender, templates
├── buyerexp/          ← buyer-facing public pages
├── support/           ← tickets
├── admin/             ← internal ops console backend
├── risk/              ← rules-based risk signals (v1 minimum)
└── contracts/         ← contract storage, e-sign

api/
├── http/              ← chi router + handlers (thin layer over internal/)
├── webhooks/          ← per-source webhook receivers
└── openapi.yaml

cmd/pikshipp/main.go   ← wiring + flag handling
migrations/            ← .sql files
```

**Strict dependency direction:** `internal/<module>` may import:
- `internal/core` (always).
- Other modules' **public interfaces** (their root `.go` files).
- Cross-cutting modules (`auth`, `policy`, `audit`, `outbox`, `idempotency`).

It may **NOT** import another module's internal packages or DB code. Enforced by linting (`internal` packages already enforce part of this).

## Three operational modes from one binary

| Flag | What it runs | Used at |
|---|---|---|
| `--role=api` | HTTP server, webhook receivers, no jobs | v1+ multi-instance |
| `--role=worker` | River job runner, outbox forwarder, no HTTP except `/healthz` | v1+ multi-instance |
| `--role=all` | Both | v0 single instance; local dev |

The split exists so we can scale API and worker independently. **Same binary, same code, different flags.**

## What ships at v0 (echoing PRD)

- Identity + Google OAuth + sessions.
- Seller signup + manual KYC (Karza deferred).
- Channels: manual + CSV + Shopify.
- Carriers: Delhivery only.
- Pricing engine + Allocation engine.
- Shipment booking + tracking (webhook) + NDR detection (no auto-actions yet).
- Wallet (recharge + reserve/confirm/release) + RTO charge stub.
- Audit + outbox + idempotency.
- Admin console (basic CRUD + investigation).

## What ships at v1 (echoing PRD)

Everything above, plus:
- WooCommerce + Amazon channels.
- 7 more carriers (Bluedart, DTDC, Ekart, Xpressbees, Ecom Express, Shadowfax, India Post).
- Allocation engine multi-objective.
- COD verification + remittance.
- NDR action loop + auto-rules.
- Returns/RTO portal.
- Weight reconciliation.
- Reports.
- Multi-instance + ALB.
- Karza integration.

## Where to read next

1. [`01-architecture/01-monolith-shape.md`](./01-architecture/01-monolith-shape.md) — package layout, dependency rules.
2. [`01-architecture/02-data-and-transactions.md`](./01-architecture/02-data-and-transactions.md) — RLS, transaction patterns, money invariants.
3. [`01-architecture/03-async-and-outbox.md`](./01-architecture/03-async-and-outbox.md) — outbox + river details.
4. [`01-architecture/04-multi-instance-readiness.md`](./01-architecture/04-multi-instance-readiness.md) — every piece of in-process state, multi-instance safety audit.
5. [`02-data-model.md`](./02-data-model.md) — physical schema by domain.
6. [`03-services/`](./03-services/) — per-module deep dives (six biggest first).
7. [`04-cross-cutting/`](./04-cross-cutting/) — auth, observability, testing, deployment, resilience.
8. [`05-decisions/`](./05-decisions/) — ADRs, including the JWT-vs-sessions reversal rationale.
