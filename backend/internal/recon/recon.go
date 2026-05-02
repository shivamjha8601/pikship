// Package recon handles weight discrepancy reconciliation between declared
// and carrier-billed weights. Per LLD §03-services/18-recon.
package recon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// WeightDiscrepancy is one reconciliation line.
type WeightDiscrepancy struct {
	ID                   core.WeightDisputeID
	ReconBatchID         uuid.UUID
	SellerID             *core.SellerID
	ShipmentID           *core.ShipmentID
	CarrierCode          string
	AWB                  string
	DeclaredWeightG      int
	NewWeightG           int
	OriginalChargePaise  core.Paise
	NewChargePaise       core.Paise
	DeltaPaise           core.Paise
	State                string
	DisputeWindowUntil   *time.Time
	CreatedAt            time.Time
}

// Service manages weight reconciliation.
type Service interface {
	RaiseDiscrepancy(ctx context.Context, d WeightDiscrepancy) error
	Dispute(ctx context.Context, id core.WeightDisputeID, reason string, sellerID core.SellerID) error
	Approve(ctx context.Context, id core.WeightDisputeID, reason string, by core.UserID) error
	Reject(ctx context.Context, id core.WeightDisputeID, reason string, by core.UserID) error
	Post(ctx context.Context, id core.WeightDisputeID) error
	GetByShipment(ctx context.Context, shipmentID core.ShipmentID) (WeightDiscrepancy, error)
}

type service struct {
	pool   *pgxpool.Pool
	wallet wallet.Service
}

// New constructs the recon service.
func New(pool *pgxpool.Pool, w wallet.Service) Service {
	return &service{pool: pool, wallet: w}
}

const insertDiscrepancySQL = `
    INSERT INTO weight_discrepancy
        (recon_batch_id, seller_id, shipment_id, carrier_code, awb,
         declared_weight_g, declared_volumetric_g, original_charge_paise,
         new_weight_g, new_volumetric_g, new_charge_paise, new_billing_weight_g,
         delta_paise, state, dispute_window_until)
    VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8,0,$9,$8,$10,'raised', now()+interval '7 days')
    ON CONFLICT DO NOTHING
`

func (s *service) RaiseDiscrepancy(ctx context.Context, d WeightDiscrepancy) error {
	var sellerID, shipmentID *uuid.UUID
	if d.SellerID != nil {
		u := d.SellerID.UUID()
		sellerID = &u
	}
	if d.ShipmentID != nil {
		u := d.ShipmentID.UUID()
		shipmentID = &u
	}
	_, err := s.pool.Exec(ctx, insertDiscrepancySQL,
		d.ReconBatchID, sellerID, shipmentID, d.CarrierCode, d.AWB,
		d.DeclaredWeightG, int64(d.OriginalChargePaise),
		d.NewWeightG, int64(d.NewChargePaise), int64(d.DeltaPaise),
	)
	return err
}

func (s *service) Dispute(ctx context.Context, id core.WeightDisputeID, reason string, sellerID core.SellerID) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE weight_discrepancy SET state='disputed', disputed_at=now(), dispute_reason=$2, updated_at=now()
        WHERE id=$1 AND seller_id=$3 AND state='raised' AND dispute_window_until > now()`,
		id.UUID(), reason, sellerID.UUID())
	return err
}

func (s *service) Approve(ctx context.Context, id core.WeightDisputeID, reason string, by core.UserID) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE weight_discrepancy SET state='approved', decided_at=now(), decided_by=$2, decision_reason=$3, updated_at=now()
        WHERE id=$1 AND state='disputed'`, id.UUID(), by.UUID(), reason)
	return err
}

func (s *service) Reject(ctx context.Context, id core.WeightDisputeID, reason string, by core.UserID) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE weight_discrepancy SET state='rejected', decided_at=now(), decided_by=$2, decision_reason=$3, updated_at=now()
        WHERE id=$1 AND state='disputed'`, id.UUID(), by.UUID(), reason)
	return err
}

func (s *service) Post(ctx context.Context, id core.WeightDisputeID) error {
	var wd WeightDiscrepancy
	var wdID, batchID uuid.UUID
	var sellerUUID *uuid.UUID
	var originalCharge, delta int64
	if err := s.pool.QueryRow(ctx, `
        SELECT id, recon_batch_id, seller_id, delta_paise, original_charge_paise
        FROM weight_discrepancy WHERE id=$1 AND state IN ('raised','approved')`,
		id.UUID(),
	).Scan(&wdID, &batchID, &sellerUUID, &delta, &originalCharge); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.ErrNotFound
		}
		return fmt.Errorf("recon.Post lookup: %w", err)
	}
	wd.ID = core.WeightDisputeIDFromUUID(wdID)
	wd.DeltaPaise = core.Paise(delta)

	if sellerUUID != nil && delta > 0 {
		sellerID := core.SellerIDFromUUID(*sellerUUID)
		if err := s.wallet.Debit(ctx, sellerID, core.Paise(delta), wallet.Ref{
			Type: "weight_recon", ID: id.String(),
		}, "weight_discrepancy_charge", "system"); err != nil {
			return fmt.Errorf("recon.Post debit: %w", err)
		}
	}

	_, err := s.pool.Exec(ctx, `UPDATE weight_discrepancy SET state='posted', posted_at=now(), updated_at=now() WHERE id=$1`, id.UUID())
	return err
}

func (s *service) GetByShipment(ctx context.Context, shipmentID core.ShipmentID) (WeightDiscrepancy, error) {
	var wd WeightDiscrepancy
	var id, batchID uuid.UUID
	var sellerUUID, shipmentUUID *uuid.UUID
	var delta, original, newCharge int64
	if err := s.pool.QueryRow(ctx, `
        SELECT id, recon_batch_id, seller_id, shipment_id, carrier_code, awb,
               declared_weight_g, original_charge_paise, new_weight_g,
               new_charge_paise, delta_paise, state, dispute_window_until, created_at
        FROM weight_discrepancy WHERE shipment_id=$1 ORDER BY created_at DESC LIMIT 1`,
		shipmentID.UUID(),
	).Scan(&id, &batchID, &sellerUUID, &shipmentUUID, &wd.CarrierCode, &wd.AWB,
		&wd.DeclaredWeightG, &original, &wd.NewWeightG, &newCharge, &delta,
		&wd.State, &wd.DisputeWindowUntil, &wd.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WeightDiscrepancy{}, core.ErrNotFound
		}
		return WeightDiscrepancy{}, fmt.Errorf("recon.GetByShipment: %w", err)
	}
	wd.ID = core.WeightDisputeIDFromUUID(id)
	wd.ReconBatchID = batchID
	wd.OriginalChargePaise = core.Paise(original)
	wd.NewChargePaise = core.Paise(newCharge)
	wd.DeltaPaise = core.Paise(delta)
	if sellerUUID != nil {
		s := core.SellerIDFromUUID(*sellerUUID)
		wd.SellerID = &s
	}
	if shipmentUUID != nil {
		sh := core.ShipmentIDFromUUID(*shipmentUUID)
		wd.ShipmentID = &sh
	}
	return wd, nil
}
