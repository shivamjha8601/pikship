# Pikshipp

Multi-courier shipping aggregator for India. End-to-end logistics platform with
seller onboarding, order management, allocation, tracking, NDR, COD, RTO,
weight reconciliation, and enterprise contracts.

## Stack

**Backend** — Go 1.25, PostgreSQL 17 with Row-Level Security, chi router,
pgx, River for background jobs, Prometheus metrics, structured slog logs.

**Frontend** — Next.js 15 (App Router), React 19, TypeScript, TailwindCSS.

## Repo layout

```
backend/                  Go service (single binary, --role=api|worker|all)
  api/http/               chi router + middleware + handlers
  cmd/pikshipp/           main binary
  internal/
    audit/                hash-chained per-seller audit log
    auth/                 opaque session auth + middleware
    contracts/            per-seller contracts that push policy overrides
    core/                 typed UUIDs, Paise money, errors
    identity/             users, OAuth links, seller memberships, invites
    limits/               usage caps enforcement (orders/day, shipments/month)
    orders/               canonical order + 9-state FSM
    policy/               configurability substrate; 22 keys; 5s TTL cache
    seller/               lifecycle FSM, KYC
    shipments/            two-phase booking (pending_carrier → booked)
    slt/                  System-Level Tests using testcontainers
    wallet/               two-phase Reserve/Confirm/Release
    ...                   pricing, allocation, carriers, tracking, ndr, cod, rto, recon, ...
  migrations/             20 migrations covering full schema
frontend/                 Next.js app
  src/app/                routes (login, onboarding, dashboard, orders, ...)
  src/components/         Shell + UI primitives (Button, Card, Input, ...)
  src/lib/api.ts          typed API client
docs/                     TRD/LLD specs (52 documents, 37k+ lines)
```

## Local development

### Prerequisites

- Docker (for Postgres)
- Go 1.25+
- Node 20+
- `golang-migrate` (binary at `/Users/ravisharma/work/bin/migrate` in dev)

### Boot Postgres + apply migrations

```bash
cd backend
make db-bootstrap        # creates pikshipp_dev DB + login user (idempotent)
make db-grant-roles      # grants pikshipp_app/reports/admin to dev user
PGPASSWORD=root /Users/ravisharma/work/bin/migrate \
  -path migrations \
  -database "postgres://root:root@127.0.0.1:5432/pikshipp_dev?sslmode=disable" up
```

### Run backend

```bash
cd backend
cp .env.example .env     # if you don't have one
go run ./cmd/pikshipp/... --role=api
# Listens on :8081 (per .env)
```

### Run frontend

```bash
cd frontend
npm install
npm run dev
# Open http://localhost:3000
```

The frontend proxies `/api/v1/*` → backend `/v1/*` (see `next.config.mjs`),
so no CORS plumbing is needed in dev.

## Tests

### Unit tests (fast, no Docker)

```bash
cd backend
go test -short ./...
```

14 packages with tests, covering core, auth, audit, secrets, policy,
orders, tracking, wallet, risk, notifications, allocation, carriers, sandbox.

### System-Level Tests (requires Docker)

```bash
cd backend
go test ./internal/slt/...
```

Each SLT spins up a Postgres 17 container via testcontainers, runs all
migrations, and exercises real services. Tests included:

- `TestHappyPath_OrderToDelivery` — draft → ready → allocating → booked
  → in_transit → delivered → closed; buyer tracking token issued
- `TestNDR_OpenAndResolve` — NDR case open → reattempt → delivered_on_reattempt
- `TestCOD_RegisterCollectRemit` — COD lifecycle → wallet credited
- `TestEnterpriseUpgrade_LiftsOrderLimit` — small_business → enterprise,
  contract activates, limits go from 200/day → unlimited, insurance enabled
- `TestAPI_OrderLifecycle` — full HTTP round-trip via httptest.Server
- `TestAPI_OrderLimitsEnforced` — 429 response when daily cap exceeded

### CI

`.github/workflows/ci.yml` runs build + vet + tests against Postgres 17
on every PR.

## Onboarding flow (live)

```bash
# 1. Login (dev mode bypass for Google OAuth)
curl -X POST http://localhost:8081/v1/auth/dev-login \
  -H "Content-Type: application/json" \
  -d '{"email":"founder@example.com","name":"Founder"}'
# → returns token

# 2. Provision a seller (auth required)
curl -X POST http://localhost:8081/v1/sellers \
  -H "Authorization: Bearer <token>" \
  -d '{"legal_name":"Demo Co","display_name":"Demo","primary_phone":"+919999"}'
# → returns seller + new seller-scoped token

# 3. Submit KYC
curl -X POST http://localhost:8081/v1/seller/kyc \
  -H "Authorization: Bearer <new_token>" \
  -d '{"legal_name":"Demo Co","gstin":"29AABCU9603R1ZX","pan":"AABCU9603R"}'
```

Or just visit http://localhost:3000 and click through the UI.

## Enterprise upgrade

```bash
# Operator (auth required) upgrades a seller atomically:
# - changes seller_type → enterprise
# - creates a contract with the supplied terms
# - activates the contract → pushes terms.policy_overrides into per-seller
#   policy overrides (limits, features, credit limit, etc.)
curl -X POST "http://localhost:8081/v1/admin/sellers/<seller_id>/upgrade" \
  -H "Authorization: Bearer <token>" \
  -d '{
    "new_type": "enterprise",
    "terms": {
      "policy_overrides": {
        "limits.orders_per_day": 0,
        "limits.shipments_per_month": 0,
        "features.insurance": true,
        "wallet.credit_limit_inr": 50000000
      },
      "monthly_minimum_paise": 100000000,
      "sla_delivered_p95_days": 3
    }
  }'
```

After upgrade:
- `GET /v1/seller/usage` returns `{shipment_month_limit: 0, order_day_limit: 0}` (unlimited)
- `GET /v1/seller/contract` returns the active contract
- `POST /v1/orders` no longer 429s at 200/day

Termination via `contracts.Terminate` reverses all overrides cleanly.

## Architecture highlights

- **Three-role pool design**: `pikshipp_app` (RLS-enforced), `pikshipp_reports`
  (BYPASSRLS for cross-seller aggregates), `pikshipp_admin` (BYPASSRLS for ops).
- **`set_config('app.seller_id', uuid, true)`** at transaction start drives RLS
  policies via `app.current_seller_id()`.
- **Hash-chained audit log** (SHA-256 per seller) — verifier sweeps weekly.
- **Outbox pattern** — domain writes commit atomically with `outbox_event`;
  forwarder polls `FOR UPDATE SKIP LOCKED` and enqueues River jobs.
- **Two-phase wallet** — Reserve/Confirm/Release with idempotent ledger entries.
- **Two-phase booking** — pending_carrier row persisted before carrier API call,
  AWB stamped on success, retried on transient failure.
- **Contract enforcement** — `contracts.Activate` writes terms into
  `policy_seller_override`; `policy.Resolve` reads the layered chain
  (lock > seller_override > seller_type default > global default).
- **Limits guard** — `limits.Guard.CheckOrderDay/Month` reads policy + counts
  to enforce caps before mutation.
