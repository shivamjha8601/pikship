// Package limits enforces per-seller usage caps from policy.
//
// Two caps are exposed:
// - shipments per calendar month (KeyShipmentsPerMonthLimit)
// - orders per calendar day      (KeyOrdersPerDayLimit)
//
// Both honor 0 == unlimited. Calls are cheap (a single COUNT against an
// indexed table); add a small in-memory cache later if the COUNT shows up
// in profiles.
package limits

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/policy"
)

// ErrLimitExceeded is returned when a usage cap would be breached.
var ErrLimitExceeded = errors.New("limits: usage cap exceeded")

// Guard checks usage caps before allowing operations.
type Guard interface {
	// CheckShipmentMonth returns ErrLimitExceeded if creating another
	// shipment this calendar month would breach the cap.
	CheckShipmentMonth(ctx context.Context, sellerID core.SellerID) error

	// CheckOrderDay returns ErrLimitExceeded if creating another order
	// today would breach the cap.
	CheckOrderDay(ctx context.Context, sellerID core.SellerID) error

	// Usage returns the current rolling-window counts and limits.
	Usage(ctx context.Context, sellerID core.SellerID) (Usage, error)
}

// Usage is the per-seller usage snapshot.
type Usage struct {
	ShipmentsThisMonth   int64 `json:"shipments_this_month"`
	ShipmentMonthLimit   int64 `json:"shipment_month_limit"`   // 0 = unlimited
	OrdersToday          int64 `json:"orders_today"`
	OrderDayLimit        int64 `json:"order_day_limit"`        // 0 = unlimited
}

type guard struct {
	pool   *pgxpool.Pool
	policy policy.Engine
}

// New constructs a Guard. pool should be the reports pool (BYPASSRLS) so
// the COUNT works regardless of seller scope.
func New(pool *pgxpool.Pool, p policy.Engine) Guard {
	return &guard{pool: pool, policy: p}
}

func (g *guard) CheckShipmentMonth(ctx context.Context, sellerID core.SellerID) error {
	limit, err := g.shipmentLimit(ctx, sellerID)
	if err != nil {
		return err
	}
	if limit == 0 {
		return nil
	}
	count, err := g.shipmentsThisMonth(ctx, sellerID)
	if err != nil {
		return err
	}
	if count >= limit {
		return fmt.Errorf("%w: shipments=%d limit=%d (this month)", ErrLimitExceeded, count, limit)
	}
	return nil
}

func (g *guard) CheckOrderDay(ctx context.Context, sellerID core.SellerID) error {
	limit, err := g.orderLimit(ctx, sellerID)
	if err != nil {
		return err
	}
	if limit == 0 {
		return nil
	}
	count, err := g.ordersToday(ctx, sellerID)
	if err != nil {
		return err
	}
	if count >= limit {
		return fmt.Errorf("%w: orders=%d limit=%d (today)", ErrLimitExceeded, count, limit)
	}
	return nil
}

func (g *guard) Usage(ctx context.Context, sellerID core.SellerID) (Usage, error) {
	var u Usage
	var err error
	if u.ShipmentMonthLimit, err = g.shipmentLimit(ctx, sellerID); err != nil {
		return u, err
	}
	if u.OrderDayLimit, err = g.orderLimit(ctx, sellerID); err != nil {
		return u, err
	}
	if u.ShipmentsThisMonth, err = g.shipmentsThisMonth(ctx, sellerID); err != nil {
		return u, err
	}
	if u.OrdersToday, err = g.ordersToday(ctx, sellerID); err != nil {
		return u, err
	}
	return u, nil
}

// --- internals ---

func (g *guard) shipmentLimit(ctx context.Context, sellerID core.SellerID) (int64, error) {
	v, err := g.policy.Resolve(ctx, sellerID, policy.KeyShipmentsPerMonthLimit)
	if err != nil {
		return 0, fmt.Errorf("limits: resolve shipment cap: %w", err)
	}
	return v.AsInt64()
}

func (g *guard) orderLimit(ctx context.Context, sellerID core.SellerID) (int64, error) {
	v, err := g.policy.Resolve(ctx, sellerID, policy.KeyOrdersPerDayLimit)
	if err != nil {
		return 0, fmt.Errorf("limits: resolve order cap: %w", err)
	}
	return v.AsInt64()
}

func (g *guard) shipmentsThisMonth(ctx context.Context, sellerID core.SellerID) (int64, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var n int64
	err := g.pool.QueryRow(ctx, `
        SELECT count(*) FROM shipment
        WHERE seller_id = $1 AND created_at >= $2`,
		sellerID.UUID(), monthStart,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("limits.shipmentsThisMonth: %w", err)
	}
	return n, nil
}

func (g *guard) ordersToday(ctx context.Context, sellerID core.SellerID) (int64, error) {
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var n int64
	err := g.pool.QueryRow(ctx, `
        SELECT count(*) FROM order_record
        WHERE seller_id = $1 AND created_at >= $2`,
		sellerID.UUID(), dayStart,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("limits.ordersToday: %w", err)
	}
	return n, nil
}
