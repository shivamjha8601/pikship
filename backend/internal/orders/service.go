// Package orders manages the canonical order record and its state machine.
//
// Per LLD §03-services/10-orders.
package orders

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Service is the public API of the orders module.
type Service interface {
	Create(ctx context.Context, req CreateRequest) (Order, error)
	Get(ctx context.Context, sellerID core.SellerID, id core.OrderID) (Order, error)
	GetByChannelRef(ctx context.Context, sellerID core.SellerID, channel, channelOrderID string) (Order, error)
	Update(ctx context.Context, sellerID core.SellerID, id core.OrderID, patch UpdatePatch) (Order, error)
	MarkReady(ctx context.Context, sellerID core.SellerID, id core.OrderID) error
	Cancel(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error
	MarkAllocating(ctx context.Context, sellerID core.SellerID, id core.OrderID) error
	MarkBooked(ctx context.Context, sellerID core.SellerID, id core.OrderID, ref BookedRef) error
	MarkInTransit(ctx context.Context, sellerID core.SellerID, id core.OrderID) error
	MarkDelivered(ctx context.Context, sellerID core.SellerID, id core.OrderID) error
	MarkRTO(ctx context.Context, sellerID core.SellerID, id core.OrderID, reason string) error
	Close(ctx context.Context, sellerID core.SellerID, id core.OrderID) error
	List(ctx context.Context, q ListQuery) (ListResult, error)
}

// OrderState is the order lifecycle state.
type OrderState string

const (
	StateDraft      OrderState = "draft"
	StateReady      OrderState = "ready"
	StateAllocating OrderState = "allocating"
	StateBooked     OrderState = "booked"
	StateInTransit  OrderState = "in_transit"
	StateDelivered  OrderState = "delivered"
	StateClosed     OrderState = "closed"
	StateCancelled  OrderState = "cancelled"
	StateRTO        OrderState = "rto"
)

// allowedTransitions defines valid state transitions.
var allowedTransitions = map[OrderState]map[OrderState]struct{}{
	StateDraft:      {StateReady: {}, StateCancelled: {}},
	StateReady:      {StateAllocating: {}, StateCancelled: {}, StateDraft: {}},
	StateAllocating: {StateBooked: {}, StateReady: {}, StateCancelled: {}},
	StateBooked:     {StateInTransit: {}, StateCancelled: {}, StateRTO: {}},
	StateInTransit:  {StateDelivered: {}, StateRTO: {}},
	StateDelivered:  {StateClosed: {}, StateRTO: {}},
	StateClosed:     {},
	StateCancelled:  {},
	StateRTO:        {StateClosed: {}},
}

// CanTransition returns whether from→to is a valid transition.
func CanTransition(from, to OrderState) bool {
	if targets, ok := allowedTransitions[from]; ok {
		_, ok := targets[to]
		return ok
	}
	return false
}

// Order is the full order record.
type Order struct {
	ID               core.OrderID
	SellerID         core.SellerID
	State            OrderState
	Channel          string
	ChannelOrderID   string
	OrderRef         string
	BuyerName        string
	BuyerPhone       string
	BuyerEmail       string
	BillingAddress   core.Address
	ShippingAddress  core.Address
	ShippingPincode  core.Pincode
	ShippingState    string
	PaymentMethod    core.PaymentMode
	SubtotalPaise    core.Paise
	ShippingPaise    core.Paise
	DiscountPaise    core.Paise
	TaxPaise         core.Paise
	TotalPaise       core.Paise
	CODAmountPaise   core.Paise
	PickupLocationID core.PickupLocationID
	PackageWeightG   int
	PackageLengthMM  int
	PackageWidthMM   int
	PackageHeightMM  int
	AWBNumber        string
	CarrierCode      string
	BookedAt         *time.Time
	Notes            string
	Tags             []string
	Lines            []OrderLine
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// OrderLine is one line in an order.
type OrderLine struct {
	LineNo         int
	SKU            string
	Name           string
	Quantity       int
	UnitPricePaise core.Paise
	UnitWeightG    int
	HSNCode        string
	CategoryHint   string
}

// CreateRequest carries data for a new order.
type CreateRequest struct {
	SellerID         core.SellerID
	Channel          string
	ChannelOrderID   string
	OrderRef         string
	BuyerName        string
	BuyerPhone       string
	BuyerEmail       string
	BillingAddress   core.Address
	ShippingAddress  core.Address
	ShippingPincode  core.Pincode
	ShippingState    string
	PaymentMethod    core.PaymentMode
	SubtotalPaise    core.Paise
	ShippingPaise    core.Paise
	DiscountPaise    core.Paise
	TaxPaise         core.Paise
	TotalPaise       core.Paise
	CODAmountPaise   core.Paise
	PickupLocationID core.PickupLocationID
	PackageWeightG   int
	PackageLengthMM  int
	PackageWidthMM   int
	PackageHeightMM  int
	Notes            string
	Tags             []string
	Lines            []OrderLine
}

// UpdatePatch has optional fields for partial updates.
type UpdatePatch struct {
	BuyerPhone      *string
	BuyerEmail      *string
	ShippingAddress *core.Address
	BillingAddress  *core.Address
	Notes           *string
	Tags            *[]string
}

// BookedRef is the AWB + carrier from the booking confirmation.
type BookedRef struct {
	AWBNumber   string
	CarrierCode string
}

// ListQuery filters the order list.
type ListQuery struct {
	SellerID  core.SellerID
	States    []OrderState
	Channels  []string
	SearchQ   string
	Tags      []string
	Limit     int
	Offset    int
}

// ListResult is a page of orders.
type ListResult struct {
	Orders []Order
	Total  int
}
