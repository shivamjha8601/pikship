package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	ensureAccountSQL = `
        INSERT INTO wallet_account (seller_id) VALUES ($1)
        ON CONFLICT (seller_id) DO NOTHING
    `
	getAccountForUpdateSQL = `
        SELECT id, balance_minor, hold_total_minor, credit_limit_minor,
               grace_negative_amount_minor, status
        FROM wallet_account WHERE seller_id = $1 FOR UPDATE
    `
	getBalanceSQL = `
        SELECT id, balance_minor, hold_total_minor, credit_limit_minor,
               grace_negative_amount_minor, status
        FROM wallet_account WHERE seller_id = $1
    `
	updateBalanceSQL = `
        UPDATE wallet_account
        SET balance_minor = balance_minor + $2, updated_at = now()
        WHERE id = $1
    `
	updateHoldTotalSQL = `
        UPDATE wallet_account
        SET hold_total_minor = hold_total_minor + $2, updated_at = now()
        WHERE id = $1
    `
	insertLedgerEntrySQL = `
        INSERT INTO wallet_ledger_entry
            (id, seller_id, wallet_id, direction, amount_minor, ref_type, ref_id, reason, actor_ref)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        ON CONFLICT (ref_type, ref_id, direction) DO NOTHING
    `
	// On conflict, only return the existing id when the prior hold is still
	// active. Stale (expired/released/confirmed) rows would otherwise be
	// handed back to callers who'd then fail Confirm/Release.
	insertHoldSQL = `
        INSERT INTO wallet_hold
            (id, seller_id, wallet_id, amount_minor, ref_type, ref_id, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (ref_type, ref_id) DO UPDATE
            SET id = CASE WHEN wallet_hold.status = 'active'
                          THEN wallet_hold.id
                          ELSE EXCLUDED.id END
        RETURNING id
    `
	getHoldForUpdateSQL = `
        SELECT id, wallet_id, seller_id, amount_minor, status
        FROM wallet_hold WHERE id = $1 FOR UPDATE
    `
	resolveHoldSQL = `
        UPDATE wallet_hold
        SET status = $2, resolved_at = now(), resolved_to_ledger_entry_id = $3
        WHERE id = $1
    `
)

type accountRow struct {
	id          uuid.UUID
	balance     int64
	holdTotal   int64
	creditLimit int64
	graceAmount int64
	status      string
}

type holdRow struct {
	id       uuid.UUID
	walletID uuid.UUID
	sellerID uuid.UUID
	amount   int64
	status   string
}

type repo struct{ pool *pgxpool.Pool }

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

func (r *repo) ensureAccount(ctx context.Context, tx pgx.Tx, sellerID core.SellerID) error {
	_, err := tx.Exec(ctx, ensureAccountSQL, sellerID.UUID())
	return err
}

func (r *repo) getAccountForUpdate(ctx context.Context, tx pgx.Tx, sellerID core.SellerID) (accountRow, error) {
	var a accountRow
	err := tx.QueryRow(ctx, getAccountForUpdateSQL, sellerID.UUID()).
		Scan(&a.id, &a.balance, &a.holdTotal, &a.creditLimit, &a.graceAmount, &a.status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accountRow{}, core.ErrNotFound
		}
		return accountRow{}, fmt.Errorf("wallet.getAccount: %w", err)
	}
	return a, nil
}

func (r *repo) getBalance(ctx context.Context, sellerID core.SellerID) (accountRow, error) {
	var a accountRow
	err := r.pool.QueryRow(ctx, getBalanceSQL, sellerID.UUID()).
		Scan(&a.id, &a.balance, &a.holdTotal, &a.creditLimit, &a.graceAmount, &a.status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accountRow{}, core.ErrNotFound
		}
		return accountRow{}, fmt.Errorf("wallet.getBalance: %w", err)
	}
	return a, nil
}

func (r *repo) updateBalance(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, delta int64) error {
	_, err := tx.Exec(ctx, updateBalanceSQL, walletID, delta)
	return err
}

func (r *repo) updateHoldTotal(ctx context.Context, tx pgx.Tx, walletID uuid.UUID, delta int64) error {
	_, err := tx.Exec(ctx, updateHoldTotalSQL, walletID, delta)
	return err
}

func (r *repo) insertLedgerEntry(ctx context.Context, tx pgx.Tx, id, walletID, sellerID uuid.UUID, dir Direction, amount int64, ref Ref, reason, actor string) error {
	_, err := tx.Exec(ctx, insertLedgerEntrySQL,
		id, sellerID, walletID, string(dir), amount, ref.Type, ref.ID, reason, actor,
	)
	return err
}

func (r *repo) insertOrGetHold(ctx context.Context, tx pgx.Tx, holdID, walletID, sellerID uuid.UUID, amount int64, ref Ref, expiresAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, insertHoldSQL,
		holdID, sellerID, walletID, amount, ref.Type, ref.ID, expiresAt,
	).Scan(&id)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("wallet.insertHold: %w", err)
	}
	return id, nil
}

func (r *repo) getHoldForUpdate(ctx context.Context, tx pgx.Tx, holdID core.HoldID) (holdRow, error) {
	var h holdRow
	err := tx.QueryRow(ctx, getHoldForUpdateSQL, holdID.UUID()).
		Scan(&h.id, &h.walletID, &h.sellerID, &h.amount, &h.status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return holdRow{}, core.ErrNotFound
		}
		return holdRow{}, fmt.Errorf("wallet.getHold: %w", err)
	}
	return h, nil
}

func (r *repo) resolveHold(ctx context.Context, tx pgx.Tx, holdID uuid.UUID, status string, ledgerEntryID *uuid.UUID) error {
	_, err := tx.Exec(ctx, resolveHoldSQL, holdID, status, ledgerEntryID)
	return err
}
