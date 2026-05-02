// Package wallet implements the two-phase wallet: Reserve → Confirm/Release.
//
// Per LLD §03-services/05-wallet.
package wallet

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Service is the public API of the wallet module.
type Service interface {
	// EnsureAccount creates a wallet_account for sellerID if one doesn't exist.
	EnsureAccount(ctx context.Context, sellerID core.SellerID) error

	// Balance returns the current balance view for a seller.
	Balance(ctx context.Context, sellerID core.SellerID) (Balance, error)

	// Credit posts an immediate credit (funds added, COD remittance, etc.).
	// Idempotent on (ref.Type, ref.ID).
	Credit(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, reason, actor string) error

	// Reserve creates a hold for the given amount and returns the HoldID.
	// Returns ErrInsufficientFunds if available < amount.
	// Idempotent on ref.
	Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, ttl time.Duration) (core.HoldID, error)

	// Confirm debits the wallet and resolves the hold.
	// Idempotent on (ref.Type, ref.ID).
	Confirm(ctx context.Context, sellerID core.SellerID, holdID core.HoldID, ref Ref, reason, actor string) error

	// Release releases a hold (no charge).
	// Idempotent: safe to call on an already-released hold.
	Release(ctx context.Context, sellerID core.SellerID, holdID core.HoldID) error

	// Debit posts an immediate debit (charges not covered by a prior hold).
	Debit(ctx context.Context, sellerID core.SellerID, amount core.Paise, ref Ref, reason, actor string) error
}

// Balance is the current balance view.
type Balance struct {
	SellerID    core.SellerID
	Balance     core.Paise // ledger balance (credits - debits)
	HoldTotal   core.Paise // sum of active holds
	Available   core.Paise // balance + credit_limit - hold_total
	CreditLimit core.Paise
	GraceAmount core.Paise
	Status      string
}

// Ref is an idempotency reference for a wallet operation.
type Ref struct {
	Type string // e.g., "shipment", "recharge", "cod_remittance"
	ID   string // the external ID (shipment UUID, order ID, etc.)
}

// Direction is debit or credit.
type Direction string

const (
	DirectionCredit Direction = "credit"
	DirectionDebit  Direction = "debit"
)

// Sentinel errors.
var (
	ErrInsufficientFunds = core.ErrInvalidArgument // wrap in service calls
	ErrAccountFrozen     = core.ErrPermissionDenied
	ErrHoldNotFound      = core.ErrNotFound
)
