// Package reports provides seller dashboard aggregates and CSV exports.
// Per LLD §03-services/19-reports.
package reports

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// ShipmentSummary is the dashboard aggregate for a date range.
type ShipmentSummary struct {
	SellerID        core.SellerID
	From            time.Time
	To              time.Time
	TotalOrders     int64
	TotalShipments  int64
	Delivered       int64
	RTO             int64
	Pending         int64
	RevenueChargesPaise core.Paise
	CODCollectedPaise   core.Paise
}

// Service provides reporting queries.
type Service interface {
	ShipmentSummary(ctx context.Context, sellerID core.SellerID, from, to time.Time) (ShipmentSummary, error)
	ExportShipmentsCSV(ctx context.Context, sellerID core.SellerID, from, to time.Time, w io.Writer) error
	DashboardSummary(ctx context.Context, sellerID core.SellerID) (DashboardSummary, error)
}

// DashboardSummary is the at-a-glance home-page aggregate.
type DashboardSummary struct {
	OrdersByState     map[string]int64 `json:"orders_by_state"`
	OrdersToday       int64            `json:"orders_today"`
	OrdersThisWeek    int64            `json:"orders_this_week"`
	ShippingSpendPaise core.Paise      `json:"shipping_spend_paise"`
	CODOutstandingPaise core.Paise     `json:"cod_outstanding_paise"`
	UnpaidPrepaidCount int64           `json:"unpaid_prepaid_count"`
	OrdersByDay       []DayBucket      `json:"orders_by_day"`
}

// DayBucket is one day's order count for the last-7-days sparkline.
type DayBucket struct {
	Day   string `json:"day"`   // YYYY-MM-DD
	Count int64  `json:"count"`
}

type service struct{ pool *pgxpool.Pool }

// New constructs the reports service. pool should be the reports pool (BYPASSRLS).
func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

func (s *service) ShipmentSummary(ctx context.Context, sellerID core.SellerID, from, to time.Time) (ShipmentSummary, error) {
	var sum ShipmentSummary
	sum.SellerID = sellerID
	sum.From = from
	sum.To = to

	err := s.pool.QueryRow(ctx, `
        SELECT
            COUNT(*) FILTER (WHERE TRUE)                       AS total,
            COUNT(*) FILTER (WHERE state = 'delivered')        AS delivered,
            COUNT(*) FILTER (WHERE state IN ('rto_in_progress','rto_completed')) AS rto,
            COUNT(*) FILTER (WHERE state NOT IN ('delivered','cancelled','rto_completed','closed')) AS pending,
            COALESCE(SUM(charges_paise), 0)                    AS charges,
            COALESCE(SUM(cod_amount_paise) FILTER (WHERE state='delivered'), 0) AS cod
        FROM shipment
        WHERE seller_id = $1 AND created_at >= $2 AND created_at < $3`,
		sellerID.UUID(), from, to,
	).Scan(&sum.TotalShipments, &sum.Delivered, &sum.RTO, &sum.Pending,
		(*int64)(&sum.RevenueChargesPaise), (*int64)(&sum.CODCollectedPaise))
	if err != nil {
		return ShipmentSummary{}, fmt.Errorf("reports.ShipmentSummary: %w", err)
	}
	return sum, nil
}

// DashboardSummary aggregates everything the /reports landing page shows in
// one round-trip. Designed for the at-a-glance card row + 7-day sparkline.
func (s *service) DashboardSummary(ctx context.Context, sellerID core.SellerID) (DashboardSummary, error) {
	out := DashboardSummary{OrdersByState: map[string]int64{}}

	// Orders by state.
	rows, err := s.pool.Query(ctx, `
        SELECT state, COUNT(*) FROM order_record
        WHERE seller_id = $1
        GROUP BY state`, sellerID.UUID())
	if err != nil {
		return out, fmt.Errorf("reports.DashboardSummary by_state: %w", err)
	}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.OrdersByState[state] = n
	}
	rows.Close()

	// Today + this week counts + unpaid prepaid count.
	if err := s.pool.QueryRow(ctx, `
        SELECT
            COUNT(*) FILTER (WHERE created_at >= date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata'),
            COUNT(*) FILTER (WHERE created_at >= date_trunc('week', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata'),
            COUNT(*) FILTER (WHERE payment_method='prepaid' AND payment_status='unpaid' AND state NOT IN ('cancelled','closed'))
        FROM order_record WHERE seller_id = $1`,
		sellerID.UUID(),
	).Scan(&out.OrdersToday, &out.OrdersThisWeek, &out.UnpaidPrepaidCount); err != nil {
		return out, fmt.Errorf("reports.DashboardSummary counts: %w", err)
	}

	// Shipping spend + COD outstanding (from shipments).
	if err := s.pool.QueryRow(ctx, `
        SELECT
            COALESCE(SUM(charges_paise), 0),
            COALESCE(SUM(cod_amount_paise) FILTER (WHERE state IN ('booked','in_transit')), 0)
        FROM shipment WHERE seller_id = $1`,
		sellerID.UUID(),
	).Scan((*int64)(&out.ShippingSpendPaise), (*int64)(&out.CODOutstandingPaise)); err != nil {
		return out, fmt.Errorf("reports.DashboardSummary money: %w", err)
	}

	// 7-day order counts (oldest → newest). LEFT JOIN with generate_series so
	// days with zero orders still appear with a count of 0.
	dayRows, err := s.pool.Query(ctx, `
        SELECT to_char(d::date, 'YYYY-MM-DD'),
               COALESCE(COUNT(o.id), 0)
        FROM generate_series(
            (now() AT TIME ZONE 'Asia/Kolkata')::date - INTERVAL '6 days',
            (now() AT TIME ZONE 'Asia/Kolkata')::date,
            '1 day'
        ) d
        LEFT JOIN order_record o
          ON o.seller_id = $1
         AND (o.created_at AT TIME ZONE 'Asia/Kolkata')::date = d::date
        GROUP BY d
        ORDER BY d`, sellerID.UUID())
	if err != nil {
		return out, fmt.Errorf("reports.DashboardSummary by_day: %w", err)
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var b DayBucket
		if err := dayRows.Scan(&b.Day, &b.Count); err != nil {
			return out, err
		}
		out.OrdersByDay = append(out.OrdersByDay, b)
	}
	return out, nil
}

func (s *service) ExportShipmentsCSV(ctx context.Context, sellerID core.SellerID, from, to time.Time, w io.Writer) error {
	rows, err := s.pool.Query(ctx, `
        SELECT s.id, s.awb, s.carrier_code, s.state,
               s.drop_pincode, s.package_weight_g, s.charges_paise,
               s.cod_amount_paise, s.booked_at, s.created_at
        FROM shipment s
        WHERE s.seller_id = $1 AND s.created_at >= $2 AND s.created_at < $3
        ORDER BY s.created_at DESC`,
		sellerID.UUID(), from, to)
	if err != nil {
		return fmt.Errorf("reports.ExportCSV: %w", err)
	}
	defer rows.Close()

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"shipment_id", "awb", "carrier", "state", "pincode", "weight_g", "charges_paise", "cod_paise", "booked_at", "created_at"})

	for rows.Next() {
		var (
			id, awb, carrier, state, pincode string
			weightG                          int
			charges, cod                     int64
			bookedAt                         *time.Time
			createdAt                        time.Time
		)
		if err := rows.Scan(&id, &awb, &carrier, &state, &pincode, &weightG, &charges, &cod, &bookedAt, &createdAt); err != nil {
			return err
		}
		bookedStr := ""
		if bookedAt != nil {
			bookedStr = bookedAt.Format(time.RFC3339)
		}
		_ = cw.Write([]string{id, awb, carrier, state, pincode,
			fmt.Sprintf("%d", weightG), fmt.Sprintf("%d", charges),
			fmt.Sprintf("%d", cod), bookedStr, createdAt.Format(time.RFC3339)})
	}
	cw.Flush()
	return rows.Err()
}
