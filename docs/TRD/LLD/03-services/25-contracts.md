# Contracts Service

## Purpose

The contracts service owns **per-seller commercial agreements** with Pikshipp: rate cards, billing terms, COD remittance frequency, top-up commitments, special pricing arrangements. It binds the legal/commercial relationship to the operational system so that pricing, COD settlement, and reports use the right terms.

Most v0 sellers will be on a single **default contract** (the platform's standard offer). Enterprise sellers and friendly-seller bypass paths use **negotiated contracts** with custom rate cards and remittance schedules.

Responsibilities:

- Persist contract documents (text + metadata + signed PDF reference).
- Track contract lifecycle (`draft → active → expired | terminated`).
- Bind a contract to a rate-card revision so commercial terms are unambiguous at any point in time.
- Provide `GetActiveContract(seller_id)` for pricing/COD/reports.
- Maintain version history (every change creates a new version; old ones are retained for audit/disputes).

Out of scope:

- Rate-card content modeling — pricing (LLD §03-services/06).
- Digital signature / e-sign integration — out of scope for v0; sellers e-sign externally and we record the PDF.
- Subscription billing — Pikshipp doesn't charge sellers a subscription at v0.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors, money. |
| `internal/db` | persistence. |
| `internal/seller` | active seller check. |
| `internal/pricing` | rate-card binding. |
| `internal/audit`, `internal/outbox` | every change audited. |
| `internal/objstore` | signed-PDF refs. |

## Package Layout

```
internal/contracts/
├── service.go             // Service interface
├── service_impl.go
├── repo.go
├── types.go               // Contract, Version, Term
├── lifecycle.go
├── jobs.go                // ExpiryAlertJob, AutoActivateJob
├── errors.go
├── events.go
└── service_test.go
```

## Public API

```go
package contracts

type Service interface {
    // Create a draft contract. Operator-only; sellers don't create their own.
    CreateDraft(ctx context.Context, req CreateDraftRequest) (*Contract, error)

    // UpdateDraft mutates a draft (terms, dates, rate_card_id).
    UpdateDraft(ctx context.Context, req UpdateDraftRequest) (*Contract, error)

    // Activate moves draft → active. If the seller already has an active
    // contract, that one is moved to `superseded` simultaneously.
    Activate(ctx context.Context, req ActivateRequest) (*Contract, error)

    // Terminate moves active → terminated.
    Terminate(ctx context.Context, req TerminateRequest) error

    // GetActive returns the currently active contract for a seller.
    // Used by pricing, COD, recon to resolve commercial terms.
    GetActive(ctx context.Context, sellerID core.SellerID) (*Contract, error)

    // GetAtTime returns the contract that was active at a specific point
    // in time (for reports / disputes).
    GetAtTime(ctx context.Context, sellerID core.SellerID, when time.Time) (*Contract, error)

    // List all contracts for a seller (audit purposes).
    List(ctx context.Context, sellerID core.SellerID) ([]*Contract, error)

    // AttachSignedPDF records that the seller has e-signed the contract.
    AttachSignedPDF(ctx context.Context, req AttachSignedPDFRequest) error
}
```

### Types

```go
type ContractState string

const (
    StateDraft       ContractState = "draft"
    StateActive      ContractState = "active"
    StateSuperseded  ContractState = "superseded"
    StateTerminated  ContractState = "terminated"
)

type Contract struct {
    ID            core.ContractID
    SellerID      core.SellerID
    State         ContractState
    Version       int                    // monotonic per seller
    Terms         Terms                   // structured terms (see below)
    RateCardID    core.RateCardID         // bound rate card
    EffectiveFrom time.Time
    EffectiveTo   *time.Time              // null = open-ended
    SignedPDFKey  string                  // S3 key
    SignedAt      *time.Time
    CreatedBy     core.UserID
    CreatedAt     time.Time
    ActivatedAt   *time.Time
    TerminatedAt  *time.Time
    TerminationReason string
}

type Terms struct {
    // Commercial
    BillingCycle           string         // "weekly" | "biweekly" | "monthly"
    PaymentTerms           string         // "prepaid" | "net7" | "net15" | "net30"
    CODRemittanceFrequency string         // "weekly" | "biweekly" | "monthly"
    CODHoldbackDays        int             // post-delivery days before remit

    // Pricing
    BaseDiscountPercent    float64        // applied on top of rate-card public price
    MinMonthlyShipments    int             // SLA: seller commits to N/mo
    PenaltyBelowMin        bool

    // Operational
    AllowedCarriers        []string       // empty = all
    ExcludedCarriers       []string

    // Misc
    CustomNotes            string
}
```

### Sentinel Errors

```go
var (
    ErrNotFound            = errors.New("contracts: not found")
    ErrInvalidState        = errors.New("contracts: invalid state for operation")
    ErrNoActive            = errors.New("contracts: no active contract for seller")
    ErrSignedPDFRequired   = errors.New("contracts: signed PDF required to activate")
    ErrEffectiveDateConflict = errors.New("contracts: effective date overlaps existing active contract")
)
```

## DB Schema

```sql
CREATE TABLE contract (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id          uuid        NOT NULL REFERENCES seller(id),
    version            integer     NOT NULL,
    state              text        NOT NULL CHECK (state IN ('draft','active','superseded','terminated')),

    rate_card_id       uuid        REFERENCES rate_card(id),
    terms              jsonb       NOT NULL,           -- Terms struct serialized

    effective_from     timestamptz NOT NULL,
    effective_to       timestamptz,

    signed_pdf_key     text,
    signed_at          timestamptz,

    created_by         uuid        NOT NULL REFERENCES app_user(id),
    activated_by       uuid        REFERENCES app_user(id),
    terminated_by      uuid        REFERENCES app_user(id),
    activated_at       timestamptz,
    terminated_at      timestamptz,
    termination_reason text,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    UNIQUE (seller_id, version)
);

-- At most one active contract per seller at a time.
CREATE UNIQUE INDEX contract_one_active_per_seller
    ON contract(seller_id) WHERE state = 'active';

CREATE INDEX contract_seller_state_idx ON contract(seller_id, state);

-- RLS: sellers see all their own contracts; operators bypass.
ALTER TABLE contract ENABLE ROW LEVEL SECURITY;
CREATE POLICY contract_isolation ON contract
    USING (seller_id = current_setting('app.seller_id')::uuid);

GRANT SELECT, INSERT, UPDATE ON contract TO pikshipp_app;
GRANT SELECT ON contract TO pikshipp_reports;
```

The `effective_from` / `effective_to` pair lets `GetAtTime` answer "what contract governed this shipment at the time it was booked?" — vital for dispute resolution months later.

## sqlc Queries

```sql
-- name: ContractInsertDraft :one
INSERT INTO contract (
    id, seller_id, version, state, rate_card_id, terms,
    effective_from, effective_to, created_by
) VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7, $8)
RETURNING *;

-- name: ContractUpdateDraft :one
UPDATE contract
SET rate_card_id = COALESCE(sqlc.narg('rate_card_id'), rate_card_id),
    terms        = COALESCE(sqlc.narg('terms'), terms),
    effective_from = COALESCE(sqlc.narg('effective_from'), effective_from),
    effective_to   = COALESCE(sqlc.narg('effective_to'), effective_to),
    updated_at = now()
WHERE id = $1 AND state = 'draft'
RETURNING *;

-- name: ContractActivate :one
UPDATE contract
SET state = 'active', activated_by = $2, activated_at = now(), updated_at = now()
WHERE id = $1 AND state = 'draft'
RETURNING *;

-- name: ContractSupersede :exec
UPDATE contract
SET state = 'superseded', updated_at = now(),
    effective_to = COALESCE(effective_to, $2)
WHERE seller_id = $1 AND state = 'active' AND id <> $3;

-- name: ContractTerminate :one
UPDATE contract
SET state = 'terminated',
    terminated_by = $2,
    terminated_at = now(),
    termination_reason = $3,
    effective_to = COALESCE(effective_to, now()),
    updated_at = now()
WHERE id = $1 AND state = 'active'
RETURNING *;

-- name: ContractGetActive :one
SELECT * FROM contract WHERE seller_id = $1 AND state = 'active' LIMIT 1;

-- name: ContractGetAtTime :one
SELECT * FROM contract
WHERE seller_id = $1
  AND effective_from <= $2
  AND (effective_to IS NULL OR effective_to > $2)
ORDER BY effective_from DESC
LIMIT 1;

-- name: ContractListBySeller :many
SELECT * FROM contract WHERE seller_id = $1 ORDER BY version DESC;

-- name: ContractAttachSignedPDF :exec
UPDATE contract
SET signed_pdf_key = $2, signed_at = now(), updated_at = now()
WHERE id = $1;

-- name: ContractMaxVersion :one
SELECT COALESCE(MAX(version), 0) AS max_version FROM contract WHERE seller_id = $1;
```

## Implementation

### CreateDraft

```go
func (s *service) CreateDraft(ctx context.Context, req CreateDraftRequest) (*Contract, error) {
    if err := validateTerms(req.Terms); err != nil {
        return nil, err
    }
    var out *Contract
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        max, err := qtx.ContractMaxVersion(ctx, req.SellerID.UUID())
        if err != nil {
            return err
        }
        nextVersion := int(max) + 1
        row, err := qtx.ContractInsertDraft(ctx, sqlcgen.ContractInsertDraftParams{
            ID:           core.NewContractID().UUID(),
            SellerID:     req.SellerID.UUID(),
            Version:      int32(nextVersion),
            RateCardID:   pgxNullUUID(req.RateCardID),
            Terms:        jsonbFrom(req.Terms),
            EffectiveFrom: req.EffectiveFrom,
            EffectiveTo:   pgxNullTimestampPtr(req.EffectiveTo),
            CreatedBy:    req.OperatorID.UUID(),
        })
        if err != nil {
            return err
        }
        out = contractFromRow(row)
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: req.SellerID,
            Actor:    audit.ActorUser(req.OperatorID),
            Action:   "contract.draft.created",
            Object:   audit.ObjContract(out.ID),
            Payload:  map[string]any{"version": nextVersion, "rate_card_id": req.RateCardID},
        })
    })
    return out, err
}
```

### Activate

```go
func (s *service) Activate(ctx context.Context, req ActivateRequest) (*Contract, error) {
    var out *Contract
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.ContractGetByID(ctx, req.ContractID.UUID())
        if err != nil {
            return ErrNotFound
        }
        if ContractState(cur.State) != StateDraft {
            return fmt.Errorf("%w: state=%s", ErrInvalidState, cur.State)
        }
        if !cur.SignedPDFKey.Valid || cur.SignedPDFKey.String == "" {
            return ErrSignedPDFRequired
        }

        // Supersede current active first
        if err := qtx.ContractSupersede(ctx, sqlcgen.ContractSupersedeParams{
            SellerID:    cur.SellerID,
            EffectiveTo: cur.EffectiveFrom,
            ID:          cur.ID,
        }); err != nil {
            return err
        }
        // Activate new
        row, err := qtx.ContractActivate(ctx, sqlcgen.ContractActivateParams{
            ID:          req.ContractID.UUID(),
            ActivatedBy: req.OperatorID.UUID(),
        })
        if err != nil {
            return err
        }
        out = contractFromRow(row)

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(cur.SellerID),
            Actor:    audit.ActorUser(req.OperatorID),
            Action:   "contract.activated",
            Object:   audit.ObjContract(out.ID),
            Payload:  map[string]any{"version": out.Version},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind: "contract.activated",
            Key:  string(out.ID),
            Payload: map[string]any{"contract_id": out.ID, "seller_id": cur.SellerID, "version": out.Version},
        })
    })
    return out, err
}
```

### GetActive (with cache)

`GetActive` is on the hot path of pricing; we cache per seller.

```go
type activeCache struct {
    mu      sync.RWMutex
    entries map[core.SellerID]activeCacheEntry
    ttl     time.Duration
}

type activeCacheEntry struct {
    contract  *Contract
    fetchedAt time.Time
}

func (s *service) GetActive(ctx context.Context, sellerID core.SellerID) (*Contract, error) {
    s.cache.mu.RLock()
    e, ok := s.cache.entries[sellerID]
    s.cache.mu.RUnlock()
    if ok && time.Since(e.fetchedAt) < s.cache.ttl {
        return e.contract, nil
    }
    row, err := s.q.ContractGetActive(ctx, sellerID.UUID())
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNoActive
        }
        return nil, err
    }
    c := contractFromRow(row)
    s.cache.mu.Lock()
    s.cache.entries[sellerID] = activeCacheEntry{contract: c, fetchedAt: time.Now()}
    s.cache.mu.Unlock()
    return c, nil
}
```

The cache invalidates via `LISTEN contract_active_changed` (notification emitted from outbox forwarder when `contract.activated` events fire).

### Validation

```go
func validateTerms(t Terms) error {
    switch t.BillingCycle {
    case "weekly", "biweekly", "monthly":
    default:
        return fmt.Errorf("contracts: invalid billing_cycle %q", t.BillingCycle)
    }
    switch t.PaymentTerms {
    case "prepaid", "net7", "net15", "net30":
    default:
        return fmt.Errorf("contracts: invalid payment_terms %q", t.PaymentTerms)
    }
    switch t.CODRemittanceFrequency {
    case "weekly", "biweekly", "monthly":
    default:
        return fmt.Errorf("contracts: invalid cod_remittance_frequency %q", t.CODRemittanceFrequency)
    }
    if t.BaseDiscountPercent < 0 || t.BaseDiscountPercent > 50 {
        return fmt.Errorf("contracts: base_discount_percent must be 0..50")
    }
    if t.CODHoldbackDays < 0 || t.CODHoldbackDays > 30 {
        return fmt.Errorf("contracts: cod_holdback_days must be 0..30")
    }
    return nil
}
```

## Outbox Routing

- `contract.activated` → `pricing.InvalidateRateCardCacheJob`, `cod.UpdateRemittanceScheduleJob`
- `contract.terminated` → `notifications.SellerOpsAlertJob`, `seller.OnContractTerminatedJob`
- `contract.draft.created` → `notifications.OperatorReviewNudgeJob`

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `GetActive` (cache hit) | 100 ns | 300 ns | 0 allocs |
| `GetActive` (cache miss) | 1.5 ms | 4 ms | 1 SELECT |
| `CreateDraft` | 6 ms | 18 ms | INSERT + audit |
| `Activate` | 10 ms | 30 ms | UPDATE × 2 + audit + outbox |
| `Terminate` | 6 ms | 18 ms | UPDATE + audit + outbox |
| `GetAtTime` | 2 ms | 6 ms | indexed seek |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Activate without signed PDF | service-level check | `ErrSignedPDFRequired`. |
| Activate while another active exists | partial unique index `contract_one_active_per_seller` | Within tx we explicitly Supersede first; if the constraint still fires, surface error. |
| Effective dates overlap | application-level check at activate time | Return `ErrEffectiveDateConflict` if a future-dated active contract would conflict. |
| Cache stale after Terminate | LISTEN/NOTIFY + 60s TTL | Worst case: 60s of pricing using superseded contract; tracking-rate-card pricing logic is forgiving. |
| Pricing reads contract that was wound down with seller | `seller.LifecycleCache` already gates writes; reads still resolve | Acceptable. |

## Testing

```go
func TestCreateDraft_VersionIncrements_SLT(t *testing.T) { /* v1, v2, ... */ }
func TestActivate_SupersedesPrevious_SLT(t *testing.T) { /* ... */ }
func TestActivate_RequiresSignedPDF_SLT(t *testing.T) { /* ... */ }
func TestGetAtTime_HistoricalCorrectness_SLT(t *testing.T) {
    // Three contract versions across time; query for a specific point
    // returns the right one.
}
func TestTerminate_TransitionsAndAudits_SLT(t *testing.T) { /* ... */ }
func TestCacheInvalidation_OnActivate_SLT(t *testing.T) { /* ... */ }
func TestRLS_ContractIsolation_SLT(t *testing.T) { /* ... */ }
func TestValidateTerms_AllFields(t *testing.T) { /* ... */ }
```

## Open Questions

1. **e-Signature integration.** Sellers e-sign externally (DocuSign, etc.) and we record the PDF key. **Decision:** keep this minimal for v0; if we later integrate an e-sign API, it lands as an adapter.
2. **Per-line-item pricing.** Some enterprise contracts have SKU-level pricing. **Decision:** out of scope; rate-card adjustments are the v0 mechanism.
3. **SLA contract terms.** Seller commits to N shipments/month with platform commits to X% delivery rate. **Decision:** record in `Terms`; don't auto-enforce SLA-breach penalties in v0.
4. **Bulk contract change.** Renegotiate terms across 1k sellers. **Decision:** out of scope; ops scripts.
5. **Effective-from-future drafts.** A contract that activates on Jan 1 of next year. **Decision:** supported via `effective_from` in the future; activation moves state but the active query checks `effective_from <= now()`. Add a daily `AutoActivateJob` to flip drafts at their effective date if pre-approved.

## References

- LLD §03-services/06-pricing: rate cards bound here.
- LLD §03-services/16-cod: COD remittance frequency from contract.
- LLD §03-services/02-audit: every change audited.
- LLD §03-services/19-reports: historical contracts via GetAtTime for disputes.
