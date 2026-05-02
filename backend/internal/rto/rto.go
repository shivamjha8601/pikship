// Package rto manages Return-To-Origin cases.
// Per LLD §03-services/17-rto-returns.
package rto

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

// RTOCase tracks a return-to-origin journey.
type RTOCase struct {
	ID          core.RTOID
	SellerID    core.SellerID
	ShipmentID  core.ShipmentID
	OrderID     core.OrderID
	State       string
	Reason      string
	InitiatedAt time.Time
	DeliveredAt *time.Time
	ClosedAt    *time.Time
	CreatedAt   time.Time
}

// Service manages RTO cases.
type Service interface {
	Open(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, reason string) (RTOCase, error)
	MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.RTOID) error
	MarkDeliveredToOrigin(ctx context.Context, sellerID core.SellerID, id core.RTOID) error
	Close(ctx context.Context, sellerID core.SellerID, id core.RTOID) error
	GetByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (RTOCase, error)
}

type service struct{ pool *pgxpool.Pool }

// New constructs the RTO service.
func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

const insertRTOSQL = `
    INSERT INTO rto_case (seller_id, shipment_id, order_id, reason)
    VALUES ($1,$2,$3,$4)
    ON CONFLICT (shipment_id) DO UPDATE SET state='initiated', reason=EXCLUDED.reason, updated_at=now()
    RETURNING id, seller_id, shipment_id, order_id, state, COALESCE(reason,''), initiated_at,
              delivered_at, closed_at, created_at
`

func (s *service) Open(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, reason string) (RTOCase, error) {
	return s.scan(s.pool.QueryRow(ctx, insertRTOSQL,
		sellerID.UUID(), shipmentID.UUID(), orderID.UUID(), reason))
}

func (s *service) MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.RTOID) error {
	_, err := s.pool.Exec(ctx, `UPDATE rto_case SET state='in_transit', updated_at=now() WHERE id=$1 AND seller_id=$2`, id.UUID(), sellerID.UUID())
	return err
}

func (s *service) MarkDeliveredToOrigin(ctx context.Context, sellerID core.SellerID, id core.RTOID) error {
	_, err := s.pool.Exec(ctx, `UPDATE rto_case SET state='delivered_to_origin', delivered_at=now(), updated_at=now() WHERE id=$1 AND seller_id=$2`, id.UUID(), sellerID.UUID())
	return err
}

func (s *service) Close(ctx context.Context, sellerID core.SellerID, id core.RTOID) error {
	_, err := s.pool.Exec(ctx, `UPDATE rto_case SET state='closed', closed_at=now(), updated_at=now() WHERE id=$1 AND seller_id=$2`, id.UUID(), sellerID.UUID())
	return err
}

func (s *service) GetByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (RTOCase, error) {
	return s.scan(s.pool.QueryRow(ctx, `
        SELECT id, seller_id, shipment_id, order_id, state, COALESCE(reason,''),
               initiated_at, delivered_at, closed_at, created_at
        FROM rto_case WHERE shipment_id=$1 AND seller_id=$2 ORDER BY created_at DESC LIMIT 1`,
		shipmentID.UUID(), sellerID.UUID()))
}

func (s *service) scan(row pgx.Row) (RTOCase, error) {
	var c RTOCase
	var id, sellerID, shipmentID, orderID uuid.UUID
	if err := row.Scan(&id, &sellerID, &shipmentID, &orderID, &c.State, &c.Reason,
		&c.InitiatedAt, &c.DeliveredAt, &c.ClosedAt, &c.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RTOCase{}, core.ErrNotFound
		}
		return RTOCase{}, fmt.Errorf("rto.scan: %w", err)
	}
	c.ID = core.RTOIDFromUUID(id)
	c.SellerID = core.SellerIDFromUUID(sellerID)
	c.ShipmentID = core.ShipmentIDFromUUID(shipmentID)
	c.OrderID = core.OrderIDFromUUID(orderID)
	return c, nil
}
