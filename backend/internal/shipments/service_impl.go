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
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// service implements Service.
type service struct {
	pool     *pgxpool.Pool
	registry *carriers.Registry
	wallet   wallet.Service
	orders   orders.Service
	log      *slog.Logger
}

// New constructs the shipments service. pool must be the app pool.
func New(pool *pgxpool.Pool, reg *carriers.Registry, w wallet.Service, o orders.Service, log *slog.Logger) Service {
	return &service{pool: pool, registry: reg, wallet: w, orders: o, log: log}
}

func (s *service) Book(ctx context.Context, req BookRequest) (Shipment, error) {
	if len(req.Decision.Candidates) == 0 {
		return Shipment{}, fmt.Errorf("shipments.Book: no candidates in decision")
	}
	candidate := req.Decision.Candidates[req.Decision.RecommendedIdx]

	adapter, ok := s.registry.Get(candidate.CarrierID.String())
	if !ok {
		return Shipment{}, fmt.Errorf("shipments.Book: no adapter for carrier %s", candidate.CarrierID)
	}

	// Phase A: persist allocation decision + shipment in pending_carrier state.
	shipmentID, _, err := s.persistPhaseA(ctx, req, candidate)
	if err != nil {
		return Shipment{}, fmt.Errorf("shipments.Book: phase A: %w", err)
	}

	// Phase B: call carrier.
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
		// Mark shipment failed or retry-able.
		_ = s.markAttemptFailed(ctx, shipmentID, req.SellerID, bookResp.Err.Error())
		return Shipment{}, fmt.Errorf("shipments.Book: carrier: %w", bookResp.Err)
	}

	// Phase B success: update shipment with AWB + booked state.
	if err := s.confirmPhaseB(ctx, shipmentID, req.SellerID, bookResp.Value); err != nil {
		return Shipment{}, fmt.Errorf("shipments.Book: phase B confirm: %w", err)
	}

	// Transition order state to booked.
	_ = s.orders.MarkBooked(ctx, req.SellerID, req.OrderID, orders.BookedRef{
		AWBNumber:   bookResp.Value.AWBNumber,
		CarrierCode: candidate.CarrierID.String(),
	})

	return s.Get(ctx, req.SellerID, shipmentID)
}

const insertDecisionSQL = `
    INSERT INTO allocation_decision (id, seller_id, order_id, payload)
    VALUES ($1, $2, $3, $4::jsonb)
    RETURNING id
`

const insertShipmentSQL = `
    INSERT INTO shipment (
        id, seller_id, order_id, allocation_decision_id, state,
        carrier_code, service_type, charges_paise, cod_amount_paise,
        pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
        drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm
    ) VALUES ($1,$2,$3,$4,'pending_carrier',$5,$6,$7,$8,$9,$10::jsonb,$11::jsonb,$12,$13,$14,$15,$16)
`

func (s *service) persistPhaseA(ctx context.Context, req BookRequest, c allocation.Candidate) (core.ShipmentID, core.AllocationDecisionID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.ShipmentID{}, core.AllocationDecisionID{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	decisionID := uuid.New()
	decPayload, _ := json.Marshal(req.Decision)
	if _, err := tx.Exec(ctx, insertDecisionSQL, decisionID, req.SellerID.UUID(), req.OrderID.UUID(), decPayload); err != nil {
		return core.ShipmentID{}, core.AllocationDecisionID{}, fmt.Errorf("persist decision: %w", err)
	}

	shipmentID := uuid.New()
	pickupJSON, _ := json.Marshal(req.PickupAddress)
	dropJSON, _ := json.Marshal(req.DropAddress)
	// Use the zero PickupLocationID placeholder — will be filled by caller eventually.
	var pickupLocID uuid.UUID
	if _, err := tx.Exec(ctx, insertShipmentSQL,
		shipmentID, req.SellerID.UUID(), req.OrderID.UUID(), decisionID,
		c.CarrierID.String(), string(c.ServiceType),
		int64(c.Quote.TotalPaise),
		int64(req.CODAmount),
		pickupLocID,
		pickupJSON, dropJSON, string(req.DropPincode),
		req.PackageWeightG, req.PackageLengthMM, req.PackageWidthMM, req.PackageHeightMM,
	); err != nil {
		return core.ShipmentID{}, core.AllocationDecisionID{}, fmt.Errorf("insert shipment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return core.ShipmentID{}, core.AllocationDecisionID{}, err
	}
	return core.ShipmentIDFromUUID(shipmentID), core.AllocationDecisionIDFromUUID(decisionID), nil
}

func (s *service) confirmPhaseB(ctx context.Context, id core.ShipmentID, sellerID core.SellerID, resp carriers.BookResponse) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE shipment SET
            state = 'booked', awb = $2, carrier_shipment_id = $3,
            booked_at = now(), attempt_count = attempt_count + 1, updated_at = now()
        WHERE id = $1 AND seller_id = $4`,
		id.UUID(), resp.AWBNumber, resp.CarrierShipmentRef, sellerID.UUID(),
	)
	return err
}

func (s *service) markAttemptFailed(ctx context.Context, id core.ShipmentID, sellerID core.SellerID, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE shipment SET
            last_carrier_error = $2, last_attempt_at = now(),
            attempt_count = attempt_count + 1, updated_at = now()
        WHERE id = $1 AND seller_id = $3`,
		id.UUID(), errMsg, sellerID.UUID(),
	)
	return err
}

const getShipmentSQL = `
    SELECT id, seller_id, order_id, allocation_decision_id, state,
           carrier_code, service_type, COALESCE(awb,''), COALESCE(carrier_shipment_id,''),
           estimated_delivery_at, booked_at,
           charges_paise, cod_amount_paise,
           pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
           drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
           COALESCE(last_carrier_error,''), attempt_count, created_at, updated_at
    FROM shipment WHERE id = $1 AND seller_id = $2
`

func (s *service) Get(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) (Shipment, error) {
	return s.scanShipment(s.pool.QueryRow(ctx, getShipmentSQL, id.UUID(), sellerID.UUID()))
}

func (s *service) GetByAWB(ctx context.Context, awb string) (Shipment, error) {
	row := s.pool.QueryRow(ctx, `
        SELECT id, seller_id, order_id, allocation_decision_id, state,
               carrier_code, service_type, COALESCE(awb,''), COALESCE(carrier_shipment_id,''),
               estimated_delivery_at, booked_at,
               charges_paise, cod_amount_paise,
               pickup_location_id, pickup_address_snapshot, drop_address_snapshot,
               drop_pincode, package_weight_g, package_length_mm, package_width_mm, package_height_mm,
               COALESCE(last_carrier_error,''), attempt_count, created_at, updated_at
        FROM shipment WHERE awb = $1`, awb)
	return s.scanShipment(row)
}

func (s *service) scanShipment(row pgx.Row) (Shipment, error) {
	var sh Shipment
	var id, sellerID, orderID, decisionID, pickupLocID uuid.UUID
	var state, carrierCode, serviceType string
	var charges, cod int64
	var pickupJSON, dropJSON []byte
	var pincode string
	if err := row.Scan(
		&id, &sellerID, &orderID, &decisionID, &state,
		&carrierCode, &serviceType, &sh.AWB, &sh.CarrierShipmentID,
		&sh.EstimatedDeliveryAt, &sh.BookedAt,
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

func (s *service) transition(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, from, to ShipmentState, reason string) error {
	sh, err := s.Get(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if sh.State != from {
		return fmt.Errorf("%w: shipment is %s not %s", core.ErrInvalidArgument, sh.State, from)
	}
	_, err = s.pool.Exec(ctx, `UPDATE shipment SET state=$2, updated_at=now() WHERE id=$1 AND seller_id=$3`,
		id.UUID(), string(to), sellerID.UUID())
	if err != nil {
		return fmt.Errorf("shipments.transition: %w", err)
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO shipment_state_event (shipment_id, seller_id, from_state, to_state, reason) VALUES ($1,$2,$3,$4,$5)`,
		id.UUID(), sellerID.UUID(), string(from), string(to), reason)
	return nil
}

func (s *service) Cancel(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error {
	sh, err := s.Get(ctx, sellerID, id)
	if err != nil {
		return err
	}
	if sh.AWB != "" {
		if adapter, ok := s.registry.Get(sh.CarrierCode); ok {
			r := adapter.Cancel(ctx, carriers.CancelRequest{AWBNumber: sh.AWB, Reason: reason})
			if !r.OK() {
				s.log.WarnContext(ctx, "carrier cancel failed", slog.String("awb", sh.AWB), slog.String("err", r.Err.Error()))
			}
		}
	}
	_, err = s.pool.Exec(ctx, `UPDATE shipment SET state='cancelled', cancelled_at=now(), cancelled_reason=$2, updated_at=now() WHERE id=$1 AND seller_id=$3`,
		id.UUID(), reason, sellerID.UUID())
	return err
}

func (s *service) MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error {
	return s.transition(ctx, sellerID, id, StateBooked, StateInTransit, "in_transit")
}

func (s *service) MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, at time.Time) error {
	return s.transition(ctx, sellerID, id, StateInTransit, StateDelivered, "delivered")
}

func (s *service) MarkRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error {
	sh, err := s.Get(ctx, sellerID, id)
	if err != nil {
		return err
	}
	from := sh.State
	if from != StateInTransit && from != StateBooked {
		return fmt.Errorf("%w: cannot mark RTO from %s", core.ErrInvalidArgument, from)
	}
	return s.transition(ctx, sellerID, id, from, StateRTOInProgress, reason)
}

func (s *service) CompleteRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error {
	return s.transition(ctx, sellerID, id, StateRTOInProgress, StateRTOCompleted, "rto_completed")
}

func (s *service) ListByPickupDate(ctx context.Context, sellerID core.SellerID, pickupLocationID core.PickupLocationID, carrierCode string, date time.Time) ([]Shipment, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id FROM shipment
        WHERE seller_id=$1 AND pickup_location_id=$2 AND carrier_code=$3
          AND booked_at::date = $4::date AND state = 'booked'
        ORDER BY created_at`,
		sellerID.UUID(), pickupLocationID.UUID(), carrierCode, date,
	)
	if err != nil {
		return nil, fmt.Errorf("shipments.ListByPickupDate: %w", err)
	}
	defer rows.Close()
	var out []Shipment
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		sh, err := s.Get(ctx, sellerID, core.ShipmentIDFromUUID(id))
		if err != nil {
			continue
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}
