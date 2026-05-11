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
	ID               core.OrderID          `json:"id"`
	SellerID         core.SellerID         `json:"seller_id"`
	State            OrderState            `json:"state"`
	Channel          string                `json:"channel"`
	ChannelOrderID   string                `json:"channel_order_id"`
	OrderRef         string                `json:"order_ref,omitempty"`
	BuyerName        string                `json:"buyer_name"`
	BuyerPhone       string                `json:"buyer_phone"`
	BuyerEmail       string                `json:"buyer_email,omitempty"`
	BillingAddress   core.Address          `json:"billing_address"`
	ShippingAddress  core.Address          `json:"shipping_address"`
	ShippingPincode  core.Pincode          `json:"shipping_pincode"`
	ShippingState    string                `json:"shipping_state"`
	PaymentMethod    core.PaymentMode      `json:"payment_method"`
	SubtotalPaise    core.Paise            `json:"subtotal_paise"`
	ShippingPaise    core.Paise            `json:"shipping_paise"`
	DiscountPaise    core.Paise            `json:"discount_paise"`
	TaxPaise         core.Paise            `json:"tax_paise"`
	TotalPaise       core.Paise            `json:"total_paise"`
	CODAmountPaise   core.Paise            `json:"cod_amount_paise"`
	PickupLocationID core.PickupLocationID `json:"pickup_location_id"`
	PackageWeightG   int                   `json:"package_weight_g"`
	PackageLengthMM  int                   `json:"package_length_mm"`
	PackageWidthMM   int                   `json:"package_width_mm"`
	PackageHeightMM  int                   `json:"package_height_mm"`
	AWBNumber        string                `json:"awb_number,omitempty"`
	CarrierCode      string                `json:"carrier_code,omitempty"`
	BookedAt         *time.Time            `json:"booked_at,omitempty"`
	ShippedAt        *time.Time            `json:"shipped_at,omitempty"`
	OutForDeliveryAt *time.Time            `json:"out_for_delivery_at,omitempty"`
	DeliveredAt      *time.Time            `json:"delivered_at,omitempty"`
	CancelledAt      *time.Time            `json:"cancelled_at,omitempty"`
	Notes            string                `json:"notes,omitempty"`
	Tags             []string              `json:"tags,omitempty"`
	Lines            []OrderLine           `json:"lines"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// OrderLine is one line in an order.
type OrderLine struct {
	LineNo         int        `json:"line_no"`
	SKU            string     `json:"sku"`
	Name           string     `json:"name"`
	Quantity       int        `json:"quantity"`
	UnitPricePaise core.Paise `json:"unit_price_paise"`
	UnitWeightG    int        `json:"unit_weight_g"`
	HSNCode        string     `json:"hsn_code,omitempty"`
	CategoryHint   string     `json:"category_hint,omitempty"`
}

// CreateRequest carries data for a new order.
type CreateRequest struct {
	SellerID         core.SellerID         `json:"seller_id,omitempty"`
	Channel          string                `json:"channel"`
	ChannelOrderID   string                `json:"channel_order_id"`
	OrderRef         string                `json:"order_ref,omitempty"`
	BuyerName        string                `json:"buyer_name"`
	BuyerPhone       string                `json:"buyer_phone"`
	BuyerEmail       string                `json:"buyer_email,omitempty"`
	BillingAddress   core.Address          `json:"billing_address"`
	ShippingAddress  core.Address          `json:"shipping_address"`
	ShippingPincode  core.Pincode          `json:"shipping_pincode"`
	ShippingState    string                `json:"shipping_state"`
	PaymentMethod    core.PaymentMode      `json:"payment_method"`
	SubtotalPaise    core.Paise            `json:"subtotal_paise"`
	ShippingPaise    core.Paise            `json:"shipping_paise"`
	DiscountPaise    core.Paise            `json:"discount_paise"`
	TaxPaise         core.Paise            `json:"tax_paise"`
	TotalPaise       core.Paise            `json:"total_paise"`
	CODAmountPaise   core.Paise            `json:"cod_amount_paise"`
	PickupLocationID core.PickupLocationID `json:"pickup_location_id"`
	PackageWeightG   int                   `json:"package_weight_g"`
	PackageLengthMM  int                   `json:"package_length_mm"`
	PackageWidthMM   int                   `json:"package_width_mm"`
	PackageHeightMM  int                   `json:"package_height_mm"`
	Notes            string                `json:"notes,omitempty"`
	Tags             []string              `json:"tags,omitempty"`
	Lines            []OrderLine           `json:"lines"`
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
	Orders []Order `json:"orders"`
	Total  int     `json:"total"`
}
