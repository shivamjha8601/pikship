package catalog

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

type pickupServiceImpl struct {
	repo *repo
	log  *slog.Logger
}

// NewPickupService constructs the pickup location service. pool must be app pool.
func NewPickupService(pool *pgxpool.Pool, log *slog.Logger) PickupService {
	return &pickupServiceImpl{repo: newRepo(pool), log: log}
}

func (s *pickupServiceImpl) Create(ctx context.Context, req PickupCreateRequest) (PickupLocation, error) {
	p, err := s.repo.insertPickup(ctx, req)
	if err != nil {
		return PickupLocation{}, fmt.Errorf("catalog.Create: %w", err)
	}
	return p, nil
}

func (s *pickupServiceImpl) Get(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) (PickupLocation, error) {
	return s.repo.getPickup(ctx, sellerID, id)
}

func (s *pickupServiceImpl) List(ctx context.Context, sellerID core.SellerID) ([]PickupLocation, error) {
	return s.repo.listPickups(ctx, sellerID)
}

func (s *pickupServiceImpl) Update(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID, patch PickupPatch) (PickupLocation, error) {
	if err := s.repo.updatePickup(ctx, sellerID, id, patch); err != nil {
		return PickupLocation{}, fmt.Errorf("catalog.Update: %w", err)
	}
	return s.repo.getPickup(ctx, sellerID, id)
}

func (s *pickupServiceImpl) SetDefault(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return s.repo.setDefaultPickup(ctx, sellerID, id)
}

func (s *pickupServiceImpl) Deactivate(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return s.repo.deactivatePickup(ctx, sellerID, id)
}

func (s *pickupServiceImpl) SoftDelete(ctx context.Context, sellerID core.SellerID, id core.PickupLocationID) error {
	return s.repo.softDeletePickup(ctx, sellerID, id)
}

// --- ProductService ---

type productServiceImpl struct {
	repo *repo
	log  *slog.Logger
}

// NewProductService constructs the product catalogue service.
func NewProductService(pool *pgxpool.Pool, log *slog.Logger) ProductService {
	return &productServiceImpl{repo: newRepo(pool), log: log}
}

func (s *productServiceImpl) Upsert(ctx context.Context, req ProductUpsertRequest) (Product, error) {
	return s.repo.upsertProduct(ctx, req)
}

func (s *productServiceImpl) Get(ctx context.Context, sellerID core.SellerID, sku string) (Product, error) {
	return s.repo.getProductBySKU(ctx, sellerID, sku)
}

func (s *productServiceImpl) List(ctx context.Context, sellerID core.SellerID, q ProductListQuery) ([]Product, error) {
	return s.repo.listProducts(ctx, sellerID, q.Limit, q.Offset)
}

func (s *productServiceImpl) Delete(ctx context.Context, sellerID core.SellerID, sku string) error {
	return s.repo.softDeleteProduct(ctx, sellerID, sku)
}
