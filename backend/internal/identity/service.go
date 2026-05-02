// Package identity manages user accounts, OAuth links, seller membership,
// and pending invitations.
//
// Per LLD §03-services/08-identity.
package identity

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Service is the public API of the identity module.
type Service interface {
	// UpsertFromOAuth finds or creates a user from an OAuth callback.
	// If the provider_user_id already exists in oauth_link the record is
	// updated; otherwise a new app_user + oauth_link are created atomically.
	UpsertFromOAuth(ctx context.Context, provider Provider, profile OAuthProfile) (User, error)

	// GetUser returns a user by ID.
	GetUser(ctx context.Context, userID core.UserID) (User, error)

	// ListUserSellers returns the seller memberships for userID.
	ListUserSellers(ctx context.Context, userID core.UserID) ([]SellerMembership, error)

	// SelectSeller checks the user belongs to sellerID and returns the membership.
	SelectSeller(ctx context.Context, userID core.UserID, sellerID core.SellerID) (SellerMembership, error)

	// InviteUserToSeller creates a pending invite and returns it.
	InviteUserToSeller(ctx context.Context, sellerID core.SellerID, byUser core.UserID, in InviteInput) (Invite, error)

	// AcceptInvite marks an invite accepted and creates/updates seller_user.
	// The invite is looked up by raw token (not hash). Profile is used to
	// upsert the user account if they don't have one yet.
	AcceptInvite(ctx context.Context, token string, profile OAuthProfile) (User, SellerMembership, error)

	// RemoveUserFromSeller sets seller_user.status = 'removed'.
	RemoveUserFromSeller(ctx context.Context, sellerID core.SellerID, userID core.UserID, byUser core.UserID, reason string) error

	// UpdateUserRoles changes the roles of a user within a seller.
	UpdateUserRoles(ctx context.Context, sellerID core.SellerID, userID core.UserID, roles []core.SellerRole, byUser core.UserID) error

	// LockUser sets app_user.status = 'locked', revokes all sessions.
	LockUser(ctx context.Context, userID core.UserID, reason string, by core.UserID) error

	// UnlockUser sets app_user.status = 'active'.
	UnlockUser(ctx context.Context, userID core.UserID, by core.UserID) error
}

// Provider identifies the OAuth provider.
type Provider string

const (
	ProviderGoogle Provider = "google"
)

// OAuthProfile is the normalized profile from an OAuth callback.
type OAuthProfile struct {
	ProviderUserID string
	Email          string
	Name           string
	PictureURL     string
	Raw            map[string]any
}

// User is the public view of app_user.
type User struct {
	ID        core.UserID
	Email     string
	Name      string
	Status    string
	Kind      string
	CreatedAt time.Time
}

// SellerMembership is a user's membership in a seller.
type SellerMembership struct {
	UserID   core.UserID
	SellerID core.SellerID
	Roles    []core.SellerRole
	Status   string
}

// InviteInput carries the parameters for sending an invite.
type InviteInput struct {
	Email  string
	Roles  []core.SellerRole
	TTL    time.Duration // zero → 7 days
}

// Invite is a pending seller invitation.
type Invite struct {
	ID        core.IdempotencyKeyID // re-using a uuid ID type (no dedicated type yet)
	SellerID  core.SellerID
	Email     string
	Roles     []core.SellerRole
	ExpiresAt time.Time
	Token     string // raw token (returned only on creation, not in reads)
}
