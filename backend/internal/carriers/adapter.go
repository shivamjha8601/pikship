// Package carriers defines the carrier adapter interface and the registry/
// circuit-breaker framework.
//
// Each carrier (Delhivery, BlueDart, etc.) provides a concrete Adapter
// implementation. Domain code (shipments, tracking) calls through the
// Registry which wraps every call with the breaker.
//
// Per LLD §03-services/12-carriers-framework.
package carriers

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Adapter is the contract every carrier must implement.
type Adapter interface {
	// Identity
	Code() string
	DisplayName() string
	Capabilities() Capabilities

	// Serviceability
	CheckServiceability(ctx context.Context, q ServiceabilityQuery) (bool, error)

	// Booking lifecycle
	Book(ctx context.Context, req BookRequest) Result[BookResponse]
	Cancel(ctx context.Context, req CancelRequest) Result[CancelResponse]
	FetchLabel(ctx context.Context, req LabelRequest) Result[LabelResponse]
	FetchTrackingEvents(ctx context.Context, awb string, since time.Time) Result[[]TrackingEvent]
	RaiseNDRAction(ctx context.Context, req NDRActionRequest) Result[NDRActionResponse]
}

// Capabilities describes what a carrier supports.
type Capabilities struct {
	Services             []core.ServiceType
	SupportsCOD          bool
	MaxDeclaredValuePaise core.Paise
	MaxWeightG           int
	MaxLengthMM          int
	SupportsNDRActions   bool
	SupportsLabelFetch   bool
}

// ServiceabilityQuery checks if a (pickup→ship) route is served.
type ServiceabilityQuery struct {
	PickupPincode core.Pincode
	ShipToPincode core.Pincode
	ServiceType   core.ServiceType
	WeightG       int
}

// BookRequest is a shipment booking.
type BookRequest struct {
	OrderID          core.OrderID
	ShipmentID       core.ShipmentID
	PickupPincode    core.Pincode
	ShipToPincode    core.Pincode
	ServiceType      core.ServiceType
	PaymentMode      core.PaymentMode
	DeclaredWeightG  int
	LengthMM         int
	WidthMM          int
	HeightMM         int
	DeclaredValue    core.Paise
	CODAmount        core.Paise
	PickupContact    core.ContactInfo
	PickupAddress    core.Address
	ReceiverContact  core.ContactInfo
	ShippingAddress  core.Address
	InvoiceNumber    string
	SellerReference  string
}

// BookResponse is the carrier's booking confirmation.
type BookResponse struct {
	AWBNumber          string
	CarrierShipmentRef string
	LabelURL           string
	ExpectedPickupAt   *time.Time
}

// CancelRequest cancels a booked shipment.
type CancelRequest struct {
	AWBNumber string
	Reason    string
}

// CancelResponse confirms a cancellation.
type CancelResponse struct {
	Cancelled bool
	Message   string
}

// LabelRequest fetches a shipping label.
type LabelRequest struct {
	AWBNumber string
	Format    string // "pdf" | "zpl"
}

// LabelResponse contains the label data.
type LabelResponse struct {
	Format  string
	Data    []byte
	URL     string
}

// TrackingEvent is one carrier-reported shipment event.
type TrackingEvent struct {
	CarrierCode string
	AWBNumber   string
	Status      string
	StatusCode  string
	Location    string
	Timestamp   time.Time
	Remarks     string
	IsDelivered bool
	IsRTO       bool
}

// NDRActionRequest sends a buyer action to the carrier.
type NDRActionRequest struct {
	AWBNumber      string
	Action         string // "reattempt" | "rto" | "address_change"
	NewAddress     *core.Address
	BuyerRemarks   string
	ReattemptDate  *time.Time
}

// NDRActionResponse confirms the NDR action.
type NDRActionResponse struct {
	Accepted bool
	Message  string
}

// ErrorClass classifies carrier errors.
type ErrorClass string

const (
	ErrClassTransient   ErrorClass = "transient"   // retry
	ErrClassPermanent   ErrorClass = "permanent"   // don't retry
	ErrClassUnsupported ErrorClass = "unsupported" // capability gap
	ErrClassAuth        ErrorClass = "auth"        // credentials issue
)

// Result wraps a carrier response with error metadata.
type Result[T any] struct {
	Value      T
	Err        error
	ErrClass   ErrorClass
	RetryAfter *time.Duration
	RawBody    []byte // for debugging
}

// OK reports whether the result has no error.
func (r Result[T]) OK() bool { return r.Err == nil }
