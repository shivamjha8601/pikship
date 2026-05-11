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

// Shipment is the full shipment record.
type Shipment struct {
	ID                    core.ShipmentID
	SellerID              core.SellerID
	OrderID               core.OrderID
	AllocationDecisionID  core.AllocationDecisionID
	State                 ShipmentState
	CarrierCode           string
	ServiceType           core.ServiceType
	AWB                   string
	CarrierShipmentID     string
	EstimatedDeliveryAt   *time.Time
	BookedAt              *time.Time
	ChargesPaise          core.Paise
	CODAmountPaise        core.Paise
	PickupLocationID      core.PickupLocationID
	PickupAddress         core.Address
	DropAddress           core.Address
	DropPincode           core.Pincode
	PackageWeightG        int
	PackageLengthMM       int
	PackageWidthMM        int
	PackageHeightMM       int
	LastCarrierError      string
	AttemptCount          int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// BookRequest carries all the data needed to book a shipment.
type BookRequest struct {
	SellerID        core.SellerID
	OrderID         core.OrderID
	Decision        allocation.Decision
	PickupAddress   core.Address
	PickupContact   core.ContactInfo
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
