package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
	"github.com/vishal1132/pikshipp/backend/internal/observability/dbtx"
)

// BuyerAddressService manages the seller's address book of buyer/consignee
// destinations. Saved entries are reused when creating orders so users don't
// re-type the same address. The order record still snapshots the address at
// creation time — editing or deleting a book entry never mutates past orders.
type BuyerAddressService interface {
	Create(ctx context.Context, req BuyerAddressCreateRequest) (BuyerAddress, error)
	Get(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) (BuyerAddress, error)
	List(ctx context.Context, sellerID core.SellerID) ([]BuyerAddress, error)
	Update(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID, patch BuyerAddressPatch) (BuyerAddress, error)
	SetDefault(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) error
	SoftDelete(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) error
}

// BuyerAddress is one entry in the address book.
type BuyerAddress struct {
	ID          core.BuyerAddressID `json:"id"`
	SellerID    core.SellerID       `json:"seller_id"`
	Label       string              `json:"label"`
	BuyerName   string              `json:"buyer_name"`
	BuyerPhone  string              `json:"buyer_phone"`
	BuyerEmail  string              `json:"buyer_email,omitempty"`
	Address     core.Address        `json:"address"`
	Pincode     core.Pincode        `json:"pincode"`
	State       string              `json:"state"`
	IsDefault   bool                `json:"is_default"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// BuyerAddressCreateRequest carries the data for a new entry.
type BuyerAddressCreateRequest struct {
	SellerID   core.SellerID `json:"seller_id,omitempty"`
	Label      string        `json:"label"`
	BuyerName  string        `json:"buyer_name"`
	BuyerPhone string        `json:"buyer_phone"`
	BuyerEmail string        `json:"buyer_email,omitempty"`
	Address    core.Address  `json:"address"`
	Pincode    core.Pincode  `json:"pincode"`
	State      string        `json:"state"`
	IsDefault  bool          `json:"is_default"`
}

// BuyerAddressPatch holds optional fields for partial updates.
type BuyerAddressPatch struct {
	Label      *string       `json:"label,omitempty"`
	BuyerName  *string       `json:"buyer_name,omitempty"`
	BuyerPhone *string       `json:"buyer_phone,omitempty"`
	BuyerEmail *string       `json:"buyer_email,omitempty"`
	Address    *core.Address `json:"address,omitempty"`
	Pincode    *core.Pincode `json:"pincode,omitempty"`
	State      *string       `json:"state,omitempty"`
}

const (
	insertBuyerAddressSQL = `
        INSERT INTO buyer_address
            (seller_id, label, buyer_name, buyer_phone, buyer_email,
             address, pincode, state, is_default)
        VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)
        RETURNING id, seller_id, label, buyer_name, buyer_phone,
                  COALESCE(buyer_email,''), address, pincode, state,
                  is_default, created_at, updated_at
    `
	getBuyerAddressSQL = `
        SELECT id, seller_id, label, buyer_name, buyer_phone,
               COALESCE(buyer_email,''), address, pincode, state,
               is_default, created_at, updated_at
        FROM buyer_address
        WHERE id = $1 AND seller_id = $2 AND deleted_at IS NULL
    `
	listBuyerAddressesSQL = `
        SELECT id, seller_id, label, buyer_name, buyer_phone,
               COALESCE(buyer_email,''), address, pincode, state,
               is_default, created_at, updated_at
        FROM buyer_address
        WHERE seller_id = $1 AND deleted_at IS NULL
        ORDER BY is_default DESC, updated_at DESC
    `
	setDefaultBuyerAddressSQL = `
        UPDATE buyer_address SET is_default = (id = $2), updated_at = now()
        WHERE seller_id = $1 AND deleted_at IS NULL
    `
	softDeleteBuyerAddressSQL = `
        UPDATE buyer_address SET deleted_at = now(), is_default = false
        WHERE id = $1 AND seller_id = $2
    `
	updateBuyerAddressSQL = `
        UPDATE buyer_address SET
            label=$3, buyer_name=$4, buyer_phone=$5, buyer_email=$6,
            address=$7::jsonb, pincode=$8, state=$9, updated_at=now()
        WHERE id=$1 AND seller_id=$2
    `
)

type buyerAddressServiceImpl struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// NewBuyerAddressService constructs the buyer-address service.
func NewBuyerAddressService(pool *pgxpool.Pool, log *slog.Logger) BuyerAddressService {
	return &buyerAddressServiceImpl{pool: pool, log: log}
}

func (s *buyerAddressServiceImpl) Create(ctx context.Context, req BuyerAddressCreateRequest) (BuyerAddress, error) {
	if req.Label == "" || req.BuyerName == "" || req.BuyerPhone == "" {
		return BuyerAddress{}, fmt.Errorf("buyer_address.Create: %w", core.ErrInvalidArgument)
	}
	if !req.Pincode.IsValid() {
		return BuyerAddress{}, fmt.Errorf("buyer_address.Create: invalid pincode: %w", core.ErrInvalidArgument)
	}
	var out BuyerAddress
	err := dbtx.WithSellerTx(ctx, s.pool, req.SellerID, func(ctx context.Context, tx pgx.Tx) error {
		if req.IsDefault {
			if _, err := tx.Exec(ctx, `UPDATE buyer_address SET is_default = false WHERE seller_id = $1 AND deleted_at IS NULL`, req.SellerID.UUID()); err != nil {
				return err
			}
		}
		addrJSON, _ := json.Marshal(req.Address)
		b, err := scanBuyerAddress(tx.QueryRow(ctx, insertBuyerAddressSQL,
			req.SellerID.UUID(), req.Label, req.BuyerName, req.BuyerPhone, req.BuyerEmail,
			addrJSON, string(req.Pincode), req.State, req.IsDefault,
		))
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	if err != nil {
		return BuyerAddress{}, fmt.Errorf("buyer_address.Create: %w", err)
	}
	return out, nil
}

func (s *buyerAddressServiceImpl) Get(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) (BuyerAddress, error) {
	var out BuyerAddress
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		b, err := scanBuyerAddress(tx.QueryRow(ctx, getBuyerAddressSQL, id.UUID(), sellerID.UUID()))
		if err != nil {
			return err
		}
		out = b
		return nil
	})
	return out, err
}

func (s *buyerAddressServiceImpl) List(ctx context.Context, sellerID core.SellerID) ([]BuyerAddress, error) {
	var out []BuyerAddress
	err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, listBuyerAddressesSQL, sellerID.UUID())
		if err != nil {
			return fmt.Errorf("buyer_address.List: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBuyerAddress(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

func (s *buyerAddressServiceImpl) Update(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID, patch BuyerAddressPatch) (BuyerAddress, error) {
	var out BuyerAddress
	err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := scanBuyerAddress(tx.QueryRow(ctx, getBuyerAddressSQL, id.UUID(), sellerID.UUID()))
		if err != nil {
			return err
		}
		if patch.Label != nil {
			cur.Label = *patch.Label
		}
		if patch.BuyerName != nil {
			cur.BuyerName = *patch.BuyerName
		}
		if patch.BuyerPhone != nil {
			cur.BuyerPhone = *patch.BuyerPhone
		}
		if patch.BuyerEmail != nil {
			cur.BuyerEmail = *patch.BuyerEmail
		}
		if patch.Address != nil {
			cur.Address = *patch.Address
		}
		if patch.Pincode != nil {
			cur.Pincode = *patch.Pincode
		}
		if patch.State != nil {
			cur.State = *patch.State
		}
		addrJSON, _ := json.Marshal(cur.Address)
		if _, err := tx.Exec(ctx, updateBuyerAddressSQL,
			id.UUID(), sellerID.UUID(),
			cur.Label, cur.BuyerName, cur.BuyerPhone, cur.BuyerEmail,
			addrJSON, string(cur.Pincode), cur.State,
		); err != nil {
			return err
		}
		out = cur
		return nil
	})
	return out, err
}

func (s *buyerAddressServiceImpl) SetDefault(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, setDefaultBuyerAddressSQL, sellerID.UUID(), id.UUID())
		return err
	})
}

func (s *buyerAddressServiceImpl) SoftDelete(ctx context.Context, sellerID core.SellerID, id core.BuyerAddressID) error {
	return dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, softDeleteBuyerAddressSQL, id.UUID(), sellerID.UUID())
		return err
	})
}

func scanBuyerAddress(row interface{ Scan(...any) error }) (BuyerAddress, error) {
	var b BuyerAddress
	var id, sellerID uuid.UUID
	var addrJSON []byte
	var pincode string
	if err := row.Scan(
		&id, &sellerID, &b.Label, &b.BuyerName, &b.BuyerPhone, &b.BuyerEmail,
		&addrJSON, &pincode, &b.State, &b.IsDefault, &b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BuyerAddress{}, core.ErrNotFound
		}
		return BuyerAddress{}, fmt.Errorf("buyer_address.scan: %w", err)
	}
	b.ID = core.BuyerAddressIDFromUUID(id)
	b.SellerID = core.SellerIDFromUUID(sellerID)
	b.Pincode = core.Pincode(pincode)
	_ = json.Unmarshal(addrJSON, &b.Address)
	return b, nil
}
