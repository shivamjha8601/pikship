# Cross-cutting: Authn & authz

> Quick reference for how authentication and authorization work across the platform. Detailed service spec in [`../03-services/06-authn-authz.md`](../03-services/06-authn-authz.md).

## TL;DR

- **Authentication** is opaque server-side sessions at v0, behind the `auth.Authenticator` interface.
- **Authorization** is RBAC: roles per `(user, seller)` checked at handler boundary via `auth.RequireRole(...)`.
- **Pluggable**: swap to JWT or any other scheme by implementing `auth.Authenticator` and changing wiring in `main.go`.

## Roles

### Seller-side
- `Owner` — full control.
- `Manager` — operational + non-financial.
- `Operator` — orders, bookings, NDR.
- `Finance` — wallet, invoices, disputes.
- `ReadOnly` — view all; create nothing.

### Pikshipp-side (cross-seller)
- `pikshipp_admin` — everything; audit-logged elevation.
- `pikshipp_ops` — operational across sellers (KYC review, manual interventions).
- `pikshipp_support` — read with consent-based impersonation; ticket reply.
- `pikshipp_finance` — wallet adjustments, COD remittance, GST.
- `pikshipp_eng` — read-only across sellers + trigger reruns.

Pikshipp users live in a separate `pikshipp_user` table. The auth flow is identical; the system distinguishes user_kind in `Principal`.

## Pluggable design

```go
package auth

type Authenticator interface {
    Authenticate(ctx context.Context, req *http.Request) (Principal, error)
    IssueCredentials(ctx context.Context, userID core.UserID, sellerID core.SellerID) (Credentials, error)
    Revoke(ctx context.Context, credentialID string) error
}
```

**Today**: `OpaqueSessionAuthenticator`.
**When we need to swap**: implement another `Authenticator`, change the wiring in `main.go`. No domain code changes.

The cost of pluggability is **two extra Go files** (`session_auth.go` + `auth.go` interface). The benefit is "we can change authn strategy without rewriting handlers".

## Request flow

```
HTTP request
   │
   ▼
[middleware.RequestID]      adds request_id to context
   │
   ▼
[middleware.Recover]        panic → 500
   │
   ▼
[middleware.Auth]           authenticator.Authenticate(req) → principal in context
                            (or 401 if unauthenticated)
   │
   ▼
[middleware.SellerScope]    BEGIN tx; SET LOCAL app.seller_id = principal.SellerID
   │
   ▼
[middleware.Idempotency]    if Idempotency-Key cached, replay
   │
   ▼
[middleware.RequireRole]    check principal.Roles against route requirement
   │
   ▼
[Handler]                   call domain service
   │
   ▼
[middleware.SellerScope]    COMMIT (or ROLLBACK)
   │
   ▼
HTTP response
```

## Public endpoints (no auth)

- `GET /healthz`
- `GET /readyz`
- `POST /webhooks/*` (HMAC-verified, not auth-gated)
- `GET /v1/track/{token}` (buyer tracking page; opaque token validates)
- `POST /v1/auth/google/start`
- `GET /v1/auth/google/callback`

Everything else requires auth.

## Buyer-page tokens (different mechanism)

Buyer-facing pages (tracking, NDR feedback, returns) don't have user accounts. Authentication is by **opaque token in URL**:

```
GET /v1/track/{token}
```

Where `{token}` is a 32-byte random value bound to a shipment. Stored in `buyer_session` with TTL.

Rate-limited per IP. Token entropy resists brute force.

For sensitive buyer actions (cancel COD, initiate return), additional verification via phone OTP.
