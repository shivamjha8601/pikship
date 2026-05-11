// Package shipments manages the shipment lifecycle: two-phase booking
// (create pending_carrier → confirm with carrier → booked), tracking state
// transitions, and manifests.
//
// Per LLD §03-services/13-shipments.
package shipments

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/allocation"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Service is the public API of the shipments module.
type Service interface {
	// Book initiates two-phase booking: persists decision → calls carrier → returns Shipment.
	Book(ctx context.Context, req BookRequest) (Shipment, error)

	// Get returns a shipment by ID.
	Get(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) (Shipment, error)

	// GetByAWB returns a shipment by AWB number.
	GetByAWB(ctx context.Context, awb string) (Shipment, error)

	// GetByOrderID returns the (latest) shipment for an order. Used by the
	// order detail page to surface tracking after a page reload, since the
	// frontend only has the order ID in its URL.
	GetByOrderID(ctx context.Context, sellerID core.SellerID, orderID core.OrderID) (Shipment, error)

	// Cancel cancels a booked shipment with the carrier.
	Cancel(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error

	// MarkInTransit transitions the shipment to in_transit.
	MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error

	// MarkDelivered transitions to delivered.
	MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, at time.Time) error

	// MarkRTO transitions to rto_in_progress.
	MarkRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID, reason string) error

	// CompleteRTO transitions to rto_completed.
	CompleteRTO(ctx context.Context, sellerID core.SellerID, id core.ShipmentID) error

	// ListByPickupDate lists shipments for manifest generation.
	ListByPickupDate(ctx context.Context, sellerID core.SellerID, pickupLocationID core.PickupLocationID, carrierCode string, date time.Time) ([]Shipment, error)

	// FetchLabelPDF returns the carrier-issued shipping label as PDF bytes.
	FetchLabelPDF(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID) ([]byte, error)
}

// ShipmentState represents the shipment's lifecycle state.
type ShipmentState string

const (
	StatePendingCarrier ShipmentState = "pending_carrier"
	StateBooked         ShipmentState = "booked"
	StateInTransit      ShipmentState = "in_transit"
	StateDelivered      ShipmentState = "delivered"
	StateRTOInProgress  ShipmentState = "rto_in_progress"
	StateRTOCompleted   ShipmentState = "rto_completed"
	StateCancelled      ShipmentState = "cancelled"
	StateFailed         ShipmentState = "failed"
)

// Shipment is the full shipment record. JSON tags follow the rest of the API
// (snake_case) — previously this struct went through Go's default field-name
// marshaling and emitted PascalCase keys, which forced every frontend caller
// to special-case it.
type Shipment struct {
	ID                   core.ShipmentID           `json:"id"`
	SellerID             core.SellerID             `json:"seller_id"`
	OrderID              core.OrderID              `json:"order_id"`
	AllocationDecisionID core.AllocationDecisionID `json:"allocation_decision_id"`
	State                ShipmentState             `json:"state"`
	CarrierCode          string                    `json:"carrier_code"`
	ServiceType          core.ServiceType          `json:"service_type"`
	AWB                  string                    `json:"awb"`
	CarrierShipmentID    string                    `json:"carrier_shipment_id"`
	EstimatedDeliveryAt  *time.Time                `json:"estimated_delivery_at,omitempty"`
	BookedAt             *time.Time                `json:"booked_at,omitempty"`
	ShippedAt            *time.Time                `json:"shipped_at,omitempty"`
	DeliveredAt          *time.Time                `json:"delivered_at,omitempty"`
	CancelledAt          *time.Time                `json:"cancelled_at,omitempty"`
	ChargesPaise         core.Paise                `json:"charges_paise"`
	CODAmountPaise       core.Paise                `json:"cod_amount_paise"`
	PickupLocationID     core.PickupLocationID     `json:"pickup_location_id"`
	PickupAddress        core.Address              `json:"pickup_address"`
	DropAddress          core.Address              `json:"drop_address"`
	DropPincode          core.Pincode              `json:"drop_pincode"`
	PackageWeightG       int                       `json:"package_weight_g"`
	PackageLengthMM      int                       `json:"package_length_mm"`
	PackageWidthMM       int                       `json:"package_width_mm"`
	PackageHeightMM      int                       `json:"package_height_mm"`
	LastCarrierError     string                    `json:"last_carrier_error,omitempty"`
	AttemptCount         int                       `json:"attempt_count"`
	CreatedAt            time.Time                 `json:"created_at"`
	UpdatedAt            time.Time                 `json:"updated_at"`
}

// BookRequest carries all the data needed to book a shipment.
type BookRequest struct {
	SellerID         core.SellerID
	OrderID          core.OrderID
	PickupLocationID core.PickupLocationID
	Decision         allocation.Decision
	PickupAddress    core.Address
	PickupContact    core.ContactInfo
	DropAddress     core.Address
	DropContact     core.ContactInfo
	DropPincode     core.Pincode
	PackageWeightG  int
	PackageLengthMM int
	PackageWidthMM  int
	PackageHeightMM int
	PaymentMode     core.PaymentMode
	DeclaredValue   core.Paise
	CODAmount       core.Paise
	InvoiceNumber   string
	SellerReference string
}
