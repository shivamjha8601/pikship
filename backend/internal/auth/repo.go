package auth

import (
	"context"
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
	getSessionSQL = `
        SELECT s.id, s.token_hash, s.user_id, s.selected_seller_id,
               s.expires_at, s.last_seen_at, s.revoked_at,
               u.kind, u.status,
               COALESCE(su.roles_jsonb, '[]'::jsonb)
        FROM session s
        JOIN app_user u ON u.id = s.user_id
        LEFT JOIN seller_user su
            ON su.user_id = s.user_id
           AND su.seller_id = s.selected_seller_id
           AND su.status = 'active'
        WHERE s.token_hash = $1
    `

	insertSessionSQL = `
        INSERT INTO session (token_hash, user_id, selected_seller_id, expires_at)
        VALUES ($1, $2, $3, $4)
    `

	revokeSessionSQL = `
        UPDATE session SET revoked_at = $2
        WHERE token_hash = $1 AND revoked_at IS NULL
    `

	revokeAllForUserSQL = `
        UPDATE session SET revoked_at = $2
        WHERE user_id = $1 AND revoked_at IS NULL
        RETURNING token_hash
    `

	touchSessionSQL = `
        UPDATE session
        SET last_seen_at = $2,
            expires_at   = $2 + ($3 * INTERVAL '1 microsecond')
        WHERE token_hash = $1 AND revoked_at IS NULL
    `

	notifyRevocationSQL = `SELECT pg_notify('session_revoked', $1)`
)

type sessionRow struct {
	tokenHash          string
	userID             uuid.UUID
	selectedSellerID   *uuid.UUID
	expiresAt          time.Time
	lastSeenAt         time.Time
	revokedAt          *time.Time
	userKind           string
	userStatus         string
	rolesJSON          []byte
}

type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

func (r *repo) getSession(ctx context.Context, tokenHash string) (sessionRow, error) {
	row := r.pool.QueryRow(ctx, getSessionSQL, tokenHash)
	var sr sessionRow
	if err := row.Scan(
		new(uuid.UUID), // session.id (unused)
		&sr.tokenHash,
		&sr.userID,
		&sr.selectedSellerID,
		&sr.expiresAt,
		&sr.lastSeenAt,
		&sr.revokedAt,
		&sr.userKind,
		&sr.userStatus,
		&sr.rolesJSON,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sessionRow{}, ErrUnauthenticated
		}
		return sessionRow{}, fmt.Errorf("auth.getSession: %w", err)
	}
	return sr, nil
}

func (r *repo) insertSession(ctx context.Context, tokenHash string, userID core.UserID, sellerID *core.SellerID, expiresAt time.Time) error {
	var sellerUUID *uuid.UUID
	if sellerID != nil {
		u := sellerID.UUID()
		sellerUUID = &u
	}
	_, err := r.pool.Exec(ctx, insertSessionSQL,
		tokenHash, userID.UUID(), sellerUUID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("auth.insertSession: %w", err)
	}
	return nil
}

func (r *repo) revokeSession(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := r.pool.Exec(ctx, revokeSessionSQL, tokenHash, now)
	if err != nil {
		return fmt.Errorf("auth.revokeSession: %w", err)
	}
	return nil
}

func (r *repo) revokeAllForUser(ctx context.Context, userID core.UserID, now time.Time) ([]string, error) {
	rows, err := r.pool.Query(ctx, revokeAllForUserSQL, userID.UUID(), now)
	if err != nil {
		return nil, fmt.Errorf("auth.revokeAllForUser: %w", err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("auth.revokeAllForUser scan: %w", err)
		}
		hashes = append(hashes, h)
	}
	return hashes, rows.Err()
}

func (r *repo) touchSession(ctx context.Context, tokenHash string, now time.Time, idleWindow time.Duration) {
	// Fire-and-forget. Errors are non-fatal.
	_, _ = r.pool.Exec(ctx, touchSessionSQL,
		tokenHash, now, idleWindow.Microseconds(),
	)
}

func (r *repo) notifyRevocation(ctx context.Context, tokenHash string) {
	_, _ = r.pool.Exec(ctx, notifyRevocationSQL, tokenHash)
}

// rolesFromJSON decodes the seller_user.roles_jsonb value.
func rolesFromJSON(data []byte) []core.SellerRole {
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make([]core.SellerRole, len(raw))
	for i, r := range raw {
		out[i] = core.SellerRole(r)
	}
	return out
}
