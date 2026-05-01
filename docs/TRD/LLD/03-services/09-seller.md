# Seller Service

## Purpose

The seller service owns the **seller organization** — the merchant entity that ships through Pikshipp. Every domain row outside of platform-internal tables is scoped to exactly one seller. The service is responsible for:

- Provisioning new sellers (onboarding form → sandbox seller).
- Tracking lifecycle state (`provisioning → sandbox → active → suspended → wound_down`).
- Maintaining seller-level configuration that does not belong in the policy engine (legal name, GSTIN, billing email, support email, ops contacts).
- Owning the KYC application record (the **state machine**, not the document storage — that is the KYC adapter's job).
- Publishing `seller.lifecycle.changed` events that drive cache invalidation, gating, and downstream features.

It does **not** own:

- User membership, roles, or invites — those belong to identity (LLD §03-services/08).
- Wallet — wallet (LLD §03-services/05) creates the account on `seller.activated`.
- Policy values — policy engine (LLD §03-services/01) owns per-seller overrides.
- Catalog — catalog (LLD §03-services/11) owns pickup locations and products.

The seller table is the **referential anchor** for every seller-scoped row; deleting a seller is not supported (only soft wind-down).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | `core.Clock`, `core.Validator`, IDs, errors. |
| `internal/db` | `pgxpool.Pool`, `WithTx`, sqlc-generated `Queries`. |
| `internal/audit` | All lifecycle transitions emit audit events synchronously. |
| `internal/outbox` | Lifecycle changes emit outbox events for downstream consumers. |
| `internal/policy` | On `provisioning → sandbox`, seed seller-type defaults (no-op; policy engine resolves on-read). |
| `internal/identity` | On `provisioning`, the founding user is bound (caller passes `founding_user_id`). |

The seller service does **not** depend on wallet, catalog, carriers, or any feature service. The dependency edge is one-way: `seller → core/db/audit/outbox` only.

## Package Layout

```
internal/seller/
├── service.go            // Service interface + constructor
├── service_impl.go       // Implementation
├── repo.go               // sqlc wrapper + raw SQL helpers
├── types.go              // Seller, KYCApplication, LifecycleState, SellerType
├── lifecycle.go          // State-machine table (allowed transitions)
├── errors.go             // Sentinel errors
├── jobs.go               // River job: SellerLifecycleAuditJob, KYCExpiryJob
├── events.go             // Outbox event payloads
├── service_test.go       // Unit tests (with mock repo)
├── service_slt_test.go   // System-level tests (testcontainers postgres)
└── bench_test.go         // Microbenchmarks for hot reads
```

## Public API

```go
package seller

// Service is the public surface of the seller module.
//
// All methods are seller-scoped: callers must have already resolved
// the seller_id (via auth.Authenticator + middleware). The service
// does NOT perform authorization — that is the caller's responsibility.
type Service interface {
    // Provision creates a new seller in the `provisioning` state.
    // Idempotent on (founding_user_id, legal_name) within a 24h window
    // to absorb double-clicks on the onboarding form.
    Provision(ctx context.Context, req ProvisionRequest) (*Seller, error)

    // Get returns the seller by ID. Returns ErrNotFound if the seller
    // does not exist OR is `wound_down` (wound-down sellers are invisible
    // to operational reads; only the reports role can see them).
    Get(ctx context.Context, id core.SellerID) (*Seller, error)

    // GetForAdmin returns the seller including wound-down state.
    // ONLY callable from the admin role; enforced at the handler layer.
    GetForAdmin(ctx context.Context, id core.SellerID) (*Seller, error)

    // UpdateProfile mutates non-lifecycle fields (legal name, GSTIN,
    // billing email, support email, ops contacts). Emits an audit event.
    UpdateProfile(ctx context.Context, id core.SellerID, patch ProfilePatch) (*Seller, error)

    // SubmitKYC moves the application from `not_started → submitted`.
    // Requires legal_name and gstin to be set. The actual document
    // upload + verification is the KYC adapter's job; this method only
    // records the application metadata.
    SubmitKYC(ctx context.Context, id core.SellerID, app KYCApplication) error

    // ApproveKYC is called by the KYC adapter (or operator) when
    // verification succeeds. Triggers `sandbox → active` if the seller
    // is currently in sandbox.
    ApproveKYC(ctx context.Context, id core.SellerID, ref KYCRef) error

    // RejectKYC is called by the KYC adapter (or operator) on failure.
    // Records the reason; seller stays in sandbox until they re-submit.
    RejectKYC(ctx context.Context, id core.SellerID, reason string, ref KYCRef) error

    // Activate moves `sandbox → active`. Called automatically on
    // ApproveKYC; callable manually by operators (audited) for
    // friendly-seller bypass during v0.
    Activate(ctx context.Context, id core.SellerID, reason ActivationReason) error

    // Suspend moves `active → suspended`. Used for risk holds, payment
    // disputes, or operator action. While suspended:
    //   - new shipments are blocked
    //   - in-flight shipments continue (we do not abandon parcels)
    //   - wallet remains accessible for reconciliation
    Suspend(ctx context.Context, id core.SellerID, req SuspendRequest) error

    // Reinstate moves `suspended → active`.
    Reinstate(ctx context.Context, id core.SellerID, reason string) error

    // WindDown is the terminal state. Soft-deletes the seller for all
    // operational queries. Wallet must have zero balance and zero
    // open holds (validated; rejected otherwise). Irreversible.
    WindDown(ctx context.Context, id core.SellerID, reason string) error

    // ListByState is used by ops dashboards. Reports role only.
    ListByState(ctx context.Context, state LifecycleState, page Page) ([]*Seller, error)
}
```

### Request / Response Types

```go
type ProvisionRequest struct {
    FoundingUserID core.UserID
    LegalName      string        // company / proprietor legal name
    DisplayName    string        // shown to buyers; defaults to LegalName
    SellerType     SellerType    // small_business | mid_market | enterprise
    BillingEmail   string
    SupportEmail   string
    GSTIN          string        // optional at provision; required by SubmitKYC
    PrimaryPhone   string        // E.164
    SignupSource   string        // "web" | "ops_console" | "import"
}

type ProfilePatch struct {
    LegalName    *string
    DisplayName  *string
    BillingEmail *string
    SupportEmail *string
    OpsContacts  *[]OpsContact   // setter replaces full slice (not patch-per-element)
}

type OpsContact struct {
    Name  string
    Email string
    Phone string
    Role  string // "ops_lead" | "ndr_handler" | "finance" | ...
}

type SuspendRequest struct {
    Reason         string         // free-text; surfaces in audit
    Category       SuspendReason  // "risk" | "payment" | "ops" | "fraud"
    OperatorID     *core.UserID   // nil if system-initiated
    ExpiresAt      *time.Time     // optional auto-reinstate
}

type ActivationReason struct {
    Source     string         // "kyc_approved" | "operator_manual" | "friendly_seller"
    OperatorID *core.UserID   // required if Source == "operator_manual" or "friendly_seller"
    Notes      string
}

type KYCApplication struct {
    LegalName        string
    GSTIN            string
    PAN              string
    BusinessAddress  Address
    AuthorizedSigner Person
    Documents        []KYCDocumentRef // refs into adapter-stored docs
}

type KYCRef struct {
    AdapterName    string         // "in-house" | "veridoc" | etc.
    ExternalRef    string
    VerifiedAt     time.Time
    VerifiedBy     string         // operator email or "auto"
}
```

### Sentinel Errors

```go
package seller

import "errors"

var (
    ErrNotFound              = errors.New("seller: not found")
    ErrInvalidLifecycleTrans = errors.New("seller: invalid lifecycle transition")
    ErrKYCAlreadySubmitted   = errors.New("seller: KYC already submitted")
    ErrKYCNotSubmitted       = errors.New("seller: KYC not yet submitted")
    ErrWindDownBlocked       = errors.New("seller: wind-down blocked (open balance/holds)")
    ErrSuspended             = errors.New("seller: suspended")
    ErrAlreadyExists         = errors.New("seller: already exists for this user/legal-name")
    ErrInvalidGSTIN          = errors.New("seller: invalid GSTIN format")
    ErrInvalidProfile        = errors.New("seller: invalid profile field")
)
```

## Lifecycle State Machine

```
                  ┌────────────────────┐
                  │   provisioning     │  Provision()
                  └──────────┬─────────┘
                             │  immediately on commit
                             ▼
                  ┌────────────────────┐
        ┌─────────│      sandbox       │  test shipments only
        │         └──────────┬─────────┘
        │                    │  ApproveKYC()  OR  Activate(operator_manual)
        │                    ▼
        │         ┌────────────────────┐
        │ ┌───────│       active       │◄────┐
        │ │       └──────────┬─────────┘     │  Reinstate()
        │ │                  │                │
        │ │                  │  Suspend()     │
        │ │                  ▼                │
        │ │       ┌────────────────────┐      │
        │ │       │     suspended      │──────┘
        │ │       └──────────┬─────────┘
        │ │                  │  WindDown()
        │ │                  ▼
        │ │       ┌────────────────────┐
        │ └──────►│     wound_down     │  terminal; soft-deleted
        │         └────────────────────┘
        │                    ▲
        └────────────────────┘  WindDown() from sandbox (rare; abandoned signup)
```

The transition table lives in `lifecycle.go`:

```go
package seller

type LifecycleState string

const (
    StateProvisioning LifecycleState = "provisioning"
    StateSandbox      LifecycleState = "sandbox"
    StateActive       LifecycleState = "active"
    StateSuspended    LifecycleState = "suspended"
    StateWoundDown    LifecycleState = "wound_down"
)

// allowedTransitions[from] = set of valid next states.
var allowedTransitions = map[LifecycleState]map[LifecycleState]struct{}{
    StateProvisioning: {StateSandbox: {}, StateWoundDown: {}},
    StateSandbox:      {StateActive: {}, StateWoundDown: {}},
    StateActive:       {StateSuspended: {}, StateWoundDown: {}},
    StateSuspended:    {StateActive: {}, StateWoundDown: {}},
    StateWoundDown:    {}, // terminal
}

func canTransition(from, to LifecycleState) bool {
    next, ok := allowedTransitions[from]
    if !ok {
        return false
    }
    _, ok = next[to]
    return ok
}
```

## DB Schema

```sql
-- Seller is THE referential anchor. Almost every other domain table
-- has seller_id NOT NULL REFERENCES seller(id) and an RLS policy
-- using app.seller_id GUC.

CREATE TABLE seller (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    legal_name      text        NOT NULL,
    display_name    text        NOT NULL,
    seller_type     text        NOT NULL CHECK (seller_type IN ('small_business','mid_market','enterprise')),
    lifecycle_state text        NOT NULL CHECK (lifecycle_state IN ('provisioning','sandbox','active','suspended','wound_down')),

    gstin           text,        -- nullable until KYC submitted
    pan             text,
    billing_email   text        NOT NULL,
    support_email   text        NOT NULL,
    primary_phone   text        NOT NULL,
    ops_contacts    jsonb       NOT NULL DEFAULT '[]'::jsonb,

    signup_source   text        NOT NULL,
    founding_user_id uuid       NOT NULL REFERENCES app_user(id),

    suspended_reason     text,
    suspended_category   text,
    suspended_at         timestamptz,
    suspended_expires_at timestamptz,

    wound_down_at        timestamptz,
    wound_down_reason    text,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT seller_gstin_format
        CHECK (gstin IS NULL OR gstin ~ '^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$'),
    CONSTRAINT seller_legal_name_min_len
        CHECK (char_length(legal_name) >= 3),

    -- Suspended state must have a reason and category.
    CONSTRAINT seller_suspended_has_reason
        CHECK (lifecycle_state <> 'suspended'
               OR (suspended_reason IS NOT NULL AND suspended_category IS NOT NULL)),

    -- Wound-down state must have a reason and timestamp.
    CONSTRAINT seller_wound_down_complete
        CHECK (lifecycle_state <> 'wound_down'
               OR (wound_down_at IS NOT NULL AND wound_down_reason IS NOT NULL))
);

CREATE INDEX seller_lifecycle_idx ON seller(lifecycle_state)
    WHERE lifecycle_state IN ('provisioning','sandbox','active','suspended');
CREATE INDEX seller_founding_user_idx ON seller(founding_user_id);
CREATE UNIQUE INDEX seller_gstin_unique ON seller(gstin) WHERE gstin IS NOT NULL;

-- KYC application: state machine for verification.
-- We store the metadata; documents themselves are in adapter-managed S3.

CREATE TABLE kyc_application (
    seller_id        uuid        PRIMARY KEY REFERENCES seller(id),
    state            text        NOT NULL CHECK (state IN ('not_started','submitted','approved','rejected')),
    legal_name       text,
    gstin            text,
    pan              text,
    business_address jsonb,
    authorized_signer jsonb,
    document_refs    jsonb       NOT NULL DEFAULT '[]'::jsonb,

    submitted_at     timestamptz,
    decided_at       timestamptz,
    decision_reason  text,
    adapter_name     text,
    adapter_external_ref text,
    verified_by      text,

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Lifecycle transition history. Append-only. Used for audit and
-- post-hoc analytics.

CREATE TABLE seller_lifecycle_event (
    id           bigserial   PRIMARY KEY,
    seller_id    uuid        NOT NULL REFERENCES seller(id),
    from_state   text        NOT NULL,
    to_state     text        NOT NULL,
    reason       text        NOT NULL,
    category     text,
    operator_id  uuid        REFERENCES app_user(id),
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX seller_lifecycle_event_seller_created_idx
    ON seller_lifecycle_event(seller_id, created_at DESC);

-- RLS: seller_app role can only see non-wound-down sellers matching
-- the app.seller_id GUC. The reports role bypasses RLS.

ALTER TABLE seller ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_isolation ON seller
    USING (id = current_setting('app.seller_id')::uuid
           AND lifecycle_state <> 'wound_down');

-- The kyc_application and seller_lifecycle_event tables follow the
-- same pattern keyed on seller_id.

ALTER TABLE kyc_application ENABLE ROW LEVEL SECURITY;
CREATE POLICY kyc_app_isolation ON kyc_application
    USING (seller_id = current_setting('app.seller_id')::uuid);

ALTER TABLE seller_lifecycle_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY seller_lifecycle_isolation ON seller_lifecycle_event
    USING (seller_id = current_setting('app.seller_id')::uuid);

-- Grants
GRANT SELECT, INSERT, UPDATE ON seller, kyc_application, seller_lifecycle_event TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE seller_lifecycle_event_id_seq TO pikshipp_app;
GRANT SELECT ON seller, kyc_application, seller_lifecycle_event TO pikshipp_reports;
```

### Why no FK from other tables to `lifecycle_state`?

Lifecycle is a column on `seller`, not a separate table. Other tables read `seller.lifecycle_state` via either (a) joins in admin queries or (b) the in-memory `LifecycleCache`. We deliberately do **not** denormalize lifecycle into every domain row — the cost of fanning out updates outweighs the read cost.

## sqlc Queries

```sql
-- name: SellerInsert :one
INSERT INTO seller (
    id, legal_name, display_name, seller_type, lifecycle_state,
    billing_email, support_email, primary_phone,
    signup_source, founding_user_id
) VALUES (
    $1, $2, $3, $4, 'provisioning',
    $5, $6, $7,
    $8, $9
)
RETURNING *;

-- name: SellerGet :one
SELECT * FROM seller WHERE id = $1;

-- name: SellerGetForAdmin :one
-- Bypasses RLS; called only with admin role connection.
SELECT * FROM seller WHERE id = $1;

-- name: SellerUpdateProfile :one
UPDATE seller
SET legal_name    = COALESCE(sqlc.narg('legal_name'),    legal_name),
    display_name  = COALESCE(sqlc.narg('display_name'),  display_name),
    billing_email = COALESCE(sqlc.narg('billing_email'), billing_email),
    support_email = COALESCE(sqlc.narg('support_email'), support_email),
    ops_contacts  = COALESCE(sqlc.narg('ops_contacts'),  ops_contacts),
    updated_at    = now()
WHERE id = $1
RETURNING *;

-- name: SellerTransitionLifecycle :one
UPDATE seller
SET lifecycle_state    = $2,
    suspended_reason   = $3,
    suspended_category = $4,
    suspended_at       = $5,
    suspended_expires_at = $6,
    wound_down_at      = $7,
    wound_down_reason  = $8,
    updated_at         = now()
WHERE id = $1 AND lifecycle_state = $9   -- optimistic guard on from_state
RETURNING *;

-- name: SellerLifecycleEventInsert :exec
INSERT INTO seller_lifecycle_event (
    seller_id, from_state, to_state, reason, category, operator_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: SellerListByState :many
SELECT * FROM seller
WHERE lifecycle_state = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: SellerProvisionFingerprint :one
-- Used for 24h provision-idempotency: returns the most recent provisioning
-- attempt by this user with this legal name, if within window.
SELECT id FROM seller
WHERE founding_user_id = $1
  AND legal_name = $2
  AND created_at > now() - interval '24 hours'
LIMIT 1;

-- KYC

-- name: KYCApplicationGet :one
SELECT * FROM kyc_application WHERE seller_id = $1;

-- name: KYCApplicationUpsertSubmitted :one
INSERT INTO kyc_application (
    seller_id, state, legal_name, gstin, pan, business_address,
    authorized_signer, document_refs, submitted_at
) VALUES (
    $1, 'submitted', $2, $3, $4, $5, $6, $7, now()
)
ON CONFLICT (seller_id) DO UPDATE
    SET state = 'submitted',
        legal_name = EXCLUDED.legal_name,
        gstin = EXCLUDED.gstin,
        pan = EXCLUDED.pan,
        business_address = EXCLUDED.business_address,
        authorized_signer = EXCLUDED.authorized_signer,
        document_refs = EXCLUDED.document_refs,
        submitted_at = now(),
        decided_at = NULL,
        decision_reason = NULL,
        updated_at = now()
WHERE kyc_application.state IN ('not_started','rejected')   -- can re-submit only from these
RETURNING *;

-- name: KYCApplicationDecide :one
UPDATE kyc_application
SET state = $2,
    decided_at = now(),
    decision_reason = $3,
    adapter_name = $4,
    adapter_external_ref = $5,
    verified_by = $6,
    updated_at = now()
WHERE seller_id = $1 AND state = 'submitted'
RETURNING *;
```

## Implementation

```go
package seller

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/db"
    "github.com/pikshipp/pikshipp/internal/outbox"
    "github.com/pikshipp/pikshipp/internal/seller/sqlcgen"
)

type service struct {
    pool   *pgxpool.Pool
    q      *sqlcgen.Queries  // pool-level (non-tx) queries
    clock  core.Clock
    audit  audit.Service
    outb   outbox.Emitter
    logger *slog.Logger
}

func New(pool *pgxpool.Pool, clock core.Clock, aud audit.Service, ob outbox.Emitter, logger *slog.Logger) Service {
    return &service{
        pool:   pool,
        q:      sqlcgen.New(pool),
        clock:  clock,
        audit:  aud,
        outb:   ob,
        logger: logger,
    }
}

func (s *service) Provision(ctx context.Context, req ProvisionRequest) (*Seller, error) {
    if err := validateProvision(req); err != nil {
        return nil, err
    }

    // 24h idempotency check (deliberately a soft check, not a unique index;
    // legal_name can repeat across users and we don't want to block that
    // globally).
    if existing, err := s.q.SellerProvisionFingerprint(ctx, sqlcgen.SellerProvisionFingerprintParams{
        FoundingUserID: req.FoundingUserID.UUID(),
        LegalName:      req.LegalName,
    }); err == nil {
        s.logger.Info("seller: provision short-circuited via fingerprint",
            "seller_id", existing, "founding_user_id", req.FoundingUserID)
        sel, err := s.Get(ctx, core.SellerIDFromUUID(existing))
        if err != nil {
            return nil, err
        }
        return sel, nil
    } else if !errors.Is(err, pgx.ErrNoRows) {
        return nil, fmt.Errorf("seller: fingerprint lookup: %w", err)
    }

    var out *Seller
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.SellerInsert(ctx, sqlcgen.SellerInsertParams{
            ID:             core.NewSellerID().UUID(),
            LegalName:      req.LegalName,
            DisplayName:    coalesceStr(req.DisplayName, req.LegalName),
            SellerType:     string(req.SellerType),
            BillingEmail:   req.BillingEmail,
            SupportEmail:   req.SupportEmail,
            PrimaryPhone:   req.PrimaryPhone,
            SignupSource:   req.SignupSource,
            FoundingUserID: req.FoundingUserID.UUID(),
        })
        if err != nil {
            return fmt.Errorf("seller: insert: %w", err)
        }
        out = sellerFromRow(row)

        // Append lifecycle event
        if err := qtx.SellerLifecycleEventInsert(ctx, sqlcgen.SellerLifecycleEventInsertParams{
            SellerID:  out.ID.UUID(),
            FromState: "",
            ToState:   string(StateProvisioning),
            Reason:    "initial_provision",
            Category:  pgxNullString(""),
            Payload:   jsonbFrom(map[string]any{"signup_source": req.SignupSource}),
        }); err != nil {
            return err
        }

        // Audit (sync, in-tx)
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: out.ID,
            Actor:    audit.ActorUser(req.FoundingUserID),
            Action:   "seller.provision",
            Object:   audit.ObjSeller(out.ID),
            Payload:  map[string]any{"legal_name": req.LegalName, "seller_type": req.SellerType},
        }); err != nil {
            return err
        }

        // Outbox event
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:     "seller.provisioned",
            Key:      string(out.ID),
            Payload:  out.toEventPayload(),
        })
    })
    if err != nil {
        return nil, err
    }

    // Auto-advance to sandbox in a separate tx so the provisioned event
    // is durably emitted before the sandbox event.
    return s.advanceToSandbox(ctx, out.ID)
}

func (s *service) advanceToSandbox(ctx context.Context, id core.SellerID) (*Seller, error) {
    var out *Seller
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.SellerTransitionLifecycle(ctx, sqlcgen.SellerTransitionLifecycleParams{
            ID:                 id.UUID(),
            LifecycleState:     string(StateSandbox),
            FromState:          string(StateProvisioning),
            // ... null fields for suspended/wound_down columns
        })
        if err != nil {
            return fmt.Errorf("seller: advance to sandbox: %w", err)
        }
        out = sellerFromRow(row)

        if err := qtx.SellerLifecycleEventInsert(ctx, sqlcgen.SellerLifecycleEventInsertParams{
            SellerID:  id.UUID(),
            FromState: string(StateProvisioning),
            ToState:   string(StateSandbox),
            Reason:    "auto_advance",
        }); err != nil {
            return err
        }

        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: id,
            Actor:    audit.ActorSystem(),
            Action:   "seller.lifecycle.changed",
            Payload:  map[string]any{"from": "provisioning", "to": "sandbox"},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:    "seller.lifecycle.changed",
            Key:     string(id),
            Payload: map[string]any{"from": "provisioning", "to": "sandbox"},
        })
    })
    return out, err
}

func (s *service) UpdateProfile(ctx context.Context, id core.SellerID, patch ProfilePatch) (*Seller, error) {
    if err := validateProfilePatch(patch); err != nil {
        return nil, err
    }
    var out *Seller
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        row, err := qtx.SellerUpdateProfile(ctx, sqlcgen.SellerUpdateProfileParams{
            ID:           id.UUID(),
            LegalName:    pgxOptStr(patch.LegalName),
            DisplayName:  pgxOptStr(patch.DisplayName),
            BillingEmail: pgxOptStr(patch.BillingEmail),
            SupportEmail: pgxOptStr(patch.SupportEmail),
            OpsContacts:  pgxOptJSON(patch.OpsContacts),
        })
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrNotFound
            }
            return err
        }
        out = sellerFromRow(row)

        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: id,
            Action:   "seller.profile.updated",
            Payload:  patchAuditPayload(patch),
        })
    })
    return out, err
}

func (s *service) Suspend(ctx context.Context, id core.SellerID, req SuspendRequest) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.SellerGet(ctx, id.UUID())
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrNotFound
            }
            return err
        }
        if !canTransition(LifecycleState(cur.LifecycleState), StateSuspended) {
            return fmt.Errorf("%w: %s -> suspended", ErrInvalidLifecycleTrans, cur.LifecycleState)
        }
        _, err = qtx.SellerTransitionLifecycle(ctx, sqlcgen.SellerTransitionLifecycleParams{
            ID:                 id.UUID(),
            LifecycleState:     string(StateSuspended),
            SuspendedReason:    pgxNullString(req.Reason),
            SuspendedCategory:  pgxNullString(string(req.Category)),
            SuspendedAt:        pgxNullTimestamp(s.clock.Now()),
            SuspendedExpiresAt: pgxNullTimestampPtr(req.ExpiresAt),
            FromState:          cur.LifecycleState,
        })
        if err != nil {
            return err
        }
        if err := qtx.SellerLifecycleEventInsert(ctx, sqlcgen.SellerLifecycleEventInsertParams{
            SellerID:    id.UUID(),
            FromState:   cur.LifecycleState,
            ToState:     string(StateSuspended),
            Reason:      req.Reason,
            Category:    pgxNullString(string(req.Category)),
            OperatorID:  pgxNullUUID(req.OperatorID),
            Payload:     jsonbFrom(map[string]any{"expires_at": req.ExpiresAt}),
        }); err != nil {
            return err
        }
        // Audit synchronously (high-value action)
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: id,
            Actor:    actorFromOperator(req.OperatorID),
            Action:   "seller.suspended",
            Payload:  map[string]any{"reason": req.Reason, "category": req.Category},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:    "seller.lifecycle.changed",
            Key:     string(id),
            Payload: map[string]any{"from": cur.LifecycleState, "to": "suspended", "reason": req.Reason},
        })
    })
}

func (s *service) WindDown(ctx context.Context, id core.SellerID, reason string) error {
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        cur, err := qtx.SellerGet(ctx, id.UUID())
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                return ErrNotFound
            }
            return err
        }
        if !canTransition(LifecycleState(cur.LifecycleState), StateWoundDown) {
            return fmt.Errorf("%w: %s -> wound_down", ErrInvalidLifecycleTrans, cur.LifecycleState)
        }
        // Hard guard: wallet must be zero balance / zero open holds.
        // The wallet service exposes a checker; we call it via dependency
        // injection. NOT shown here to keep this LLD self-contained;
        // the contract is "wallet.AssertWindDownSafe(ctx, tx, sellerID) error".

        now := s.clock.Now()
        _, err = qtx.SellerTransitionLifecycle(ctx, sqlcgen.SellerTransitionLifecycleParams{
            ID:               id.UUID(),
            LifecycleState:   string(StateWoundDown),
            WoundDownAt:      pgxNullTimestamp(now),
            WoundDownReason:  pgxNullString(reason),
            FromState:        cur.LifecycleState,
        })
        if err != nil {
            return err
        }
        if err := qtx.SellerLifecycleEventInsert(ctx, sqlcgen.SellerLifecycleEventInsertParams{
            SellerID:  id.UUID(),
            FromState: cur.LifecycleState,
            ToState:   string(StateWoundDown),
            Reason:    reason,
        }); err != nil {
            return err
        }
        if err := s.audit.Emit(ctx, tx, audit.Event{
            SellerID: id,
            Action:   "seller.wound_down",
            Payload:  map[string]any{"reason": reason},
        }); err != nil {
            return err
        }
        return s.outb.Emit(ctx, tx, outbox.Event{
            Kind:    "seller.wound_down",
            Key:     string(id),
            Payload: map[string]any{"reason": reason},
        })
    })
}
```

The Activate, Reinstate, ApproveKYC, RejectKYC, SubmitKYC methods follow the **same template** as Suspend. The template is:

1. Begin tx.
2. Read current row.
3. Validate transition via `canTransition`.
4. Update row + write lifecycle event.
5. Emit audit synchronously (in-tx).
6. Emit outbox event (in-tx).
7. Commit.

This template is the **single rule** for every lifecycle change in the entire codebase.

### Validation

```go
func validateProvision(r ProvisionRequest) error {
    if r.FoundingUserID.IsZero() {
        return fmt.Errorf("%w: founding_user_id required", ErrInvalidProfile)
    }
    if len(r.LegalName) < 3 {
        return fmt.Errorf("%w: legal_name too short", ErrInvalidProfile)
    }
    if !core.IsValidEmail(r.BillingEmail) {
        return fmt.Errorf("%w: billing_email invalid", ErrInvalidProfile)
    }
    if !core.IsValidEmail(r.SupportEmail) {
        return fmt.Errorf("%w: support_email invalid", ErrInvalidProfile)
    }
    if !core.IsValidE164(r.PrimaryPhone) {
        return fmt.Errorf("%w: primary_phone must be E.164", ErrInvalidProfile)
    }
    switch r.SellerType {
    case SellerTypeSmallBiz, SellerTypeMidMarket, SellerTypeEnterprise:
    default:
        return fmt.Errorf("%w: seller_type invalid", ErrInvalidProfile)
    }
    return nil
}

// GSTIN: 15-char alphanumeric per Indian govt spec.
// Format: 2 digits state code + 10 chars PAN + 1 digit entity + 1 'Z' + 1 alnum checksum.
var gstinRegex = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z]{1}[1-9A-Z]{1}Z[0-9A-Z]{1}$`)

func validateGSTIN(g string) error {
    if !gstinRegex.MatchString(g) {
        return ErrInvalidGSTIN
    }
    return nil
}
```

## Background Jobs (river)

```go
// Auto-reinstate expired suspensions.
type AutoReinstateJob struct {
    river.JobArgs
}
func (AutoReinstateJob) Kind() string { return "seller.auto_reinstate" }

// Worker scans seller WHERE lifecycle_state='suspended' AND suspended_expires_at < now()
// and reinstates each. Runs every 5 minutes via river periodic job.
type AutoReinstateWorker struct {
    river.WorkerDefaults[AutoReinstateJob]
    svc Service
}

func (w *AutoReinstateWorker) Work(ctx context.Context, j *river.Job[AutoReinstateJob]) error {
    expired, err := w.svc.(*service).listExpiredSuspensions(ctx, 100)
    if err != nil {
        return err
    }
    for _, sel := range expired {
        if err := w.svc.Reinstate(ctx, sel.ID, "auto_reinstate_expiry"); err != nil {
            slog.Error("auto-reinstate failed", "seller_id", sel.ID, "err", err)
        }
    }
    return nil
}
```

## Outbox Event Payloads

```go
package seller

// Schema: stable across versions. Add new fields, never rename or remove.
//
// All payloads are JSON-encoded into outbox_event.payload.

type ProvisionedPayload struct {
    SchemaVersion int                 `json:"schema_version"` // = 1
    SellerID      string              `json:"seller_id"`
    LegalName     string              `json:"legal_name"`
    DisplayName   string              `json:"display_name"`
    SellerType    string              `json:"seller_type"`
    SignupSource  string              `json:"signup_source"`
    OccurredAt    time.Time           `json:"occurred_at"`
}

type LifecycleChangedPayload struct {
    SchemaVersion int       `json:"schema_version"`
    SellerID      string    `json:"seller_id"`
    From          string    `json:"from"`
    To            string    `json:"to"`
    Reason        string    `json:"reason"`
    Category      string    `json:"category,omitempty"`
    OperatorID    string    `json:"operator_id,omitempty"`
    OccurredAt    time.Time `json:"occurred_at"`
}

type WoundDownPayload struct {
    SchemaVersion int       `json:"schema_version"`
    SellerID      string    `json:"seller_id"`
    Reason        string    `json:"reason"`
    OccurredAt    time.Time `json:"occurred_at"`
}
```

The outbox **forwarder** (LLD §03-services/03-outbox) routes:
- `seller.provisioned` → `wallet.CreateAccountForSellerJob` + `policy.SeedDefaultsJob`
- `seller.lifecycle.changed` → `lifecycle_cache.InvalidateJob` + (downstream consumers)
- `seller.wound_down` → `cleanup.SellerArchiveJob`

## Caching: LifecycleCache

Most domain services need to know "is this seller active?" before mutating data. Hitting `seller` table on every request is wasteful.

```go
package seller

// LifecycleCache is an in-process cache of lifecycle states.
// It is loaded lazily and invalidated via LISTEN/NOTIFY on
// channel "seller_lifecycle_changed" + a 60s TTL safety net.

type LifecycleCache struct {
    mu        sync.RWMutex
    entries   map[core.SellerID]lifecycleEntry
    ttl       time.Duration
    repo      *repo
}

type lifecycleEntry struct {
    state     LifecycleState
    fetchedAt time.Time
}

func NewLifecycleCache(repo *repo) *LifecycleCache {
    return &LifecycleCache{
        entries: make(map[core.SellerID]lifecycleEntry),
        ttl:     60 * time.Second,
        repo:    repo,
    }
}

func (c *LifecycleCache) Get(ctx context.Context, id core.SellerID) (LifecycleState, error) {
    c.mu.RLock()
    e, ok := c.entries[id]
    c.mu.RUnlock()
    if ok && time.Since(e.fetchedAt) < c.ttl {
        return e.state, nil
    }
    state, err := c.repo.GetLifecycleState(ctx, id)
    if err != nil {
        return "", err
    }
    c.mu.Lock()
    c.entries[id] = lifecycleEntry{state: state, fetchedAt: time.Now()}
    c.mu.Unlock()
    return state, nil
}

// Invalidate is called by the LISTEN/NOTIFY consumer when
// seller_lifecycle_event rows are inserted.
func (c *LifecycleCache) Invalidate(id core.SellerID) {
    c.mu.Lock()
    delete(c.entries, id)
    c.mu.Unlock()
}

// AssertActive is a sugar for guarding feature paths.
func (c *LifecycleCache) AssertActive(ctx context.Context, id core.SellerID) error {
    state, err := c.Get(ctx, id)
    if err != nil {
        return err
    }
    switch state {
    case StateActive:
        return nil
    case StateSandbox:
        return nil // sandbox can ship; just to test couriers
    case StateSuspended:
        return ErrSuspended
    default:
        return fmt.Errorf("seller in non-operational state: %s", state)
    }
}
```

The cache is registered as a singleton in `cmd/server/wire.go` and injected wherever the active-state guard is needed.

## Testing

### Unit Tests (`service_test.go`)

```go
func TestProvisionValidation(t *testing.T) {
    cases := []struct {
        name string
        req  ProvisionRequest
        err  error
    }{
        {"empty legal name", ProvisionRequest{
            FoundingUserID: core.NewUserID(),
            LegalName:      "",
            BillingEmail:   "a@b.com",
            SupportEmail:   "s@b.com",
            PrimaryPhone:   "+919876543210",
            SellerType:     SellerTypeSmallBiz,
        }, ErrInvalidProfile},
        {"bad email", ProvisionRequest{
            // ... bad email
        }, ErrInvalidProfile},
        {"bad phone", ProvisionRequest{
            // ... bad phone
        }, ErrInvalidProfile},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            err := validateProvision(c.req)
            if !errors.Is(err, c.err) {
                t.Fatalf("expected %v, got %v", c.err, err)
            }
        })
    }
}

func TestCanTransition(t *testing.T) {
    cases := []struct {
        from, to LifecycleState
        ok       bool
    }{
        {StateProvisioning, StateSandbox, true},
        {StateSandbox, StateActive, true},
        {StateActive, StateSuspended, true},
        {StateSuspended, StateActive, true},
        {StateActive, StateProvisioning, false}, // can never go back
        {StateWoundDown, StateActive, false},     // terminal
    }
    for _, c := range cases {
        if got := canTransition(c.from, c.to); got != c.ok {
            t.Errorf("%s -> %s: got %v want %v", c.from, c.to, got, c.ok)
        }
    }
}
```

### System-Level Tests (`service_slt_test.go`)

```go
func TestLifecycleHappyPath_SLT(t *testing.T) {
    ctx := context.Background()
    pg := slt.StartPG(t)
    svc, _, _ := slt.NewSellerService(t, pg)

    // Provision
    sel, err := svc.Provision(ctx, ProvisionRequest{
        FoundingUserID: core.NewUserID(),
        LegalName:      "Acme Trading Co",
        DisplayName:    "Acme",
        SellerType:     SellerTypeSmallBiz,
        BillingEmail:   "billing@acme.test",
        SupportEmail:   "support@acme.test",
        PrimaryPhone:   "+919999999999",
        SignupSource:   "web",
    })
    require.NoError(t, err)

    // Should auto-advance to sandbox
    sel2, err := svc.Get(ctx, sel.ID)
    require.NoError(t, err)
    require.Equal(t, StateSandbox, sel2.LifecycleState)

    // Outbox should have provisioned + lifecycle.changed events
    events := slt.OutboxEventsFor(t, pg, sel.ID)
    require.ElementsMatch(t,
        []string{"seller.provisioned", "seller.lifecycle.changed"},
        eventKinds(events))

    // Activate
    require.NoError(t, svc.Activate(ctx, sel.ID, ActivationReason{
        Source: "operator_manual",
        OperatorID: ptr(core.NewUserID()),
    }))
    sel3, _ := svc.Get(ctx, sel.ID)
    require.Equal(t, StateActive, sel3.LifecycleState)

    // Suspend
    require.NoError(t, svc.Suspend(ctx, sel.ID, SuspendRequest{
        Reason:   "risk hold",
        Category: SuspendCategoryRisk,
    }))
    sel4, _ := svc.Get(ctx, sel.ID)
    require.Equal(t, StateSuspended, sel4.LifecycleState)

    // Reinstate
    require.NoError(t, svc.Reinstate(ctx, sel.ID, "manual"))
    sel5, _ := svc.Get(ctx, sel.ID)
    require.Equal(t, StateActive, sel5.LifecycleState)
}

func TestProvisionIdempotency_SLT(t *testing.T) {
    ctx := context.Background()
    svc, _, _ := slt.NewSellerService(t, slt.StartPG(t))
    user := core.NewUserID()

    req := ProvisionRequest{
        FoundingUserID: user,
        LegalName:      "Idem Co",
        BillingEmail:   "b@i.test", SupportEmail: "s@i.test",
        PrimaryPhone:   "+919111111111", SellerType: SellerTypeSmallBiz,
        SignupSource:   "web",
    }
    a, err := svc.Provision(ctx, req)
    require.NoError(t, err)
    b, err := svc.Provision(ctx, req)
    require.NoError(t, err)
    require.Equal(t, a.ID, b.ID, "double provision must return same seller")
}

func TestWindDownBlockedWhenWalletNonZero_SLT(t *testing.T) {
    // Provisions a seller, credits the wallet, then tries to wind down.
    // Expect ErrWindDownBlocked.
    // ...
}

func TestRLSIsolation_SLT(t *testing.T) {
    // Two sellers; with app.seller_id GUC set to A, queries must not
    // return rows for B even with explicit WHERE id = B.
    // ...
}
```

### Microbenchmarks (`bench_test.go`)

```go
func BenchmarkLifecycleCache_Get_Hit(b *testing.B) {
    c := newCacheWithEntry(testSellerID, StateActive)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _ = c.Get(context.Background(), testSellerID)
    }
}

func BenchmarkProvisionValidation(b *testing.B) {
    req := goodProvisionRequest()
    for i := 0; i < b.N; i++ {
        _ = validateProvision(req)
    }
}
```

Targets:
- `BenchmarkLifecycleCache_Get_Hit`: < 100 ns, 0 allocs.
- `BenchmarkProvisionValidation`: < 5 µs, < 5 allocs (regex compile is one-time).

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Provision` (no fingerprint hit) | 8 ms | 25 ms | 2 inserts + audit + outbox + advance-tx |
| `Provision` (fingerprint hit) | 1 ms | 4 ms | 1 SELECT + 1 SELECT |
| `Get` (cache miss) | 0.5 ms | 2 ms | 1 SELECT by PK |
| `LifecycleCache.Get` (hit) | 80 ns | 200 ns | 0 allocs |
| `Suspend` / `Reinstate` / `Activate` | 4 ms | 12 ms | 1 UPDATE + 1 INSERT + audit + outbox |
| `WindDown` | 6 ms | 18 ms | + wallet zero-balance check |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| `Provision` insert collides on `seller_gstin_unique` | pgconn unique violation | Map to `ErrAlreadyExists`; surface 409. |
| Lifecycle transition raced (state changed between read and write) | `SellerTransitionLifecycle` returns 0 rows | Retry once (re-read state); on second failure return `ErrInvalidLifecycleTrans`. |
| Audit emit fails inside lifecycle tx | `audit.Emit` returns error | Tx rolls back; client gets 500; no partial state. |
| Outbox emit fails inside tx | `outbox.Emit` returns error | Tx rolls back; ditto. |
| `LifecycleCache` LISTEN connection dies | Postgres notification consumer logs disconnect | TTL (60s) ensures eventual freshness; reconnect loop in cache infrastructure. |
| Wallet check fails during WindDown | `ErrWindDownBlocked` | Surface 409 with `open_balance_paise` and `open_holds` in body. |
| Auto-reinstate worker can't run (river queue down) | River health check | Suspensions stay live until manual intervention; surfaced on ops dashboard. |

## Open Questions

1. **Sub-sellers / parent-child orgs.** The PRD allows enterprise sellers to have child sub-orgs (e.g., a brand with multiple stores). Current schema does not model this. **Decision: defer to v1.** When added, introduce `seller.parent_id` and a partial unique index ensuring single-level hierarchy.
2. **Soft-delete vs. hard-delete on `wound_down`.** We currently only hide via RLS predicate. PII compliance (GDPR-style) may require purge after a grace window. **Decision: defer to v1**; add a `seller_purge` cron that runs 90d after `wound_down_at` and replaces PII fields with hashes.
3. **GSTIN verification.** We validate format only, not whether the GSTIN is real / belongs to this entity. **Decision: defer to KYC adapter integration.**
4. **Multi-region wind-down.** N/A in v0 (single region). Re-evaluate when expanding regions.

## References

- HLD §03-services/01-policy: policy engine resolves per-seller config; this service does NOT own those.
- HLD §03-services/02-pricing: rate cards are scoped to seller; price service watches for `seller.lifecycle.changed` to invalidate caches.
- HLD §03-services/04-wallet-and-ledger: wallet account is created on `seller.provisioned`; closed on `seller.wound_down`.
- HLD §03-services/06-authn-authz: identity owns user/seller membership; this service does NOT model users.
- HLD §01-architecture/03-async-and-outbox: outbox forwarder routes events listed in §Outbox Event Payloads.
- LLD §03-services/02-audit: audit emission contract used here.
- LLD §03-services/03-outbox: outbox emit contract used here.
- LLD §02-infrastructure/01-database-access: `db.WithTx` helper.
