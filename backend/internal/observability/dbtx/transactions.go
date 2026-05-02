package dbtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// TxFunc is the body of a transactional operation. The provided pgx.Tx is
// the active transaction; the seller scope GUC (when applicable) is set.
// fn must NOT call Commit / Rollback — the helper handles both.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// WithSellerTx executes fn inside a transaction scoped to sellerID.
//
//  1. Begins a transaction with default isolation.
//  2. SET LOCAL app.seller_id = <sellerID> so RLS scopes the work.
//  3. Calls fn.
//  4. Commits on nil; rolls back on error.
//
// The helper rolls back automatically on context cancellation (pgx behavior).
// This is the canonical entry point for any seller-scoped DB work — domain
// code MUST NOT call pool.Begin() directly. (LLD §02-infrastructure/01.)
func WithSellerTx(ctx context.Context, pool *pgxpool.Pool, sellerID core.SellerID, fn TxFunc) error {
	if sellerID.IsZero() {
		return fmt.Errorf("dbtx.WithSellerTx: %w", core.ErrSellerScope)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("dbtx.WithSellerTx: begin: %w", err)
	}
	// Best-effort rollback if fn panics or returns an error.
	// pgx.Tx.Rollback after Commit is a no-op; safe to defer unconditionally.
	defer func() { _ = tx.Rollback(context.Background()) }()

	// SET LOCAL is scoped to this transaction; auto-cleared on COMMIT/ROLLBACK.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.seller_id', $1, true)", sellerID.String()); err != nil {
		return fmt.Errorf("dbtx.WithSellerTx: set seller_id: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbtx.WithSellerTx: commit: %w", err)
	}
	return nil
}

// WithReadOnlyTx is WithSellerTx with read-only access mode. Useful for
// query handlers that only read.
func WithReadOnlyTx(ctx context.Context, pool *pgxpool.Pool, sellerID core.SellerID, fn TxFunc) error {
	if sellerID.IsZero() {
		return fmt.Errorf("dbtx.WithReadOnlyTx: %w", core.ErrSellerScope)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("dbtx.WithReadOnlyTx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.seller_id', $1, true)", sellerID.String()); err != nil {
		return fmt.Errorf("dbtx.WithReadOnlyTx: set seller_id: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}
	// Read-only commit is cheap but still required to release the snapshot.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbtx.WithReadOnlyTx: commit: %w", err)
	}
	return nil
}

// WithAdminTx executes fn inside a transaction with no seller scope. The
// pool MUST be the admin pool (RoleAdmin), which has BYPASSRLS.
//
// Pre-conditions enforced by the caller, NOT this helper:
//   - Caller has authorized this access via Pikshipp Admin role check.
//   - Caller emits an audit event for the cross-seller access.
//
// Misuse is a code-review failure; the helper is intentionally minimal.
func WithAdminTx(ctx context.Context, pool *pgxpool.Pool, fn TxFunc) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("dbtx.WithAdminTx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbtx.WithAdminTx: commit: %w", err)
	}
	return nil
}

// IsRetryableError reports whether err is a transient Postgres condition
// that warrants a retry (serialization failure, deadlock detected). Domain
// code rarely retries directly; this is for the wrapper that runs around
// transaction helpers in workers.
func IsRetryableError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", // serialization_failure
			"40P01": // deadlock_detected
			return true
		}
	}
	return false
}
