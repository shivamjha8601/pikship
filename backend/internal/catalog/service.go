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
	SellerID     core.SellerID
	Label        string
	ContactName  string
	ContactPhone string
	ContactEmail string
	Address      core.Address
	Pincode      core.Pincode
	State        string
	PickupHours  string
	GSTIN        string
	Active       bool
	IsDefault    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PickupCreateRequest carries the data for a new pickup location.
type PickupCreateRequest struct {
	SellerID     core.SellerID
	Label        string
	ContactName  string
	ContactPhone string
	ContactEmail string
	Address      core.Address
	Pincode      core.Pincode
	State        string
	PickupHours  string
	GSTIN        string
	Active       bool
	IsDefault    bool
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
	ID              core.ProductID
	SellerID        core.SellerID
	SKU             string
	Name            string
	Description     string
	UnitWeightG     int
	LengthMM        int
	WidthMM         int
	HeightMM        int
	HSNCode         string
	CategoryHint    string
	UnitPricePaise  core.Paise
	Active          bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ProductUpsertRequest creates or updates a product by SKU.
type ProductUpsertRequest struct {
	SellerID       core.SellerID
	SKU            string
	Name           string
	Description    string
	UnitWeightG    int
	LengthMM       int
	WidthMM        int
	HeightMM       int
	HSNCode        string
	CategoryHint   string
	UnitPricePaise core.Paise
	Active         bool
}

// ProductListQuery filters the product list.
type ProductListQuery struct {
	Search     string
	OnlyActive bool
	Limit      int
	Offset     int
}
