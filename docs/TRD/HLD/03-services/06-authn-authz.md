# Service: Authn / authz

> **Module:** `internal/auth/`
> **Maps to PRD:** [`PRD/04-features/01-identity-and-onboarding.md`](../../../PRD/04-features/01-identity-and-onboarding.md)
> **Decision record:** [`05-decisions/0003-server-side-sessions-not-jwt.md`](../05-decisions/0003-server-side-sessions-not-jwt.md)
>
> Server-side opaque sessions at v0, behind an interface designed to swap to JWT (or any other) without disturbing the rest of the codebase.

## Responsibility

- Authenticate incoming HTTP requests.
- Resolve `Principal{user_id, seller_id, roles}` from the request.
- Authorize based on role + per-resource policy.
- Issue/revoke sessions.
- OAuth (Google) login flow.
- Phone OTP verification (post-signup, not a login factor at v0).

## The pluggable interface

```go
package auth

// Authenticator extracts a Principal from an inbound request, or returns ErrUnauthenticated.
type Authenticator interface {
    Authenticate(ctx context.Context, req *http.Request) (Principal, error)
    IssueCredentials(ctx context.Context, userID core.UserID, sellerID core.SellerID) (Credentials, error)
    Revoke(ctx context.Context, credentialID string) error
}

type Principal struct {
    UserID    core.UserID
    SellerID  core.SellerID    // selected seller; may be zero if user hasn't picked yet
    Roles     []Role
    AuthMethod string           // 'session', 'jwt' (later), 'api_key' (v2+)
}

type Credentials struct {
    Token       string         // opaque session token, JWT, or API key
    ExpiresAt   time.Time
    Refreshable bool
}
```

**v0 implementation:** `OpaqueSessionAuthenticator`.
**Future implementations** (interface-compatible):
- `JWTAuthenticator` — same interface; reads `Authorization: Bearer <jwt>`; signature verify; no DB hit on hot path.
- `APIKeyAuthenticator` — for v2 public API.
- `OIDCAuthenticator` — for enterprise SAML/OIDC SSO.

The rest of the codebase imports only `auth.Authenticator`. Switching is wiring in `main.go`.

## Data model (v0)

```sql
CREATE TABLE app_user (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email             TEXT UNIQUE NOT NULL,
  name              TEXT,
  phone             TEXT,                       -- nullable until verified
  phone_verified_at TIMESTAMPTZ,
  status            TEXT NOT NULL,              -- 'active' | 'locked' | 'wound_down'
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE oauth_link (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES app_user(id),
  provider    TEXT NOT NULL,           -- 'google'
  provider_user_id TEXT NOT NULL,      -- Google's `sub`
  email       TEXT NOT NULL,
  raw_profile JSONB,
  linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id)
);

CREATE TABLE seller_user (
  id           UUID PRIMARY KEY,
  user_id      UUID NOT NULL REFERENCES app_user(id),
  seller_id    UUID NOT NULL,
  roles_jsonb  JSONB NOT NULL,         -- ['Owner', 'Operator', ...]
  status       TEXT NOT NULL,           -- 'invited' | 'active' | 'removed'
  invited_at, joined_at, removed_at,
  UNIQUE (user_id, seller_id)
);

CREATE TABLE session (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash      TEXT UNIQUE NOT NULL,           -- sha256 of opaque token
  user_id         UUID NOT NULL REFERENCES app_user(id),
  selected_seller_id UUID,                         -- the seller this session is currently scoped to
  user_agent      TEXT,
  ip              INET,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,            -- short-lived: 24h since last activity
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at      TIMESTAMPTZ
);

CREATE INDEX session_user_idx ON session (user_id) WHERE revoked_at IS NULL;
```

The `session` table is **not** seller-scoped (a user can switch sellers within a session).

## OpaqueSessionAuthenticator

```go
type OpaqueSessionAuthenticator struct {
    db    DB
    cache *sessionCache  // small in-process cache; 30s TTL; LISTEN-invalidated on revoke
    clock core.Clock
}

func (a *OpaqueSessionAuthenticator) Authenticate(ctx, req) (Principal, error) {
    token := extractToken(req)  // cookie 'pikshipp_session' or Authorization: Bearer <token>
    if token == "" { return Principal{}, ErrUnauthenticated }

    hash := sha256(token)

    // Cache check
    if p, ok := a.cache.Get(hash); ok && p.ExpiresAt.After(a.clock.Now()) {
        return p, nil
    }

    // DB lookup
    var s session
    err := a.db.QueryRow(ctx, `
        SELECT user_id, selected_seller_id, expires_at, revoked_at
        FROM session WHERE token_hash = $1
    `, hash).Scan(&s.UserID, &s.SelectedSellerID, &s.ExpiresAt, &s.RevokedAt)

    if err != nil || s.RevokedAt.Valid || s.ExpiresAt.Before(a.clock.Now()) {
        return Principal{}, ErrUnauthenticated
    }

    // Resolve roles
    roles := a.lookupRoles(ctx, s.UserID, s.SelectedSellerID)

    p := Principal{
        UserID: s.UserID,
        SellerID: s.SelectedSellerID,
        Roles: roles,
        AuthMethod: "session",
    }

    a.cache.Put(hash, p)

    // Update last_seen_at (async)
    go a.touchSession(s.ID)

    return p, nil
}
```

### Token characteristics
- **Opaque, 32 bytes**, base64-url-encoded.
- Generated via `crypto/rand`.
- Stored as SHA-256 hash; the plaintext is only in the user's cookie/header.
- TTL: 24h since last activity. After 30min idle, the next request still validates but extends; after 24h with no activity, expired.

### Cookie configuration
```
Set-Cookie: pikshipp_session=<token>;
            HttpOnly;
            Secure;
            SameSite=Lax;
            Path=/;
            Max-Age=86400;
            Domain=app.pikshipp.com
```

## Login flow (Google OAuth)

```
1. User clicks "Login with Google" in dashboard
   GET /v1/auth/google/start
       → server generates state token, sets in cookie
       → 302 to Google authorize URL

2. Google → /v1/auth/google/callback?code=...&state=...
       → verify state cookie
       → exchange code for tokens with Google
       → fetch user profile (email, name, sub)
       → upsert app_user by email
       → upsert oauth_link
       → resolve seller_user(s) for this user
       → if exactly one: create session with selected_seller_id
         else: create session with selected_seller_id=NULL; redirect to seller picker
       → set cookie

3. Subsequent requests: Authenticate middleware reads cookie, sets Principal in context.

4. Logout
   POST /v1/auth/logout
       → revoke session (set revoked_at)
       → NOTIFY session_revoked (so other instances drop cache)
       → clear cookie
       → 200
```

## Authorization (RBAC)

```go
package auth

type Role string

const (
    RoleOwner    Role = "owner"
    RoleManager  Role = "manager"
    RoleOperator Role = "operator"
    RoleFinance  Role = "finance"
    RoleReadOnly Role = "read_only"
)

// Used at handler boundary
func RequireRole(roles ...Role) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w, r) {
            p := PrincipalFrom(r.Context())
            if !hasAnyRole(p.Roles, roles) {
                writeError(w, ErrForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

// Per-resource:
// e.g., wallet operations require Owner OR Finance
mux.With(auth.RequireRole(RoleOwner, RoleFinance)).Post("/v1/wallet/recharge", ...)
```

Pikshipp staff have separate roles (`pikshipp_admin`, `pikshipp_ops`, `pikshipp_support`, `pikshipp_finance`, `pikshipp_eng`) that grant cross-seller access. Stored in a `pikshipp_user` table; resolution is identical at the auth layer (different `user_kind`).

## Phone verification

Post-signup, on profile page:
```
1. Seller enters phone, clicks "Verify".
   POST /v1/auth/phone/start { phone }
       → call MSG91 OTP API (sendotp endpoint)
       → return masked phone in response

2. Seller receives OTP, enters it.
   POST /v1/auth/phone/verify { phone, otp }
       → call MSG91 verify endpoint
       → on success: UPDATE app_user SET phone=?, phone_verified_at=now()
       → 200
```

Phone is **not** a login factor at v0. Used for KYC, COD verification, NDR contact.

## Session revocation

Cases:
- User clicks logout → revoke that session.
- User is locked / removed from seller → revoke all sessions for that user.
- Suspicious activity detected → revoke all sessions; user re-authenticates.
- Owner removes a user from seller → revoke all sessions of that user that have `selected_seller_id` matching.

Revocation:
1. UPDATE `session` SET `revoked_at = now()`.
2. NOTIFY `session_revoked` channel with `token_hash` so other instances drop cache.
3. Audit event.

## Cache invalidation

- In-process cache TTL: 30s (small enough that revocation is acceptable).
- LISTEN/NOTIFY on `session_revoked` for sub-second invalidation across instances.
- Cache size capped (LRU); entries evicted on memory pressure.

## Session table size

At v1 with 2k active sellers × ~3 users each × ~3 active sessions each = ~18k rows. At v3 with 50k sellers, ~450k rows. Trivial for indexed lookup. Cleanup cron deletes rows where `revoked_at < now() - 30 days` and `expires_at < now() - 30 days`.

## Why this beats JWT for our use case

- **Immediate revocation.** Suspending a user kicks them out within the cache TTL (30s) — vs. JWT's 15-minute access-token window.
- **No refresh token gymnastics.** No 401-on-expiry → 401-on-refresh → original-request-retry dance.
- **Auditable.** Every active session is a row; ops can list all of a user's sessions.
- **Sub-millisecond DB lookup.** Indexed `token_hash` UNIQUE. No bottleneck.
- **Pluggable.** When we need JWT (e.g., for v2 public API or for performance reasons we can't predict), we swap implementations.

See [`05-decisions/0003-server-side-sessions-not-jwt.md`](../05-decisions/0003-server-side-sessions-not-jwt.md) for the full rationale and the JWT path forward.

## Performance

- `Authenticate` cache hit: P95 < 1ms.
- `Authenticate` cache miss: P95 < 5ms (one indexed PG query).
- `IssueCredentials`: P95 < 30ms (one INSERT).
- `Revoke`: P95 < 30ms.

## Failure modes

| Failure | Behavior |
|---|---|
| DB unreachable on cache miss | Return cached if available with stale flag; else `ErrUnauthenticated` (fail closed) |
| LISTEN drops | TTL fallback ensures eventual revocation |
| Token theft | Compromised user revokes session via "log out all devices"; we audit log all active sessions per user |
| Concurrent revocation + auth | Revoke sets `revoked_at`; auth checks `revoked_at IS NULL`; race resolved by next read |

## Test coverage

- **Unit**: token generation, hash, principal resolution, role checks.
- **SLT**: end-to-end OAuth callback (mock Google); session creation; concurrent ops; revocation propagation across two-process simulation.
- **Bench**: `Authenticate` cache hit and cache miss.

## Observability

- Counter: logins by method.
- Counter: revocations by reason.
- Histogram: auth middleware latency.
- Active sessions gauge.
- Failed-auth rate (alert threshold).

## Open questions

- **Q-AU-1.** Refresh tokens? Not at v0; sessions are short-lived but extend on activity. Add refresh tokens if we move to a SPA that needs background refresh.
- **Q-AU-2.** TOTP MFA at v1: which library? `pquerna/otp` is standard. Wait until v1 spec.
- **Q-AU-3.** Cross-seller user (one user across multiple sellers): supported in `seller_user` model; UI lets user switch via session re-scope. Already in design.
