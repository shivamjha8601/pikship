# Service: Wallet & ledger (`internal/wallet`)

> Money is sacred. Double-entry ledger, two-phase reservations, paise-only arithmetic, daily invariant check, reverse-leg grace handling.

## Purpose

- `Reserve / Confirm / Release` — two-phase wallet ops for the booking flow.
- `Post` — direct ledger entry for non-booking flows (recharge, refund, RTO charge, weight reversal, manual adjustment).
- `Balance / Statement` — reads.
- Daily invariant: `SUM(ledger) == cached_balance` per wallet.
- Hold-expiry sweeper.
- Reverse-leg charging with grace-cap suspension.

## Dependencies

- `internal/core` (Paise, IDs, Clock)
- `internal/audit` (high-value events emit synchronously)
- `internal/outbox` (async events: `wallet.recharged`, etc.)
- `internal/policy` (reads `wallet.credit_limit_inr`, `wallet.grace_negative_amount`, etc.)
- `internal/observability/dbtx`
- `internal/observability` (logger)
- `github.com/jackc/pgx/v5/pgxpool`
- `github.com/riverqueue/river` (for cron jobs)

## Package layout

```
internal/wallet/
├── doc.go
├── service.go              ← Service interface
├── service_impl.go         ← serviceImpl
├── repo.go                 ← sqlc-backed repo
├── types.go                ← Hold, Balance, LedgerEntry, LedgerPost
├── errors.go               ← sentinel errors
├── reserve.go              ← Reserve algorithm
├── confirm.go              ← Confirm algorithm
├── release.go              ← Release algorithm
├── post.go                 ← Post (direct) algorithm + grace-cap check
├── balance.go              ← Balance computation
├── statement.go            ← Ledger pagination
├── reverse_leg.go          ← RTO charge handling
├── invariant.go            ← daily check
├── hold_expiry.go          ← per-minute sweep
├── events.go               ← outbox event payloads
├── policy_keys.go          ← keys this module reads
├── jobs.go                 ← river jobs
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package wallet implements seller wallets, two-phase reservations,
// double-entry ledger, and grace-cap suspension semantics.
//
// All money is in paise (int64). Idempotency is enforced by
// UNIQUE (ref_type, ref_id, direction) on the ledger.
//
// Service is the only public type; constructors are for the implementation.
package wallet

import (
    "context"
    "errors"
    "time"

    "github.com/google/uuid"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Service is THE public API of the wallet module.
type Service interface {
    // Reserve places a hold for amount on the seller's wallet for ttl.
    //
    // Returns ErrInsufficientFunds if available balance + credit < amount.
    // The returned HoldID is passed to Confirm or Release.
    //
    // ttl must be in [10s, 10min]. Holds past ttl are auto-released by
    // the per-minute sweep.
    Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error)

    // Confirm finalizes a hold into a debit ledger entry.
    //
    // Idempotent on (ref.Type, ref.ID, direction=debit) — calling Confirm
    // twice with the same Ref returns the same LedgerEntryID.
    //
    // Returns ErrHoldNotFound if hold doesn't exist or isn't active.
    // Returns ErrHoldExpired if hold has expired (caller must re-Reserve).
    Confirm(ctx context.Context, holdID HoldID, ref Ref) (core.LedgerEntryID, error)

    // Release cancels a hold, restoring availability.
    //
    // Idempotent: releasing an already-released or confirmed hold is a no-op.
    Release(ctx context.Context, holdID HoldID) error

    // Post directly creates a ledger entry, bypassing two-phase.
    //
    // Used for: recharges (credit), refunds, RTO charges, weight reversals,
    // manual adjustments, COD remittance to seller.
    //
    // For debits: applies grace-cap check; returns ErrGraceCapBreached if
    // the resulting balance would exceed grace + credit.
    //
    // Idempotent on (post.Ref.Type, post.Ref.ID, post.Direction).
    Post(ctx context.Context, sellerID core.SellerID, post LedgerPost) (core.LedgerEntryID, error)

    // Balance returns the seller's current cached balance plus availability.
    Balance(ctx context.Context, sellerID core.SellerID) (Balance, error)

    // Statement returns ledger entries for the period (paginated cursor).
    Statement(ctx context.Context, sellerID core.SellerID, query StatementQuery) (StatementResult, error)
}

// HoldID identifies an active reservation.
type HoldID uuid.UUID

func NewHoldID() HoldID { return HoldID(uuid.New()) }
func (h HoldID) String() string { return uuid.UUID(h).String() }
func (h HoldID) IsZero() bool { return uuid.UUID(h) == uuid.Nil }

// Ref identifies the source of a wallet operation. Combined with direction,
// it forms the idempotency key on the ledger.
type Ref struct {
    Type RefType
    ID   string
}

// RefType is an enum of accepted reference kinds.
type RefType string

const (
    RefShipmentCharge        RefType = "shipment_charge"
    RefCODHandling           RefType = "cod_handling"
    RefCODRemitToSeller      RefType = "cod_remit_to_seller"
    RefRecharge              RefType = "recharge"
    RefRefund                RefType = "refund"
    RefRTOCharge             RefType = "rto_charge"
    RefReversePickupCharge   RefType = "reverse_pickup_charge"
    RefWeightDisputeCharge   RefType = "weight_dispute_charge"
    RefWeightDisputeReversal RefType = "weight_dispute_reversal"
    RefInsurancePremium      RefType = "insurance_premium"
    RefInsurancePayout       RefType = "insurance_payout"
    RefSubscriptionFee       RefType = "subscription_fee"
    RefManualAdjustment      RefType = "manual_adjustment"
    RefAutoRecharge          RefType = "auto_recharge"
    RefChargeback            RefType = "chargeback"
)

// Direction is debit (money leaving the seller) or credit (money in).
type Direction string

const (
    DirectionDebit  Direction = "debit"
    DirectionCredit Direction = "credit"
)

// LedgerPost is the input to Service.Post.
type LedgerPost struct {
    Direction Direction
    Amount    core.Paise         // always positive; direction sets sign
    Ref       Ref                // idempotency key
    Reason    string             // optional; required for ManualAdjustment
    ActorRef  string             // user_id or system identifier
}

// Balance is the read view of a wallet.
type Balance struct {
    SellerID                  core.SellerID
    BalanceMinor              core.Paise   // current cached balance (may be negative within grace)
    HoldTotalMinor            core.Paise   // sum of active holds
    AvailableMinor            core.Paise   // balance + credit_limit - hold_total
    CreditLimitMinor          core.Paise
    GraceNegativeAmountMinor  core.Paise
    Status                    AccountStatus
    LastUpdated               time.Time
}

// AccountStatus enum.
type AccountStatus string

const (
    StatusActive    AccountStatus = "active"
    StatusFrozen    AccountStatus = "frozen"
    StatusWoundDown AccountStatus = "wound_down"
)

// StatementQuery filters and paginates ledger reads.
type StatementQuery struct {
    From          time.Time           // inclusive
    To            time.Time           // exclusive
    RefTypes      []RefType           // optional filter
    Direction     *Direction          // optional filter
    StartingAfter *core.LedgerEntryID // cursor
    Limit         int                 // 1..100
}

// StatementResult is one page of ledger entries.
type StatementResult struct {
    Entries []LedgerEntry
    HasMore bool
    NextCursor *core.LedgerEntryID
}

// LedgerEntry is one append-only ledger row.
type LedgerEntry struct {
    ID           core.LedgerEntryID
    SellerID     core.SellerID
    Direction    Direction
    AmountMinor  core.Paise
    Ref          Ref
    ReversesID   *core.LedgerEntryID
    Reason       string
    ActorRef     string
    PostedAt     time.Time
}

// Sentinel errors.
var (
    ErrInsufficientFunds  = fmt.Errorf("wallet: insufficient funds: %w", core.ErrInvalidArgument)
    ErrHoldNotFound       = fmt.Errorf("wallet: hold not found: %w", core.ErrNotFound)
    ErrHoldExpired        = errors.New("wallet: hold expired")
    ErrGraceCapBreached   = errors.New("wallet: grace cap breached")
    ErrInvariantViolation = errors.New("wallet: invariant violation")
    ErrAccountFrozen      = errors.New("wallet: account frozen")
    ErrAccountWoundDown   = errors.New("wallet: account wound down")
    ErrInvalidAmount      = fmt.Errorf("wallet: amount must be positive: %w", core.ErrInvalidArgument)
    ErrInvalidTTL         = fmt.Errorf("wallet: ttl out of range: %w", core.ErrInvalidArgument)
)
```

## DB schema

```sql
-- migrations/00NN_create_wallet.up.sql

CREATE TABLE wallet_account (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id                   UUID NOT NULL UNIQUE,
    currency                    TEXT NOT NULL DEFAULT 'INR',
    balance_minor               BIGINT NOT NULL DEFAULT 0,
    hold_total_minor            BIGINT NOT NULL DEFAULT 0,
    credit_limit_minor          BIGINT NOT NULL DEFAULT 0,
    grace_negative_amount_minor BIGINT NOT NULL DEFAULT 50000,  -- ₹500 default
    status                      TEXT NOT NULL DEFAULT 'active',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallet_account_currency_inr CHECK (currency = 'INR'),
    CONSTRAINT wallet_account_status_valid CHECK (status IN ('active','frozen','wound_down')),
    CONSTRAINT wallet_account_hold_nonneg CHECK (hold_total_minor >= 0),
    CONSTRAINT wallet_account_grace_nonneg CHECK (grace_negative_amount_minor >= 0),
    CONSTRAINT wallet_account_credit_nonneg CHECK (credit_limit_minor >= 0)
);

CREATE INDEX wallet_account_seller_idx ON wallet_account (seller_id);

CREATE TRIGGER wallet_account_updated_at
BEFORE UPDATE ON wallet_account
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE wallet_account ENABLE ROW LEVEL SECURITY;
CREATE POLICY wallet_account_seller ON wallet_account
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE ON wallet_account TO pikshipp_app;
GRANT SELECT ON wallet_account TO pikshipp_reports;
GRANT ALL ON wallet_account TO pikshipp_admin;

CREATE TABLE wallet_ledger_entry (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id     UUID NOT NULL,                                  -- denormalized for RLS
    wallet_id     UUID NOT NULL REFERENCES wallet_account(id),
    direction     TEXT NOT NULL,                                  -- 'credit' | 'debit'
    amount_minor  BIGINT NOT NULL,
    ref_type      TEXT NOT NULL,
    ref_id        TEXT NOT NULL,
    reverses_id   UUID,
    reason        TEXT,
    actor_ref     TEXT NOT NULL,
    posted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallet_ledger_direction_valid CHECK (direction IN ('credit','debit')),
    CONSTRAINT wallet_ledger_amount_pos     CHECK (amount_minor > 0),

    -- THE contract: idempotency
    CONSTRAINT wallet_ledger_idempotency UNIQUE (ref_type, ref_id, direction)
);

CREATE INDEX wallet_ledger_seller_posted_idx ON wallet_ledger_entry (seller_id, posted_at DESC);
CREATE INDEX wallet_ledger_wallet_idx        ON wallet_ledger_entry (wallet_id);

ALTER TABLE wallet_ledger_entry ENABLE ROW LEVEL SECURITY;
CREATE POLICY wallet_ledger_seller ON wallet_ledger_entry
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT ON wallet_ledger_entry TO pikshipp_app;
GRANT SELECT ON wallet_ledger_entry TO pikshipp_reports;
GRANT ALL ON wallet_ledger_entry TO pikshipp_admin;

CREATE TABLE wallet_hold (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL,
    wallet_id       UUID NOT NULL REFERENCES wallet_account(id),
    amount_minor    BIGINT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    resolved_at     TIMESTAMPTZ,
    resolved_to_ledger_entry_id UUID,

    CONSTRAINT wallet_hold_status_valid CHECK (status IN ('active','confirmed','released','expired')),
    CONSTRAINT wallet_hold_amount_pos   CHECK (amount_minor > 0)
);

CREATE INDEX wallet_hold_active_idx ON wallet_hold (wallet_id, expires_at) WHERE status = 'active';
CREATE INDEX wallet_hold_seller_idx ON wallet_hold (seller_id);

ALTER TABLE wallet_hold ENABLE ROW LEVEL SECURITY;
CREATE POLICY wallet_hold_seller ON wallet_hold
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE ON wallet_hold TO pikshipp_app;
GRANT SELECT ON wallet_hold TO pikshipp_reports;
GRANT ALL ON wallet_hold TO pikshipp_admin;

CREATE TABLE wallet_invariant_check_result (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL,
    wallet_id       UUID NOT NULL,
    computed_minor  BIGINT NOT NULL,
    cached_minor    BIGINT NOT NULL,
    diff_minor      BIGINT NOT NULL,                              -- 0 if invariant holds
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wallet_invariant_diff_idx ON wallet_invariant_check_result (diff_minor) WHERE diff_minor != 0;
CREATE INDEX wallet_invariant_seller_idx ON wallet_invariant_check_result (seller_id, checked_at DESC);

GRANT SELECT, INSERT ON wallet_invariant_check_result TO pikshipp_app, pikshipp_admin;
GRANT SELECT ON wallet_invariant_check_result TO pikshipp_reports;
```

## SQL queries

```sql
-- query/wallet.sql

-- name: GetWalletForUpdate :one
-- The locking variant for two-phase ops; serializes per seller.
SELECT id, seller_id, balance_minor, hold_total_minor, credit_limit_minor,
       grace_negative_amount_minor, status
FROM wallet_account
WHERE seller_id = $1
FOR UPDATE;

-- name: GetWallet :one
SELECT id, seller_id, balance_minor, hold_total_minor, credit_limit_minor,
       grace_negative_amount_minor, status, updated_at
FROM wallet_account
WHERE seller_id = $1;

-- name: InsertWallet :exec
INSERT INTO wallet_account (id, seller_id, credit_limit_minor, grace_negative_amount_minor)
VALUES ($1, $2, $3, $4)
ON CONFLICT (seller_id) DO NOTHING;

-- name: UpdateWalletBalance :exec
UPDATE wallet_account
SET balance_minor    = balance_minor + $2,
    hold_total_minor = hold_total_minor + $3
WHERE id = $1;

-- name: UpdateWalletPolicy :exec
UPDATE wallet_account
SET credit_limit_minor = $2, grace_negative_amount_minor = $3
WHERE seller_id = $1;

-- name: UpdateWalletStatus :exec
UPDATE wallet_account
SET status = $2
WHERE seller_id = $1;

-- name: InsertHold :exec
INSERT INTO wallet_hold (id, wallet_id, seller_id, amount_minor, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetHoldForUpdate :one
SELECT id, wallet_id, seller_id, amount_minor, status, expires_at
FROM wallet_hold
WHERE id = $1
FOR UPDATE;

-- name: ResolveHold :exec
UPDATE wallet_hold
SET status = $2, resolved_at = now(), resolved_to_ledger_entry_id = $3
WHERE id = $1;

-- name: InsertLedgerEntry :one
INSERT INTO wallet_ledger_entry
  (id, seller_id, wallet_id, direction, amount_minor, ref_type, ref_id, reverses_id, reason, actor_ref)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (ref_type, ref_id, direction) DO NOTHING
RETURNING id;

-- name: GetLedgerEntryByRef :one
SELECT id, seller_id, wallet_id, direction, amount_minor, ref_type, ref_id, reverses_id,
       reason, actor_ref, posted_at
FROM wallet_ledger_entry
WHERE ref_type = $1 AND ref_id = $2 AND direction = $3;

-- name: ListLedgerEntries :many
SELECT id, seller_id, direction, amount_minor, ref_type, ref_id, reverses_id, reason,
       actor_ref, posted_at
FROM wallet_ledger_entry
WHERE seller_id = $1
  AND posted_at >= $2 AND posted_at < $3
  AND (sqlc.narg('direction')::text IS NULL OR direction = sqlc.narg('direction'))
  AND (sqlc.narg('ref_types')::text[] IS NULL OR ref_type = ANY(sqlc.narg('ref_types')::text[]))
  AND (sqlc.narg('starting_after_id')::uuid IS NULL OR id < sqlc.narg('starting_after_id')::uuid)
ORDER BY posted_at DESC, id DESC
LIMIT $4 + 1;

-- name: ExpireOldHolds :many
-- Returns IDs of expired holds (caller releases each).
SELECT id, wallet_id, amount_minor
FROM wallet_hold
WHERE status = 'active' AND expires_at < now()
ORDER BY expires_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: WalletInvariantCheck :many
-- One row per wallet with computed vs cached.
SELECT
    wa.id          AS wallet_id,
    wa.seller_id,
    wa.balance_minor AS cached_minor,
    COALESCE(SUM(CASE WHEN le.direction = 'credit' THEN le.amount_minor ELSE -le.amount_minor END), 0)::bigint AS computed_minor
FROM wallet_account wa
LEFT JOIN wallet_ledger_entry le ON le.wallet_id = wa.id
GROUP BY wa.id, wa.seller_id, wa.balance_minor;

-- name: InsertInvariantCheckResult :exec
INSERT INTO wallet_invariant_check_result
  (seller_id, wallet_id, computed_minor, cached_minor, diff_minor)
VALUES ($1, $2, $3, $4, $5);
```

## Implementation: serviceImpl

```go
// internal/wallet/service_impl.go
package wallet

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
    "github.com/pikshipp/pikshipp/internal/outbox"
    "github.com/pikshipp/pikshipp/internal/policy"
)

type serviceImpl struct {
    pool   *pgxpool.Pool
    repo   *repo
    audit  audit.Emitter
    outbox outbox.Emitter
    policy policy.Engine
    clock  core.Clock
    log    *slog.Logger
}

const (
    minTTL = 10 * time.Second
    maxTTL = 10 * time.Minute
)

func New(pool *pgxpool.Pool, audit audit.Emitter, ob outbox.Emitter, pol policy.Engine, clock core.Clock, log *slog.Logger) Service {
    return &serviceImpl{
        pool: pool, repo: newRepo(pool),
        audit: audit, outbox: ob, policy: pol,
        clock: clock, log: log,
    }
}
```

## Reserve

```go
// internal/wallet/reserve.go
package wallet

import (
    "context"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error) {
    if amount <= 0 {
        return HoldID{}, ErrInvalidAmount
    }
    if ttl < minTTL || ttl > maxTTL {
        return HoldID{}, ErrInvalidTTL
    }

    var holdID HoldID

    err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        wa, err := q.GetWalletForUpdate(ctx, sellerID)
        if err != nil {
            return fmt.Errorf("wallet.Reserve: get wallet: %w", err)
        }

        if AccountStatus(wa.Status) == StatusFrozen {
            return ErrAccountFrozen
        }
        if AccountStatus(wa.Status) == StatusWoundDown {
            return ErrAccountWoundDown
        }

        // Compute available
        // available = balance + credit_limit - hold_total
        // (does NOT include grace_negative_amount; grace is for post-event RTO charges, not reservations)
        avail := core.Paise(wa.BalanceMinor) + core.Paise(wa.CreditLimitMinor) - core.Paise(wa.HoldTotalMinor)
        if amount > avail {
            return ErrInsufficientFunds
        }

        holdID = NewHoldID()
        expiresAt := s.clock.Now().Add(ttl)

        if err := q.InsertHold(ctx, db.InsertHoldParams{
            ID:          uuid.UUID(holdID),
            WalletID:    wa.ID,
            SellerID:    uuid.UUID(sellerID),
            AmountMinor: int64(amount),
            ExpiresAt:   expiresAt,
        }); err != nil {
            return fmt.Errorf("wallet.Reserve: insert hold: %w", err)
        }

        if err := q.UpdateWalletBalance(ctx, db.UpdateWalletBalanceParams{
            ID:               wa.ID,
            BalanceMinorDelta: 0,                  // balance unchanged
            HoldTotalMinorDelta: int64(amount),    // hold incremented
        }); err != nil {
            return fmt.Errorf("wallet.Reserve: bump hold_total: %w", err)
        }

        return nil
    })
    if err != nil {
        return HoldID{}, err
    }

    s.log.InfoContext(ctx, "wallet hold reserved",
        slog.String("hold_id", holdID.String()),
        slog.Int64("amount_minor", int64(amount)))

    return holdID, nil
}
```

## Confirm

```go
// internal/wallet/confirm.go
package wallet

import (
    "context"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

func (s *serviceImpl) Confirm(ctx context.Context, holdID HoldID, ref Ref) (core.LedgerEntryID, error) {
    if ref.Type == "" || ref.ID == "" {
        return core.LedgerEntryID{}, fmt.Errorf("wallet.Confirm: empty ref: %w", core.ErrInvalidArgument)
    }

    var entryID core.LedgerEntryID

    err := dbtx.WithSellerTx(ctx, s.pool, /* sellerID resolved inside */ core.SellerID{}, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        hold, err := q.GetHoldForUpdate(ctx, uuid.UUID(holdID))
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrHoldNotFound
            }
            return fmt.Errorf("wallet.Confirm: get hold: %w", err)
        }

        // We need to set seller_id GUC; do it now (we couldn't earlier without
        // knowing the hold's seller). This re-arms RLS for this tx.
        sellerID := core.SellerID(hold.SellerID)
        _, err = tx.Exec(ctx, "SET LOCAL app.seller_id = $1", sellerID.String())
        if err != nil {
            return fmt.Errorf("wallet.Confirm: re-set seller_id: %w", err)
        }

        if hold.Status != "active" {
            // Idempotent: if already confirmed for this ref, return its entry id
            if hold.Status == "confirmed" {
                existing, lookupErr := q.GetLedgerEntryByRef(ctx, db.GetLedgerEntryByRefParams{
                    RefType:   string(ref.Type),
                    RefID:     ref.ID,
                    Direction: string(DirectionDebit),
                })
                if lookupErr == nil {
                    entryID = core.LedgerEntryID(existing.ID)
                    return nil
                }
            }
            return ErrHoldNotFound  // or expired/released
        }

        if hold.ExpiresAt.Before(s.clock.Now()) {
            return ErrHoldExpired
        }

        // Idempotent insert
        actor, _ := actorRefFromCtx(ctx)
        ledgerID := uuid.New()
        gotID, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
            ID:          ledgerID,
            SellerID:    uuid.UUID(sellerID),
            WalletID:    hold.WalletID,
            Direction:   string(DirectionDebit),
            AmountMinor: hold.AmountMinor,
            RefType:     string(ref.Type),
            RefID:       ref.ID,
            ActorRef:    actor,
        })
        if err != nil {
            return fmt.Errorf("wallet.Confirm: insert ledger: %w", err)
        }
        if gotID == uuid.Nil {
            // ON CONFLICT DO NOTHING — entry already existed; fetch its ID.
            existing, _ := q.GetLedgerEntryByRef(ctx, db.GetLedgerEntryByRefParams{
                RefType:   string(ref.Type),
                RefID:     ref.ID,
                Direction: string(DirectionDebit),
            })
            entryID = core.LedgerEntryID(existing.ID)
        } else {
            entryID = core.LedgerEntryID(gotID)
        }

        // Mark hold confirmed
        if err := q.ResolveHold(ctx, db.ResolveHoldParams{
            ID:                       uuid.UUID(holdID),
            Status:                   "confirmed",
            ResolvedToLedgerEntryID:  uuid.UUID(entryID),
        }); err != nil {
            return fmt.Errorf("wallet.Confirm: resolve hold: %w", err)
        }

        // Update cached balance: -amount; -hold_total
        if err := q.UpdateWalletBalance(ctx, db.UpdateWalletBalanceParams{
            ID:                  hold.WalletID,
            BalanceMinorDelta:   -hold.AmountMinor,
            HoldTotalMinorDelta: -hold.AmountMinor,
        }); err != nil {
            return fmt.Errorf("wallet.Confirm: update balance: %w", err)
        }

        // High-value audit (synchronous, in same tx)
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: &sellerID,
            Action:   "wallet.charged",
            Target:   audit.Target{Kind: "wallet_account", Ref: hold.WalletID.String()},
            Payload: map[string]any{
                "ledger_entry_id": entryID,
                "amount_minor":    hold.AmountMinor,
                "ref_type":        ref.Type,
                "ref_id":          ref.ID,
            },
        }); err != nil {
            return fmt.Errorf("wallet.Confirm: audit: %w", err)
        }

        return nil
    })

    return entryID, err
}
```

## Release

```go
// internal/wallet/release.go
package wallet

import (
    "context"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

func (s *serviceImpl) Release(ctx context.Context, holdID HoldID) error {
    return dbtx.WithSellerTx(ctx, s.pool, core.SellerID{}, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        hold, err := q.GetHoldForUpdate(ctx, uuid.UUID(holdID))
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return nil  // idempotent: not-found = already released
            }
            return fmt.Errorf("wallet.Release: %w", err)
        }

        if hold.Status != "active" {
            return nil  // idempotent: already resolved
        }

        sellerID := core.SellerID(hold.SellerID)
        _, _ = tx.Exec(ctx, "SET LOCAL app.seller_id = $1", sellerID.String())

        if err := q.ResolveHold(ctx, db.ResolveHoldParams{
            ID:     uuid.UUID(holdID),
            Status: "released",
        }); err != nil {
            return fmt.Errorf("wallet.Release: resolve: %w", err)
        }

        if err := q.UpdateWalletBalance(ctx, db.UpdateWalletBalanceParams{
            ID:                  hold.WalletID,
            BalanceMinorDelta:   0,
            HoldTotalMinorDelta: -hold.AmountMinor,
        }); err != nil {
            return fmt.Errorf("wallet.Release: update: %w", err)
        }

        return nil
    })
}
```

## Post (direct, with grace-cap check)

```go
// internal/wallet/post.go
package wallet

import (
    "context"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
    "github.com/pikshipp/pikshipp/internal/policy"
)

func (s *serviceImpl) Post(ctx context.Context, sellerID core.SellerID, post LedgerPost) (core.LedgerEntryID, error) {
    if post.Amount <= 0 {
        return core.LedgerEntryID{}, ErrInvalidAmount
    }
    if post.Direction != DirectionDebit && post.Direction != DirectionCredit {
        return core.LedgerEntryID{}, fmt.Errorf("wallet.Post: invalid direction: %w", core.ErrInvalidArgument)
    }
    if post.Ref.Type == "" || post.Ref.ID == "" {
        return core.LedgerEntryID{}, fmt.Errorf("wallet.Post: empty ref: %w", core.ErrInvalidArgument)
    }
    if post.Ref.Type == RefManualAdjustment && post.Reason == "" {
        return core.LedgerEntryID{}, fmt.Errorf("wallet.Post: ManualAdjustment requires reason: %w", core.ErrInvalidArgument)
    }

    var entryID core.LedgerEntryID
    var bumpEvent bool

    err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.repo.queriesWith(tx)

        wa, err := q.GetWalletForUpdate(ctx, sellerID)
        if err != nil {
            return fmt.Errorf("wallet.Post: get wallet: %w", err)
        }

        if AccountStatus(wa.Status) == StatusWoundDown {
            return ErrAccountWoundDown
        }
        // Frozen accepts credits but rejects debits (refunds OK; charges blocked).
        if AccountStatus(wa.Status) == StatusFrozen && post.Direction == DirectionDebit {
            return ErrAccountFrozen
        }

        // Grace-cap check for debits
        if post.Direction == DirectionDebit {
            // post-debit balance must be >= -(grace + credit_limit)
            postBalance := core.Paise(wa.BalanceMinor) - post.Amount
            floor := -(core.Paise(wa.GraceNegativeAmountMinor) + core.Paise(wa.CreditLimitMinor))
            if postBalance < floor {
                // Grace cap breached. Emit suspension signal; reject post.
                bumpEvent = true
                return ErrGraceCapBreached
            }
        }

        // Idempotent insert
        actor, _ := actorRefFromCtx(ctx)
        if post.ActorRef != "" { actor = post.ActorRef }
        ledgerID := uuid.New()
        gotID, err := q.InsertLedgerEntry(ctx, db.InsertLedgerEntryParams{
            ID:          ledgerID,
            SellerID:    uuid.UUID(sellerID),
            WalletID:    wa.ID,
            Direction:   string(post.Direction),
            AmountMinor: int64(post.Amount),
            RefType:     string(post.Ref.Type),
            RefID:       post.Ref.ID,
            Reason:      post.Reason,
            ActorRef:    actor,
        })
        if err != nil {
            return fmt.Errorf("wallet.Post: insert ledger: %w", err)
        }
        if gotID == uuid.Nil {
            existing, _ := q.GetLedgerEntryByRef(ctx, db.GetLedgerEntryByRefParams{
                RefType:   string(post.Ref.Type),
                RefID:     post.Ref.ID,
                Direction: string(post.Direction),
            })
            entryID = core.LedgerEntryID(existing.ID)
            return nil  // idempotent: already posted; no balance update
        }
        entryID = core.LedgerEntryID(gotID)

        // Update cached balance
        delta := int64(post.Amount)
        if post.Direction == DirectionDebit { delta = -delta }
        if err := q.UpdateWalletBalance(ctx, db.UpdateWalletBalanceParams{
            ID:                  wa.ID,
            BalanceMinorDelta:   delta,
            HoldTotalMinorDelta: 0,
        }); err != nil {
            return fmt.Errorf("wallet.Post: update balance: %w", err)
        }

        // Audit (sync; high-value)
        action := "wallet.charged"
        if post.Direction == DirectionCredit { action = "wallet.credited" }
        if post.Ref.Type == RefManualAdjustment { action = "wallet.adjusted" }

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: &sellerID,
            Action:   action,
            Target:   audit.Target{Kind: "wallet_account", Ref: wa.ID.String()},
            Payload: map[string]any{
                "ledger_entry_id": entryID,
                "amount_minor":    int64(post.Amount),
                "direction":       string(post.Direction),
                "ref_type":        post.Ref.Type,
                "ref_id":          post.Ref.ID,
                "reason":          post.Reason,
            },
        }); err != nil {
            return err
        }

        // Outbox events (async)
        switch post.Ref.Type {
        case RefRecharge:
            if err := s.outbox.Emit(ctx, tx, outbox.Event{
                SellerID: &sellerID,
                Kind:     "wallet.recharged",
                Payload:  marshalRechargedPayload(sellerID, wa.ID, post.Amount, entryID),
            }); err != nil {
                return err
            }
        case RefRefund, RefWeightDisputeReversal:
            // similar emit
        }

        return nil
    })

    if errors.Is(err, ErrGraceCapBreached) {
        // Emit suspension signal — bypass tx (we don't want to commit anything from this attempt)
        s.emitGraceCapBreachAlert(ctx, sellerID, post.Amount)
    }
    return entryID, err
}

// emitGraceCapBreachAlert: best-effort, async. Triggers seller suspension downstream.
func (s *serviceImpl) emitGraceCapBreachAlert(ctx context.Context, sellerID core.SellerID, attempted core.Paise) {
    s.outbox.Emit(ctx, /* needs separate tx */ nil, outbox.Event{
        SellerID: &sellerID,
        Kind:     "wallet.grace_cap_breached",
        Payload:  marshalGraceBreachPayload(sellerID, attempted),
    })
    s.log.ErrorContext(ctx, "wallet grace cap breached",
        slog.String("seller_id", sellerID.String()),
        slog.Int64("attempted_amount_minor", int64(attempted)))
}
```

## Balance & Statement

```go
// internal/wallet/balance.go
package wallet

import (
    "context"
    "fmt"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

func (s *serviceImpl) Balance(ctx context.Context, sellerID core.SellerID) (Balance, error) {
    var b Balance
    err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        wa, err := s.repo.queriesWith(tx).GetWallet(ctx, sellerID)
        if err != nil {
            return fmt.Errorf("wallet.Balance: %w", err)
        }
        b = Balance{
            SellerID:                 sellerID,
            BalanceMinor:             core.Paise(wa.BalanceMinor),
            HoldTotalMinor:           core.Paise(wa.HoldTotalMinor),
            CreditLimitMinor:         core.Paise(wa.CreditLimitMinor),
            GraceNegativeAmountMinor: core.Paise(wa.GraceNegativeAmountMinor),
            Status:                   AccountStatus(wa.Status),
            LastUpdated:              wa.UpdatedAt,
        }
        b.AvailableMinor = b.BalanceMinor + b.CreditLimitMinor - b.HoldTotalMinor
        return nil
    })
    return b, err
}
```

```go
// internal/wallet/statement.go
package wallet

func (s *serviceImpl) Statement(ctx context.Context, sellerID core.SellerID, q StatementQuery) (StatementResult, error) {
    if q.Limit <= 0 || q.Limit > 100 { q.Limit = 50 }
    if q.From.IsZero() { q.From = s.clock.Now().Add(-30 * 24 * time.Hour) }
    if q.To.IsZero() { q.To = s.clock.Now() }

    var rows []db.ListLedgerEntriesRow
    err := dbtx.WithReadOnlyTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        params := buildStatementQueryParams(sellerID, q)
        var err error
        rows, err = s.repo.queriesWith(tx).ListLedgerEntries(ctx, params)
        return err
    })
    if err != nil {
        return StatementResult{}, fmt.Errorf("wallet.Statement: %w", err)
    }

    hasMore := len(rows) > q.Limit
    if hasMore { rows = rows[:q.Limit] }

    entries := make([]LedgerEntry, len(rows))
    var nextCursor *core.LedgerEntryID
    for i, r := range rows {
        entries[i] = LedgerEntry{
            ID:          core.LedgerEntryID(r.ID),
            SellerID:    core.SellerID(r.SellerID),
            Direction:   Direction(r.Direction),
            AmountMinor: core.Paise(r.AmountMinor),
            Ref:         Ref{Type: RefType(r.RefType), ID: r.RefID},
            ReversesID:  ledgerEntryIDPtr(r.ReversesID),
            Reason:      r.Reason.String,
            ActorRef:    r.ActorRef,
            PostedAt:    r.PostedAt,
        }
        if i == len(rows)-1 && hasMore {
            id := core.LedgerEntryID(r.ID)
            nextCursor = &id
        }
    }

    return StatementResult{Entries: entries, HasMore: hasMore, NextCursor: nextCursor}, nil
}
```

## Hold expiry sweeper

```go
// internal/wallet/hold_expiry.go
package wallet

import (
    "context"

    "github.com/google/uuid"
    "github.com/riverqueue/river"

    "github.com/pikshipp/pikshipp/internal/observability/dbtx"
)

type ExpireHoldsArgs struct{}

func (ExpireHoldsArgs) Kind() string { return "wallet.expire_holds" }

type ExpireHoldsWorker struct {
    river.WorkerDefaults[ExpireHoldsArgs]
    svc Service
    pool *pgxpool.Pool
    log  *slog.Logger
}

// Work claims expired-but-active holds in batches and releases them.
//
// Schedule: every minute via river PeriodicJobs.
func (w *ExpireHoldsWorker) Work(ctx context.Context, j *river.Job[ExpireHoldsArgs]) error {
    const batch = 100

    for {
        var ids []HoldID
        err := dbtx.WithAdminTx(ctx, w.pool, func(ctx context.Context, tx pgx.Tx) error {
            q := db.New(tx).WithTx(tx)
            rows, err := q.ExpireOldHolds(ctx, batch)
            if err != nil { return err }

            for _, r := range rows {
                ids = append(ids, HoldID(r.ID))
            }
            return nil
        })
        if err != nil { return fmt.Errorf("expire_holds: %w", err) }

        if len(ids) == 0 { return nil }

        for _, id := range ids {
            if err := w.svc.Release(ctx, id); err != nil {
                w.log.WarnContext(ctx, "expire_holds release failed",
                    slog.String("hold_id", id.String()),
                    slog.Any("error", err))
            }
        }
    }
}
```

## Daily invariant check

```go
// internal/wallet/invariant.go
package wallet

import (
    "context"

    "github.com/riverqueue/river"
)

type InvariantCheckArgs struct{}

func (InvariantCheckArgs) Kind() string { return "wallet.invariant_check" }

type InvariantCheckWorker struct {
    river.WorkerDefaults[InvariantCheckArgs]
    pool *pgxpool.Pool
    audit audit.Emitter
    log  *slog.Logger
}

// Work runs the cross-seller invariant check using BYPASSRLS pool.
//
// Schedule: daily at 02:00 IST via river PeriodicJobs.
//
// Runs as admin role; reports findings; alerts on any non-zero diff.
func (w *InvariantCheckWorker) Work(ctx context.Context, j *river.Job[InvariantCheckArgs]) error {
    rows, err := db.New(w.pool).WalletInvariantCheck(ctx)
    if err != nil { return fmt.Errorf("invariant_check: %w", err) }

    var failures int
    for _, r := range rows {
        diff := r.ComputedMinor - r.CachedMinor

        // Always insert a result row
        if err := db.New(w.pool).InsertInvariantCheckResult(ctx, db.InsertInvariantCheckResultParams{
            SellerID:      r.SellerID,
            WalletID:      r.WalletID,
            ComputedMinor: r.ComputedMinor,
            CachedMinor:   r.CachedMinor,
            DiffMinor:     diff,
        }); err != nil {
            w.log.ErrorContext(ctx, "insert invariant result failed", slog.Any("error", err))
        }

        if diff != 0 {
            failures++
            sid := core.SellerID(r.SellerID)
            // Async audit; this is platform-level, but per-seller for traceability
            w.audit.EmitAsync(ctx, audit.Event{
                SellerID: &sid,
                Action:   "wallet.invariant_check_failed",
                Target:   audit.Target{Kind: "wallet_account", Ref: r.WalletID.String()},
                Payload: map[string]any{
                    "computed_minor": r.ComputedMinor,
                    "cached_minor":   r.CachedMinor,
                    "diff_minor":     diff,
                },
            })
            w.log.ErrorContext(ctx, "wallet invariant violation",
                slog.String("seller_id", sid.String()),
                slog.Int64("computed", r.ComputedMinor),
                slog.Int64("cached", r.CachedMinor),
                slog.Int64("diff", diff))
        }
    }

    if failures > 0 {
        return fmt.Errorf("wallet invariant: %d failures", failures)  // dead-letter; ops investigates
    }
    w.log.InfoContext(ctx, "wallet invariant check passed", slog.Int("wallets", len(rows)))
    return nil
}
```

## Reverse-leg charging

```go
// internal/wallet/reverse_leg.go
package wallet

import (
    "context"
    "fmt"

    "github.com/pikshipp/pikshipp/internal/core"
)

// ChargeRTO posts an RTO charge against the seller. Idempotent on shipment_id.
//
// On grace-cap breach, the post fails and the wallet.grace_cap_breached event
// is emitted; downstream consumer (seller service) suspends the seller and
// queues this charge for retry on recharge.
//
// Returns the LedgerEntryID, or ErrGraceCapBreached.
func (s *serviceImpl) ChargeRTO(ctx context.Context, sellerID core.SellerID, shipmentID core.ShipmentID, amount core.Paise) (core.LedgerEntryID, error) {
    return s.Post(ctx, sellerID, LedgerPost{
        Direction: DirectionDebit,
        Amount:    amount,
        Ref:       Ref{Type: RefRTOCharge, ID: shipmentID.String()},
        Reason:    "RTO charge from carrier",
        ActorRef:  "system:wallet.charge_rto",
    })
}
```

## Test patterns

### SLT — concurrent reservations

```go
func TestReserve_ConcurrentDoesntDoubleSpend_SLT(t *testing.T) {
    p := testdb.New(t)
    clock := core.NewFakeClock(time.Now())
    svc := setupWalletSvc(t, p.App, clock)

    sid := core.NewSellerID()
    seedWallet(t, p.App, sid, balance(10_000), creditLimit(0))  // ₹100

    // Try to reserve ₹60 ten times in parallel; only 1 should succeed
    var wg sync.WaitGroup
    succeeded := atomic.Int32{}
    failed := atomic.Int32{}

    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := svc.Reserve(context.Background(), sid, paise(6000), 1*time.Minute)
            if err == nil {
                succeeded.Add(1)
            } else if errors.Is(err, wallet.ErrInsufficientFunds) {
                failed.Add(1)
            }
        }()
    }
    wg.Wait()

    require.Equal(t, int32(1), succeeded.Load(), "exactly one Reserve should win")
    require.Equal(t, int32(9), failed.Load(), "the rest should see ErrInsufficientFunds")

    bal, _ := svc.Balance(context.Background(), sid)
    require.Equal(t, paise(10_000), bal.BalanceMinor)
    require.Equal(t, paise(6_000), bal.HoldTotalMinor)
    require.Equal(t, paise(4_000), bal.AvailableMinor)
}
```

### SLT — Confirm idempotency

```go
func TestConfirm_Idempotent_SLT(t *testing.T) {
    svc := setupWalletSvc(t, ...)

    sid := core.NewSellerID()
    seedWallet(t, ..., sid, balance(100_000), 0)

    holdID, _ := svc.Reserve(ctx, sid, paise(5000), 1*time.Minute)
    ref := wallet.Ref{Type: wallet.RefShipmentCharge, ID: "S-001"}

    id1, err := svc.Confirm(ctx, holdID, ref)
    require.NoError(t, err)

    // Second Confirm with same ref returns same ID; no double-charge
    id2, err := svc.Confirm(ctx, holdID, ref)
    require.NoError(t, err)
    require.Equal(t, id1, id2)

    bal, _ := svc.Balance(ctx, sid)
    require.Equal(t, paise(95_000), bal.BalanceMinor)
}
```

### SLT — grace cap

```go
func TestPost_GraceCapBreach_SLT(t *testing.T) {
    svc := setupWalletSvc(t, ...)

    sid := core.NewSellerID()
    seedWallet(t, ..., sid, balance(100), 0, graceCap(500))  // ₹1, ₹0 credit, ₹5 grace

    // ₹6 RTO charge — would push balance to -₹5; that's exactly grace; allowed.
    _, err := svc.Post(ctx, sid, wallet.LedgerPost{
        Direction: wallet.DirectionDebit,
        Amount:    paise(600),
        Ref:       wallet.Ref{Type: wallet.RefRTOCharge, ID: "S1"},
    })
    require.NoError(t, err)

    bal, _ := svc.Balance(ctx, sid)
    require.Equal(t, paise(-500), bal.BalanceMinor)

    // Another ₹100 — would push to -₹6; exceeds grace.
    _, err = svc.Post(ctx, sid, wallet.LedgerPost{
        Direction: wallet.DirectionDebit,
        Amount:    paise(100),
        Ref:       wallet.Ref{Type: wallet.RefRTOCharge, ID: "S2"},
    })
    require.ErrorIs(t, err, wallet.ErrGraceCapBreached)
}
```

### SLT — invariant check detects drift

```go
func TestInvariantCheck_DetectsDrift_SLT(t *testing.T) {
    svc := setupWalletSvc(t, ...)

    sid := core.NewSellerID()
    seedWallet(t, ..., sid, balance(10_000), 0)

    // Manually corrupt the cache (simulating bug)
    p.App.Exec(ctx, "UPDATE wallet_account SET balance_minor = 99999 WHERE seller_id = $1", sid.String())

    worker := &wallet.InvariantCheckWorker{...}
    err := worker.Work(ctx, &river.Job[wallet.InvariantCheckArgs]{})
    require.Error(t, err)  // expected; reports failures

    // Audit event emitted
    requireAuditEvent(t, p.App, sid, "wallet.invariant_check_failed")
}
```

### Bench

```go
func BenchmarkReserveConfirm(b *testing.B) {
    p := testdb.New(b)
    svc := setupWalletSvc(b, p.App, core.SystemClock{})
    sid := core.NewSellerID()
    seedWallet(b, p.App, sid, balance(b.N*100), 0)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        holdID, _ := svc.Reserve(context.Background(), sid, paise(50), 1*time.Minute)
        svc.Confirm(context.Background(), holdID, wallet.Ref{
            Type: wallet.RefShipmentCharge,
            ID:   fmt.Sprintf("S-%d", i),
        })
    }
}

func BenchmarkPostCredit(b *testing.B) {
    // ... setup ...
    for i := 0; i < b.N; i++ {
        svc.Post(context.Background(), sid, wallet.LedgerPost{
            Direction: wallet.DirectionCredit,
            Amount:    paise(100),
            Ref:       wallet.Ref{Type: wallet.RefRecharge, ID: fmt.Sprintf("R-%d", i)},
        })
    }
}
```

Targets: `BenchmarkReserveConfirm` < 30ms/op (P95); `BenchmarkPostCredit` < 15ms/op.

## Performance

- `Reserve`: 1 SELECT FOR UPDATE + 1 INSERT + 1 UPDATE = ~10ms.
- `Confirm`: 1 SELECT FOR UPDATE (hold) + 1 INSERT (ledger; idempotent) + 1 UPDATE (hold resolve) + 1 UPDATE (wallet balance) + 1 audit emit = ~20ms.
- `Release`: 1 SELECT + 2 UPDATEs = ~10ms.
- `Post`: 1 SELECT FOR UPDATE + 1 INSERT + 1 UPDATE + 1 audit = ~20ms.
- `Balance`: 1 SELECT (no lock) = ~5ms.
- `Statement(50 rows)`: 1 SELECT with index = ~10ms.

The benchmark gate is documented in HLD and in ADR; if `Reserve+Confirm` P95 > 30ms at v1 traffic, we revisit (sharding, caching, etc.).

## Failure modes

| Failure | Behavior |
|---|---|
| Concurrent Reserves on same wallet | `FOR UPDATE` serializes; first wins, others see updated `hold_total` and either succeed (if room) or fail with `ErrInsufficientFunds` |
| Confirm with expired hold | `ErrHoldExpired`; caller must Reserve again or post directly |
| Confirm of already-confirmed hold | Idempotent — returns same `LedgerEntryID` |
| Hold expires before Confirm | Sweeper releases; Confirm fails with `ErrHoldNotFound` |
| Concurrent Confirm of same hold | `FOR UPDATE` serializes; first wins; second sees `confirmed` status; idempotent |
| Concurrent Post with same Ref | UNIQUE constraint; one INSERT, the other returns existing |
| Grace-cap breach | `ErrGraceCapBreached` returned; `wallet.grace_cap_breached` outbox event; downstream suspends seller |
| Invariant check fails | P0 alert; audit event; ops investigates; manual reconciliation |
| `audit.Emit` fails inside tx | Tx rolls back — money operation does not commit |
| Outbox emit fails after wallet update | Whole tx rolls back; wallet update is undone |

## Open questions

- **Per-second wallet ops at festive peak**: 50 ops/sec sustained for one seller is conceivable. Benchmark gate is at v1; if breached, options:
  - Partition wallet rows by seller (already are; one row per seller).
  - Move to event-sourced ledger: balance is a projection; FOR UPDATE goes away.
  - Batch operations.
- **Multi-currency** (international expansion): not v0/v1; would require currency on every entry.
- **Auto-recharge implementation**: deferred to v1; trigger on low-balance threshold.
- **Negative-balance interest** (for credit-line customers): not v0/v1; possibly v2 if commercials demand.

## Configuration knobs (from policy engine)

```go
// internal/wallet/policy_keys.go
package wallet

import "github.com/pikshipp/pikshipp/internal/policy"

const (
    KeyCreditLimit          = policy.KeyWalletCreditLimitInr
    KeyGraceNegativeAmount  = policy.KeyWalletGraceNegativeAmount
    KeyPosture              = policy.KeyWalletPosture
)
```

The constructor reads these on startup AND we re-read on each operation (cached via policy engine; cheap). Wallet account columns (`credit_limit_minor`, `grace_negative_amount_minor`) are kept in sync with the policy engine via a daily reconcile job (deferred — v0 syncs on create only; ops manually updates if needed).

## References

- HLD `03-services/04-wallet-and-ledger.md`.
- HLD `04-cross-cutting/05-resilience.md` (idempotency, transactions).
- ADR 0006 (booking two-tx flow that Wallet participates in).
- LLD `01-core/01-money.md` (Paise type).
- LLD `02-infrastructure/01-database-access.md` (RLS, transactions).
