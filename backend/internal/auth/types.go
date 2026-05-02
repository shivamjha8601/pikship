// Package auth handles opaque-token session authentication.
//
// Authenticator is the public interface; OpaqueSessionAuth is the production
// implementation. The package calls audit.WithActor to populate the Actor
// context key so audit.ActorFromContext works throughout the request.
//
// Per LLD §02-infrastructure/06-auth and ADR 0003.
package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/secrets"
)

// Principal carries the authenticated identity for one HTTP request.
type Principal struct {
	UserID   core.UserID
	SellerID core.SellerID  // zero if user hasn't selected a seller yet
	Roles    []core.SellerRole
	UserKind UserKind
	AuthMethod string // "session"
}

// UserKind classifies the kind of user account.
type UserKind string

const (
	UserKindSeller          UserKind = "seller"
	UserKindPikshippAdmin   UserKind = "pikshipp_admin"
	UserKindPikshippOps     UserKind = "pikshipp_ops"
	UserKindPikshippSupport UserKind = "pikshipp_support"
	UserKindPikshippFinance UserKind = "pikshipp_finance"
	UserKindPikshippEng     UserKind = "pikshipp_eng"
)

// Credentials is returned by IssueCredentials.
type Credentials struct {
	Token     string
	ExpiresAt time.Time
}

// SessionAuthConfig configures OpaqueSessionAuth.
type SessionAuthConfig struct {
	HMACKey      secrets.Secret
	MaxIdle      time.Duration // zero → 24h
	CookieName   string        // zero → "pikshipp_session"
	CookieDomain string
	CookieSecure bool
	CookiePath   string // zero → "/"
}

// Sentinel errors returned by Authenticator.
var (
	ErrUnauthenticated = errors.New("auth: unauthenticated")
	ErrSessionExpired  = errors.New("auth: session expired")
	ErrSessionRevoked  = errors.New("auth: session revoked")
)

// tokenFromRequest extracts the raw opaque token from either
// the "pikshipp_session" cookie or the Authorization: Bearer header.
func tokenFromRequest(r *http.Request, cookieName string) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	const prefix = "Bearer "
	if hdr := r.Header.Get("Authorization"); len(hdr) > len(prefix) && hdr[:len(prefix)] == prefix {
		return hdr[len(prefix):]
	}
	return ""
}
