# Infrastructure: Auth implementation (`internal/auth`)

> Server-side opaque sessions behind a pluggable `Authenticator` interface. Google OAuth login flow. Cookie-based for dashboard; bearer token for API clients.

## Purpose

- Implement the `auth.Authenticator` interface with `OpaqueSessionAuth`.
- Manage Google OAuth login + session creation.
- Manage session lifecycle (create, validate, revoke).
- Provide RBAC role checks.

## Dependencies

- `crypto/rand`, `crypto/sha256`, `crypto/hmac` (stdlib)
- `encoding/base64` (stdlib)
- `golang.org/x/oauth2`, `golang.org/x/oauth2/google`
- `internal/core`, `internal/observability/secrets`, `internal/observability/dbtx`

## Package layout

```
internal/auth/
├── doc.go
├── authenticator.go     ← Authenticator interface + Principal
├── session_auth.go      ← OpaqueSessionAuth implementation
├── oauth_google.go      ← Google OAuth start + callback
├── repo.go              ← session table access
├── token.go             ← opaque token generation + signing
├── errors.go
├── types.go             ← Principal, Role, etc.
├── context.go           ← context propagation
├── jobs.go              ← session cleanup cron
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package auth provides authentication and authorization primitives.
//
// The Authenticator interface lets us swap session-based (v0) for JWT or
// any other scheme later. The current default is OpaqueSessionAuth.
//
// Authorization (role checks) is RBAC; per-seller role assignments live
// in the seller_user table; checks happen at the HTTP middleware layer.
package auth

import (
    "context"
    "errors"
    "net/http"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Authenticator extracts a Principal from an inbound request.
//
// Implementations:
//   - OpaqueSessionAuth (v0): server-side sessions in DB.
//   - JWTAuth (future): stateless JWT.
//   - APIKeyAuth (v2): for public API.
type Authenticator interface {
    // Authenticate returns the Principal for this request, or
    // ErrUnauthenticated.
    //
    // Reads token from cookie ("pikshipp_session") or Authorization header
    // ("Bearer <token>"); validates; returns Principal.
    Authenticate(ctx context.Context, req *http.Request) (Principal, error)

    // IssueCredentials creates new credentials (a session, a JWT, etc.) for
    // the given user/seller pair.
    //
    // Returns the token (to be set in cookie or returned to client) and
    // expiry.
    IssueCredentials(ctx context.Context, userID core.UserID, sellerID core.SellerID) (Credentials, error)

    // Revoke invalidates the credential identified by token (or its hash;
    // implementation-defined).
    Revoke(ctx context.Context, token string) error

    // RevokeAllForUser invalidates every credential for the given user.
    // Used on user lockout, password reset, suspicious activity.
    RevokeAllForUser(ctx context.Context, userID core.UserID) error
}

// Principal is the authenticated identity for a request.
type Principal struct {
    UserID     core.UserID
    SellerID   core.SellerID
    Roles      []core.SellerRole
    UserKind   UserKind  // 'seller' | 'pikshipp_admin' | 'pikshipp_ops' | etc.
    AuthMethod string    // 'session' | 'jwt' | 'api_key'
}

// UserKind distinguishes seller users from Pikshipp staff.
type UserKind string

const (
    UserKindSeller         UserKind = "seller"
    UserKindPikshippAdmin  UserKind = "pikshipp_admin"
    UserKindPikshippOps    UserKind = "pikshipp_ops"
    UserKindPikshippSupport UserKind = "pikshipp_support"
    UserKindPikshippFinance UserKind = "pikshipp_finance"
    UserKindPikshippEng    UserKind = "pikshipp_eng"
)

// Credentials is what we issue when a user logs in.
type Credentials struct {
    Token     string    // opaque token string (or JWT for that impl)
    ExpiresAt time.Time
}

// Sentinel errors.
var (
    ErrUnauthenticated = errors.New("auth: unauthenticated")
    ErrSessionExpired  = errors.New("auth: session expired")
    ErrSessionRevoked  = errors.New("auth: session revoked")
)
```

## Context propagation

```go
// internal/auth/context.go
package auth

import "context"

type ctxKey int

const principalKey ctxKey = 0

// WithPrincipal attaches a Principal to the context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
    return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the Principal in ctx, or ok=false if none.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
    p, ok := ctx.Value(principalKey).(Principal)
    return p, ok
}

// MustPrincipalFrom is the panicking variant for paths that have already
// passed through auth middleware (handler bodies).
func MustPrincipalFrom(ctx context.Context) Principal {
    p, ok := PrincipalFrom(ctx)
    if !ok {
        panic("auth: principal not in context")
    }
    return p
}
```

## OpaqueSessionAuth

```go
// internal/auth/session_auth.go
package auth

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "errors"
    "fmt"
    "log/slog"
    "net/http"
    "strings"
    "sync"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability"
    "github.com/pikshipp/pikshipp/internal/observability/secrets"
)

// SessionAuthConfig holds runtime config for OpaqueSessionAuth.
type SessionAuthConfig struct {
    HMACKey       secrets.Secret  // for token signing (defense in depth)
    MaxIdle       time.Duration   // session expires this long after last activity
    CookieName    string          // default "pikshipp_session"
    CookieDomain  string          // e.g., "app.pikshipp.com"
    CookieSecure  bool            // false in dev only
    CookiePath    string          // default "/"
}

// OpaqueSessionAuth implements Authenticator with server-stored sessions.
//
// Token: 32 bytes random; SHA-256 hash stored in DB.
// In-process cache: 30s TTL, LRU bounded; LISTEN/NOTIFY-invalidated on revoke.
type OpaqueSessionAuth struct {
    repo  *repo
    cfg   SessionAuthConfig
    cache *sessionCache
    clock core.Clock
    log   *slog.Logger
}

// NewOpaqueSessionAuth constructs the v0 implementation.
func NewOpaqueSessionAuth(pool *pgxpool.Pool, cfg SessionAuthConfig, clock core.Clock, log *slog.Logger) (*OpaqueSessionAuth, error) {
    if cfg.HMACKey.IsZero() {
        return nil, errors.New("auth: HMACKey is required")
    }
    if cfg.MaxIdle == 0 {
        cfg.MaxIdle = 24 * time.Hour
    }
    if cfg.CookieName == "" {
        cfg.CookieName = "pikshipp_session"
    }
    if cfg.CookiePath == "" {
        cfg.CookiePath = "/"
    }

    a := &OpaqueSessionAuth{
        repo:  newRepo(pool),
        cfg:   cfg,
        cache: newSessionCache(10000, 30*time.Second, clock),
        clock: clock,
        log:   log,
    }

    // Spawn LISTEN goroutine for cross-instance revocation
    observability.SafeGo(context.Background(), a.listenForRevocations)

    return a, nil
}

// Authenticate implements Authenticator.
func (a *OpaqueSessionAuth) Authenticate(ctx context.Context, req *http.Request) (Principal, error) {
    token := a.extractToken(req)
    if token == "" {
        return Principal{}, ErrUnauthenticated
    }

    hash := hashToken(token)

    // Cache lookup
    if p, ok := a.cache.Get(hash); ok {
        return p, nil
    }

    // DB lookup
    s, err := a.repo.GetSession(ctx, hash)
    if err != nil {
        if errors.Is(err, core.ErrNotFound) {
            return Principal{}, ErrUnauthenticated
        }
        return Principal{}, fmt.Errorf("auth.Authenticate: %w", err)
    }

    if s.RevokedAt != nil {
        return Principal{}, ErrSessionRevoked
    }
    if s.ExpiresAt.Before(a.clock.Now()) {
        return Principal{}, ErrSessionExpired
    }

    // Resolve roles
    roles, kind, err := a.repo.LookupRoles(ctx, s.UserID, s.SelectedSellerID)
    if err != nil {
        return Principal{}, fmt.Errorf("auth.Authenticate: lookup roles: %w", err)
    }

    p := Principal{
        UserID:     s.UserID,
        SellerID:   s.SelectedSellerID,
        Roles:      roles,
        UserKind:   kind,
        AuthMethod: "session",
    }
    a.cache.Put(hash, p)

    // Best-effort touch of last_seen_at (async; doesn't block request)
    observability.SafeGo(ctx, func(ctx context.Context) {
        if err := a.repo.TouchSession(ctx, hash, a.clock.Now()); err != nil {
            a.log.WarnContext(ctx, "touch session failed", slog.Any("error", err))
        }
    })

    return p, nil
}

// IssueCredentials implements Authenticator.
//
// Creates a new session, returns the opaque token. The session is bound to
// (userID, sellerID). userID is required; sellerID may be zero if the user
// hasn't selected one yet (e.g., immediately after OAuth callback).
func (a *OpaqueSessionAuth) IssueCredentials(ctx context.Context, userID core.UserID, sellerID core.SellerID) (Credentials, error) {
    if userID.IsZero() {
        return Credentials{}, fmt.Errorf("auth.IssueCredentials: %w", core.ErrInvalidArgument)
    }

    token, err := generateToken()
    if err != nil {
        return Credentials{}, fmt.Errorf("auth.IssueCredentials: gen token: %w", err)
    }
    hash := hashToken(token)
    expiresAt := a.clock.Now().Add(a.cfg.MaxIdle)

    err = a.repo.InsertSession(ctx, sessionRecord{
        TokenHash:        hash,
        UserID:           userID,
        SelectedSellerID: sellerID,
        ExpiresAt:        expiresAt,
        CreatedAt:        a.clock.Now(),
    })
    if err != nil {
        return Credentials{}, fmt.Errorf("auth.IssueCredentials: insert: %w", err)
    }

    return Credentials{
        Token:     token,
        ExpiresAt: expiresAt,
    }, nil
}

// Revoke implements Authenticator.
//
// Marks the session as revoked. Idempotent: revoking an already-revoked
// session is a no-op.
func (a *OpaqueSessionAuth) Revoke(ctx context.Context, token string) error {
    hash := hashToken(token)

    err := a.repo.RevokeSession(ctx, hash, a.clock.Now())
    if err != nil {
        return fmt.Errorf("auth.Revoke: %w", err)
    }

    a.cache.Delete(hash)

    // Cross-instance NOTIFY
    if err := a.repo.NotifyRevocation(ctx, hash); err != nil {
        a.log.WarnContext(ctx, "notify revocation failed", slog.Any("error", err))
    }

    return nil
}

// RevokeAllForUser revokes every session for the given user.
func (a *OpaqueSessionAuth) RevokeAllForUser(ctx context.Context, userID core.UserID) error {
    hashes, err := a.repo.RevokeAllForUser(ctx, userID, a.clock.Now())
    if err != nil {
        return fmt.Errorf("auth.RevokeAllForUser: %w", err)
    }

    for _, h := range hashes {
        a.cache.Delete(h)
        if err := a.repo.NotifyRevocation(ctx, h); err != nil {
            a.log.WarnContext(ctx, "notify revocation failed", slog.Any("error", err))
        }
    }
    return nil
}

// SetSessionCookie sets the session cookie on a response.
func (a *OpaqueSessionAuth) SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
    http.SetCookie(w, &http.Cookie{
        Name:     a.cfg.CookieName,
        Value:    token,
        Domain:   a.cfg.CookieDomain,
        Path:     a.cfg.CookiePath,
        Expires:  expiresAt,
        MaxAge:   int(a.cfg.MaxIdle.Seconds()),
        HttpOnly: true,
        Secure:   a.cfg.CookieSecure,
        SameSite: http.SameSiteLaxMode,
    })
}

// ClearSessionCookie expires the session cookie.
func (a *OpaqueSessionAuth) ClearSessionCookie(w http.ResponseWriter) {
    http.SetCookie(w, &http.Cookie{
        Name:     a.cfg.CookieName,
        Value:    "",
        Domain:   a.cfg.CookieDomain,
        Path:     a.cfg.CookiePath,
        MaxAge:   -1,
        HttpOnly: true,
        Secure:   a.cfg.CookieSecure,
        SameSite: http.SameSiteLaxMode,
    })
}

// extractToken pulls the token from cookie or Authorization header.
func (a *OpaqueSessionAuth) extractToken(req *http.Request) string {
    if c, err := req.Cookie(a.cfg.CookieName); err == nil && c.Value != "" {
        return c.Value
    }
    auth := req.Header.Get("Authorization")
    if strings.HasPrefix(auth, "Bearer ") {
        return strings.TrimPrefix(auth, "Bearer ")
    }
    return ""
}

// listenForRevocations subscribes to PG NOTIFY for cross-instance cache invalidation.
func (a *OpaqueSessionAuth) listenForRevocations(ctx context.Context) {
    backoff := time.Second
    for {
        select {
        case <-ctx.Done(): return
        default:
        }

        err := a.repo.ListenForRevocations(ctx, func(hashHex string) {
            a.cache.Delete(hashHex)
        })
        if err != nil {
            a.log.WarnContext(ctx, "session listen disconnected; reconnecting",
                slog.Any("error", err),
                slog.Duration("backoff", backoff))
            time.Sleep(backoff)
            if backoff < 30*time.Second {
                backoff *= 2
            }
            continue
        }
        backoff = time.Second
    }
}
```

## Token generation + hashing

```go
// internal/auth/token.go
package auth

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
)

// generateToken returns a 32-byte cryptographically random token,
// base64-url-encoded (no padding).
func generateToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 hash of token, hex-encoded.
//
// We store the hash, not the plaintext. If the DB is compromised, attackers
// don't get usable tokens.
func hashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return base64.RawURLEncoding.EncodeToString(h[:])
}
```

## Session cache

```go
// internal/auth/cache.go
package auth

import (
    "container/list"
    "sync"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// sessionCache is an LRU + TTL cache of authenticated principals by token hash.
//
// Bounded size; entries auto-evicted when full. TTL ensures eventual
// freshness even without explicit invalidation.
type sessionCache struct {
    mu      sync.Mutex
    entries map[string]*list.Element
    order   *list.List
    cap     int
    ttl     time.Duration
    clock   core.Clock
}

type cacheEntry struct {
    hash      string
    principal Principal
    insertedAt time.Time
}

func newSessionCache(cap int, ttl time.Duration, clock core.Clock) *sessionCache {
    return &sessionCache{
        entries: make(map[string]*list.Element, cap),
        order:   list.New(),
        cap:     cap,
        ttl:     ttl,
        clock:   clock,
    }
}

func (c *sessionCache) Get(hash string) (Principal, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    el, ok := c.entries[hash]
    if !ok { return Principal{}, false }

    e := el.Value.(*cacheEntry)
    if c.clock.Now().Sub(e.insertedAt) > c.ttl {
        c.removeLocked(el)
        return Principal{}, false
    }
    c.order.MoveToFront(el)
    return e.principal, true
}

func (c *sessionCache) Put(hash string, p Principal) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if el, ok := c.entries[hash]; ok {
        el.Value.(*cacheEntry).principal = p
        el.Value.(*cacheEntry).insertedAt = c.clock.Now()
        c.order.MoveToFront(el)
        return
    }

    e := &cacheEntry{hash: hash, principal: p, insertedAt: c.clock.Now()}
    el := c.order.PushFront(e)
    c.entries[hash] = el

    for c.order.Len() > c.cap {
        c.removeLocked(c.order.Back())
    }
}

func (c *sessionCache) Delete(hash string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if el, ok := c.entries[hash]; ok {
        c.removeLocked(el)
    }
}

func (c *sessionCache) removeLocked(el *list.Element) {
    e := el.Value.(*cacheEntry)
    delete(c.entries, e.hash)
    c.order.Remove(el)
}
```

## Repository

```go
// internal/auth/repo.go
package auth

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/db"
)

type repo struct {
    pool    *pgxpool.Pool
    queries *db.Queries
}

func newRepo(pool *pgxpool.Pool) *repo {
    return &repo{pool: pool, queries: db.New(pool)}
}

type sessionRecord struct {
    ID               core.SessionID
    TokenHash        string
    UserID           core.UserID
    SelectedSellerID core.SellerID
    CreatedAt        time.Time
    ExpiresAt        time.Time
    LastSeenAt       time.Time
    RevokedAt        *time.Time
}

func (r *repo) GetSession(ctx context.Context, tokenHash string) (sessionRecord, error) {
    row, err := r.queries.GetSessionByTokenHash(ctx, tokenHash)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return sessionRecord{}, core.ErrNotFound
        }
        return sessionRecord{}, fmt.Errorf("auth.repo.GetSession: %w", err)
    }
    return sessionRecord{
        ID:               core.SessionID(row.ID),
        TokenHash:        row.TokenHash,
        UserID:           core.UserID(row.UserID),
        SelectedSellerID: core.SellerID(row.SelectedSellerID),
        CreatedAt:        row.CreatedAt,
        ExpiresAt:        row.ExpiresAt,
        LastSeenAt:       row.LastSeenAt,
        RevokedAt:        row.RevokedAt,
    }, nil
}

func (r *repo) InsertSession(ctx context.Context, s sessionRecord) error {
    return r.queries.InsertSession(ctx, db.InsertSessionParams{
        TokenHash:        s.TokenHash,
        UserID:           s.UserID,
        SelectedSellerID: s.SelectedSellerID,
        ExpiresAt:        s.ExpiresAt,
    })
}

func (r *repo) RevokeSession(ctx context.Context, tokenHash string, now time.Time) error {
    return r.queries.RevokeSessionByTokenHash(ctx, db.RevokeSessionByTokenHashParams{
        TokenHash: tokenHash,
        RevokedAt: now,
    })
}

func (r *repo) RevokeAllForUser(ctx context.Context, userID core.UserID, now time.Time) ([]string, error) {
    return r.queries.RevokeAllSessionsForUser(ctx, db.RevokeAllSessionsForUserParams{
        UserID:    userID,
        RevokedAt: now,
    })
}

func (r *repo) TouchSession(ctx context.Context, tokenHash string, now time.Time) error {
    return r.queries.TouchSession(ctx, db.TouchSessionParams{
        TokenHash:  tokenHash,
        LastSeenAt: now,
    })
}

func (r *repo) LookupRoles(ctx context.Context, userID core.UserID, sellerID core.SellerID) ([]core.SellerRole, UserKind, error) {
    if sellerID.IsZero() {
        // Resolve user kind only
        u, err := r.queries.GetUserByID(ctx, userID)
        if err != nil {
            return nil, "", err
        }
        return nil, UserKind(u.Kind), nil
    }
    row, err := r.queries.GetSellerUserRoles(ctx, db.GetSellerUserRolesParams{
        UserID:   userID,
        SellerID: sellerID,
    })
    if err != nil {
        return nil, "", err
    }
    return parseRoles(row.RolesJsonb), UserKindSeller, nil
}

func (r *repo) NotifyRevocation(ctx context.Context, hash string) error {
    _, err := r.pool.Exec(ctx, "SELECT pg_notify('session_revoked', $1)", hash)
    return err
}

func (r *repo) ListenForRevocations(ctx context.Context, onMsg func(hashHex string)) error {
    conn, err := r.pool.Acquire(ctx)
    if err != nil { return err }
    defer conn.Release()

    if _, err := conn.Exec(ctx, "LISTEN session_revoked"); err != nil {
        return err
    }

    for {
        n, err := conn.Conn().WaitForNotification(ctx)
        if err != nil { return err }
        onMsg(n.Payload)
    }
}
```

## DB schema (migration)

```sql
-- migrations/00NN_create_session.up.sql

CREATE TABLE app_user (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             TEXT UNIQUE NOT NULL,
    name              TEXT,
    phone             TEXT,
    phone_verified_at TIMESTAMPTZ,
    status            TEXT NOT NULL DEFAULT 'active',
    kind              TEXT NOT NULL DEFAULT 'seller',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT app_user_status_valid CHECK (status IN ('active','locked','wound_down')),
    CONSTRAINT app_user_kind_valid   CHECK (kind IN ('seller','pikshipp_admin','pikshipp_ops','pikshipp_support','pikshipp_finance','pikshipp_eng'))
);

CREATE TABLE oauth_link (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    email            TEXT NOT NULL,
    raw_profile      JSONB,
    linked_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT oauth_link_provider_unique UNIQUE (provider, provider_user_id)
);

CREATE INDEX oauth_link_user_idx ON oauth_link (user_id);

CREATE TABLE seller_user (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    seller_id    UUID NOT NULL,
    roles_jsonb  JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    invited_at   TIMESTAMPTZ,
    joined_at    TIMESTAMPTZ,
    removed_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT seller_user_unique UNIQUE (user_id, seller_id),
    CONSTRAINT seller_user_status_valid CHECK (status IN ('invited','active','removed'))
);

CREATE INDEX seller_user_seller_idx ON seller_user (seller_id);
CREATE INDEX seller_user_user_idx ON seller_user (user_id);

CREATE TABLE session (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash          TEXT UNIQUE NOT NULL,
    user_id             UUID NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    selected_seller_id  UUID,
    user_agent          TEXT,
    ip                  INET,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at          TIMESTAMPTZ
);

CREATE INDEX session_user_active_idx ON session (user_id) WHERE revoked_at IS NULL;
CREATE INDEX session_expires_idx     ON session (expires_at) WHERE revoked_at IS NULL;
```

`session` is **not seller-scoped** (a user can switch sellers within a session). Access controlled at the application layer.

## SQL queries

```sql
-- query/auth.sql

-- name: GetSessionByTokenHash :one
SELECT id, token_hash, user_id, selected_seller_id, created_at, expires_at, last_seen_at, revoked_at
FROM session
WHERE token_hash = $1;

-- name: InsertSession :exec
INSERT INTO session (token_hash, user_id, selected_seller_id, expires_at)
VALUES ($1, $2, $3, $4);

-- name: RevokeSessionByTokenHash :exec
UPDATE session
SET revoked_at = $2
WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :many
UPDATE session
SET revoked_at = $2
WHERE user_id = $1 AND revoked_at IS NULL
RETURNING token_hash;

-- name: TouchSession :exec
UPDATE session
SET last_seen_at = $2,
    expires_at = $2 + INTERVAL '24 hours'
WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: GetSellerUserRoles :one
SELECT roles_jsonb
FROM seller_user
WHERE user_id = $1 AND seller_id = $2 AND status = 'active';

-- name: GetUserByID :one
SELECT id, email, name, status, kind
FROM app_user
WHERE id = $1;

-- name: CleanupExpiredSessions :execrows
DELETE FROM session
WHERE expires_at < now() - INTERVAL '30 days'
   OR revoked_at < now() - INTERVAL '30 days';
```

## Cleanup cron (river job)

```go
// internal/auth/jobs.go
package auth

import (
    "context"

    "github.com/riverqueue/river"
)

// CleanupSessionsArgs is the river job for periodic cleanup.
type CleanupSessionsArgs struct{}

func (CleanupSessionsArgs) Kind() string { return "auth.cleanup_sessions" }

type CleanupSessionsWorker struct {
    river.WorkerDefaults[CleanupSessionsArgs]
    repo *repo
    log  *slog.Logger
}

func (w *CleanupSessionsWorker) Work(ctx context.Context, j *river.Job[CleanupSessionsArgs]) error {
    n, err := w.repo.queries.CleanupExpiredSessions(ctx)
    if err != nil {
        return fmt.Errorf("auth.cleanup_sessions: %w", err)
    }
    w.log.InfoContext(ctx, "cleaned up sessions", slog.Int64("count", n))
    return nil
}

// Register: scheduled via river's PeriodicJobs at 04:00 IST daily.
```

## Testing

```go
func TestOpaqueSessionAuth_IssueAndAuthenticate_SLT(t *testing.T) {
    p := testdb.New(t)
    clock := core.NewFakeClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
    a, err := auth.NewOpaqueSessionAuth(p.App, auth.SessionAuthConfig{
        HMACKey: secrets.New("test-key-32-bytes-long-1234567890ab"),
        MaxIdle: 24 * time.Hour,
    }, clock, slog.Default())
    require.NoError(t, err)

    userID := makeUser(t, p.App, "test@example.com")
    sellerID := makeSellerWithUser(t, p.App, userID, []core.SellerRole{core.RoleOwner})

    // Issue
    creds, err := a.IssueCredentials(context.Background(), userID, sellerID)
    require.NoError(t, err)
    require.NotEmpty(t, creds.Token)

    // Authenticate
    req := httptest.NewRequest("GET", "/", nil)
    req.AddCookie(&http.Cookie{Name: "pikshipp_session", Value: creds.Token})

    p1, err := a.Authenticate(context.Background(), req)
    require.NoError(t, err)
    require.Equal(t, userID, p1.UserID)
    require.Equal(t, sellerID, p1.SellerID)
    require.Contains(t, p1.Roles, core.RoleOwner)
}

func TestOpaqueSessionAuth_RevokeKicksOut_SLT(t *testing.T) {
    p := testdb.New(t)
    clock := core.NewFakeClock(time.Now())
    a, _ := auth.NewOpaqueSessionAuth(p.App, defaultCfg, clock, slog.Default())

    userID := makeUser(t, p.App, "test@example.com")
    creds, _ := a.IssueCredentials(context.Background(), userID, core.SellerID{})

    req := httptest.NewRequest("GET", "/", nil)
    req.AddCookie(&http.Cookie{Name: "pikshipp_session", Value: creds.Token})

    _, err := a.Authenticate(context.Background(), req)
    require.NoError(t, err)

    err = a.Revoke(context.Background(), creds.Token)
    require.NoError(t, err)

    _, err = a.Authenticate(context.Background(), req)
    require.ErrorIs(t, err, auth.ErrSessionRevoked)
}

func TestOpaqueSessionAuth_ExpiredSession(t *testing.T) {
    p := testdb.New(t)
    clock := core.NewFakeClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
    a, _ := auth.NewOpaqueSessionAuth(p.App, auth.SessionAuthConfig{
        HMACKey: secrets.New("..."),
        MaxIdle: 1 * time.Hour,
    }, clock, slog.Default())

    userID := makeUser(t, p.App, "test@example.com")
    creds, _ := a.IssueCredentials(context.Background(), userID, core.SellerID{})

    clock.Advance(2 * time.Hour)

    req := httptest.NewRequest("GET", "/", nil)
    req.AddCookie(&http.Cookie{Name: "pikshipp_session", Value: creds.Token})
    _, err := a.Authenticate(context.Background(), req)
    require.ErrorIs(t, err, auth.ErrSessionExpired)
}

func BenchmarkOpaqueSessionAuth_AuthenticateCacheHit(b *testing.B) {
    // Setup omitted; assume `a`, `req` initialized
    for i := 0; i < b.N; i++ {
        _, _ = a.Authenticate(context.Background(), req)
    }
}
```

## Performance

- `Authenticate` cache hit: P95 < 1µs (mutex + map lookup).
- `Authenticate` cache miss: P95 < 5ms (one DB roundtrip).
- `IssueCredentials`: P95 < 30ms (one INSERT).
- `Revoke`: P95 < 30ms (one UPDATE + NOTIFY).

## Open questions

- TOTP MFA implementation (v1): use `pquerna/otp`. Spec at v1.
- Per-IP login rate limit (brute-force prevention): add at v1. v0 has trust-on-first-use.
- Session "kill all" on suspicious activity (geolocation, impossible travel): v2.

## References

- ADR 0003 (sessions vs JWT, with pluggability).
- HLD `03-services/06-authn-authz.md`.
- HLD `04-cross-cutting/01-authn-authz.md`.
