// Package contracts manages per-seller contract versioning AND enforces
// the contract's terms by writing policy overrides for the seller.
//
// Activating a contract:
// 1. Marks the contract as 'active' in the contract table.
// 2. Walks Terms.PolicyOverrides → calls policy.SetSellerOverride for each.
// 3. Sets policy.KeyContractActiveID = <contract id> so domain code can
//    cheaply check "is this seller on a contract?".
//
// Terminating a contract:
// 1. Marks 'terminated'.
// 2. Removes the seller-level policy overrides created at activation.
//
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
	"github.com/vishal1132/pikshipp/backend/internal/policy"
)

// Contract is one version of a seller's contract.
type Contract struct {
	ID                core.ContractID  `json:"id"`
	SellerID          core.SellerID    `json:"seller_id"`
	Version           int              `json:"version"`
	State             string           `json:"state"`
	RateCardID        *core.RateCardID `json:"rate_card_id,omitempty"`
	Terms             map[string]any   `json:"terms"`
	EffectiveFrom     time.Time        `json:"effective_from"`
	EffectiveTo       *time.Time       `json:"effective_to,omitempty"`
	SignedAt          *time.Time       `json:"signed_at,omitempty"`
	CreatedBy         core.UserID      `json:"created_by"`
	ActivatedAt       *time.Time       `json:"activated_at,omitempty"`
	TerminatedAt      *time.Time       `json:"terminated_at,omitempty"`
	TerminationReason string           `json:"termination_reason,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
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
	pool   *pgxpool.Pool
	audit  audit.Emitter
	policy policy.Engine // optional; nil = activation does not push overrides
}

// New constructs the contracts service. When policyEngine is non-nil,
// Activate will push the contract's PolicyOverrides into policy_seller_override
// (and Terminate will remove them).
func New(pool *pgxpool.Pool, au audit.Emitter, policyEngine policy.Engine) Service {
	return &service{pool: pool, audit: au, policy: policyEngine}
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
	// Supersede any prior active contract for this seller.
	_, _ = s.pool.Exec(ctx, `UPDATE contract SET state='superseded', effective_to=now() WHERE seller_id=$1 AND state='active'`, sellerID.UUID())

	// Mark this contract active.
	_, err := s.pool.Exec(ctx, `UPDATE contract SET state='active', activated_by=$2, activated_at=now(), updated_at=now() WHERE id=$1 AND seller_id=$3`,
		id.UUID(), by.UUID(), sellerID.UUID())
	if err != nil {
		return fmt.Errorf("contracts.Activate: %w", err)
	}

	// Apply terms as policy overrides.
	c, err := s.getRaw(ctx, id)
	if err != nil {
		return fmt.Errorf("contracts.Activate: refetch: %w", err)
	}
	if s.policy != nil {
		if err := s.applyTerms(ctx, sellerID, c, by); err != nil {
			return fmt.Errorf("contracts.Activate: apply terms: %w", err)
		}
	}

	_ = s.audit.EmitAsync(ctx, audit.Event{
		SellerID: &sellerID,
		Action:   "contract.signed",
		Target:   audit.Target{Kind: "contract", Ref: id.String()},
		Payload:  map[string]any{"version": c.Version},
	})
	return nil
}

// applyTerms walks contract.Terms.policy_overrides and writes each as a
// seller-level policy override. Keys must match policy.Key constants.
//
// Terms JSON shape:
//
//	{
//	  "policy_overrides": {
//	    "limits.shipments_per_month": 0,
//	    "features.insurance":         true,
//	    "wallet.credit_limit_inr":    50000000
//	  }
//	}
func (s *service) applyTerms(ctx context.Context, sellerID core.SellerID, c Contract, by core.UserID) error {
	overrides, ok := c.Terms["policy_overrides"].(map[string]any)
	if !ok {
		// No overrides — set the contract-active marker only.
		return s.policy.SetSellerOverride(ctx, sellerID, policy.KeyContractActiveID,
			policy.StringValue(c.ID.String()), policy.SourceOps, "contract activated (no terms)")
	}
	for k, v := range overrides {
		key := policy.Key(k)
		def := policy.DefinitionByKey(key)
		if def == nil {
			// Skip unknown keys — fail open rather than break activation.
			continue
		}
		val, err := valueForType(def.ValueType, v)
		if err != nil {
			return fmt.Errorf("term %s: %w", k, err)
		}
		if err := s.policy.SetSellerOverride(ctx, sellerID, key, val,
			policy.SourceOps,
			fmt.Sprintf("contract %s v%d activated", c.ID, c.Version),
		); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}
	// Stamp the active contract id.
	return s.policy.SetSellerOverride(ctx, sellerID, policy.KeyContractActiveID,
		policy.StringValue(c.ID.String()), policy.SourceOps, "contract active")
}

// valueForType coerces an arbitrary JSON value into a typed policy.Value
// per the registered Type. Numbers and bools come straight from json.Decode
// as float64/bool; strings as string.
func valueForType(t policy.Type, raw any) (policy.Value, error) {
	switch t {
	case policy.TypeInt64, policy.TypePaise:
		switch n := raw.(type) {
		case float64:
			return policy.Int64Value(int64(n)), nil
		case int:
			return policy.Int64Value(int64(n)), nil
		case int64:
			return policy.Int64Value(n), nil
		}
	case policy.TypeBool:
		if b, ok := raw.(bool); ok {
			return policy.BoolValue(b), nil
		}
	case policy.TypeString, policy.TypeDuration:
		if s, ok := raw.(string); ok {
			return policy.StringValue(s), nil
		}
	case policy.TypeStringList, policy.TypeStringSet:
		if arr, ok := raw.([]any); ok {
			ss := make([]string, 0, len(arr))
			for _, x := range arr {
				if s, ok := x.(string); ok {
					ss = append(ss, s)
				}
			}
			return policy.StringListValue(ss), nil
		}
	}
	return policy.Value{}, fmt.Errorf("cannot coerce %T to %s", raw, t)
}

// getRaw fetches a contract regardless of its state (Activate's caller
// can't use GetActive because the row is being toggled in the same call).
func (s *service) getRaw(ctx context.Context, id core.ContractID) (Contract, error) {
	return s.scan(s.pool.QueryRow(ctx, `
        SELECT id, seller_id, version, state, rate_card_id, terms, effective_from,
               effective_to, signed_at, created_by, activated_at, terminated_at,
               COALESCE(termination_reason,''), created_at, updated_at
        FROM contract WHERE id=$1`, id.UUID()))
}

func (s *service) Terminate(ctx context.Context, sellerID core.SellerID, id core.ContractID, reason string, by core.UserID) error {
	c, err := s.getRaw(ctx, id)
	if err != nil {
		return fmt.Errorf("contracts.Terminate: %w", err)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE contract SET state='terminated',
        terminated_by=$2, terminated_at=now(), termination_reason=$3,
        effective_to=now(), updated_at=now()
        WHERE id=$1 AND seller_id=$4 AND state='active'`,
		id.UUID(), by.UUID(), reason, sellerID.UUID()); err != nil {
		return fmt.Errorf("contracts.Terminate: %w", err)
	}

	// Remove the policy overrides this contract installed.
	if s.policy != nil {
		if overrides, ok := c.Terms["policy_overrides"].(map[string]any); ok {
			for k := range overrides {
				_ = s.policy.RemoveSellerOverride(ctx, sellerID, policy.Key(k),
					policy.SourceOps, "contract terminated")
			}
		}
		_ = s.policy.RemoveSellerOverride(ctx, sellerID, policy.KeyContractActiveID,
			policy.SourceOps, "contract terminated")
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
