// Package contracts manages per-seller contract versioning.
// Per LLD §03-services/25-contracts.
package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/audit"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Contract is one version of a seller's contract.
type Contract struct {
	ID              core.ContractID
	SellerID        core.SellerID
	Version         int
	State           string
	RateCardID      *core.RateCardID
	Terms           map[string]any
	EffectiveFrom   time.Time
	EffectiveTo     *time.Time
	SignedAt        *time.Time
	CreatedBy       core.UserID
	ActivatedAt     *time.Time
	TerminatedAt    *time.Time
	TerminationReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Service manages contracts.
type Service interface {
	Create(ctx context.Context, sellerID core.SellerID, terms map[string]any, rateCardID *core.RateCardID, effectiveFrom time.Time, by core.UserID) (Contract, error)
	Activate(ctx context.Context, sellerID core.SellerID, id core.ContractID, by core.UserID) error
	Terminate(ctx context.Context, sellerID core.SellerID, id core.ContractID, reason string, by core.UserID) error
	GetActive(ctx context.Context, sellerID core.SellerID) (Contract, error)
	List(ctx context.Context, sellerID core.SellerID) ([]Contract, error)
}

type service struct {
	pool  *pgxpool.Pool
	audit audit.Emitter
}

// New constructs the contracts service.
func New(pool *pgxpool.Pool, au audit.Emitter) Service {
	return &service{pool: pool, audit: au}
}

const insertContractSQL = `
    INSERT INTO contract (seller_id, version, state, rate_card_id, terms, effective_from, created_by)
    VALUES ($1,
        COALESCE((SELECT MAX(version)+1 FROM contract WHERE seller_id=$1), 1),
        'draft', $2, $3::jsonb, $4, $5)
    RETURNING id, seller_id, version, state, rate_card_id, terms, effective_from,
              effective_to, signed_at, created_by, activated_at, terminated_at,
              COALESCE(termination_reason,''), created_at, updated_at
`

func (s *service) Create(ctx context.Context, sellerID core.SellerID, terms map[string]any, rateCardID *core.RateCardID, effectiveFrom time.Time, by core.UserID) (Contract, error) {
	termsJSON, _ := json.Marshal(terms)
	var rcID *uuid.UUID
	if rateCardID != nil {
		u := rateCardID.UUID()
		rcID = &u
	}
	return s.scan(s.pool.QueryRow(ctx, insertContractSQL,
		sellerID.UUID(), rcID, termsJSON, effectiveFrom, by.UUID()))
}

func (s *service) Activate(ctx context.Context, sellerID core.SellerID, id core.ContractID, by core.UserID) error {
	// Supersede any existing active contract.
	_, _ = s.pool.Exec(ctx, `UPDATE contract SET state='superseded', effective_to=now() WHERE seller_id=$1 AND state='active'`, sellerID.UUID())
	_, err := s.pool.Exec(ctx, `UPDATE contract SET state='active', activated_by=$2, activated_at=now(), updated_at=now() WHERE id=$1 AND seller_id=$3`,
		id.UUID(), by.UUID(), sellerID.UUID())
	if err != nil {
		return fmt.Errorf("contracts.Activate: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "contract.signed",
		Target:   audit.Target{Kind: "contract", Ref: id.String()},
	})
	return nil
}

func (s *service) Terminate(ctx context.Context, sellerID core.SellerID, id core.ContractID, reason string, by core.UserID) error {
	_, err := s.pool.Exec(ctx, `UPDATE contract SET state='terminated',
        terminated_by=$2, terminated_at=now(), termination_reason=$3,
        effective_to=now(), updated_at=now()
        WHERE id=$1 AND seller_id=$4 AND state='active'`,
		id.UUID(), by.UUID(), reason, sellerID.UUID())
	if err != nil {
		return fmt.Errorf("contracts.Terminate: %w", err)
	}
	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "contract.terminated",
		Target:   audit.Target{Kind: "contract", Ref: id.String()},
		Payload:  map[string]any{"reason": reason},
	})
	return nil
}

func (s *service) GetActive(ctx context.Context, sellerID core.SellerID) (Contract, error) {
	return s.scan(s.pool.QueryRow(ctx, `
        SELECT id, seller_id, version, state, rate_card_id, terms, effective_from,
               effective_to, signed_at, created_by, activated_at, terminated_at,
               COALESCE(termination_reason,''), created_at, updated_at
        FROM contract WHERE seller_id=$1 AND state='active' LIMIT 1`, sellerID.UUID()))
}

func (s *service) List(ctx context.Context, sellerID core.SellerID) ([]Contract, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id, seller_id, version, state, rate_card_id, terms, effective_from,
               effective_to, signed_at, created_by, activated_at, terminated_at,
               COALESCE(termination_reason,''), created_at, updated_at
        FROM contract WHERE seller_id=$1 ORDER BY version DESC`, sellerID.UUID())
	if err != nil {
		return nil, fmt.Errorf("contracts.List: %w", err)
	}
	defer rows.Close()
	var out []Contract
	for rows.Next() {
		c, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *service) scan(row interface{ Scan(...any) error }) (Contract, error) {
	var c Contract
	var id, sellerID, createdBy uuid.UUID
	var rcID *uuid.UUID
	var termsJSON []byte
	if err := row.Scan(&id, &sellerID, &c.Version, &c.State, &rcID, &termsJSON,
		&c.EffectiveFrom, &c.EffectiveTo, &c.SignedAt, &createdBy,
		&c.ActivatedAt, &c.TerminatedAt, &c.TerminationReason,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Contract{}, core.ErrNotFound
		}
		return Contract{}, fmt.Errorf("contracts.scan: %w", err)
	}
	c.ID = core.ContractIDFromUUID(id)
	c.SellerID = core.SellerIDFromUUID(sellerID)
	c.CreatedBy = core.UserIDFromUUID(createdBy)
	_ = json.Unmarshal(termsJSON, &c.Terms)
	if rcID != nil {
		rc := core.RateCardIDFromUUID(*rcID)
		c.RateCardID = &rc
	}
	return c, nil
}
