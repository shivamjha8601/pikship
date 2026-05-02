// Package cod manages Cash-On-Delivery shipment tracking and remittance
// batch ingest. Per LLD §03-services/16-cod.
package cod

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

// CODShipment tracks one COD shipment's remittance lifecycle.
type CODShipment struct {
	ShipmentID              core.ShipmentID
	SellerID                core.SellerID
	OrderID                 core.OrderID
	CarrierCode             string
	State                   string
	ExpectedAmountPaise     core.Paise
	CarrierReportedPaise    *core.Paise
	DeliveredAt             *time.Time
	RemittedAt              *time.Time
}

// Service manages COD lifecycle.
type Service interface {
	Register(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, carrierCode string, expectedPaise core.Paise) error
	MarkCollected(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, carrierReported core.Paise, deliveredAt time.Time) error
	Remit(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) error
	Get(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (CODShipment, error)
}

type service struct {
	pool   *pgxpool.Pool
	wallet wallet.Service
}

// New constructs the COD service.
func New(pool *pgxpool.Pool, w wallet.Service) Service {
	return &service{pool: pool, wallet: w}
}

func (s *service) Register(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, carrierCode string, expectedPaise core.Paise) error {
	_, err := s.pool.Exec(ctx, `
        INSERT INTO cod_shipment (shipment_id, seller_id, order_id, carrier_code, expected_amount_paise)
        VALUES ($1,$2,$3,$4,$5) ON CONFLICT (shipment_id) DO NOTHING`,
		shipmentID.UUID(), sellerID.UUID(), orderID.UUID(), carrierCode, int64(expectedPaise))
	return err
}

func (s *service) MarkCollected(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, carrierReported core.Paise, deliveredAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE cod_shipment SET state='collected',
            carrier_reported_amount_paise=$2, delivered_at=$3, updated_at=now()
        WHERE shipment_id=$1 AND seller_id=$4 AND state='pending'`,
		shipmentID.UUID(), int64(carrierReported), deliveredAt, sellerID.UUID())
	return err
}

func (s *service) Remit(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) error {
	cs, err := s.Get(ctx, sellerID, shipmentID)
	if err != nil {
		return fmt.Errorf("cod.Remit: %w", err)
	}
	if cs.State != "collected" {
		return fmt.Errorf("cod.Remit: shipment is %s, not collected", cs.State)
	}
	amount := cs.ExpectedAmountPaise
	if cs.CarrierReportedPaise != nil {
		amount = *cs.CarrierReportedPaise
	}
	// Credit the seller's wallet.
	if err := s.wallet.Credit(ctx, sellerID, amount, wallet.Ref{
		Type: "cod_remittance", ID: shipmentID.String(),
	}, "cod_remittance", "system"); err != nil {
		return fmt.Errorf("cod.Remit: credit: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
        UPDATE cod_shipment SET state='remitted', remitted_at=now(), updated_at=now()
        WHERE shipment_id=$1 AND seller_id=$2`,
		shipmentID.UUID(), sellerID.UUID())
	return err
}

func (s *service) Get(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (CODShipment, error) {
	var cs CODShipment
	var sid, sellID, orderID uuid.UUID
	var expectedPaise int64
	var carrierReported *int64
	if err := s.pool.QueryRow(ctx, `
        SELECT shipment_id, seller_id, order_id, carrier_code, state,
               expected_amount_paise, carrier_reported_amount_paise,
               delivered_at, remitted_at
        FROM cod_shipment WHERE shipment_id=$1 AND seller_id=$2`,
		shipmentID.UUID(), sellerID.UUID(),
	).Scan(&sid, &sellID, &orderID, &cs.CarrierCode, &cs.State,
		&expectedPaise, &carrierReported, &cs.DeliveredAt, &cs.RemittedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CODShipment{}, core.ErrNotFound
		}
		return CODShipment{}, fmt.Errorf("cod.Get: %w", err)
	}
	cs.ShipmentID = core.ShipmentIDFromUUID(sid)
	cs.SellerID = core.SellerIDFromUUID(sellID)
	cs.OrderID = core.OrderIDFromUUID(orderID)
	cs.ExpectedAmountPaise = core.Paise(expectedPaise)
	if carrierReported != nil {
		p := core.Paise(*carrierReported)
		cs.CarrierReportedPaise = &p
	}
	return cs, nil
}
