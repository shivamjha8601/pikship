package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// querier is the subset of pgx that both *pgxpool.Pool and pgx.Tx satisfy.
// All repo methods take a querier so the service layer routes them through
// a seller-scoped tx (RLS enforced).
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const (
	insertPickupSQL = `
        INSERT INTO pickup_location
            (seller_id, label, contact_name, contact_phone, contact_email,
             address, pincode, state, pickup_hours, gstin, active, is_default)
        VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12)
        RETURNING id, seller_id, label, contact_name, contact_phone,
                  COALESCE(contact_email,''), address, pincode, state,
                  COALESCE(pickup_hours,''), COALESCE(gstin,''), active, is_default,
                  created_at, updated_at
    `
	getPickupSQL = `
        SELECT id, seller_id, label, contact_name, contact_phone,
               COALESCE(contact_email,''), address, pincode, state,
               COALESCE(pickup_hours,''), COALESCE(gstin,''), active, is_default,
               created_at, updated_at
        FROM pickup_location
        WHERE id = $1 AND seller_id = $2 AND deleted_at IS NULL
    `
	listPickupsSQL = `
        SELECT id, seller_id, label, contact_name, contact_phone,
               COALESCE(contact_email,''), address, pincode, state,
               COALESCE(pickup_hours,''), COALESCE(gstin,''), active, is_default,
               created_at, updated_at
        FROM pickup_location
        WHERE seller_id = $1 AND deleted_at IS NULL
        ORDER BY is_default DESC, label ASC
    `
	setDefaultPickupSQL = `
        UPDATE pickup_location SET is_default = (id = $2), updated_at = now()
        WHERE seller_id = $1 AND deleted_at IS NULL
    `
	deactivatePickupSQL = `
        UPDATE pickup_location SET active = false, updated_at = now()
        WHERE id = $1 AND seller_id = $2
    `
	softDeletePickupSQL = `
        UPDATE pickup_location SET deleted_at = now(), active = false, is_default = false
        WHERE id = $1 AND seller_id = $2
    `

	upsertProductSQL = `
        INSERT INTO product
            (seller_id, sku, name, description, unit_weight_g, length_mm, width_mm,
             height_mm, hsn_code, category_hint, unit_price_paise, active)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
        ON CONFLICT (seller_id, sku) WHERE deleted_at IS NULL
        DO UPDATE SET
            name = EXCLUDED.name, description = EXCLUDED.description,
            unit_weight_g = EXCLUDED.unit_weight_g, length_mm = EXCLUDED.length_mm,
            width_mm = EXCLUDED.width_mm, height_mm = EXCLUDED.height_mm,
            hsn_code = EXCLUDED.hsn_code, category_hint = EXCLUDED.category_hint,
            unit_price_paise = EXCLUDED.unit_price_paise, active = EXCLUDED.active,
            updated_at = now()
        RETURNING id, seller_id, sku, name, COALESCE(description,''),
                  unit_weight_g, length_mm, width_mm, height_mm,
                  COALESCE(hsn_code,''), COALESCE(category_hint,''),
                  unit_price_paise, active, created_at, updated_at
    `
	getProductBySKUSQL = `
        SELECT id, seller_id, sku, name, COALESCE(description,''),
               unit_weight_g, length_mm, width_mm, height_mm,
               COALESCE(hsn_code,''), COALESCE(category_hint,''),
               unit_price_paise, active, created_at, updated_at
        FROM product
        WHERE seller_id = $1 AND sku = $2 AND deleted_at IS NULL
    `
	listProductsSQL = `
        SELECT id, seller_id, sku, name, COALESCE(description,''),
               unit_weight_g, length_mm, width_mm, height_mm,
               COALESCE(hsn_code,''), COALESCE(category_hint,''),
               unit_price_paise, active, created_at, updated_at
        FROM product
        WHERE seller_id = $1 AND deleted_at IS NULL
        ORDER BY sku ASC
        LIMIT $2 OFFSET $3
    `
	softDeleteProductSQL = `
        UPDATE product SET deleted_at = now(), active = false
        WHERE seller_id = $1 AND sku = $2 AND deleted_at IS NULL
    `
	updatePickupSQL = `
        UPDATE pickup_location SET
            label=$3, contact_name=$4, contact_phone=$5, contact_email=$6,
            address=$7::jsonb, pincode=$8, state=$9, pickup_hours=$10, gstin=$11,
            updated_at=now()
        WHERE id=$1 AND seller_id=$2
    `
)

func insertPickup(ctx context.Context, q querier, req PickupCreateRequest) (PickupLocation, error) {
	addrJSON, _ := json.Marshal(req.Address)
	return scanPickup(q.QueryRow(ctx, insertPickupSQL,
		req.SellerID.UUID(), req.Label, req.ContactName, req.ContactPhone, req.ContactEmail,
		addrJSON, string(req.Pincode), req.State, req.PickupHours, req.GSTIN,
		req.Active, req.IsDefault,
	))
}

func getPickup(ctx context.Context, q querier, sellerID core.SellerID, id core.PickupLocationID) (PickupLocation, error) {
	return scanPickup(q.QueryRow(ctx, getPickupSQL, id.UUID(), sellerID.UUID()))
}

func listPickups(ctx context.Context, q querier, sellerID core.SellerID) ([]PickupLocation, error) {
	rows, err := q.Query(ctx, listPickupsSQL, sellerID.UUID())
	if err != nil {
		return nil, fmt.Errorf("catalog.listPickups: %w", err)
	}
	defer rows.Close()
	var out []PickupLocation
	for rows.Next() {
		p, err := scanPickup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPickup(row interface{ Scan(...any) error }) (PickupLocation, error) {
	var p PickupLocation
	var id, sellerID uuid.UUID
	var addrJSON []byte
	var pincode string
	if err := row.Scan(
		&id, &sellerID, &p.Label, &p.ContactName, &p.ContactPhone, &p.ContactEmail,
		&addrJSON, &pincode, &p.State, &p.PickupHours, &p.GSTIN,
		&p.Active, &p.IsDefault, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PickupLocation{}, core.ErrNotFound
		}
		return PickupLocation{}, fmt.Errorf("catalog.scanPickup: %w", err)
	}
	p.ID = core.PickupLocationIDFromUUID(id)
	p.SellerID = core.SellerIDFromUUID(sellerID)
	p.Pincode = core.Pincode(pincode)
	_ = json.Unmarshal(addrJSON, &p.Address)
	return p, nil
}

func setDefaultPickup(ctx context.Context, q querier, sellerID core.SellerID, id core.PickupLocationID) error {
	_, err := q.Exec(ctx, setDefaultPickupSQL, sellerID.UUID(), id.UUID())
	return err
}

func deactivatePickup(ctx context.Context, q querier, sellerID core.SellerID, id core.PickupLocationID) error {
	_, err := q.Exec(ctx, deactivatePickupSQL, id.UUID(), sellerID.UUID())
	return err
}

func softDeletePickup(ctx context.Context, q querier, sellerID core.SellerID, id core.PickupLocationID) error {
	_, err := q.Exec(ctx, softDeletePickupSQL, id.UUID(), sellerID.UUID())
	return err
}

func upsertProduct(ctx context.Context, q querier, req ProductUpsertRequest) (Product, error) {
	return scanProduct(q.QueryRow(ctx, upsertProductSQL,
		req.SellerID.UUID(), req.SKU, req.Name, req.Description,
		req.UnitWeightG, req.LengthMM, req.WidthMM, req.HeightMM,
		req.HSNCode, req.CategoryHint, int64(req.UnitPricePaise), req.Active,
	))
}

func getProductBySKU(ctx context.Context, q querier, sellerID core.SellerID, sku string) (Product, error) {
	return scanProduct(q.QueryRow(ctx, getProductBySKUSQL, sellerID.UUID(), sku))
}

func listProducts(ctx context.Context, q querier, sellerID core.SellerID, limit, offset int) ([]Product, error) {
	if limit == 0 {
		limit = 50
	}
	rows, err := q.Query(ctx, listProductsSQL, sellerID.UUID(), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("catalog.listProducts: %w", err)
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanProduct(row interface{ Scan(...any) error }) (Product, error) {
	var p Product
	var id, sellerID uuid.UUID
	var price int64
	if err := row.Scan(
		&id, &sellerID, &p.SKU, &p.Name, &p.Description,
		&p.UnitWeightG, &p.LengthMM, &p.WidthMM, &p.HeightMM,
		&p.HSNCode, &p.CategoryHint, &price, &p.Active, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, core.ErrNotFound
		}
		return Product{}, fmt.Errorf("catalog.scanProduct: %w", err)
	}
	p.ID = core.ProductIDFromUUID(id)
	p.SellerID = core.SellerIDFromUUID(sellerID)
	p.UnitPricePaise = core.Paise(price)
	return p, nil
}

func softDeleteProduct(ctx context.Context, q querier, sellerID core.SellerID, sku string) error {
	_, err := q.Exec(ctx, softDeleteProductSQL, sellerID.UUID(), sku)
	return err
}

// updatePickup applies non-nil patch fields. Reads current row, applies patch,
// writes back. Caller must run inside a tx.
func updatePickup(ctx context.Context, q querier, sellerID core.SellerID, id core.PickupLocationID, patch PickupPatch) error {
	p, err := getPickup(ctx, q, sellerID, id)
	if err != nil {
		return err
	}
	if patch.Label != nil {
		p.Label = *patch.Label
	}
	if patch.ContactName != nil {
		p.ContactName = *patch.ContactName
	}
	if patch.ContactPhone != nil {
		p.ContactPhone = *patch.ContactPhone
	}
	if patch.ContactEmail != nil {
		p.ContactEmail = *patch.ContactEmail
	}
	if patch.Address != nil {
		p.Address = *patch.Address
	}
	if patch.Pincode != nil {
		p.Pincode = *patch.Pincode
	}
	if patch.State != nil {
		p.State = *patch.State
	}
	if patch.PickupHours != nil {
		p.PickupHours = *patch.PickupHours
	}
	if patch.GSTIN != nil {
		p.GSTIN = *patch.GSTIN
	}

	addrJSON, _ := json.Marshal(p.Address)
	_, err = q.Exec(ctx, updatePickupSQL,
		id.UUID(), sellerID.UUID(),
		p.Label, p.ContactName, p.ContactPhone, p.ContactEmail,
		addrJSON, string(p.Pincode), p.State, p.PickupHours, p.GSTIN,
	)
	return err
}
