// Package tracking ingests carrier events (webhook or poll), normalises them
// to canonical statuses, and drives shipment state transitions.
//
// Per LLD §03-services/14-tracking.
package tracking

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/carriers"
	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
)

// CanonicalStatus is a normalised tracking status across all carriers.
type CanonicalStatus string

const (
	StatusPickedUp   CanonicalStatus = "picked_up"
	StatusInTransit  CanonicalStatus = "in_transit"
	StatusOutForDel  CanonicalStatus = "out_for_delivery"
	StatusDelivered  CanonicalStatus = "delivered"
	StatusRTO        CanonicalStatus = "rto_initiated"
	StatusRTODeliv   CanonicalStatus = "rto_delivered"
	StatusException  CanonicalStatus = "exception"
	StatusPending    CanonicalStatus = "pending"
)

// Event is one normalised tracking event.
type Event struct {
	ShipmentID      core.ShipmentID
	SellerID        core.SellerID
	CarrierCode     string
	AWB             string
	RawStatus       string
	CanonicalStatus CanonicalStatus
	Location        string
	OccurredAt      time.Time
	Source          string
	RawPayload      map[string]any
}

// Service handles tracking event ingestion and poll scheduling.
type Service interface {
	// IngestEvents processes carrier tracking events (from webhook or poll).
	// Idempotent: duplicate events are silently dropped.
	IngestEvents(ctx context.Context, events []carriers.TrackingEvent, source string) error

	// SchedulePoll registers a shipment for periodic polling.
	SchedulePoll(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, awb, carrierCode string) error

	// PollNow does a one-shot manual poll of the carrier for this shipment
	// and ingests any new events. Used by the UI's "refresh tracking" action
	// and by tests; periodic polling normally runs out of the worker.
	PollNow(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) error

	// ListEventsByShipment returns all events for a shipment, newest first.
	ListEventsByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) ([]Event, error)
}

type service struct {
	pool      *pgxpool.Pool
	shipments shipments.Service
	registry  *carriers.Registry
	log       *slog.Logger
}

// New constructs the tracking service. sh may be nil if state transitions
// are not needed (e.g. read-only tracking queries); call SetShipments before
// ingesting events if transitions should fire.
func New(pool *pgxpool.Pool, sh shipments.Service, reg *carriers.Registry, log *slog.Logger) *service {
	return &service{pool: pool, shipments: sh, registry: reg, log: log}
}

// SetShipments wires the shipment service after construction (breaks the
// circular initialisation when tracking and shipments depend on each other).
func (s *service) SetShipments(sh shipments.Service) {
	s.shipments = sh
}

const insertEventSQL = `
    INSERT INTO tracking_event
        (shipment_id, seller_id, carrier_code, awb, raw_status, canonical_status,
         location, occurred_at, source, raw_payload, dedupe_key)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11)
    ON CONFLICT (dedupe_key) DO NOTHING
`

const getShipmentByAWBSQL = `
    SELECT id, seller_id, state FROM shipment WHERE awb = $1
`

func (s *service) IngestEvents(ctx context.Context, events []carriers.TrackingEvent, source string) error {
	for _, e := range events {
		if err := s.ingestOne(ctx, e, source); err != nil {
			s.log.ErrorContext(ctx, "tracking.ingestOne failed",
				slog.String("awb", e.AWBNumber),
				slog.String("err", err.Error()))
		}
	}
	return nil
}

func (s *service) ingestOne(ctx context.Context, e carriers.TrackingEvent, source string) error {
	// Look up shipment.
	var shipmentID uuid.UUID
	var sellerID uuid.UUID
	var state string
	err := s.pool.QueryRow(ctx, getShipmentByAWBSQL, e.AWBNumber).
		Scan(&shipmentID, &sellerID, &state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // unknown AWB; ignore
		}
		return fmt.Errorf("tracking.ingestOne lookup: %w", err)
	}

	canonical := normalise(e.CarrierCode, e.StatusCode, e.Status)
	dedupeKey := dedupeHash(e.CarrierCode, e.AWBNumber, e.StatusCode, e.Timestamp)
	rawJSON, _ := json.Marshal(map[string]any{
		"status": e.Status, "code": e.StatusCode,
		"location": e.Location, "remarks": e.Remarks,
	})

	if _, err := s.pool.Exec(ctx, insertEventSQL,
		shipmentID, sellerID, e.CarrierCode, e.AWBNumber,
		e.Status, string(canonical), e.Location, e.Timestamp,
		source, rawJSON, dedupeKey,
	); err != nil {
		return fmt.Errorf("tracking.ingestOne insert: %w", err)
	}

	// Drive shipment state transitions.
	sid := core.ShipmentIDFromUUID(shipmentID)
	sellID := core.SellerIDFromUUID(sellerID)
	s.applyTransition(ctx, sellID, sid, canonical, state, e)
	return nil
}

func (s *service) applyTransition(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, canonical CanonicalStatus, currentState string, e carriers.TrackingEvent) {
	switch canonical {
	case StatusPickedUp, StatusInTransit, StatusOutForDel:
		if currentState == string(shipments.StateBooked) {
			_ = s.shipments.MarkInTransit(ctx, sellerID, shipmentID)
		}
	case StatusDelivered:
		if currentState == string(shipments.StateInTransit) {
			_ = s.shipments.MarkDelivered(ctx, sellerID, shipmentID, e.Timestamp)
		}
	case StatusRTO:
		_ = s.shipments.MarkRTO(ctx, sellerID, shipmentID, "carrier_initiated_rto")
	case StatusRTODeliv:
		_ = s.shipments.CompleteRTO(ctx, sellerID, shipmentID)
	}
}

const insertPollScheduleSQL = `
    INSERT INTO tracking_poll_schedule
        (shipment_id, seller_id, carrier_code, awb, next_poll_at, interval_sec)
    VALUES ($1,$2,$3,$4,$5,600)
    ON CONFLICT (carrier_code, awb) DO NOTHING
`

func (s *service) SchedulePoll(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, awb, carrierCode string) error {
	_, err := s.pool.Exec(ctx, insertPollScheduleSQL,
		shipmentID.UUID(), sellerID.UUID(), carrierCode, awb, time.Now().Add(10*time.Minute),
	)
	return err
}

const shipmentAWBLookupSQL = `
    SELECT COALESCE(awb,''), COALESCE(carrier_code,'')
    FROM shipment WHERE id = $1 AND seller_id = $2
`

// PollNow queries the carrier adapter for fresh tracking events and ingests
// them. Returns an error if the shipment has no AWB yet (i.e. not booked)
// or if no adapter is registered for the carrier.
func (s *service) PollNow(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) error {
	if s.registry == nil {
		return fmt.Errorf("tracking.PollNow: no carrier registry configured")
	}
	// RLS-scoped read: shipment is owned by a seller, so we set the seller
	// context on the tx before SELECTing or the policy filters every row.
	var awb, carrierCode string
	if err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, shipmentAWBLookupSQL, shipmentID.UUID(), sellerID.UUID()).
			Scan(&awb, &carrierCode)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.ErrNotFound
		}
		return fmt.Errorf("tracking.PollNow: lookup: %w", err)
	}
	if awb == "" {
		return fmt.Errorf("tracking.PollNow: shipment has no AWB yet: %w", core.ErrInvalidArgument)
	}
	adapter, ok := s.registry.Get(carrierCode)
	if !ok {
		return fmt.Errorf("tracking.PollNow: no adapter for carrier %q", carrierCode)
	}
	// Fetch events since the start of the epoch — the insert dedupes via
	// the (carrier, awb, status, ts) hash, so re-pulling old events is cheap.
	res := adapter.FetchTrackingEvents(ctx, awb, time.Time{})
	if res.Err != nil {
		return fmt.Errorf("tracking.PollNow: carrier: %w", res.Err)
	}
	// Insert events under a seller-scoped tx so RLS lets them land. We bypass
	// IngestEvents (which looks the shipment up by AWB without a scope) since
	// we already know the seller_id + shipment_id here.
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		for _, e := range res.Value {
			canonical := normalise(e.CarrierCode, e.StatusCode, e.Status)
			dedupeKey := dedupeHash(e.CarrierCode, e.AWBNumber, e.StatusCode, e.Timestamp)
			rawJSON, _ := json.Marshal(map[string]any{
				"status":   e.Status,
				"code":     e.StatusCode,
				"location": e.Location,
				"remarks":  e.Remarks,
			})
			if _, err := tx.Exec(ctx, insertEventSQL,
				shipmentID.UUID(), sellerID.UUID(), e.CarrierCode, e.AWBNumber,
				e.Status, string(canonical), e.Location, e.Timestamp,
				"poll", rawJSON, dedupeKey,
			); err != nil {
				return fmt.Errorf("tracking.PollNow: insert: %w", err)
			}
		}
		return nil
	})
}

const listEventsSQL = `
    SELECT carrier_code, awb, raw_status, canonical_status,
           COALESCE(location,''), occurred_at, source, raw_payload
    FROM tracking_event
    WHERE shipment_id = $1 AND seller_id = $2
    ORDER BY occurred_at DESC
    LIMIT 100
`

func (s *service) ListEventsByShipment(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) ([]Event, error) {
	var out []Event
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, listEventsSQL, shipmentID.UUID(), sellerID.UUID())
		if err != nil {
			return fmt.Errorf("tracking.ListEvents: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ev Event
			var rawJSON []byte
			var canonical string
			ev.ShipmentID = shipmentID
			ev.SellerID = sellerID
			if err := rows.Scan(&ev.CarrierCode, &ev.AWB, &ev.RawStatus, &canonical,
				&ev.Location, &ev.OccurredAt, &ev.Source, &rawJSON); err != nil {
				return err
			}
			ev.CanonicalStatus = CanonicalStatus(canonical)
			_ = json.Unmarshal(rawJSON, &ev.RawPayload)
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out, err
}

// --- normalisation ---

// normalise maps (carrierCode, statusCode, statusText) to a canonical status.
// Extend this as more carriers are onboarded.
func normalise(carrierCode, statusCode, statusText string) CanonicalStatus {
	_ = carrierCode // could switch per-carrier in future
	switch statusCode {
	case "DL", "DEL", "DLVD", "Delivered":
		return StatusDelivered
	case "OFD", "OUT-SCAN", "OUT":
		return StatusOutForDel
	case "PKP", "PU", "PKPU", "Picked Up":
		return StatusPickedUp
	case "IT", "IN-SCAN", "In Transit":
		return StatusInTransit
	case "RTO", "RTO-INIT", "RTN":
		return StatusRTO
	case "RTO-DL", "RTN-DL":
		return StatusRTODeliv
	}
	// Fallback: check status text.
	switch {
	case contains(statusText, "delivered", "dlvd"):
		return StatusDelivered
	case contains(statusText, "out for delivery", "ofd"):
		return StatusOutForDel
	case contains(statusText, "picked up", "pickup"):
		return StatusPickedUp
	case contains(statusText, "in transit", "intransit"):
		return StatusInTransit
	case contains(statusText, "rto"):
		return StatusRTO
	}
	return StatusException
}

func toLower(x string) string {
	out := make([]byte, len(x))
	for i := range x {
		c := x[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		out[i] = c
	}
	return string(out)
}

func contains(s string, subs ...string) bool {
	lower := toLower(s)
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		for i := 0; i <= len(lower)-len(sub); i++ {
			if lower[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

func dedupeHash(carrierCode, awb, statusCode string, ts time.Time) string {
	h := sha256.Sum256([]byte(carrierCode + "|" + awb + "|" + statusCode + "|" + ts.UTC().Format(time.RFC3339)))
	return fmt.Sprintf("%x", h[:8])
}
