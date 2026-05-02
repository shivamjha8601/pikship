package wallet

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
)

const defaultHoldTTL = 10 * time.Minute

type serviceImpl struct {
	repo  *repo
	pool  *pgxpool.Pool
	audit audit.Emitter
	log   *slog.Logger
}

// New constructs the wallet service. pool must be the app pool.
func New(pool *pgxpool.Pool, au audit.Emitter, log *slog.Logger) Service {
	return &serviceImpl{repo: newRepo(pool), pool: pool, audit: au, log: log}
}

func (s *serviceImpl) EnsureAccount(ctx context.Context, sellerID core.SellerID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return s.repo.ensureAccount(ctx, tx, sellerID)
	})
}

func (s *serviceImpl) Balance(ctx context.Context, sellerID core.SellerID) (Balance, error) {
	a, err := s.repo.getBalance(ctx, sellerID)
	if err != nil {
		return Balance{}, fmt.Errorf("wallet.Balance: %w", err)
	}
	available := a.balance + a.creditLimit - a.holdTotal
	return Balance{
		SellerID:    sellerID,
		Balance:     core.Paise(a.balance),
		HoldTotal:   core.Paise(a.holdTotal),
		Available:   core.Paise(available),
		CreditLimit: core.Paise(a.creditLimit),
		GraceAmount: core.Paise(a.graceAmount),
		Status:      a.status,
	}, nil
}

func (s *serviceImpl) Credit(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, reason, actor string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.repo.getAccountForUpdate(ctx, tx, sellerID)
		if err != nil {
			return fmt.Errorf("wallet.Credit: %w", err)
		}
		entryID := uuid.New()
		if err := s.repo.insertLedgerEntry(ctx, tx, entryID, a.id, sellerID.UUID(), DirectionCredit, int64(amount), ref, reason, actor); err != nil {
			return fmt.Errorf("wallet.Credit: ledger: %w", err)
		}
		if err := s.repo.updateBalance(ctx, tx, a.id, int64(amount)); err != nil {
			return fmt.Errorf("wallet.Credit: balance: %w", err)
		}
		return nil
	})
}

func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, ttl time.Duration) (core.HoldID, error) {
	if ttl == 0 {
		ttl = defaultHoldTTL
	}
	var holdID core.HoldID
	err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.repo.getAccountForUpdate(ctx, tx, sellerID)
		if err != nil {
			return fmt.Errorf("wallet.Reserve: %w", err)
		}
		if a.status != "active" {
			return fmt.Errorf("%w: account is %s", ErrAccountFrozen, a.status)
		}
		available := a.balance + a.creditLimit - a.holdTotal
		if available < int64(amount) {
			return fmt.Errorf("%w: available=%d requested=%d", ErrInsufficientFunds, available, int64(amount))
		}

		newHoldID := uuid.New()
		actualID, err := s.repo.insertOrGetHold(ctx, tx, newHoldID, a.id, sellerID.UUID(), int64(amount), ref, time.Now().Add(ttl))
		if err != nil {
			return fmt.Errorf("wallet.Reserve: insert hold: %w", err)
		}
		holdID = core.HoldIDFromUUID(actualID)

		// Only increment hold_total if this is a new hold (not an idempotent replay).
		if actualID == newHoldID {
			if err := s.repo.updateHoldTotal(ctx, tx, a.id, int64(amount)); err != nil {
				return fmt.Errorf("wallet.Reserve: update hold_total: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return core.HoldID{}, err
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "wallet.hold_created",
		Target:   audit.Target{Kind: "wallet_hold", Ref: holdID.String()},
		Payload:  map[string]any{"amount": int64(amount), "ref": ref},
	})
	return holdID, nil
}

func (s *serviceImpl) Confirm(ctx context.Context, sellerID core.SellerID, holdID core.HoldID, ref Ref, reason, actor string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		h, err := s.repo.getHoldForUpdate(ctx, tx, holdID)
		if err != nil {
			return fmt.Errorf("wallet.Confirm: %w", err)
		}
		if h.status == "confirmed" {
			return nil // idempotent
		}
		if h.status != "active" {
			return fmt.Errorf("wallet.Confirm: hold is %s, not active", h.status)
		}

		entryID := uuid.New()
		if err := s.repo.insertLedgerEntry(ctx, tx, entryID, h.walletID, h.sellerID, DirectionDebit, h.amount, ref, reason, actor); err != nil {
			return fmt.Errorf("wallet.Confirm: ledger: %w", err)
		}
		if err := s.repo.resolveHold(ctx, tx, h.id, "confirmed", &entryID); err != nil {
			return fmt.Errorf("wallet.Confirm: resolve: %w", err)
		}
		// Decrement both balance and hold_total.
		if err := s.repo.updateBalance(ctx, tx, h.walletID, -h.amount); err != nil {
			return fmt.Errorf("wallet.Confirm: balance: %w", err)
		}
		if err := s.repo.updateHoldTotal(ctx, tx, h.walletID, -h.amount); err != nil {
			return fmt.Errorf("wallet.Confirm: hold_total: %w", err)
		}
		return nil
	})
}

func (s *serviceImpl) Release(ctx context.Context, sellerID core.SellerID, holdID core.HoldID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		h, err := s.repo.getHoldForUpdate(ctx, tx, holdID)
		if err != nil {
			return fmt.Errorf("wallet.Release: %w", err)
		}
		if h.status == "released" || h.status == "expired" {
			return nil // idempotent
		}
		if h.status != "active" {
			return fmt.Errorf("wallet.Release: hold is %s", h.status)
		}
		if err := s.repo.resolveHold(ctx, tx, h.id, "released", nil); err != nil {
			return fmt.Errorf("wallet.Release: resolve: %w", err)
		}
		if err := s.repo.updateHoldTotal(ctx, tx, h.walletID, -h.amount); err != nil {
			return fmt.Errorf("wallet.Release: hold_total: %w", err)
		}
		return nil
	})
}

func (s *serviceImpl) Debit(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, reason, actor string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.repo.getAccountForUpdate(ctx, tx, sellerID)
		if err != nil {
			return fmt.Errorf("wallet.Debit: %w", err)
		}
		// Check grace floor: balance - amount >= -(grace + credit_limit)
		floor := -(a.graceAmount + a.creditLimit)
		if a.balance-int64(amount) < floor {
			return fmt.Errorf("%w: would breach grace floor", ErrInsufficientFunds)
		}
		entryID := uuid.New()
		if err := s.repo.insertLedgerEntry(ctx, tx, entryID, a.id, sellerID.UUID(), DirectionDebit, int64(amount), ref, reason, actor); err != nil {
			return fmt.Errorf("wallet.Debit: ledger: %w", err)
		}
		if err := s.repo.updateBalance(ctx, tx, a.id, -int64(amount)); err != nil {
			return fmt.Errorf("wallet.Debit: balance: %w", err)
		}
		return nil
	})
}
