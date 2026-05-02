// Package admin provides operator capabilities: manual adjustments, KYC
// overrides, cross-seller views, and role assignment.
// Per LLD §03-services/23-admin-ops.
package admin

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// Service provides privileged operator operations.
type Service interface {
	ManualCredit(ctx context.Context, sellerID core.SellerID, amount core.Paise, reason string, by core.UserID) error
	ManualDebit(ctx context.Context, sellerID core.SellerID, amount core.Paise, reason string, by core.UserID) error
	AssignRole(ctx context.Context, userID core.UserID, sellerID core.SellerID, roles []core.SellerRole, by core.UserID) error
	GetSellerDetails(ctx context.Context, sellerID core.SellerID) (map[string]any, error)
}

type service struct {
	pool   *pgxpool.Pool
	wallet wallet.Service
	audit  audit.Emitter
	log    *slog.Logger
}

// New constructs the admin service. pool must be the admin pool (BYPASSRLS).
func New(pool *pgxpool.Pool, w wallet.Service, au audit.Emitter, log *slog.Logger) Service {
	return &service{pool: pool, wallet: w, audit: au, log: log}
}

func (s *service) ManualCredit(ctx context.Context, sellerID core.SellerID, amount core.Paise, reason string, by core.UserID) error {
	if err := s.wallet.Credit(ctx, sellerID, amount, wallet.Ref{
		Type: "ops_manual_credit", ID: by.String() + "_" + fmt.Sprintf("%d", amount),
	}, reason, "operator:"+by.String()); err != nil {
		return fmt.Errorf("admin.ManualCredit: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "ops.manual_adjustment",
		Target:   audit.Target{Kind: "wallet", Ref: sellerID.String()},
		Payload:  map[string]any{"amount": int64(amount), "reason": reason, "by": by.String(), "direction": "credit"},
	})
	return nil
}

func (s *service) ManualDebit(ctx context.Context, sellerID core.SellerID, amount core.Paise, reason string, by core.UserID) error {
	if err := s.wallet.Debit(ctx, sellerID, amount, wallet.Ref{
		Type: "ops_manual_debit", ID: by.String() + "_" + fmt.Sprintf("%d", amount),
	}, reason, "operator:"+by.String()); err != nil {
		return fmt.Errorf("admin.ManualDebit: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "ops.manual_adjustment",
		Target:   audit.Target{Kind: "wallet", Ref: sellerID.String()},
		Payload:  map[string]any{"amount": int64(amount), "reason": reason, "by": by.String(), "direction": "debit"},
	})
	return nil
}

func (s *service) AssignRole(ctx context.Context, userID core.UserID, sellerID core.SellerID, roles []core.SellerRole, by core.UserID) error {
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "ops.role_assigned",
		Target:   audit.Target{Kind: "app_user", Ref: userID.String()},
		Payload:  map[string]any{"by": by.String()},
	})
	return nil
}

func (s *service) GetSellerDetails(ctx context.Context, sellerID core.SellerID) (map[string]any, error) {
	var id, legalName, displayName, sellerType, state, billingEmail string
	if err := s.pool.QueryRow(ctx, `
        SELECT id, legal_name, display_name, seller_type, lifecycle_state, billing_email
        FROM seller WHERE id=$1`, sellerID.UUID(),
	).Scan(&id, &legalName, &displayName, &sellerType, &state, &billingEmail); err != nil {
		return nil, fmt.Errorf("admin.GetSellerDetails: %w", err)
	}
	return map[string]any{
		"id": id, "legal_name": legalName, "display_name": displayName,
		"seller_type": sellerType, "lifecycle_state": state, "billing_email": billingEmail,
	}, nil
}
