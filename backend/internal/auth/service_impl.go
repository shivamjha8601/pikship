package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const defaultMaxIdle = 24 * time.Hour

// OpaqueSessionAuth is the production Authenticator. It uses a 32-byte random
// opaque token (stored as a SHA-256 hash in the DB) with a 30-second LRU
// cache. Revocation is propagated to other instances via pg_notify.
type OpaqueSessionAuth struct {
	repo   *repo
	cfg    SessionAuthConfig
	cache  *sessionCache
	clock  core.Clock
	log    *slog.Logger
}

// NewOpaqueSessionAuth constructs the authenticator. pool must be the app pool.
func NewOpaqueSessionAuth(pool *pgxpool.Pool, cfg SessionAuthConfig, clock core.Clock, log *slog.Logger) (*OpaqueSessionAuth, error) {
	if cfg.HMACKey.IsZero() {
		return nil, fmt.Errorf("auth: SessionAuthConfig.HMACKey is required")
	}
	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = defaultMaxIdle
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "pikshipp_session"
	}
	if cfg.CookiePath == "" {
		cfg.CookiePath = "/"
	}
	return &OpaqueSessionAuth{
		repo:  newRepo(pool),
		cfg:   cfg,
		cache: newSessionCache(clock),
		clock: clock,
		log:   log,
	}, nil
}

func (a *OpaqueSessionAuth) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	token := tokenFromRequest(r, a.cfg.CookieName)
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}

	hash := hashToken(token)

	if p, ok := a.cache.get(hash); ok {
		go a.repo.touchSession(context.Background(), hash, a.clock.Now(), a.cfg.MaxIdle)
		return p, nil
	}

	sr, err := a.repo.getSession(ctx, hash)
	if err != nil {
		return Principal{}, err
	}

	if sr.revokedAt != nil {
		a.cache.delete(hash)
		return Principal{}, ErrSessionRevoked
	}
	if a.clock.Now().After(sr.expiresAt) {
		return Principal{}, ErrSessionExpired
	}
	if sr.userStatus == "locked" {
		return Principal{}, fmt.Errorf("%w: user locked", ErrUnauthenticated)
	}

	p := Principal{
		UserID:     core.UserIDFromUUID(sr.userID),
		UserKind:   UserKind(sr.userKind),
		Roles:      rolesFromJSON(sr.rolesJSON),
		AuthMethod: "session",
	}
	if sr.selectedSellerID != nil {
		p.SellerID = core.SellerIDFromUUID(*sr.selectedSellerID)
	}

	a.cache.set(hash, p)
	go a.repo.touchSession(context.Background(), hash, a.clock.Now(), a.cfg.MaxIdle)

	// Populate audit actor context so downstream audit.Emit gets the right actor.
	_ = audit.WithActor(ctx, audit.Actor{
		Kind: audit.ActorKind(p.UserKind),
		Ref:  p.UserID.String(),
	})

	return p, nil
}

func (a *OpaqueSessionAuth) IssueCredentials(ctx context.Context, userID core.UserID, sellerID *core.SellerID) (Credentials, error) {
	token, err := generateToken()
	if err != nil {
		return Credentials{}, fmt.Errorf("auth.IssueCredentials: generate: %w", err)
	}

	hash := hashToken(token)
	expiresAt := a.clock.Now().Add(a.cfg.MaxIdle)

	if err := a.repo.insertSession(ctx, hash, userID, sellerID, expiresAt); err != nil {
		return Credentials{}, fmt.Errorf("auth.IssueCredentials: %w", err)
	}

	return Credentials{Token: token, ExpiresAt: expiresAt}, nil
}

func (a *OpaqueSessionAuth) Revoke(ctx context.Context, token string) error {
	hash := hashToken(token)
	if err := a.repo.revokeSession(ctx, hash, a.clock.Now()); err != nil {
		return fmt.Errorf("auth.Revoke: %w", err)
	}
	a.cache.delete(hash)
	go a.repo.notifyRevocation(context.Background(), hash)
	return nil
}

func (a *OpaqueSessionAuth) RevokeAllForUser(ctx context.Context, userID core.UserID) error {
	hashes, err := a.repo.revokeAllForUser(ctx, userID, a.clock.Now())
	if err != nil {
		return fmt.Errorf("auth.RevokeAllForUser: %w", err)
	}
	for _, h := range hashes {
		a.cache.delete(h)
		go a.repo.notifyRevocation(context.Background(), h)
	}
	return nil
}

// generateToken creates a 32-byte cryptographically random opaque token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the base64-url-encoded SHA-256 hash of token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
