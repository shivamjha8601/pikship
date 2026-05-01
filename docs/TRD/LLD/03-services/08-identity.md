# Service: Identity (`internal/identity`)

> User accounts, OAuth provider linking, seller ↔ user membership, role assignment, invites. Backs the auth layer.

## Purpose

- Manage `app_user` records (seller staff + Pikshipp staff).
- Google OAuth callback flow → upsert user.
- Seller ↔ user membership (`seller_user`) with roles.
- User invite flow (email-based, magic link).
- Lock / unlock / wind-down user lifecycle.

## Dependencies

- `internal/core`
- `internal/auth` (issues credentials after login)
- `internal/audit` (KYC and lifecycle events)
- `internal/observability/dbtx`
- `golang.org/x/oauth2` and `golang.org/x/oauth2/google`

## Package layout

```
internal/identity/
├── doc.go
├── service.go            ← Service interface
├── service_impl.go
├── repo.go
├── types.go              ← User, Invite, etc.
├── oauth_google.go       ← Google OAuth flow handler
├── invite.go             ← invite + magic-link
├── jobs.go               ← cleanup, invite expiry
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package identity manages user accounts and seller membership.
//
// User authentication is handled in internal/auth; this package owns the
// persistent user model. Login = OAuth callback → identity upserts user →
// auth.IssueCredentials.
package identity

import (
    "context"
    "errors"
    "time"

    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
)

// Service is the public API.
type Service interface {
    // UpsertFromOAuth creates or updates a user based on Google OAuth profile.
    //
    // Returns the (possibly newly-created) user. Used by the OAuth callback
    // handler to materialize a user before issuing a session.
    UpsertFromOAuth(ctx context.Context, provider Provider, profile OAuthProfile) (User, error)

    // GetUser by ID.
    GetUser(ctx context.Context, userID core.UserID) (User, error)

    // GetUserByEmail finds an existing user; returns ErrNotFound if none.
    GetUserByEmail(ctx context.Context, email string) (User, error)

    // ListUserSellers returns the sellers this user has access to.
    ListUserSellers(ctx context.Context, userID core.UserID) ([]SellerMembership, error)

    // SelectSeller validates the user has access to the seller and returns
    // their roles. Used after OAuth login if user belongs to multiple sellers.
    SelectSeller(ctx context.Context, userID core.UserID, sellerID core.SellerID) (SellerMembership, error)

    // InviteUserToSeller creates an invitation. Sends email separately
    // (caller responsible — keeps domain side decoupled from notifications).
    //
    // Returns the invite token (used in magic link).
    InviteUserToSeller(ctx context.Context, sellerID core.SellerID, byUser core.UserID, in InviteInput) (Invite, error)

    // AcceptInvite consumes an invite token. Looks up the user by email or
    // creates a new one (typically called from OAuth callback when invitee
    // first arrives).
    AcceptInvite(ctx context.Context, token string, profile OAuthProfile) (User, SellerMembership, error)

    // RemoveUserFromSeller revokes membership. Triggers session revocation.
    RemoveUserFromSeller(ctx context.Context, sellerID core.SellerID, userID core.UserID, byUser core.UserID, reason string) error

    // UpdateUserRoles changes a user's roles within a seller.
    UpdateUserRoles(ctx context.Context, sellerID core.SellerID, userID core.UserID, roles []core.SellerRole, byUser core.UserID, reason string) error

    // LockUser locks a user account (cross-seller); auth-side revokes all sessions.
    LockUser(ctx context.Context, userID core.UserID, reason string, by core.UserID) error

    // UnlockUser reverses LockUser.
    UnlockUser(ctx context.Context, userID core.UserID, by core.UserID) error
}

// Provider is the OAuth identity provider.
type Provider string

const (
    ProviderGoogle Provider = "google"
)

// OAuthProfile is what we get back from the provider after successful auth.
type OAuthProfile struct {
    ProviderUserID string                 // 'sub' from Google
    Email          string
    Name           string
    PictureURL     string
    Raw            map[string]any         // full profile for audit
}

// User is the canonical user record.
type User struct {
    ID                core.UserID
    Email             string
    Name              string
    Phone             *core.E164             // nullable
    PhoneVerifiedAt   *time.Time
    Status            UserStatus
    Kind              auth.UserKind
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

// UserStatus enum.
type UserStatus string

const (
    UserActive    UserStatus = "active"
    UserLocked    UserStatus = "locked"
    UserWoundDown UserStatus = "wound_down"
)

// SellerMembership represents a user's relationship with a seller.
type SellerMembership struct {
    SellerID    core.SellerID
    SellerName  string
    Roles       []core.SellerRole
    Status      MembershipStatus
    JoinedAt    *time.Time
    InvitedAt   *time.Time
}

// MembershipStatus enum.
type MembershipStatus string

const (
    MembershipInvited MembershipStatus = "invited"
    MembershipActive  MembershipStatus = "active"
    MembershipRemoved MembershipStatus = "removed"
)

// InviteInput is the request to create an invitation.
type InviteInput struct {
    Email string
    Roles []core.SellerRole
}

// Invite is a pending invitation.
type Invite struct {
    ID         core.InviteID
    SellerID   core.SellerID
    Email      string
    Roles      []core.SellerRole
    Token      string         // unhashed; only returned at creation
    InvitedBy  core.UserID
    CreatedAt  time.Time
    ExpiresAt  time.Time
    AcceptedAt *time.Time
}

// Sentinel errors.
var (
    ErrEmailExists       = fmt.Errorf("identity: email already in use: %w", core.ErrConflict)
    ErrInviteNotFound    = fmt.Errorf("identity: invite not found: %w", core.ErrNotFound)
    ErrInviteExpired     = errors.New("identity: invite expired")
    ErrInviteAlreadyUsed = errors.New("identity: invite already used")
    ErrUserLocked        = fmt.Errorf("identity: user locked: %w", core.ErrPermissionDenied)
    ErrNotMember         = fmt.Errorf("identity: not a member of this seller: %w", core.ErrPermissionDenied)
)
```

## DB schema

(See LLD `02-infrastructure/06-auth.md` for `app_user`, `oauth_link`, `seller_user`, `session` tables. Identity adds the invite table.)

```sql
-- migrations/00NN_create_invite.up.sql

CREATE TABLE seller_invite (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id    UUID NOT NULL,
    email        TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    roles_jsonb  JSONB NOT NULL,
    invited_by   UUID NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ,
    accepted_by_user_id UUID
);

CREATE INDEX seller_invite_seller_idx ON seller_invite (seller_id);
CREATE INDEX seller_invite_email_idx ON seller_invite (email) WHERE accepted_at IS NULL;

ALTER TABLE seller_invite ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_invite_seller ON seller_invite
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE ON seller_invite TO pikshipp_app;
GRANT ALL ON seller_invite TO pikshipp_admin;
```

## SQL queries

```sql
-- query/identity.sql

-- name: GetUserByEmail :one
SELECT id, email, name, phone, phone_verified_at, status, kind, created_at, updated_at
FROM app_user
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, name, phone, phone_verified_at, status, kind, created_at, updated_at
FROM app_user
WHERE id = $1;

-- name: InsertUser :exec
INSERT INTO app_user (id, email, name, kind)
VALUES ($1, $2, $3, $4);

-- name: UpdateUserStatus :exec
UPDATE app_user
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: UpsertOAuthLink :exec
INSERT INTO oauth_link (user_id, provider, provider_user_id, email, raw_profile)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (provider, provider_user_id) DO UPDATE SET
    email = EXCLUDED.email,
    raw_profile = EXCLUDED.raw_profile;

-- name: GetOAuthLink :one
SELECT user_id, email FROM oauth_link
WHERE provider = $1 AND provider_user_id = $2;

-- name: ListUserSellers :many
SELECT
    su.seller_id,
    s.display_name AS seller_name,
    su.roles_jsonb,
    su.status,
    su.joined_at,
    su.invited_at
FROM seller_user su
JOIN seller s ON s.id = su.seller_id
WHERE su.user_id = $1 AND su.status IN ('invited', 'active');

-- name: GetSellerUserByIDs :one
SELECT roles_jsonb, status FROM seller_user
WHERE seller_id = $1 AND user_id = $2;

-- name: InsertSellerUser :exec
INSERT INTO seller_user (user_id, seller_id, roles_jsonb, status, invited_at, joined_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateSellerUserRoles :exec
UPDATE seller_user SET roles_jsonb = $3 WHERE user_id = $1 AND seller_id = $2;

-- name: UpdateSellerUserStatus :exec
UPDATE seller_user SET status = $3, removed_at = $4 WHERE user_id = $1 AND seller_id = $2;

-- name: InsertInvite :exec
INSERT INTO seller_invite (id, seller_id, email, token_hash, roles_jsonb, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetInviteByTokenHash :one
SELECT id, seller_id, email, roles_jsonb, expires_at, accepted_at
FROM seller_invite WHERE token_hash = $1;

-- name: AcceptInvite :exec
UPDATE seller_invite SET accepted_at = now(), accepted_by_user_id = $2
WHERE id = $1 AND accepted_at IS NULL;

-- name: ExpireInvitedAt :execrows
DELETE FROM seller_invite
WHERE accepted_at IS NULL AND expires_at < now();
```

## Implementation

```go
// internal/identity/service_impl.go
package identity

import (
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

type serviceImpl struct {
    pool        *pgxpool.Pool
    repo        *repo
    audit       audit.Emitter
    auth        auth.Authenticator
    clock       core.Clock
    log         *slog.Logger
    inviteTTL   time.Duration
}

func New(pool *pgxpool.Pool, audit audit.Emitter, auth auth.Authenticator, clock core.Clock, log *slog.Logger) Service {
    return &serviceImpl{
        pool: pool, repo: newRepo(pool),
        audit: audit, auth: auth, clock: clock, log: log,
        inviteTTL: 72 * time.Hour,
    }
}

func (s *serviceImpl) UpsertFromOAuth(ctx context.Context, provider Provider, profile OAuthProfile) (User, error) {
    if profile.Email == "" || profile.ProviderUserID == "" {
        return User{}, fmt.Errorf("identity: missing email or provider id: %w", core.ErrInvalidArgument)
    }

    var user User

    err := dbtx.WithAdminTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        // Try to find existing OAuth link
        link, err := q.GetOAuthLink(ctx, db.GetOAuthLinkParams{
            Provider:       string(provider),
            ProviderUserID: profile.ProviderUserID,
        })
        if err == nil {
            // Existing OAuth → load user
            row, err := q.GetUserByID(ctx, link.UserID)
            if err != nil { return err }
            user = userFromRow(row)
            // Refresh OAuth metadata
            rawJSON, _ := json.Marshal(profile.Raw)
            _ = q.UpsertOAuthLink(ctx, db.UpsertOAuthLinkParams{
                UserID:         user.ID,
                Provider:       string(provider),
                ProviderUserID: profile.ProviderUserID,
                Email:          profile.Email,
                RawProfile:     rawJSON,
            })
            if user.Status == UserLocked { return ErrUserLocked }
            return nil
        }

        // No existing OAuth link; check email
        existing, err := q.GetUserByEmail(ctx, profile.Email)
        if err == nil {
            // Email exists with different OAuth provider; link this one
            user = userFromRow(existing)
            rawJSON, _ := json.Marshal(profile.Raw)
            _ = q.UpsertOAuthLink(ctx, db.UpsertOAuthLinkParams{
                UserID: user.ID, Provider: string(provider),
                ProviderUserID: profile.ProviderUserID,
                Email: profile.Email, RawProfile: rawJSON,
            })
            if user.Status == UserLocked { return ErrUserLocked }
            return nil
        }

        // Brand new user
        user.ID = core.UserID(uuid.New())
        user.Email = profile.Email
        user.Name = profile.Name
        user.Status = UserActive
        user.Kind = auth.UserKindSeller
        user.CreatedAt = s.clock.Now()

        if err := q.InsertUser(ctx, db.InsertUserParams{
            ID: uuid.UUID(user.ID), Email: user.Email, Name: user.Name, Kind: string(user.Kind),
        }); err != nil { return fmt.Errorf("identity: insert user: %w", err) }

        rawJSON, _ := json.Marshal(profile.Raw)
        if err := q.UpsertOAuthLink(ctx, db.UpsertOAuthLinkParams{
            UserID: uuid.UUID(user.ID), Provider: string(provider),
            ProviderUserID: profile.ProviderUserID,
            Email: profile.Email, RawProfile: rawJSON,
        }); err != nil { return fmt.Errorf("identity: insert oauth: %w", err) }

        return nil
    })

    return user, err
}

func (s *serviceImpl) ListUserSellers(ctx context.Context, userID core.UserID) ([]SellerMembership, error) {
    rows, err := s.repo.queries.ListUserSellers(ctx, uuid.UUID(userID))
    if err != nil { return nil, fmt.Errorf("identity: list user sellers: %w", err) }

    out := make([]SellerMembership, 0, len(rows))
    for _, r := range rows {
        var roles []core.SellerRole
        _ = json.Unmarshal(r.RolesJsonb, &roles)
        out = append(out, SellerMembership{
            SellerID:   core.SellerID(r.SellerID),
            SellerName: r.SellerName,
            Roles:      roles,
            Status:     MembershipStatus(r.Status),
            JoinedAt:   r.JoinedAt,
            InvitedAt:  r.InvitedAt,
        })
    }
    return out, nil
}

func (s *serviceImpl) SelectSeller(ctx context.Context, userID core.UserID, sellerID core.SellerID) (SellerMembership, error) {
    row, err := s.repo.queries.GetSellerUserByIDs(ctx, db.GetSellerUserByIDsParams{
        SellerID: uuid.UUID(sellerID), UserID: uuid.UUID(userID),
    })
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) { return SellerMembership{}, ErrNotMember }
        return SellerMembership{}, fmt.Errorf("identity: get seller user: %w", err)
    }
    if row.Status != "active" {
        return SellerMembership{}, ErrNotMember
    }
    var roles []core.SellerRole
    _ = json.Unmarshal(row.RolesJsonb, &roles)
    return SellerMembership{
        SellerID: sellerID, Roles: roles, Status: MembershipStatus(row.Status),
    }, nil
}

func (s *serviceImpl) InviteUserToSeller(ctx context.Context, sellerID core.SellerID, byUser core.UserID, in InviteInput) (Invite, error) {
    if in.Email == "" || len(in.Roles) == 0 {
        return Invite{}, fmt.Errorf("identity: invite missing email/roles: %w", core.ErrInvalidArgument)
    }

    token, err := generateInviteToken()
    if err != nil { return Invite{}, fmt.Errorf("identity: gen token: %w", err) }
    tokenHash := hashToken(token)

    invite := Invite{
        ID:        core.InviteID(uuid.New()),
        SellerID:  sellerID,
        Email:     in.Email,
        Roles:     in.Roles,
        Token:     token,
        InvitedBy: byUser,
        CreatedAt: s.clock.Now(),
        ExpiresAt: s.clock.Now().Add(s.inviteTTL),
    }

    rolesJSON, _ := json.Marshal(in.Roles)

    err = dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)
        if err := q.InsertInvite(ctx, db.InsertInviteParams{
            ID: uuid.UUID(invite.ID), SellerID: uuid.UUID(sellerID),
            Email: in.Email, TokenHash: tokenHash,
            RolesJsonb: rolesJSON, InvitedBy: uuid.UUID(byUser),
            ExpiresAt: invite.ExpiresAt,
        }); err != nil { return fmt.Errorf("insert invite: %w", err) }

        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: &sellerID, Action: "user.invited",
            Target:   audit.Target{Kind: "seller_invite", Ref: invite.ID.String()},
            Payload:  map[string]any{"email": in.Email, "roles": in.Roles},
        })
    })

    return invite, err
}

func (s *serviceImpl) AcceptInvite(ctx context.Context, token string, profile OAuthProfile) (User, SellerMembership, error) {
    tokenHash := hashToken(token)

    var user User
    var membership SellerMembership

    err := dbtx.WithAdminTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)
        invite, err := q.GetInviteByTokenHash(ctx, tokenHash)
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) { return ErrInviteNotFound }
            return err
        }
        if invite.AcceptedAt != nil { return ErrInviteAlreadyUsed }
        if invite.ExpiresAt.Before(s.clock.Now()) { return ErrInviteExpired }
        if profile.Email != invite.Email {
            return fmt.Errorf("identity: invite email mismatch: %w", core.ErrPermissionDenied)
        }

        // Upsert user (calls UpsertFromOAuth-like inline; or refactor)
        userInner, err := s.upsertFromOAuthInTx(ctx, q, ProviderGoogle, profile)
        if err != nil { return err }
        user = userInner

        // Mark invite accepted
        if err := q.AcceptInvite(ctx, db.AcceptInviteParams{ID: invite.ID, AcceptedByUserID: uuid.UUID(user.ID)}); err != nil {
            return err
        }

        // Insert seller_user
        if err := q.InsertSellerUser(ctx, db.InsertSellerUserParams{
            UserID:     uuid.UUID(user.ID),
            SellerID:   invite.SellerID,
            RolesJsonb: invite.RolesJsonb,
            Status:     "active",
            JoinedAt:   sql.NullTime{Time: s.clock.Now(), Valid: true},
        }); err != nil { return err }

        var roles []core.SellerRole
        _ = json.Unmarshal(invite.RolesJsonb, &roles)
        membership = SellerMembership{
            SellerID: core.SellerID(invite.SellerID),
            Roles:    roles,
            Status:   MembershipActive,
        }
        return nil
    })

    return user, membership, err
}

func (s *serviceImpl) RemoveUserFromSeller(ctx context.Context, sellerID core.SellerID, userID core.UserID, byUser core.UserID, reason string) error {
    err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)
        if err := q.UpdateSellerUserStatus(ctx, db.UpdateSellerUserStatusParams{
            UserID: uuid.UUID(userID), SellerID: uuid.UUID(sellerID),
            Status: "removed", RemovedAt: sql.NullTime{Time: s.clock.Now(), Valid: true},
        }); err != nil { return err }

        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: &sellerID, Action: "user.role_revoked",
            Target:   audit.Target{Kind: "seller_user", Ref: userID.String()},
            Payload:  map[string]any{"reason": reason, "by": byUser},
        })
    })
    if err != nil { return err }

    // Auth-side: revoke all sessions for this user (any seller they may have been scoped to)
    if err := s.auth.RevokeAllForUser(ctx, userID); err != nil {
        s.log.WarnContext(ctx, "revoke sessions on remove failed",
            slog.String("user_id", userID.String()), slog.Any("error", err))
        // Don't return error — membership already revoked; sessions will time out
    }
    return nil
}

func (s *serviceImpl) UpdateUserRoles(ctx context.Context, sellerID core.SellerID, userID core.UserID, roles []core.SellerRole, byUser core.UserID, reason string) error {
    if len(roles) == 0 {
        return fmt.Errorf("identity: at least one role required: %w", core.ErrInvalidArgument)
    }
    rolesJSON, _ := json.Marshal(roles)
    return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)
        if err := q.UpdateSellerUserRoles(ctx, db.UpdateSellerUserRolesParams{
            UserID: uuid.UUID(userID), SellerID: uuid.UUID(sellerID),
            RolesJsonb: rolesJSON,
        }); err != nil { return err }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: &sellerID, Action: "user.role_granted",
            Target:   audit.Target{Kind: "seller_user", Ref: userID.String()},
            Payload:  map[string]any{"roles": roles, "reason": reason, "by": byUser},
        })
    })
}

func (s *serviceImpl) LockUser(ctx context.Context, userID core.UserID, reason string, by core.UserID) error {
    err := dbtx.WithAdminTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)
        if err := q.UpdateUserStatus(ctx, db.UpdateUserStatusParams{ID: uuid.UUID(userID), Status: string(UserLocked)}); err != nil {
            return err
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            Action: "user.locked",
            Target: audit.Target{Kind: "app_user", Ref: userID.String()},
            Payload: map[string]any{"reason": reason, "by": by},
        })
    })
    if err != nil { return err }
    return s.auth.RevokeAllForUser(ctx, userID)
}
```

## Token generation

```go
// internal/identity/invite.go
package identity

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
)

func generateInviteToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil { return "", err }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return base64.RawURLEncoding.EncodeToString(h[:])
}
```

## Google OAuth flow (HTTP handlers in api/)

```go
// api/http/handlers/auth.go (sketch)
package handlers

import (
    "encoding/json"
    "net/http"

    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"

    "github.com/pikshipp/pikshipp/internal/identity"
)

// GoogleOAuthStart redirects the user to Google's consent page.
func GoogleOAuthStart(deps Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        state := generateState()
        http.SetCookie(w, &http.Cookie{
            Name: "oauth_state", Value: state,
            HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
            Path: "/v1/auth/google", MaxAge: 300,
        })
        url := deps.GoogleOAuthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
        http.Redirect(w, r, url, http.StatusFound)
    }
}

// GoogleOAuthCallback handles the redirect from Google.
func GoogleOAuthCallback(deps Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Verify state
        c, err := r.Cookie("oauth_state")
        if err != nil || r.URL.Query().Get("state") != c.Value {
            http.Error(w, "invalid state", 400)
            return
        }
        code := r.URL.Query().Get("code")
        if code == "" {
            http.Error(w, "missing code", 400)
            return
        }

        // Exchange for token
        tok, err := deps.GoogleOAuthConfig.Exchange(r.Context(), code)
        if err != nil { http.Error(w, "exchange failed", 500); return }

        // Fetch profile
        client := deps.GoogleOAuthConfig.Client(r.Context(), tok)
        resp, err := client.Get("https://www.googleapis.com/oauth2/v3/userinfo")
        if err != nil { http.Error(w, "profile fetch failed", 500); return }
        defer resp.Body.Close()

        var raw struct {
            Sub     string `json:"sub"`
            Email   string `json:"email"`
            Name    string `json:"name"`
            Picture string `json:"picture"`
        }
        if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
            http.Error(w, "profile parse failed", 500); return
        }

        profile := identity.OAuthProfile{
            ProviderUserID: raw.Sub,
            Email:          raw.Email,
            Name:           raw.Name,
            PictureURL:     raw.Picture,
            Raw:            map[string]any{"sub": raw.Sub, "email": raw.Email, "name": raw.Name, "picture": raw.Picture},
        }

        // Check for invite token in query
        inviteToken := r.URL.Query().Get("invite")
        var (
            user        identity.User
            membership  identity.SellerMembership
            err2        error
        )
        if inviteToken != "" {
            user, membership, err2 = deps.IdentitySvc.AcceptInvite(r.Context(), inviteToken, profile)
        } else {
            user, err2 = deps.IdentitySvc.UpsertFromOAuth(r.Context(), identity.ProviderGoogle, profile)
        }
        if err2 != nil { writeError(w, r, err2); return }

        // Determine seller scope
        memberships, _ := deps.IdentitySvc.ListUserSellers(r.Context(), user.ID)
        var sellerID core.SellerID
        if len(memberships) == 1 {
            sellerID = memberships[0].SellerID
        } else if inviteToken != "" {
            sellerID = membership.SellerID
        }
        // else: leave zero — frontend prompts user to pick

        // Issue session
        creds, err := deps.AuthSvc.IssueCredentials(r.Context(), user.ID, sellerID)
        if err != nil { writeError(w, r, err); return }

        // Set cookie
        deps.SessionAuth.SetSessionCookie(w, creds.Token, creds.ExpiresAt)

        // Redirect to dashboard (or seller picker)
        redirect := "/"
        if sellerID.IsZero() && len(memberships) > 1 { redirect = "/select-seller" }
        http.Redirect(w, r, redirect, http.StatusFound)
    }
}
```

## Tests

```go
func TestUpsertFromOAuth_NewUser_SLT(t *testing.T) {
    p := testdb.New(t)
    svc := setupIdentity(t, p.App)

    profile := identity.OAuthProfile{
        ProviderUserID: "google-12345",
        Email:          "alice@example.com",
        Name:           "Alice",
    }
    user, err := svc.UpsertFromOAuth(context.Background(), identity.ProviderGoogle, profile)
    require.NoError(t, err)
    require.Equal(t, "alice@example.com", user.Email)
    require.False(t, user.ID.IsZero())
}

func TestUpsertFromOAuth_ExistingUser_LinkUpdated_SLT(t *testing.T) {
    // First call creates user; second call with same email but updated name
    // should update the same user's record.
}

func TestInvite_FullFlow_SLT(t *testing.T) {
    // Owner invites
    invite, err := svc.InviteUserToSeller(...)
    require.NoError(t, err)

    // New user accepts via OAuth
    user, mem, err := svc.AcceptInvite(ctx, invite.Token, profile)
    require.NoError(t, err)
    require.Contains(t, mem.Roles, core.RoleOperator)
}

func TestInvite_ExpiredFails_SLT(t *testing.T) {
    clock := core.NewFakeClock(time.Now())
    svc := setupIdentity(t, p.App, clock)
    invite, _ := svc.InviteUserToSeller(...)

    clock.Advance(73 * time.Hour)

    _, _, err := svc.AcceptInvite(ctx, invite.Token, profile)
    require.ErrorIs(t, err, identity.ErrInviteExpired)
}

func TestRemoveUserFromSeller_RevokesSessions_SLT(t *testing.T) {
    // Verify auth.RevokeAllForUser was called for the removed user.
}
```

## Performance

- `UpsertFromOAuth`: 1-3 DB roundtrips; ~5-10ms.
- `ListUserSellers`: 1 indexed JOIN; ~5ms.
- `SelectSeller`: 1 indexed lookup; ~3ms.
- `InviteUserToSeller`: 1 INSERT + 1 audit; ~10ms.
- `AcceptInvite`: 3-5 DB ops; ~20ms.

## Open questions

- **Cross-seller user**: a CA serving multiple sellers. v0: each CA must invited by each seller separately. v1+ may support a "professional account" abstraction.
- **MFA enrollment**: TOTP comes in v1; identity service holds the secret.
- **Phone-as-login** (alternative to OAuth): not v0; v2+.
- **Pikshipp staff onboarding**: separate flow (not Google OAuth from public domain); may use an internal SSO. Not v0.

## References

- LLD `02-infrastructure/06-auth.md` (sessions table, session lifecycle).
- HLD `04-features/01-identity-and-onboarding.md`.
