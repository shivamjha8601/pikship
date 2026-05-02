package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const defaultInviteTTL = 7 * 24 * time.Hour

type serviceImpl struct {
	repo  *repo
	audit audit.Emitter
	log   *slog.Logger
}

// New constructs the identity service. pool must be the app pool.
func New(pool *pgxpool.Pool, au audit.Emitter, log *slog.Logger) Service {
	return &serviceImpl{
		repo:  newRepo(pool),
		audit: au,
		log:   log,
	}
}

func (s *serviceImpl) UpsertFromOAuth(ctx context.Context, provider Provider, profile OAuthProfile) (User, error) {
	// Check if this OAuth identity already exists.
	existingUserID, exists, err := s.repo.getOAuthLink(ctx, provider, profile.ProviderUserID)
	if err != nil {
		return User{}, fmt.Errorf("identity.UpsertFromOAuth: %w", err)
	}

	if exists {
		// Update the oauth_link and return the existing user.
		if err := s.repo.upsertOAuthLink(ctx, existingUserID, provider, profile.ProviderUserID, profile.Email, profile.Raw); err != nil {
			return User{}, fmt.Errorf("identity.UpsertFromOAuth: update link: %w", err)
		}
		return s.repo.getUserByID(ctx, existingUserID)
	}

	// Check if a user exists with this email.
	user, err := s.repo.getUserByEmail(ctx, profile.Email)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return User{}, fmt.Errorf("identity.UpsertFromOAuth: get by email: %w", err)
	}

	if errors.Is(err, core.ErrNotFound) {
		// Create new user.
		user, err = s.repo.insertUser(ctx, profile.Email, profile.Name, "seller")
		if err != nil {
			return User{}, fmt.Errorf("identity.UpsertFromOAuth: insert user: %w", err)
		}
	}

	// Link the OAuth identity.
	if err := s.repo.upsertOAuthLink(ctx, user.ID, provider, profile.ProviderUserID, profile.Email, profile.Raw); err != nil {
		return User{}, fmt.Errorf("identity.UpsertFromOAuth: link: %w", err)
	}
	return user, nil
}

func (s *serviceImpl) GetUser(ctx context.Context, userID core.UserID) (User, error) {
	return s.repo.getUserByID(ctx, userID)
}

func (s *serviceImpl) ListUserSellers(ctx context.Context, userID core.UserID) ([]SellerMembership, error) {
	return s.repo.listUserSellers(ctx, userID)
}

func (s *serviceImpl) SelectSeller(ctx context.Context, userID core.UserID, sellerID core.SellerID) (SellerMembership, error) {
	m, err := s.repo.getSellerMembership(ctx, userID, sellerID)
	if err != nil {
		return SellerMembership{}, fmt.Errorf("identity.SelectSeller: %w", err)
	}
	if m.Status != "active" {
		return SellerMembership{}, fmt.Errorf("%w: membership is not active", core.ErrPermissionDenied)
	}
	return m, nil
}

func (s *serviceImpl) InviteUserToSeller(ctx context.Context, sellerID core.SellerID, byUser core.UserID, in InviteInput) (Invite, error) {
	if in.TTL == 0 {
		in.TTL = defaultInviteTTL
	}
	token, hash, err := generateInviteToken()
	if err != nil {
		return Invite{}, fmt.Errorf("identity.InviteUserToSeller: generate token: %w", err)
	}
	expiresAt := time.Now().Add(in.TTL)
	id, err := s.repo.insertInvite(ctx, sellerID, in.Email, hash, in.Roles, byUser, expiresAt)
	if err != nil {
		return Invite{}, fmt.Errorf("identity.InviteUserToSeller: %w", err)
	}

	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "seller.invite_sent",
		Target:   audit.Target{Kind: "invite", Ref: id.String()},
		Payload:  map[string]any{"email": in.Email},
	})

	return Invite{
		SellerID:  sellerID,
		Email:     in.Email,
		Roles:     in.Roles,
		ExpiresAt: expiresAt,
		Token:     token,
	}, nil
}

func (s *serviceImpl) AcceptInvite(ctx context.Context, token string, profile OAuthProfile) (User, SellerMembership, error) {
	hash := hashInviteToken(token)

	inv, err := s.repo.getInviteByHash(ctx, hash)
	if err != nil {
		return User{}, SellerMembership{}, fmt.Errorf("identity.AcceptInvite: %w", err)
	}
	if inv.acceptedAt != nil {
		return User{}, SellerMembership{}, fmt.Errorf("%w: invite already accepted", core.ErrConflict)
	}
	if time.Now().After(inv.expiresAt) {
		return User{}, SellerMembership{}, fmt.Errorf("%w: invite expired", core.ErrInvalidArgument)
	}

	user, err := s.UpsertFromOAuth(ctx, ProviderGoogle, profile)
	if err != nil {
		return User{}, SellerMembership{}, fmt.Errorf("identity.AcceptInvite: upsert user: %w", err)
	}

	if err := s.repo.upsertSellerUser(ctx, user.ID, inv.sellerID, inv.roles); err != nil {
		return User{}, SellerMembership{}, fmt.Errorf("identity.AcceptInvite: upsert seller_user: %w", err)
	}
	if err := s.repo.acceptInvite(ctx, hash, user.ID); err != nil {
		return User{}, SellerMembership{}, fmt.Errorf("identity.AcceptInvite: mark accepted: %w", err)
	}

	m := SellerMembership{UserID: user.ID, SellerID: inv.sellerID, Roles: inv.roles, Status: "active"}
	return user, m, nil
}

func (s *serviceImpl) RemoveUserFromSeller(ctx context.Context, sellerID core.SellerID, userID core.UserID, byUser core.UserID, reason string) error {
	if err := s.repo.removeSellerUser(ctx, userID, sellerID); err != nil {
		return fmt.Errorf("identity.RemoveUserFromSeller: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "seller.member_removed",
		Target:   audit.Target{Kind: "app_user", Ref: userID.String()},
		Payload:  map[string]any{"reason": reason, "by": byUser.String()},
	})
	return nil
}

func (s *serviceImpl) UpdateUserRoles(ctx context.Context, sellerID core.SellerID, userID core.UserID, roles []core.SellerRole, byUser core.UserID) error {
	if err := s.repo.updateSellerUserRoles(ctx, userID, sellerID, roles); err != nil {
		return fmt.Errorf("identity.UpdateUserRoles: %w", err)
	}
	return nil
}

func (s *serviceImpl) LockUser(ctx context.Context, userID core.UserID, reason string, by core.UserID) error {
	if err := s.repo.updateUserStatus(ctx, userID, "locked"); err != nil {
		return fmt.Errorf("identity.LockUser: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		Action: "user.locked",
		Target: audit.Target{Kind: "app_user", Ref: userID.String()},
		Payload: map[string]any{"reason": reason, "by": by.String()},
	})
	return nil
}

func (s *serviceImpl) UnlockUser(ctx context.Context, userID core.UserID, by core.UserID) error {
	return s.repo.updateUserStatus(ctx, userID, "active")
}
