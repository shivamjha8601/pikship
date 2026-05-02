# Pikshipp Testing Plan

The system has four test layers, each with a clear scope and gating policy.
Anything below the green bar at any layer blocks merging to `main`.

```
┌──────────────────────────────────────────────────────────────────────┐
│ L4   E2E browser flow (frontend ↔ backend ↔ DB)        manual gate  │
├──────────────────────────────────────────────────────────────────────┤
│ L3   API contract tests (httptest + testcontainers)     CI gate      │
├──────────────────────────────────────────────────────────────────────┤
│ L2   System tests (services + real Postgres)            CI gate      │
├──────────────────────────────────────────────────────────────────────┤
│ L1   Unit tests (in-memory, no Docker)                  CI gate      │
└──────────────────────────────────────────────────────────────────────┘
```

## L1 — Unit tests

**Scope.** Pure logic, in-memory only, sub-second per package. Run with
`-short`. CI gates on these every push.

**Coverage targets** — package, key invariants tested:

| Package           | Invariants                                                                |
|-------------------|---------------------------------------------------------------------------|
| `core`            | Paise math (Add/Sub overflow), typed-ID round-trip, JSON marshalers       |
| `core` types      | `HasAnyRole`, `StringSet.Has`, `Pincode.IsValid` (6-digit, no leading 0)   |
| `secrets`         | redaction in fmt/JSON, constant-time Equal, EnvStore key prefix mapping   |
| `auth`            | hashToken determinism, generateToken uniqueness, session cache LRU+TTL    |
| `audit`           | high-value action matching, hash chain forward verify, broken-chain detect |
| `policy`          | Value type round-trips, Definition uniqueness, cache TTL, lock vs override |
| `orders`          | every state transition in the FSM (allowed and disallowed)                |
| `tracking`        | normalise() across 13+ codes, dedupeHash collisions                       |
| `risk`            | composite score range, threshold-based block/review/allow                 |
| `notifications`   | template render, unknown kind error, all 7 kinds covered                  |
| `carriers/breaker`| closed→open→half-open→closed transitions, recovery on success            |
| `carriers/sandbox`| book idempotency, AWB uniqueness, SimulateFailure one-shot                |
| `wallet`          | sentinel errors present, Balance.Available math                           |

**Run.** `go test -short ./...`

## L2 — System Level Tests (SLTs)

**Scope.** Each test spins up a fresh Postgres 17 container via
testcontainers, applies all 20 migrations, then exercises real services.
No mocks except for non-determinism (rand). ~1.5s per test.

**Tests:**

| Test                                  | Flow                                                                  |
|---------------------------------------|-----------------------------------------------------------------------|
| `TestHappyPath_OrderToDelivery`       | seller seed → pickup → order → ready → allocating → book → in_transit → delivered → close. Asserts each state, AWB issued, buyer tracking token resolves, wallet untouched. |
| `TestNDR_OpenAndResolve`              | Order booked → carrier reports NDR → open case → seller requests reattempt → delivered_on_reattempt. State machine checked at each step. |
| `TestCOD_RegisterCollectRemit`        | COD shipment booked → MarkCollected → Remit. Verifies wallet gets credited the full COD amount. |
| `TestEnterpriseUpgrade_LiftsOrderLimit` | small_business seller (limit 200/day) → ChangeType to enterprise → contract Activate with terms → policy resolver returns 0 (unlimited) and insurance=true. Terminate reverts to type default. |

**Run.** `go test ./internal/slt/...`

## L3 — API contract tests

**Scope.** Spins up the **real chi router** (`NewAppRouter`) against the
testcontainer pool, then exercises HTTP endpoints via `httptest.Server`.
This catches:
- Middleware ordering (auth before seller-scope before idempotency)
- JSON shape regressions
- Error-code mapping (404/400/401/403/409/429/500)
- Token lifecycle (login → re-issue scoped → revoke)

**Tests:**

| Test                            | Flow                                                                                   |
|---------------------------------|----------------------------------------------------------------------------------------|
| `TestAPI_OrderLifecycle`        | dev-login → POST /sellers → POST /pickup-locations → POST /orders → GET → list → cancel. Plus 400 on empty body, 401 without auth. |
| `TestAPI_OrderLimitsEnforced`   | Seeds limit override of 2/day → POSTs 2 orders OK → 3rd returns 429 with body explaining cap. Also asserts /v1/seller/usage shows 2/2. |

**Run.** Same target as L2 (`go test ./internal/slt/...`).

## L4 — End-to-end browser flow (manual / CI nightly)

**Scope.** Validate the complete user journey through the actual UI.
Runs against backend + frontend dev servers, no mocks.

### E2E-1: New seller signup (the "happy path")

1. Visit `http://localhost:3000` → redirected to `/login`.
2. Type email + name, click "Continue".
3. Land on `/onboarding` step 1 (business details).
4. Fill legal name, display name, phone → click "Continue".
5. Step 2 (KYC): enter GSTIN + PAN → "Submit KYC".
6. Step 3 (Done): see green checkmark and "Go to dashboard".
7. Click → land on `/dashboard` with seller name in greeting.

**Pass criteria.** Each step transitions without errors, the new session
token is scoped to the seller after step 4, and `/v1/me` shows the seller
membership at step 7.

### E2E-2: Existing user with a seller

1. Sign back in with the same email (no name change required).
2. Land directly on `/dashboard` (skip onboarding).
3. Sidebar shows all 5 nav items (Dashboard, Orders, Tracking, Wallet, Enterprise).

### E2E-3: Order create and cancel

1. From `/dashboard` click "Create order".
2. Form is pre-filled with the default pickup location.
3. Add buyer details, address, two line items.
4. Submit → land on `/orders/{id}` with state=draft.
5. Click "Cancel order" → confirm dialog → state changes to cancelled.

**Pass criteria.** Order persists across refresh; cancelled orders do not
show "Cancel order" button anymore.

### E2E-4: Limit enforcement (negative path)

1. Set the seller's daily order limit to 1 (via DB seed or override API).
2. Create an order — succeeds.
3. Create a second order — UI shows the 429 error message containing "limit".

**Pass criteria.** No JS console errors; user sees a clear, recoverable
error state with the actual limit reason from the backend.

### E2E-5: Enterprise upgrade

1. From `/enterprise`, click "Upgrade to enterprise".
2. Confirm dialog → wait for spinner → page refreshes.
3. Plan card now shows "Enterprise" badge.
4. Active contract section appears, listing all 4 policy overrides
   with human-readable labels (e.g., "Orders / day: Unlimited", "Insurance: Enabled").
5. Capacity widget shows "Unlimited" for both caps.
6. Returning to `/orders/new` and creating an order does NOT trigger 429.

### E2E-6: Public tracking

1. `GET http://localhost:8081/track/awb/<some_awb>` (no auth).
2. Returns shipment state + carrier, no PII leak.

## L5 — Cross-flow scenarios (covered by L2/L3, documented here)

These prove subsystems compose correctly:

| Scenario                                                  | Where covered                                  |
|-----------------------------------------------------------|------------------------------------------------|
| Onboard → upgrade → place order at old limit → succeeds   | `TestEnterpriseUpgrade_LiftsOrderLimit` + manual |
| RLS isolation: seller A cannot see seller B's orders      | implicit in every SLT (testcontainer is fresh)  |
| Cache invalidation: DB override picked up on next resolve | `TestAPI_OrderLimitsEnforced` (sleeps 6s past TTL) |
| Token revocation: logout invalidates session              | manual in E2E-2                                 |

## Gates

- **PR merge:** L1 + L2 + L3 must be green in CI.
- **Release:** L4 manually executed by reviewer; report in the release issue.
- **Schema change:** must add migration with `up.sql` and `down.sql`,
  bump `RequiredSchemaVersion`, and add a paragraph to this doc if a
  new test surface is needed.

## Running locally

```bash
# Quick (L1)
cd backend && go test -short ./...

# Full (L1 + L2 + L3)
cd backend && go test -timeout 300s ./...

# Frontend
cd frontend && npx tsc --noEmit && npx next build

# Live E2E (manual)
cd backend && set -a && source .env && set +a
go run ./cmd/pikshipp/... --role=api &
cd ../frontend && npm run dev
# Browse http://localhost:3000
```
