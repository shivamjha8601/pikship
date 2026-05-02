package seller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

type serviceImpl struct {
	repo  *repo
	audit audit.Emitter
	log   *slog.Logger
}

// New constructs the seller service. pool must be the admin pool so
// lifecycle transitions are not blocked by RLS.
func New(pool *pgxpool.Pool, au audit.Emitter, log *slog.Logger) Service {
	return &serviceImpl{repo: newRepo(pool), audit: au, log: log}
}

func (s *serviceImpl) Provision(ctx context.Context, in ProvisionInput) (Seller, error) {
	if in.SellerType == "" {
		in.SellerType = core.SellerTypeSmallMedium
	}
	sl, err := s.repo.insertSeller(ctx, in)
	if err != nil {
		return Seller{}, fmt.Errorf("seller.Provision: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sl.ID,
		Action:   "seller.created",
		Target:   audit.Target{Kind: "seller", Ref: sl.ID.String()},
		Payload:  map[string]any{"legal_name": sl.LegalName},
	})
	return sl, nil
}

func (s *serviceImpl) Get(ctx context.Context, id core.SellerID) (Seller, error) {
	return s.repo.getSeller(ctx, id)
}

func (s *serviceImpl) Activate(ctx context.Context, id core.SellerID, reason string) error {
	sl, err := s.repo.getSeller(ctx, id)
	if err != nil {
		return fmt.Errorf("seller.Activate: %w", err)
	}
	if err := s.repo.updateLifecycle(ctx, id, StateActive); err != nil {
		return fmt.Errorf("seller.Activate: %w", err)
	}
	_ = s.repo.insertLifecycleEvent(ctx, id, sl.LifecycleState, StateActive, reason, "", nil, nil)
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.reactivated",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
	})
	return nil
}

func (s *serviceImpl) Suspend(ctx context.Context, id core.SellerID, reason, category string, until *time.Time) error {
	sl, err := s.repo.getSeller(ctx, id)
	if err != nil {
		return fmt.Errorf("seller.Suspend: %w", err)
	}
	if err := s.repo.suspend(ctx, id, reason, category, until); err != nil {
		return fmt.Errorf("seller.Suspend: %w", err)
	}
	_ = s.repo.insertLifecycleEvent(ctx, id, sl.LifecycleState, StateSuspended, reason, category, nil, nil)
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.suspended",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
		Payload:  map[string]any{"reason": reason, "category": category},
	})
	return nil
}

func (s *serviceImpl) Reinstate(ctx context.Context, id core.SellerID, reason string) error {
	sl, err := s.repo.getSeller(ctx, id)
	if err != nil {
		return fmt.Errorf("seller.Reinstate: %w", err)
	}
	if err := s.repo.reinstate(ctx, id); err != nil {
		return fmt.Errorf("seller.Reinstate: %w", err)
	}
	_ = s.repo.insertLifecycleEvent(ctx, id, sl.LifecycleState, StateActive, reason, "", nil, nil)
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.reactivated",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
	})
	return nil
}

func (s *serviceImpl) WindDown(ctx context.Context, id core.SellerID, reason string) error {
	sl, err := s.repo.getSeller(ctx, id)
	if err != nil {
		return fmt.Errorf("seller.WindDown: %w", err)
	}
	if err := s.repo.windDown(ctx, id, reason); err != nil {
		return fmt.Errorf("seller.WindDown: %w", err)
	}
	_ = s.repo.insertLifecycleEvent(ctx, id, sl.LifecycleState, StateWoundDown, reason, "", nil, nil)
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.wound_down",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
	})
	return nil
}

func (s *serviceImpl) SubmitKYC(ctx context.Context, id core.SellerID, app KYCApplication) error {
	app.SellerID = id
	if err := s.repo.upsertKYC(ctx, app); err != nil {
		return fmt.Errorf("seller.SubmitKYC: %w", err)
	}
	return nil
}

func (s *serviceImpl) ApproveKYC(ctx context.Context, id core.SellerID, reason string, by core.UserID) error {
	if err := s.repo.approveKYC(ctx, id, reason, by); err != nil {
		return fmt.Errorf("seller.ApproveKYC: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.kyc_approved",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
		Payload:  map[string]any{"reason": reason},
	})
	return nil
}

func (s *serviceImpl) RejectKYC(ctx context.Context, id core.SellerID, reason string, by core.UserID) error {
	if err := s.repo.rejectKYC(ctx, id, reason); err != nil {
		return fmt.Errorf("seller.RejectKYC: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &id,
		Action:   "seller.kyc_rejected",
		Target:   audit.Target{Kind: "seller", Ref: id.String()},
		Payload:  map[string]any{"reason": reason},
	})
	return nil
}

func (s *serviceImpl) GetKYC(ctx context.Context, id core.SellerID) (KYCApplication, error) {
	return s.repo.getKYC(ctx, id)
}
