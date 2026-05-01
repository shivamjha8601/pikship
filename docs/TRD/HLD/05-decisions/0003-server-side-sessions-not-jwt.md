# ADR 0003 — Server-side opaque sessions, not JWT (interface-pluggable)

Date: 2026-04-30
Status: Accepted
Owner: Architect A (after Architect B push-back)
Supersedes: prior conversational decision "JWT only, no sessions"

## Context

This decision was reversed during the architectural review. The original intent was "JWT only — no sessions, hard to scale and manage." On review, that position turned out to be incoherent for a money-handling platform.

The argument for JWT:
- Stateless validation (no DB hit).
- Scales without session-replication concerns.
- Standard, widely understood.

The argument against JWT for our use case:
- **Revocation gap.** Access token valid for ~15 minutes. If we suspend a seller's user, they keep operating for up to 15 minutes. On a money platform, that's a real exposure.
- **Refresh tokens require server-side state anyway.** We end up storing refresh tokens in DB for revocation, so we have a session table by another name — just worse, because the access-token side is unrevoke-able.
- **The "JWT scales better" argument doesn't apply at our scale.** Postgres on an indexed UNIQUE lookup is sub-millisecond. We are not GitHub-scale.
- **Auditability.** Every active session as a row makes "list all of this user's sessions, revoke this one specifically" trivial. JWT denylists are awkward.
- **Simplicity.** One mechanism (cookie or bearer + DB lookup) vs. two (access JWT + refresh-token DB).

The user's earlier reasoning ("sessions are hard to scale and manage") doesn't hold for our scale or DB choice — Postgres handles 100k+ session rows trivially with proper indexing.

## Decision

**Server-side opaque sessions** at v0.

Implementation:
- Token: 32 bytes random; SHA-256 hash stored in DB.
- Storage: `session` table with `(token_hash, user_id, selected_seller_id, expires_at, revoked_at)`.
- Transport: HTTP-only secure cookie for the dashboard; `Authorization: Bearer <token>` for any API client.
- Revocation: UPDATE `revoked_at`; NOTIFY to invalidate caches; immediate effect.

**Behind a swappable interface.** The `auth.Authenticator` interface decouples the implementation. Switching to JWT is a configuration change + an alternative implementation, not a refactor.

```go
type Authenticator interface {
    Authenticate(ctx, req) (Principal, error)
    IssueCredentials(ctx, userID, sellerID) (Credentials, error)
    Revoke(ctx, credentialID) error
}

// v0:
type OpaqueSessionAuthenticator struct { db DB; cache *sessionCache; clock core.Clock }

// future:
type JWTAuthenticator struct { signingKey []byte; ... }
```

## Alternatives considered

### Pure stateless JWT
- Rejected: 15-min revocation gap unacceptable.

### JWT with denylist (DB lookup of denylisted JTIs on every request)
- Defeats the "stateless = no DB lookup" benefit; you've reinvented sessions worse.

### Short-lived JWT (e.g., 60s) + frequent refresh
- Rejected: every request that crosses the 60s boundary needs a refresh round-trip. Effectively doubles latency on a meaningful fraction of requests.

### Server-side sessions (chosen)
- Sub-millisecond DB lookup on indexed UNIQUE.
- Immediate revocation.
- Simpler to reason about.

## Consequences

### What this commits us to
- A `session` table.
- A small in-process cache (30s TTL) + LISTEN/NOTIFY for revocation propagation.
- Cleanup cron (daily) to delete expired sessions.

### What it costs
- One indexed DB lookup per authenticated request. ~1ms or less. Negligible.
- A small amount of state in `session` table; trivial at our scale.

### What it enables
- Immediate revocation.
- Audit trail of active sessions per user.
- Obvious "log out all devices" button.
- Future ability to switch (e.g., for v2 public API or for performance reasons we can't predict) by implementing `Authenticator` differently.

## Implementation note

The interface design is deliberately small (3 methods). Adding more (e.g., refresh, MFA challenge) means adding more methods, but only when we need them. Don't speculate.

## Migration to JWT (if we ever do)

When/if a future use case demands JWT (e.g., a high-traffic public API where 1ms-per-request DB hit is too much):
1. Implement `JWTAuthenticator` (a few hundred lines).
2. Update `main.go` to wire it for the relevant routes.
3. Add appropriate documentation + ADR for the new path.
4. Both implementations can coexist (e.g., dashboard on sessions, public API on JWT).

This is much cheaper than locking in JWT now.

## Open questions

- Refresh tokens? Not at v0. Sessions are short-lived (24h since last activity) and extend on use. Add refresh tokens if we ever need true offline sessions.
- TOTP MFA at v1: lives in the same Authenticator interface or as separate middleware? Decide at v1 spec time.
