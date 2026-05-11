package shipments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/allocation"
	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

type service struct {
	pool     *pgxpool.Pool
	registry *carriers.Registry
	wallet   wallet.Service
	orders   orders.Service
	log      *slog.Logger
}

// New constructs the shipments service. pool MUST be the app pool. Every
// SQL operation runs inside a seller-scoped tx so RLS resolves correctly.
func New(pool *pgxpool.Pool, reg *carriers.Registry, w wallet.Service, o orders.Service, log *slog.Logger) Service {
	return &service{pool: pool, registry: reg, wallet: w, orders: o, log: log}
}

const (
	insertDecisionSQL = `
        INSERT INTO allocation_decision (id, seller_id, order_id, payload)
        VALUES ($1, $2, $3, $4::jsonb)
    `
	insertShipmentSQL = `
        INSERT INTO shipment (
            id, seller_id, order_id, allocation_decision_id, state,
            carrier_code, service_type, charges_paise, cod_amount_paise,
            pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
            drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm
        ) VALUES ($1,$2,$3,$4,'pending_carrier',$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12,$13,$14,$15,$16)
    `
	confirmShipmentSQL = `
        UPDATE shipment SET
            state = 'booked', awb = $2, carrier_shipment_id = $3,
            booked_at = now(), attempt_count = attempt_count + 1, updated_at = now()
        WHERE id = $1 AND seller_id = $4
    `
	markFailedSQL = `
        UPDATE shipment SET
            state = 'failed', last_carrier_error = $2, last_attempt_at = now(),
            attempt_count = attempt_count + 1,
            failed_at = now(), failed_reason = $2,
            updated_at = now()
        WHERE id = $1 AND seller_id = $3
    `
	cancelShipmentSQL = `
        UPDATE shipment SET state='cancelled', cancelled_at=now(),
            cancelled_reason=$2, updated_at=now()
        WHERE id=$1 AND seller_id=$3
    `
	updateShipmentStateSQL = `
        UPDATE shipment SET state=$2, updated_at=now()
        WHERE id=$1 AND seller_id=$3
    `
	insertShipmentStateEventSQL = `
        INSERT INTO shipment_state_event (shipment_id, seller_id, from_state, to_state, reason)
        VALUES ($1,$2,$3,$4,$5)
    `
	getShipmentSQL = `
        SELECT id, seller_id, order_id, allocation_decision_id, state,
               carrier_code, service_type, COALESCE(awb,''), COALESCE(carrier_shipment_id,''),
               estimated_delivery_at, booked_at, shipped_at, delivered_at, cancelled_at,
               charges_paise, cod_amount_paise,
               pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
               drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
               COALESCE(last_carrier_error,''), attempt_count, created_at, updated_at
        FROM shipment WHERE id = $1 AND seller_id = $2
    `
)

// Book is the orchestrator: persist Phase A inside a tx, call carrier
// (no DB activity), then run Phase B inside a separate tx that either
// confirms (state=booked) or marks failed (state=failed).
func (s *service) Book(ctx context.Context, req BookRequest) (Shipment, error) {
	if len(req.Decision.Candidates) == 0 {
		return Shipment{}, fmt.Errorf("shipments.Book: no candidates in decision")
	}
	candidate := req.Decision.Candidates[req.Decision.RecommendedIdx]

	adapter, ok := s.registry.Get(candidate.CarrierID.String())
	if !ok {
		return Shipment{}, fmt.Errorf("shipments.Book: no adapter for carrier %s", candidate.CarrierID)
	}

	// Move the order to allocating BEFORE persisting Phase A so the order
	// state machine and shipment lifecycle stay aligned. Tolerate the
	// "already allocating" case (idempotent retry).
	if err := s.orders.MarkAllocating(ctx, req.SellerID, req.OrderID); err != nil &&
		!errors.Is(err, core.ErrInvalidArgument) {
		return Shipment{}, fmt.Errorf("shipments.Book: mark allocating: %w", err)
	}

	shipmentID, err := s.persistPhaseA(ctx, req, candidate)
	if err != nil {
		return Shipment{}, fmt.Errorf("shipments.Book: phase A: %w", err)
	}

	bookResp := adapter.Book(ctx, carriers.BookRequest{
		OrderID:         req.OrderID,
		ShipmentID:      shipmentID,
		PickupPincode:   core.Pincode(req.PickupAddress.Pincode),
		ShipToPincode:   req.DropPincode,
		ServiceType:     candidate.ServiceType,
		PaymentMode:     req.PaymentMode,
		DeclaredWeightG: req.PackageWeightG,
		LengthMM:        req.PackageLengthMM,
		WidthMM:         req.PackageWidthMM,
		HeightMM:        req.PackageHeightMM,
		DeclaredValue:   req.DeclaredValue,
		CODAmount:       req.CODAmount,
		PickupContact:   req.PickupContact,
		PickupAddress:   req.PickupAddress,
		ReceiverContact: req.DropContact,
		ShippingAddress: req.DropAddress,
		InvoiceNumber:   req.InvoiceNumber,
		SellerReference: req.SellerReference,
	})

	if !bookResp.OK() {
		_ = s.markFailed(ctx, shipmentID, req.SellerID, bookResp.Err.Error())
		return Shipment{}, fmt.Errorf("shipments.Book: carrier: %w", bookResp.Err)
	}

	if err := s.confirmPhaseB(ctx, shipmentID, req.SellerID, bookResp.Value); err != nil {
		// Carrier issued AWB but our DB write failed. Surface clearly so the
		// recovery worker can reconcile (out of scope for this PR).
		s.log.ErrorContext(ctx, "shipments.Book: phase B confirm failed — manual reconcile needed",
			slog.String("shipment_id", shipmentID.String()),
			slog.String("awb", bookResp.Value.AWBNumber),
			slog.String("err", err.Error()),
		)
		return Shipment{}, fmt.Errorf("shipments.Book: phase B confirm: %w", err)
	}

	// Surface order-state errors instead of swallowing them — if MarkBooked
	// fails the shipment shows booked while the order stays in allocating,
	// which is exactly the bug we want to detect.
	if err := s.orders.MarkBooked(ctx, req.SellerID, req.OrderID, orders.BookedRef{
		AWBNumber:   bookResp.Value.AWBNumber,
		CarrierCode: candidate.CarrierID.String(),
	}); err != nil {
		s.log.WarnContext(ctx, "shipments.Book: order MarkBooked failed",
			slog.String("order_id", req.OrderID.String()),
			slog.String("err", err.Error()),
		)
	}

	return s.Get(ctx, req.SellerID, shipmentID)
}

func (s *service) persistPhaseA(ctx context.Context, req BookRequest, c allocation.Candidate) (core.ShipmentID, error) {
	shipmentID := uuid.New()
	decisionID := uuid.New()

	err := dbtx.WithSellerTx(ctx, s.pool, req.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		decPayload, _ := json.Marshal(req.Decision)
		if _, err := tx.Exec(ctx, insertDecisionSQL,
			decisionID, req.SellerID.UUID(), req.OrderID.UUID(), decPayload,
		); err != nil {
			return fmt.Errorf("persist decision: %w", err)
		}

		pickupJSON, _ := json.Marshal(req.PickupAddress)
		dropJSON, _ := json.Marshal(req.DropAddress)
		_, err := tx.Exec(ctx, insertShipmentSQL,
			shipmentID, req.SellerID.UUID(), req.OrderID.UUID(), decisionID,
			c.CarrierID.String(), string(c.ServiceType),
			int64(c.Quote.TotalPaise),
			int64(req.CODAmount),
			req.PickupLocationID.UUID(),
			pickupJSON, dropJSON, string(req.DropPincode),
			req.PackageWeightG, req.PackageLengthMM, req.PackageWidthMM, req.PackageHeightMM,
		)
		if err != nil {
			return fmt.Errorf("insert shipment: %w", err)
		}
		return nil
	})
	if err != nil {
		return core.ShipmentID{}, err
	}
	return core.ShipmentIDFromUUID(shipmentID), nil
}

func (s *service) confirmPhaseB(ctx context.Context, id core.ShipmentID, sellerID core.SellerID, resp carriers.BookResponse) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, confirmShipmentSQL,
			id.UUID(), resp.AWBNumber, resp.CarrierShipmentRef, sellerID.UUID(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, insertShipmentStateEventSQL,
			id.UUID(), sellerID.UUID(), "pending_carrier", "booked", "carrier confirmed")
		return err
	})
}

// markFailed transitions a shipment to terminal `failed` state. Used when
// the carrier returns a permanent error during Phase B.
func (s *service) markFailed(ctx context.Context, id core.ShipmentID, sellerID core.SellerID, errMsg string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, markFailedSQL, id.UUID(), errMsg, sellerID.UUID()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, insertShipmentStateEventSQL,
			id.UUID(), sellerID.UUID(), "pending_carrier", "failed", errMsg)
		return err
	})
}

func (s *service) Get(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) (Shipment, error) {
	var out Shipment
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		sh, err := scanShipment(tx.QueryRow(ctx, getShipmentSQL, id.UUID(), sellerID.UUID()))
		if err != nil {
			return err
		}
		out = sh
		return nil
	})
	return out, err
}

func (s *service) GetByOrderID(ctx context.Context, sellerID core.SellerID, orderID core.OrderID) (Shipment, error) {
	var out Shipment
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		sh, err := scanShipment(tx.QueryRow(ctx, `
            SELECT id, seller_id, order_id, allocation_decision_id, state,
                   carrier_code, service_type, COALESCE(awb,''), COALESCE(carrier_shipment_id,''),
                   estimated_delivery_at, booked_at, shipped_at, delivered_at, cancelled_at,
                   charges_paise, cod_amount_paise,
                   pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
                   drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
                   COALESCE(last_carrier_error,''), attempt_count, created_at, updated_at
            FROM shipment WHERE order_id = $1 AND seller_id = $2
            ORDER BY created_at DESC LIMIT 1`,
			orderID.UUID(), sellerID.UUID()))
		if err != nil {
			return err
		}
		out = sh
		return nil
	})
	return out, err
}

// FetchLabelPDF asks the carrier adapter for a shipping label and returns
// the raw PDF bytes. Errors if the shipment hasn't booked, or if no adapter
// is registered for the carrier_code.
func (s *service) FetchLabelPDF(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) ([]byte, error) {
	sh, err := s.Get(ctx, sellerID, shipmentID)
	if err != nil {
		return nil, err
	}
	if sh.AWB == "" {
		return nil, fmt.Errorf("shipments.FetchLabelPDF: shipment has no AWB: %w", core.ErrInvalidArgument)
	}
	adapter, ok := s.registry.Get(sh.CarrierCode)
	if !ok {
		return nil, fmt.Errorf("shipments.FetchLabelPDF: no adapter for carrier %q", sh.CarrierCode)
	}
	res := adapter.FetchLabel(ctx, carriers.LabelRequest{AWBNumber: sh.AWB, Format: "pdf"})
	if res.Err != nil {
		return nil, fmt.Errorf("shipments.FetchLabelPDF: %w", res.Err)
	}
	return res.Value.Data, nil
}

// GetByAWB is unscoped (carriers webhook in by AWB) — must use admin/reports
// pool by caller. We use the app pool here without seller scope only for v0;
// production should route this via a dedicated reports-pool-backed service.
func (s *service) GetByAWB(ctx context.Context, awb string) (Shipment, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT id, seller_id, order_id, allocation_decision_id, state,
               carrier_code, service_type, COALESCE(awb,''), COALESCE(carrier_shipment_id,''),
               estimated_delivery_at, booked_at, shipped_at, delivered_at, cancelled_at,
               charges_paise, cod_amount_paise,
               pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
               drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
               COALESCE(last_carrier_error,''), attempt_count, created_at, updated_at
        FROM shipment WHERE awb = $1`, awb)
	return scanShipment(row)
}

func scanShipment(row pgx.Row) (Shipment, error) {
	var sh Shipment
	var id, sellerID, orderID, decisionID, pickupLocID uuid.UUID
	var state, carrierCode, serviceType string
	var charges, cod int64
	var pickupJSON, dropJSON []byte
	var pincode string
	if err := row.Scan(
		&id, &sellerID, &orderID, &decisionID, &state,
		&carrierCode, &serviceType, &sh.AWB, &sh.CarrierShipmentID,
		&sh.EstimatedDeliveryAt, &sh.BookedAt, &sh.ShippedAt, &sh.DeliveredAt, &sh.CancelledAt,
		&charges, &cod,
		&pickupLocID, &pickupJSON, &dropJSON,
		&pincode, &sh.PackageWeightG, &sh.PackageLengthMM, &sh.PackageWidthMM, &sh.PackageHeightMM,
		&sh.LastCarrierError, &sh.AttemptCount, &sh.CreatedAt, &sh.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Shipment{}, core.ErrNotFound
		}
		return Shipment{}, fmt.Errorf("shipments.scan: %w", err)
	}
	sh.ID = core.ShipmentIDFromUUID(id)
	sh.SellerID = core.SellerIDFromUUID(sellerID)
	sh.OrderID = core.OrderIDFromUUID(orderID)
	sh.AllocationDecisionID = core.AllocationDecisionIDFromUUID(decisionID)
	sh.State = ShipmentState(state)
	sh.CarrierCode = carrierCode
	sh.ServiceType = core.ServiceType(serviceType)
	sh.ChargesPaise = core.Paise(charges)
	sh.CODAmountPaise = core.Paise(cod)
	sh.PickupLocationID = core.PickupLocationIDFromUUID(pickupLocID)
	sh.DropPincode = core.Pincode(pincode)
	_ = json.Unmarshal(pickupJSON, &sh.PickupAddress)
	_ = json.Unmarshal(dropJSON, &sh.DropAddress)
	return sh, nil
}

// transition runs a state-event-paired transition inside a single tx so the
// shipment row and its state_event row never diverge.
func (s *service) transition(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, from, to ShipmentState, reason string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		err := tx.QueryRow(ctx, `SELECT state FROM shipment WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("shipments.transition lock: %w", err)
		}
		if ShipmentState(state) != from {
			return fmt.Errorf("%w: shipment is %s not %s", core.ErrInvalidArgument, state, from)
		}
		// Stamp the corresponding timestamp column when entering a terminal
		// or milestone state. NULL-safe (COALESCE) so a re-transition idempotent.
		var stampCol string
		switch to {
		case StateInTransit:
			stampCol = "shipped_at"
		case StateDelivered:
			stampCol = "delivered_at"
		}
		if stampCol != "" {
			if _, err := tx.Exec(ctx,
				fmt.Sprintf("UPDATE shipment SET state=$2, %s=COALESCE(%s, now()), updated_at=now() WHERE id=$1 AND seller_id=$3",
					stampCol, stampCol),
				id.UUID(), string(to), sellerID.UUID()); err != nil {
				return fmt.Errorf("shipments.transition: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, updateShipmentStateSQL, id.UUID(), string(to), sellerID.UUID()); err != nil {
				return fmt.Errorf("shipments.transition: %w", err)
			}
		}
		_, err = tx.Exec(ctx, insertShipmentStateEventSQL,
			id.UUID(), sellerID.UUID(), string(from), string(to), reason)
		return err
	})
}

func (s *service) Cancel(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error {
	// Best-effort carrier cancel before DB update.
	sh, err := s.Get(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if sh.AWB != "" {
		if adapter, ok := s.registry.Get(sh.CarrierCode); ok {
			r := adapter.Cancel(ctx, carriers.CancelRequest{AWBNumber: sh.AWB, Reason: reason})
			if !r.OK() {
				s.log.WarnContext(ctx, "carrier cancel failed",
					slog.String("awb", sh.AWB), slog.String("err", r.Err.Error()))
			}
		}
	}

	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM shipment WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("shipments.Cancel lock: %w", err)
		}
		if _, err := tx.Exec(ctx, cancelShipmentSQL, id.UUID(), reason, sellerID.UUID()); err != nil {
			return fmt.Errorf("shipments.Cancel: %w", err)
		}
		_, err := tx.Exec(ctx, insertShipmentStateEventSQL,
			id.UUID(), sellerID.UUID(), state, "cancelled", reason)
		return err
	})
}

func (s *service) MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error {
	if err := s.transition(ctx, sellerID, id, StateBooked, StateInTransit, "in_transit"); err != nil {
		return err
	}
	s.fanoutOrderTransition(ctx, sellerID, id, "in_transit")
	return nil
}

func (s *service) MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, _ time.Time) error {
	if err := s.transition(ctx, sellerID, id, StateInTransit, StateDelivered, "delivered"); err != nil {
		return err
	}
	s.fanoutOrderTransition(ctx, sellerID, id, "delivered")
	return nil
}

// fanoutOrderTransition mirrors a shipment state change to the matching
// order state. Best-effort: if the order state machine refuses (e.g.
// already past this state), we log + continue rather than rolling back
// the shipment transition.
func (s *service) fanoutOrderTransition(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, to string) {
	sh, err := s.Get(ctx, sellerID, shipmentID)
	if err != nil {
		s.log.WarnContext(ctx, "fanoutOrderTransition: get shipment failed",
			slog.String("shipment_id", shipmentID.String()), slog.String("err", err.Error()))
		return
	}
	var orderErr error
	switch to {
	case "in_transit":
		orderErr = s.orders.MarkInTransit(ctx, sellerID, sh.OrderID)
	case "delivered":
		orderErr = s.orders.MarkDelivered(ctx, sellerID, sh.OrderID)
	case "rto":
		orderErr = s.orders.MarkRTO(ctx, sellerID, sh.OrderID, "carrier_rto")
	}
	if orderErr != nil {
		s.log.WarnContext(ctx, "fanoutOrderTransition: order mark failed",
			slog.String("order_id", sh.OrderID.String()),
			slog.String("to", to),
			slog.String("err", orderErr.Error()))
	}
}

func (s *service) MarkRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM shipment WHERE id=$1 AND seller_id=$2 FOR UPDATE`,
			id.UUID(), sellerID.UUID()).Scan(&state); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return core.ErrNotFound
			}
			return fmt.Errorf("shipments.MarkRTO lock: %w", err)
		}
		if state != string(StateInTransit) && state != string(StateBooked) {
			return fmt.Errorf("%w: cannot mark RTO from %s", core.ErrInvalidArgument, state)
		}
		if _, err := tx.Exec(ctx, updateShipmentStateSQL, id.UUID(), string(StateRTOInProgress), sellerID.UUID()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, insertShipmentStateEventSQL,
			id.UUID(), sellerID.UUID(), state, string(StateRTOInProgress), reason)
		return err
	})
}

func (s *service) CompleteRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error {
	return s.transition(ctx, sellerID, id, StateRTOInProgress, StateRTOCompleted, "rto_completed")
}

func (s *service) ListByPickupDate(ctx context.Context, sellerID core.SellerID, pickupLocationID core.PickupLocationID, carrierCode string, date time.Time) ([]Shipment, error) {
	var out []Shipment
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
            SELECT id FROM shipment
            WHERE seller_id=$1 AND pickup_location_id=$2 AND carrier_code=$3
              AND booked_at::date = $4::date AND state = 'booked'
            ORDER BY created_at`,
			sellerID.UUID(), pickupLocationID.UUID(), carrierCode, date,
		)
		if err != nil {
			return fmt.Errorf("shipments.ListByPickupDate: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			sh, err := scanShipment(tx.QueryRow(ctx, getShipmentSQL, id, sellerID.UUID()))
			if err != nil {
				continue
			}
			out = append(out, sh)
		}
		return rows.Err()
	})
	return out, err
}
