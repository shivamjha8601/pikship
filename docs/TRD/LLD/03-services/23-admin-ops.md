# Admin / Ops Service

## Purpose

The admin/ops service is the **internal control plane**: a thin layer of Go code that authorizes operators (Pikshipp employees) and exposes high-privilege operations like force-cancel a shipment, override a wallet balance, suspend a seller, redrive a stuck job. Every action is **synchronously audited** with an operator identity and reason.

The other services already expose their domain APIs; this service is mostly **glue + authorization + audit**. Its design rule:

> Every operator action must answer three questions on read-back: who did it, why, and what was the state before.

Out of scope:

- Operator authentication — handled in identity (LLD §03-services/08); operators log in via the same Google OAuth flow with role `operator` or `admin`.
- The HTML dashboard — separate frontend repo.
- Direct DB access in prod — this service is the *only* path for mutations; ad-hoc psql is restricted.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors. |
| `internal/seller` | `Suspend`, `Reinstate`, `WindDown`. |
| `internal/wallet` | adjustments. |
| `internal/shipments` | force-cancel, force-fail. |
| `internal/orders` | force-cancel. |
| `internal/identity` | user-lock, role-grant. |
| `internal/recon`, `internal/cod`, `internal/ndr` | manual interventions. |
| `internal/audit` | every action. |
| `internal/policy` | operator-can-do toggles (separately from per-seller policy). |

## Package Layout

```
internal/admin/
├── service.go             // Service interface
├── service_impl.go
├── repo.go
├── types.go
├── authz.go               // role checks + capability-per-action map
├── handlers.go            // chi handlers under /admin/*
├── jobs.go                // RedriveJob, MaintenanceJob
├── errors.go
├── service_test.go
└── service_slt_test.go
```

## Authorization Model

```go
package admin

type OperatorRole string

const (
    RoleOperator OperatorRole = "operator"   // standard agent
    RoleAdmin    OperatorRole = "admin"      // platform admin (engineering / finance lead)
)

type Capability string

const (
    CapSellerSuspend       Capability = "seller.suspend"
    CapSellerWindDown      Capability = "seller.wind_down"
    CapWalletAdjust        Capability = "wallet.adjust"
    CapShipmentForceCancel Capability = "shipment.force_cancel"
    CapShipmentForceFail   Capability = "shipment.force_fail"
    CapOrderForceCancel    Capability = "order.force_cancel"
    CapUserLock            Capability = "user.lock"
    CapRedriveJob          Capability = "job.redrive"
    CapPolicySet           Capability = "policy.set"
    CapDBReadFull          Capability = "db.read_full"
)

var capabilityMatrix = map[OperatorRole]map[Capability]bool{
    RoleOperator: {
        CapShipmentForceCancel: true,
        CapShipmentForceFail:   true,
        CapOrderForceCancel:    true,
        CapRedriveJob:          true,
        CapDBReadFull:          true,
    },
    RoleAdmin: {
        CapSellerSuspend:       true,
        CapSellerWindDown:      true,
        CapWalletAdjust:        true,
        CapShipmentForceCancel: true,
        CapShipmentForceFail:   true,
        CapOrderForceCancel:    true,
        CapUserLock:            true,
        CapRedriveJob:          true,
        CapPolicySet:           true,
        CapDBReadFull:          true,
    },
}

func (s *service) Authorize(ctx context.Context, op core.UserID, cap Capability) error {
    role, err := s.identity.GetOperatorRole(ctx, op)
    if err != nil {
        return ErrNotOperator
    }
    if !capabilityMatrix[role][cap] {
        return fmt.Errorf("%w: role=%s cap=%s", ErrInsufficient, role, cap)
    }
    return nil
}
```

The `Authorize` check is the **first thing every Admin method does**. Failures are logged at WARN with operator identity for security review.

## Public API

```go
package admin

type Service interface {
    // Seller
    SuspendSeller(ctx context.Context, req SuspendSellerRequest) error
    ReinstateSeller(ctx context.Context, req ReinstateSellerRequest) error
    WindDownSeller(ctx context.Context, req WindDownSellerRequest) error

    // Wallet
    AdjustWallet(ctx context.Context, req WalletAdjustRequest) error

    // Shipment / order
    ForceCancelShipment(ctx context.Context, req ForceCancelShipmentRequest) error
    ForceFailShipment(ctx context.Context, req ForceFailShipmentRequest) error
    ForceCancelOrder(ctx context.Context, req ForceCancelOrderRequest) error

    // Identity
    LockUser(ctx context.Context, req LockUserRequest) error
    UnlockUser(ctx context.Context, req UnlockUserRequest) error

    // Jobs
    RedriveJob(ctx context.Context, req RedriveJobRequest) error
    ListStuckJobs(ctx context.Context, q StuckJobQuery) ([]*StuckJob, error)

    // Reads (BYPASSRLS, audited)
    GetSeller(ctx context.Context, req GetSellerRequest) (*seller.Seller, error)
    GetShipment(ctx context.Context, req GetShipmentRequest) (*shipments.Shipment, error)
    GetWalletStatement(ctx context.Context, req GetStatementRequest) ([]wallet.LedgerEntry, error)
}
```

### Request Types — Common Header

Every admin request embeds an `Operator` field used for authz + audit. Without an operator id, the call is rejected.

```go
type Operator struct {
    UserID core.UserID
    Reason string         // free-text rationale; required, min 10 chars
    Tags   []string       // optional: ticket-id, incident-id
}

type SuspendSellerRequest struct {
    Operator Operator
    SellerID core.SellerID
    Category seller.SuspendReason
    ExpiresAt *time.Time
}

type WalletAdjustRequest struct {
    Operator    Operator
    SellerID    core.SellerID
    AmountPaise core.Paise        // positive = credit, negative = debit
    Reason      string
    RefType     string             // e.g., "manual_adjustment", "carrier_dispute_credit"
}

type ForceCancelShipmentRequest struct {
    Operator   Operator
    ShipmentID core.ShipmentID
    AlsoRefundWallet bool          // refund the charge
}

type RedriveJobRequest struct {
    Operator Operator
    JobID    int64                  // river job id
}
```

### Sentinel Errors

```go
var (
    ErrNotOperator    = errors.New("admin: caller is not an operator")
    ErrInsufficient   = errors.New("admin: insufficient capability")
    ErrInvalidReason  = errors.New("admin: reason too short or missing")
    ErrNotFound       = errors.New("admin: not found")
    ErrAlreadyDone    = errors.New("admin: action would be a no-op")
)
```

## Implementation Pattern

Every admin method follows this template:

```go
func (s *service) SomeAction(ctx context.Context, req SomeRequest) error {
    // 1. Validate request (reason length, etc.)
    if err := validateOperator(req.Operator); err != nil {
        return err
    }
    // 2. Authorize
    if err := s.Authorize(ctx, req.Operator.UserID, CapForThisAction); err != nil {
        return err
    }
    // 3. Snapshot pre-state for audit
    pre, err := s.snapshotPreState(ctx, req)
    if err != nil {
        return err
    }
    // 4. Delegate to the underlying service (the actual mutation)
    if err := s.someService.DoTheThing(ctx, ...); err != nil {
        return err
    }
    // 5. Synchronous audit with pre-state and operator
    return s.audit.Emit(ctx, nil, audit.Event{
        // ... action: "admin.some_action", actor: operator, payload: { pre, post, reason, tags }
    })
}

func validateOperator(op Operator) error {
    if op.UserID.IsZero() {
        return fmt.Errorf("%w: operator user_id required", ErrInvalidReason)
    }
    if len(strings.TrimSpace(op.Reason)) < 10 {
        return fmt.Errorf("%w: reason must be ≥ 10 chars", ErrInvalidReason)
    }
    return nil
}
```

### Example: AdjustWallet

```go
func (s *service) AdjustWallet(ctx context.Context, req WalletAdjustRequest) error {
    if err := validateOperator(req.Operator); err != nil {
        return err
    }
    if err := s.Authorize(ctx, req.Operator.UserID, CapWalletAdjust); err != nil {
        return err
    }
    if req.AmountPaise == 0 {
        return ErrAlreadyDone
    }

    direction := wallet.DirectionCredit
    amount := req.AmountPaise
    if amount < 0 {
        direction = wallet.DirectionDebit
        amount = -amount
    }

    // We use the operator user id as the ref_id so re-runs by the same
    // operator are NOT idempotent — the operator must intentionally
    // create a new adjustment id. Adjustments use a unique adjustment id
    // generated server-side here.
    adjID := core.NewWalletAdjustmentID()

    // Capture balance pre-state
    pre, err := s.wallet.Balance(ctx, req.SellerID)
    if err != nil {
        return err
    }

    if err := s.wallet.Post(ctx, wallet.PostRequest{
        SellerID:    req.SellerID,
        AmountPaise: amount,
        RefType:     coalesceString(req.RefType, "manual_adjustment"),
        RefID:       adjID.String(),
        Direction:   direction,
        Reason:      req.Reason,
    }); err != nil {
        return err
    }

    return s.audit.Emit(ctx, nil, audit.Event{
        SellerID: req.SellerID,
        Actor:    audit.ActorUser(req.Operator.UserID),
        Action:   "admin.wallet.adjust",
        Object:   audit.ObjSeller(req.SellerID),
        Payload: map[string]any{
            "adjustment_id":  adjID,
            "amount_paise":   req.AmountPaise,
            "direction":      direction,
            "reason":         req.Reason,
            "operator_tags":  req.Operator.Tags,
            "balance_pre":    pre,
        },
    })
}
```

### Example: ForceCancelShipment

```go
func (s *service) ForceCancelShipment(ctx context.Context, req ForceCancelShipmentRequest) error {
    if err := validateOperator(req.Operator); err != nil {
        return err
    }
    if err := s.Authorize(ctx, req.Operator.UserID, CapShipmentForceCancel); err != nil {
        return err
    }
    sh, err := s.shipments.GetSystem(ctx, req.ShipmentID)
    if err != nil {
        return ErrNotFound
    }
    // Use the public Cancel API; operator id surfaces in audit.
    err = s.shipments.Cancel(ctx, req.ShipmentID, shipments.CancelRequest{
        Reason:     req.Operator.Reason,
        OperatorID: &req.Operator.UserID,
    })
    if errors.Is(err, shipments.ErrInvalidState) {
        // Force-mode: bypass state guard and write the row directly.
        if err := s.shipments.ForceCancelInternal(ctx, req.ShipmentID, req.Operator.UserID, req.Operator.Reason); err != nil {
            return err
        }
    } else if err != nil {
        return err
    }
    if req.AlsoRefundWallet && sh.ChargesPaise > 0 {
        if err := s.wallet.Post(ctx, wallet.PostRequest{
            SellerID:    sh.SellerID,
            AmountPaise: core.Paise(sh.ChargesPaise),
            RefType:     "shipment_force_cancel_refund",
            RefID:       req.ShipmentID.String(),
            Direction:   wallet.DirectionCredit,
            Reason:      "operator_force_cancel:" + req.Operator.Reason,
        }); err != nil {
            return err
        }
    }
    return s.audit.Emit(ctx, nil, audit.Event{
        SellerID: sh.SellerID,
        Actor:    audit.ActorUser(req.Operator.UserID),
        Action:   "admin.shipment.force_cancel",
        Object:   audit.ObjShipment(req.ShipmentID),
        Payload:  map[string]any{"reason": req.Operator.Reason, "also_refunded": req.AlsoRefundWallet},
    })
}
```

### Example: RedriveJob

```go
func (s *service) RedriveJob(ctx context.Context, req RedriveJobRequest) error {
    if err := validateOperator(req.Operator); err != nil {
        return err
    }
    if err := s.Authorize(ctx, req.Operator.UserID, CapRedriveJob); err != nil {
        return err
    }
    if err := s.river.JobRetry(ctx, req.JobID); err != nil {
        return err
    }
    return s.audit.Emit(ctx, nil, audit.Event{
        Actor:    audit.ActorUser(req.Operator.UserID),
        Action:   "admin.job.redrive",
        Payload:  map[string]any{"job_id": req.JobID, "reason": req.Operator.Reason},
    })
}
```

## Operator Audit Trail

The audit service (LLD §03-services/02) writes to `operator_action_audit`. It's a separate table from `audit_event` because the integrity model differs: operator actions are not seller-chained, and they're queried cross-seller for compliance / investigation purposes.

```sql
-- (defined in audit LLD)
CREATE TABLE operator_action_audit (
    id              bigserial    PRIMARY KEY,
    operator_id     uuid         NOT NULL REFERENCES app_user(id),
    action          text         NOT NULL,
    seller_id       uuid,
    object_type     text,
    object_id       text,
    payload         jsonb        NOT NULL,
    request_id      text,
    ip              inet,
    user_agent      text,
    created_at      timestamptz  NOT NULL DEFAULT now()
);
```

## Handlers

Every admin endpoint mounts under `/admin` and is gated by:

1. The standard auth middleware (operator must be authenticated).
2. A `RequireOperatorRole` middleware (admin or operator) that fast-fails non-operators.
3. Standard request_id and request-body logging with PII redaction.

```go
func MountAdminRoutes(r chi.Router, svc Service, auth auth.Authenticator) {
    r.With(auth.Middleware, RequireOperatorRole(auth)).Route("/admin", func(r chi.Router) {
        r.Post("/sellers/{seller_id}/suspend",   makeSuspendHandler(svc))
        r.Post("/sellers/{seller_id}/reinstate", makeReinstateHandler(svc))
        r.Post("/sellers/{seller_id}/wind_down", makeWindDownHandler(svc))

        r.Post("/wallets/{seller_id}/adjust", makeAdjustWalletHandler(svc))
        r.Get("/wallets/{seller_id}/statement", makeWalletStatementHandler(svc))

        r.Post("/shipments/{shipment_id}/force_cancel", makeForceCancelShipmentHandler(svc))
        r.Post("/orders/{order_id}/force_cancel",       makeForceCancelOrderHandler(svc))

        r.Post("/users/{user_id}/lock",   makeLockUserHandler(svc))
        r.Post("/users/{user_id}/unlock", makeUnlockUserHandler(svc))

        r.Post("/jobs/{job_id}/redrive", makeRedriveJobHandler(svc))
        r.Get("/jobs/stuck",             makeStuckJobsHandler(svc))
    })
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `Authorize` | 0.5 ms | 2 ms | role lookup (cached) |
| `AdjustWallet` | 12 ms | 35 ms | wallet Post + audit |
| `ForceCancelShipment` | 10 ms | 30 ms | shipment Cancel + (optional) wallet refund + audit |
| `RedriveJob` | 8 ms | 25 ms | river retry + audit |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Non-operator hits an admin endpoint | `RequireOperatorRole` middleware | 403; audit log entry "non_operator_admin_attempt". |
| Operator with insufficient role for capability | `Authorize` returns ErrInsufficient | 403 + audit. |
| Reason too short | validateOperator | 422; no mutation. |
| Underlying service rejects (e.g., invalid state) | error propagated | surface to operator UI; no audit row written for the mutation (only for the attempt). |
| Wallet adjust would underflow | wallet rejects | adjust fails; explicit error to operator. |
| Operator session hijacked | Identity service revokes session globally | next call fails auth; session blacklist (LLD §02-infra/06-auth). |

## Testing

```go
func TestAuthorize_Operator_Allowed(t *testing.T) { /* ... */ }
func TestAuthorize_Operator_DeniedAdminCap(t *testing.T) { /* ... */ }
func TestAuthorize_NonOperator_Rejected(t *testing.T) { /* ... */ }

func TestValidateOperator_ShortReasonRejected(t *testing.T) { /* ... */ }

func TestSuspendSeller_HappyPath_SLT(t *testing.T) {
    // Admin operator suspends; seller goes to suspended; audit row exists with
    // operator id and reason.
}
func TestAdjustWallet_PositiveCredit_SLT(t *testing.T) { /* ... */ }
func TestAdjustWallet_NegativeDebit_SLT(t *testing.T) { /* ... */ }
func TestAdjustWallet_RoleOperatorRejected_SLT(t *testing.T) { /* ... */ }
func TestForceCancelShipment_AlsoRefunds_SLT(t *testing.T) { /* ... */ }
func TestRedriveJob_HappyPath_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Approval workflows for high-impact actions.** Wallet adjustments above ₹10,000 currently go straight through. **Decision: ship as-is for v0**; add two-person approve at v0.5 by introducing a `pending_admin_action` table.
2. **Action allowlists per operator.** Today role-level granularity. **Decision:** add per-user capability overrides at v0.5 if compliance asks.
3. **Read-only audit search UI.** Operators want to query "what did I do today?" **Decision:** part of the dashboard frontend, not this service; this service exposes search endpoints.
4. **Bulk operations.** Suspend 50 sellers at once for a fraud sweep. **Decision:** out of scope for v0; do via script with per-action confirmation.
5. **Ops jail / break-glass.** A future "emergency, please grant me admin for 1 hour" flow. **Decision:** out of scope.

## References

- LLD §03-services/02-audit: `operator_action_audit` schema and the dual-table design.
- LLD §03-services/05-wallet: `wallet.Post` semantics.
- LLD §03-services/08-identity: operator role storage + `GetOperatorRole`.
- LLD §03-services/13-shipments: `ForceCancelInternal` escape-hatch.
- LLD §02-infrastructure/04-http-server: middleware integration.
