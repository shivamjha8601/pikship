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
