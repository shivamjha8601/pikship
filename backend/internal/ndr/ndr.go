// Package ndr manages Non-Delivery Report cases — the state machine for
// failed first-attempt deliveries. Per LLD §03-services/15-ndr.
package ndr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// State is the NDR case state.
type State string

const (
	StateOpen              State = "open"
	StateRequestedReattempt State = "requested_reattempt"
	StateRequestedAddrChange State = "requested_addr_change"
	StateInCarrierRetrying  State = "in_carrier_retrying"
	StateAutoRTOPending     State = "auto_rto_pending"
	StateRTOInitiated       State = "rto_initiated"
	StateDeliveredOnReattempt State = "delivered_on_reattempt"
	StateClosed             State = "closed"
)

// Case is one NDR case record.
type Case struct {
	ID                  core.NDRCaseID
	SellerID            core.SellerID
	ShipmentID          core.ShipmentID
	OrderID             core.OrderID
	State               State
	AttemptCount        int
	LastAttemptAt       *time.Time
	LastAttemptReason   string
	DecisionAction      string
	DecisionBy          string
	ResponseWindowUntil *time.Time
	ClosedAt            *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Service manages NDR cases.
type Service interface {
	OpenCase(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, reason, location string) (Case, error)
	RecordAttempt(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, reason, location string) error
	RequestReattempt(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, by string) error
	RequestAddressChange(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, newAddress core.Address, by string) error
	InitiateRTO(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, reason, by string) error
	CloseDelivered(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID) error
	Get(ctx context.Context, sellerID core.SellerID, id core.NDRCaseID) (Case, error)
	GetByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (Case, error)
}

type service struct{ pool *pgxpool.Pool }

// New constructs the NDR service.
func New(pool *pgxpool.Pool) Service { return &service{pool: pool} }

const insertCaseSQL = `
    INSERT INTO ndr_case (seller_id, shipment_id, order_id, state,
        attempt_count, last_attempt_at, last_attempt_reason, last_attempt_location,
        response_window_until)
    VALUES ($1,$2,$3,'open',1,now(),$4,$5, now() + interval '48 hours')
    RETURNING id, seller_id, shipment_id, order_id, state, attempt_count,
              last_attempt_at, COALESCE(last_attempt_reason,''), COALESCE(decision_action,''),
              COALESCE(decision_by,''), response_window_until, closed_at, created_at, updated_at
`

func (s *service) OpenCase(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, orderID core.OrderID, reason, location string) (Case, error) {
	return s.scanCase(s.pool.QueryRow(ctx, insertCaseSQL,
		sellerID.UUID(), shipmentID.UUID(), orderID.UUID(), reason, location))
}

const updateAttemptSQL = `
    UPDATE ndr_case SET
        attempt_count = attempt_count + 1,
        last_attempt_at = now(), last_attempt_reason = $2, last_attempt_location = $3,
        state = 'open', response_window_until = now() + interval '48 hours',
        updated_at = now()
    WHERE id = $1 AND seller_id = $4
`

func (s *service) RecordAttempt(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, reason, location string) error {
	_, err := s.pool.Exec(ctx, updateAttemptSQL, caseID.UUID(), reason, location, sellerID.UUID())
	return err
}

func (s *service) RequestReattempt(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, by string) error {
	_, err := s.pool.Exec(ctx, `UPDATE ndr_case SET state='requested_reattempt',
        decision_action='reattempt', decision_by=$2, decision_at=now(), updated_at=now()
        WHERE id=$1 AND seller_id=$3`, caseID.UUID(), by, sellerID.UUID())
	return err
}

func (s *service) RequestAddressChange(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, newAddress core.Address, by string) error {
	addrJSON, _ := json.Marshal(newAddress)
	_, err := s.pool.Exec(ctx, `UPDATE ndr_case SET state='requested_addr_change',
        decision_action='change_address', decision_by=$2, decision_at=now(),
        new_address=$3::jsonb, updated_at=now()
        WHERE id=$1 AND seller_id=$4`, caseID.UUID(), by, addrJSON, sellerID.UUID())
	return err
}

func (s *service) InitiateRTO(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID, reason, by string) error {
	_, err := s.pool.Exec(ctx, `UPDATE ndr_case SET state='rto_initiated',
        decision_action='rto', decision_by=$2, decision_at=now(), updated_at=now()
        WHERE id=$1 AND seller_id=$3`, caseID.UUID(), by, sellerID.UUID())
	return err
}

func (s *service) CloseDelivered(ctx context.Context, sellerID core.SellerID, caseID core.NDRCaseID) error {
	_, err := s.pool.Exec(ctx, `UPDATE ndr_case SET state='delivered_on_reattempt', closed_at=now(), updated_at=now()
        WHERE id=$1 AND seller_id=$2`, caseID.UUID(), sellerID.UUID())
	return err
}

const getCaseSQL = `
    SELECT id, seller_id, shipment_id, order_id, state, attempt_count,
           last_attempt_at, COALESCE(last_attempt_reason,''), COALESCE(decision_action,''),
           COALESCE(decision_by,''), response_window_until, closed_at, created_at, updated_at
    FROM ndr_case WHERE id=$1 AND seller_id=$2
`
const getCaseByShipmentSQL = `
    SELECT id, seller_id, shipment_id, order_id, state, attempt_count,
           last_attempt_at, COALESCE(last_attempt_reason,''), COALESCE(decision_action,''),
           COALESCE(decision_by,''), response_window_until, closed_at, created_at, updated_at
    FROM ndr_case WHERE shipment_id=$1 AND seller_id=$2
    ORDER BY created_at DESC LIMIT 1
`

func (s *service) Get(ctx context.Context, sellerID core.SellerID, id core.NDRCaseID) (Case, error) {
	return s.scanCase(s.pool.QueryRow(ctx, getCaseSQL, id.UUID(), sellerID.UUID()))
}

func (s *service) GetByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) (Case, error) {
	return s.scanCase(s.pool.QueryRow(ctx, getCaseByShipmentSQL, shipmentID.UUID(), sellerID.UUID()))
}

func (s *service) scanCase(row pgx.Row) (Case, error) {
	var c Case
	var id, sellerID, shipmentID, orderID uuid.UUID
	var state string
	if err := row.Scan(&id, &sellerID, &shipmentID, &orderID, &state,
		&c.AttemptCount, &c.LastAttemptAt, &c.LastAttemptReason,
		&c.DecisionAction, &c.DecisionBy, &c.ResponseWindowUntil,
		&c.ClosedAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Case{}, core.ErrNotFound
		}
		return Case{}, fmt.Errorf("ndr.scanCase: %w", err)
	}
	c.ID = core.NDRCaseIDFromUUID(id)
	c.SellerID = core.SellerIDFromUUID(sellerID)
	c.ShipmentID = core.ShipmentIDFromUUID(shipmentID)
	c.OrderID = core.OrderIDFromUUID(orderID)
	c.State = State(state)
	return c, nil
}
