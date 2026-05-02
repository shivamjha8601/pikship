// Package catalog manages pickup locations and products for a seller.
//
// Per LLD §03-services/11-catalog.
package catalog

import (
	"context"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// PickupService manages pickup locations.
type PickupService interface {
	Create(ctx context.Context, req PickupCreateRequest) (PickupLocation, error)
	Get(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (PickupLocation, error)
	List(ctx context.Context, sellerID core.SellerID) ([]PickupLocation, error)
	Update(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, patch PickupPatch) (PickupLocation, error)
	SetDefault(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error
	Deactivate(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error
	SoftDelete(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error
}

// ProductService manages product catalogue.
type ProductService interface {
	Upsert(ctx context.Context, req ProductUpsertRequest) (Product, error)
	Get(ctx context.Context, sellerID core.SellerID, sku string) (Product, error)
	List(ctx context.Context, sellerID core.SellerID, q ProductListQuery) ([]Product, error)
	Delete(ctx context.Context, sellerID core.SellerID, sku string) error
}

// PickupLocation is one row from pickup_location.
type PickupLocation struct {
	ID           core.PickupLocationID
	SellerID     core.SellerID `json:"seller_id"`
	Label        string        `json:"label"`
	ContactName  string        `json:"contact_name"`
	ContactPhone string        `json:"contact_phone"`
	ContactEmail string        `json:"contact_email,omitempty"`
	Address      core.Address  `json:"address"`
	Pincode      core.Pincode  `json:"pincode"`
	State        string        `json:"state"`
	PickupHours  string        `json:"pickup_hours,omitempty"`
	GSTIN        string        `json:"gstin,omitempty"`
	Active       bool          `json:"active"`
	IsDefault    bool          `json:"is_default"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// PickupCreateRequest carries the data for a new pickup location.
type PickupCreateRequest struct {
	SellerID     core.SellerID `json:"seller_id,omitempty"`
	Label        string        `json:"label"`
	ContactName  string        `json:"contact_name"`
	ContactPhone string        `json:"contact_phone"`
	ContactEmail string        `json:"contact_email,omitempty"`
	Address      core.Address  `json:"address"`
	Pincode      core.Pincode  `json:"pincode"`
	State        string        `json:"state"`
	PickupHours  string        `json:"pickup_hours,omitempty"`
	GSTIN        string        `json:"gstin,omitempty"`
	Active       bool          `json:"active"`
	IsDefault    bool          `json:"is_default"`
}

// PickupPatch holds optional fields for partial updates.
type PickupPatch struct {
	Label        *string
	ContactName  *string
	ContactPhone *string
	ContactEmail *string
	Address      *core.Address
	Pincode      *core.Pincode
	State        *string
	PickupHours  *string
	GSTIN        *string
}

// Product is one row from the product table.
type Product struct {
	ID             core.ProductID `json:"id"`
	SellerID       core.SellerID  `json:"seller_id"`
	SKU            string         `json:"sku"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	UnitWeightG    int            `json:"unit_weight_g"`
	LengthMM       int            `json:"length_mm"`
	WidthMM        int            `json:"width_mm"`
	HeightMM       int            `json:"height_mm"`
	HSNCode        string         `json:"hsn_code,omitempty"`
	CategoryHint   string         `json:"category_hint,omitempty"`
	UnitPricePaise core.Paise     `json:"unit_price_paise"`
	Active         bool           `json:"active"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// ProductUpsertRequest creates or updates a product by SKU.
type ProductUpsertRequest struct {
	SellerID       core.SellerID `json:"seller_id,omitempty"`
	SKU            string        `json:"sku"`
	Name           string        `json:"name"`
	Description    string        `json:"description,omitempty"`
	UnitWeightG    int           `json:"unit_weight_g"`
	LengthMM       int           `json:"length_mm"`
	WidthMM        int           `json:"width_mm"`
	HeightMM       int           `json:"height_mm"`
	HSNCode        string        `json:"hsn_code,omitempty"`
	CategoryHint   string        `json:"category_hint,omitempty"`
	UnitPricePaise core.Paise    `json:"unit_price_paise"`
	Active         bool          `json:"active"`
}

// ProductListQuery filters the product list.
type ProductListQuery struct {
	Search     string
	OnlyActive bool
	Limit      int
	Offset     int
}
