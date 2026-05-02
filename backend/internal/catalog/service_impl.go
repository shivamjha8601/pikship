package catalog

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
)

type pickupServiceImpl struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewPickupService constructs the pickup location service. pool MUST be the
// app pool — RLS on pickup_location requires app.seller_id to be set in tx.
func NewPickupService(pool *pgxpool.Pool, log *slog.Logger) PickupService {
	return &pickupServiceImpl{pool: pool, log: log}
}

func (s *pickupServiceImpl) Create(ctx context.Context, req PickupCreateRequest) (PickupLocation, error) {
	var out PickupLocation
	err := dbtx.WithSellerTx(ctx, s.pool, req.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		p, err := insertPickup(ctx, tx, req)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return PickupLocation{}, fmt.Errorf("catalog.Create: %w", err)
	}
	return out, nil
}

func (s *pickupServiceImpl) Get(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (PickupLocation, error) {
	var out PickupLocation
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		p, err := getPickup(ctx, tx, sellerID, id)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

func (s *pickupServiceImpl) List(ctx context.Context, sellerID core.SellerID) ([]PickupLocation, error) {
	var out []PickupLocation
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		list, err := listPickups(ctx, tx, sellerID)
		if err != nil {
			return err
		}
		out = list
		return nil
	})
	return out, err
}

func (s *pickupServiceImpl) Update(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, patch PickupPatch) (PickupLocation, error) {
	var out PickupLocation
	err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		if err := updatePickup(ctx, tx, sellerID, id, patch); err != nil {
			return fmt.Errorf("catalog.Update: %w", err)
		}
		p, err := getPickup(ctx, tx, sellerID, id)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

func (s *pickupServiceImpl) SetDefault(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return setDefaultPickup(ctx, tx, sellerID, id)
	})
}

func (s *pickupServiceImpl) Deactivate(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return deactivatePickup(ctx, tx, sellerID, id)
	})
}

func (s *pickupServiceImpl) SoftDelete(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return softDeletePickup(ctx, tx, sellerID, id)
	})
}

// --- ProductService ---

type productServiceImpl struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewProductService constructs the product catalogue service.
func NewProductService(pool *pgxpool.Pool, log *slog.Logger) ProductService {
	return &productServiceImpl{pool: pool, log: log}
}

func (s *productServiceImpl) Upsert(ctx context.Context, req ProductUpsertRequest) (Product, error) {
	var out Product
	err := dbtx.WithSellerTx(ctx, s.pool, req.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		p, err := upsertProduct(ctx, tx, req)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

func (s *productServiceImpl) Get(ctx context.Context, sellerID core.SellerID, sku string) (Product, error) {
	var out Product
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		p, err := getProductBySKU(ctx, tx, sellerID, sku)
		if err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

func (s *productServiceImpl) List(ctx context.Context, sellerID core.SellerID, q ProductListQuery) ([]Product, error) {
	var out []Product
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		list, err := listProducts(ctx, tx, sellerID, q.Limit, q.Offset)
		if err != nil {
			return err
		}
		out = list
		return nil
	})
	return out, err
}

func (s *productServiceImpl) Delete(ctx context.Context, sellerID core.SellerID, sku string) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		return softDeleteProduct(ctx, tx, sellerID, sku)
	})
}
