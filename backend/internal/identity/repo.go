package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	getUserByIDSQL = `
        SELECT id, email, name, status, kind, created_at
        FROM app_user WHERE id = $1
    `
	getUserByEmailSQL = `
        SELECT id, email, name, status, kind, created_at
        FROM app_user WHERE email = $1
    `
	insertUserSQL = `
        INSERT INTO app_user (email, name, kind)
        VALUES ($1, $2, $3)
        RETURNING id, email, name, status, kind, created_at
    `
	updateUserStatusSQL = `
        UPDATE app_user SET status = $2 WHERE id = $1
    `

	getOAuthLinkSQL = `
        SELECT user_id FROM oauth_link
        WHERE provider = $1 AND provider_user_id = $2
    `
	upsertOAuthLinkSQL = `
        INSERT INTO oauth_link (user_id, provider, provider_user_id, email, raw_profile)
        VALUES ($1, $2, $3, $4, $5::jsonb)
        ON CONFLICT (provider, provider_user_id) DO UPDATE
            SET email = EXCLUDED.email, raw_profile = EXCLUDED.raw_profile
    `

	listUserSellersSQL = `
        SELECT user_id, seller_id, roles_jsonb, status
        FROM seller_user
        WHERE user_id = $1 AND status = 'active'
    `
	getSellerMembershipSQL = `
        SELECT user_id, seller_id, roles_jsonb, status
        FROM seller_user
        WHERE user_id = $1 AND seller_id = $2
    `
	upsertSellerUserSQL = `
        INSERT INTO seller_user (user_id, seller_id, roles_jsonb, status, joined_at)
        VALUES ($1, $2, $3::jsonb, 'active', now())
        ON CONFLICT (user_id, seller_id) DO UPDATE
            SET roles_jsonb = EXCLUDED.roles_jsonb,
                status = 'active',
                joined_at = COALESCE(seller_user.joined_at, now())
    `
	updateSellerUserRolesSQL = `
        UPDATE seller_user SET roles_jsonb = $3::jsonb WHERE user_id = $1 AND seller_id = $2
    `
	removeSellerUserSQL = `
        UPDATE seller_user SET status = 'removed', removed_at = now()
        WHERE user_id = $1 AND seller_id = $2
    `

	insertInviteSQL = `
        INSERT INTO seller_invite (seller_id, email, token_hash, roles_jsonb, invited_by, expires_at)
        VALUES ($1, $2, $3, $4::jsonb, $5, $6)
        RETURNING id
    `
	getInviteByHashSQL = `
        SELECT id, seller_id, email, roles_jsonb, expires_at, accepted_at
        FROM seller_invite
        WHERE token_hash = $1
    `
	acceptInviteSQL = `
        UPDATE seller_invite
        SET accepted_at = now(), accepted_by_user_id = $2
        WHERE token_hash = $1 AND accepted_at IS NULL
    `
)

type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

func (r *repo) getUserByID(ctx context.Context, id core.UserID) (User, error) {
	return r.scanUser(r.pool.QueryRow(ctx, getUserByIDSQL, id.UUID()))
}

func (r *repo) getUserByEmail(ctx context.Context, email string) (User, error) {
	return r.scanUser(r.pool.QueryRow(ctx, getUserByEmailSQL, email))
}

func (r *repo) insertUser(ctx context.Context, email, name, kind string) (User, error) {
	return r.scanUser(r.pool.QueryRow(ctx, insertUserSQL, email, name, kind))
}

func (r *repo) scanUser(row pgx.Row) (User, error) {
	var u User
	var id uuid.UUID
	if err := row.Scan(&id, &u.Email, &u.Name, &u.Status, &u.Kind, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, core.ErrNotFound
		}
		return User{}, fmt.Errorf("identity.scanUser: %w", err)
	}
	u.ID = core.UserIDFromUUID(id)
	return u, nil
}

func (r *repo) updateUserStatus(ctx context.Context, id core.UserID, status string) error {
	_, err := r.pool.Exec(ctx, updateUserStatusSQL, id.UUID(), status)
	return err
}

func (r *repo) getOAuthLink(ctx context.Context, provider Provider, providerUserID string) (core.UserID, bool, error) {
	var uid uuid.UUID
	err := r.pool.QueryRow(ctx, getOAuthLinkSQL, string(provider), providerUserID).Scan(&uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.UserID{}, false, nil
		}
		return core.UserID{}, false, fmt.Errorf("identity.getOAuthLink: %w", err)
	}
	return core.UserIDFromUUID(uid), true, nil
}

func (r *repo) upsertOAuthLink(ctx context.Context, userID core.UserID, provider Provider, providerUserID, email string, raw map[string]any) error {
	rawJSON, _ := json.Marshal(raw)
	_, err := r.pool.Exec(ctx, upsertOAuthLinkSQL,
		userID.UUID(), string(provider), providerUserID, email, rawJSON,
	)
	if err != nil {
		return fmt.Errorf("identity.upsertOAuthLink: %w", err)
	}
	return nil
}

func (r *repo) listUserSellers(ctx context.Context, userID core.UserID) ([]SellerMembership, error) {
	rows, err := r.pool.Query(ctx, listUserSellersSQL, userID.UUID())
	if err != nil {
		return nil, fmt.Errorf("identity.listUserSellers: %w", err)
	}
	defer rows.Close()
	return r.scanMemberships(rows)
}

func (r *repo) getSellerMembership(ctx context.Context, userID core.UserID, sellerID core.SellerID) (SellerMembership, error) {
	rows, err := r.pool.Query(ctx, getSellerMembershipSQL, userID.UUID(), sellerID.UUID())
	if err != nil {
		return SellerMembership{}, fmt.Errorf("identity.getSellerMembership: %w", err)
	}
	defer rows.Close()
	ms, err := r.scanMemberships(rows)
	if err != nil {
		return SellerMembership{}, err
	}
	if len(ms) == 0 {
		return SellerMembership{}, core.ErrNotFound
	}
	return ms[0], nil
}

func (r *repo) scanMemberships(rows pgx.Rows) ([]SellerMembership, error) {
	var out []SellerMembership
	for rows.Next() {
		var uid, sid uuid.UUID
		var rolesJSON []byte
		var status string
		if err := rows.Scan(&uid, &sid, &rolesJSON, &status); err != nil {
			return nil, fmt.Errorf("identity.scanMemberships: %w", err)
		}
		var rawRoles []string
		_ = json.Unmarshal(rolesJSON, &rawRoles)
		roles := make([]core.SellerRole, len(rawRoles))
		for i, rr := range rawRoles {
			roles[i] = core.SellerRole(rr)
		}
		out = append(out, SellerMembership{
			UserID:   core.UserIDFromUUID(uid),
			SellerID: core.SellerIDFromUUID(sid),
			Roles:    roles,
			Status:   status,
		})
	}
	return out, rows.Err()
}

func (r *repo) upsertSellerUser(ctx context.Context, userID core.UserID, sellerID core.SellerID, roles []core.SellerRole) error {
	rolesJSON, _ := json.Marshal(rolesToStrings(roles))
	_, err := r.pool.Exec(ctx, upsertSellerUserSQL, userID.UUID(), sellerID.UUID(), rolesJSON)
	if err != nil {
		return fmt.Errorf("identity.upsertSellerUser: %w", err)
	}
	return nil
}

func (r *repo) updateSellerUserRoles(ctx context.Context, userID core.UserID, sellerID core.SellerID, roles []core.SellerRole) error {
	rolesJSON, _ := json.Marshal(rolesToStrings(roles))
	_, err := r.pool.Exec(ctx, updateSellerUserRolesSQL, userID.UUID(), sellerID.UUID(), rolesJSON)
	return err
}

func (r *repo) removeSellerUser(ctx context.Context, userID core.UserID, sellerID core.SellerID) error {
	_, err := r.pool.Exec(ctx, removeSellerUserSQL, userID.UUID(), sellerID.UUID())
	return err
}

func (r *repo) insertInvite(ctx context.Context, sellerID core.SellerID, email, tokenHash string, roles []core.SellerRole, invitedBy core.UserID, expiresAt time.Time) (uuid.UUID, error) {
	rolesJSON, _ := json.Marshal(rolesToStrings(roles))
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, insertInviteSQL,
		sellerID.UUID(), email, tokenHash, rolesJSON, invitedBy.UUID(), expiresAt,
	).Scan(&id)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity.insertInvite: %w", err)
	}
	return id, nil
}

type inviteRow struct {
	id         uuid.UUID
	sellerID   core.SellerID
	email      string
	roles      []core.SellerRole
	expiresAt  time.Time
	acceptedAt *time.Time
}

func (r *repo) getInviteByHash(ctx context.Context, tokenHash string) (inviteRow, error) {
	var inv inviteRow
	var sid uuid.UUID
	var id uuid.UUID
	var rolesJSON []byte
	err := r.pool.QueryRow(ctx, getInviteByHashSQL, tokenHash).
		Scan(&id, &sid, &inv.email, &rolesJSON, &inv.expiresAt, &inv.acceptedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return inviteRow{}, core.ErrNotFound
		}
		return inviteRow{}, fmt.Errorf("identity.getInviteByHash: %w", err)
	}
	inv.id = id
	inv.sellerID = core.SellerIDFromUUID(sid)
	var rawRoles []string
	_ = json.Unmarshal(rolesJSON, &rawRoles)
	inv.roles = make([]core.SellerRole, len(rawRoles))
	for i, rr := range rawRoles {
		inv.roles[i] = core.SellerRole(rr)
	}
	return inv, nil
}

func (r *repo) acceptInvite(ctx context.Context, tokenHash string, userID core.UserID) error {
	_, err := r.pool.Exec(ctx, acceptInviteSQL, tokenHash, userID.UUID())
	return err
}

func rolesToStrings(roles []core.SellerRole) []string {
	s := make([]string, len(roles))
	for i, r := range roles {
		s[i] = string(r)
	}
	return s
}

func generateInviteToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	hash = hashInviteToken(token)
	return
}

func hashInviteToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
