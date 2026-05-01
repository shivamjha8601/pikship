# Service: Audit (`internal/audit`)

> Append-only event log; per-seller hash-chained for tamper evidence; sync emit for high-value events, async for the rest.

## Purpose

- `Emit(ctx, event)` — synchronous emit for high-value events (must commit with originating tx).
- `EmitAsync(ctx, event)` — outbox-routed for everything else.
- Per-seller hash chain for tamper evidence on financial/identity events.
- Verification job that recomputes chains daily.

## Dependencies

- `internal/core`
- `internal/observability`
- `internal/observability/dbtx`
- `internal/outbox` (for async path)

## Package layout

```
internal/audit/
├── doc.go
├── service.go         ← Emitter interface
├── service_impl.go
├── repo.go
├── types.go           ← Event, Actor, Target
├── chain.go           ← hash computation + verification
├── high_value.go      ← list of categories that emit synchronously
├── jobs.go            ← daily verification
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package audit provides an append-only, hash-chained event log.
//
// High-value events emit synchronously inside the originating DB transaction
// — guaranteeing audit-or-rollback. Lower-value events emit async via outbox.
//
// Per-seller hash chains let us export a verifiable history.
package audit

import (
    "context"
    "encoding/json"
    "errors"
    "time"

    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Emitter is the public API.
type Emitter interface {
    // Emit writes the event synchronously inside the provided transaction.
    //
    // Use for high-value events: financial mutations, KYC decisions, ops
    // privileged actions, contract changes. The audit row commits with the
    // domain change; if either fails, both roll back.
    //
    // For Action values not in HighValueActions, this returns an error to
    // prevent accidental bloat. Use EmitAsync instead.
    Emit(ctx context.Context, tx pgx.Tx, event Event) error

    // EmitAsync writes the event via the outbox; the outbox forwarder
    // dispatches to the audit consumer.
    //
    // For lower-value events: status changes, login events, view actions.
    EmitAsync(ctx context.Context, event Event) error
}

// Event is an audit record.
type Event struct {
    ID         core.AuditEventID  // generated if zero
    SellerID   *core.SellerID     // nil for platform events
    Actor      Actor
    Action     string             // dotted; e.g., 'wallet.charged'
    Target     Target
    Payload    map[string]any     // arbitrary; serialized to JSONB
    OccurredAt time.Time          // defaults to now if zero
}

// Actor identifies who performed the action.
type Actor struct {
    Kind ActorKind
    Ref  string  // e.g., user_id, "system", "scheduled_job"
    ImpersonatedBy *core.UserID  // optional
    IPAddress      string
    UserAgent      string
}

// ActorKind classifies the actor.
type ActorKind string

const (
    ActorSellerUser     ActorKind = "seller_user"
    ActorPikshippAdmin  ActorKind = "pikshipp_admin"
    ActorPikshippOps    ActorKind = "pikshipp_ops"
    ActorPikshippSupport ActorKind = "pikshipp_support"
    ActorSystem          ActorKind = "system"
    ActorScheduledJob    ActorKind = "scheduled_job"
    ActorAPIKey          ActorKind = "api_key"
    ActorWebhook         ActorKind = "webhook"
)

// Target identifies what was affected.
type Target struct {
    Kind string  // e.g., 'wallet_account', 'shipment', 'policy_setting'
    Ref  string  // e.g., the entity ID or key
}

// Sentinel errors.
var (
    ErrNotHighValue = errors.New("audit: action is not in HighValueActions; use EmitAsync")
)
```

## Types

```go
// internal/audit/types.go
package audit

import (
    "context"

    "github.com/pikshipp/pikshipp/internal/auth"
    "github.com/pikshipp/pikshipp/internal/observability"
)

// ActorFromContext extracts an Actor from the request context.
//
// Used by domain code that calls Emit/EmitAsync without manually constructing
// an Actor.
func ActorFromContext(ctx context.Context) Actor {
    p, ok := auth.PrincipalFrom(ctx)
    if !ok {
        return Actor{Kind: ActorSystem, Ref: "no-principal"}
    }
    var kind ActorKind
    switch p.UserKind {
    case auth.UserKindSeller:           kind = ActorSellerUser
    case auth.UserKindPikshippAdmin:    kind = ActorPikshippAdmin
    case auth.UserKindPikshippOps:      kind = ActorPikshippOps
    case auth.UserKindPikshippSupport:  kind = ActorPikshippSupport
    default:                            kind = ActorSystem
    }
    return Actor{
        Kind:      kind,
        Ref:       p.UserID.String(),
        // IP / UA could be in context too if middleware set them
    }
}
```

## High-value action list

```go
// internal/audit/high_value.go
package audit

// HighValueActions is the set of actions that REQUIRE synchronous (in-tx) emit.
//
// Any action prefixed with these is in the set:
var HighValueActions = []string{
    // Financial — every wallet movement
    "wallet.",                  // wallet.charged, wallet.refunded, wallet.adjusted, ...
    "cod.remitted",             // cash flowing
    "weight_dispute.",          // money on the line

    // Identity / lifecycle
    "seller.kyc_",              // KYC decisions
    "seller.suspended",
    "seller.reactivated",
    "seller.wound_down",
    "user.role_granted",
    "user.role_revoked",
    "user.locked",

    // Privileged ops
    "ops.manual_adjustment",    // manual ledger adjustment
    "ops.kyc_override",
    "ops.cross_seller_view",    // Pikshipp staff impersonating / viewing seller

    // Policy & contract (drives runtime behavior)
    "policy.lock_set",
    "policy.lock_removed",
    "policy.seller_type_default_changed",
    "contract.signed",
    "contract.terminated",
    "contract.amended",

    // Carrier credentials
    "carrier.credential_rotated",
}

// IsHighValue reports whether action requires sync emit.
func IsHighValue(action string) bool {
    for _, prefix := range HighValueActions {
        if action == prefix || (len(prefix) > 0 && prefix[len(prefix)-1] == '.' && hasPrefix(action, prefix)) {
            return true
        }
    }
    return false
}

func hasPrefix(s, p string) bool {
    return len(s) >= len(p) && s[:len(p)] == p
}
```

## Implementation

```go
// internal/audit/service_impl.go
package audit

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/outbox"
)

type emitterImpl struct {
    repo     *repo
    outbox   outbox.Emitter
    clock    core.Clock
    log      *slog.Logger
}

func New(pool *pgxpool.Pool, ob outbox.Emitter, clock core.Clock, log *slog.Logger) Emitter {
    return &emitterImpl{
        repo:   newRepo(pool),
        outbox: ob,
        clock:  clock,
        log:    log,
    }
}

func (e *emitterImpl) Emit(ctx context.Context, tx pgx.Tx, event Event) error {
    if !IsHighValue(event.Action) {
        return fmt.Errorf("audit.Emit %s: %w", event.Action, ErrNotHighValue)
    }

    // Fill defaults
    if event.ID.IsZero() {
        event.ID = core.AuditEventID(uuid.New())
    }
    if event.OccurredAt.IsZero() {
        event.OccurredAt = e.clock.Now()
    }
    if event.Actor.Kind == "" {
        event.Actor = ActorFromContext(ctx)
    }

    // Compute hash chain (per seller; or platform chain if SellerID nil)
    prevHash, err := e.repo.GetLastChainHashTx(ctx, tx, event.SellerID)
    if err != nil {
        return fmt.Errorf("audit.Emit get prev hash: %w", err)
    }
    eventHash := computeEventHash(event, prevHash)

    // Insert
    err = e.repo.InsertEventTx(ctx, tx, eventRow{
        ID:         event.ID,
        SellerID:   event.SellerID,
        ActorJSONB: marshalActor(event.Actor),
        Action:     event.Action,
        TargetJSONB: marshalTarget(event.Target),
        PayloadJSONB: marshalPayload(event.Payload),
        OccurredAt: event.OccurredAt,
        PrevHash:   prevHash,
        EventHash:  eventHash,
    })
    if err != nil {
        return fmt.Errorf("audit.Emit insert: %w", err)
    }

    return nil
}

func (e *emitterImpl) EmitAsync(ctx context.Context, event Event) error {
    if event.ID.IsZero() {
        event.ID = core.AuditEventID(uuid.New())
    }
    if event.OccurredAt.IsZero() {
        event.OccurredAt = e.clock.Now()
    }
    if event.Actor.Kind == "" {
        event.Actor = ActorFromContext(ctx)
    }

    // Best effort: persist via outbox for the audit consumer.
    // We open a tiny tx to write the outbox row.
    return e.repo.WithTx(ctx, func(tx pgx.Tx) error {
        return e.outbox.Emit(ctx, tx, outbox.Event{
            ID:       uuid.New(),
            SellerID: event.SellerID,
            Kind:     "audit.write",
            Payload:  marshalEvent(event),
            OccurredAt: event.OccurredAt,
        })
    })
}
```

## Hash chain

```go
// internal/audit/chain.go
package audit

import (
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "sort"
)

// computeEventHash returns base64-url-encoded SHA-256 over a canonicalized
// event representation chained from prevHash.
//
// Canonicalization: sorted JSON keys, fixed field order, RFC3339Nano time.
func computeEventHash(e Event, prevHash string) string {
    h := sha256.New()
    h.Write([]byte(prevHash))
    h.Write([]byte(e.ID.String()))
    if e.SellerID != nil {
        h.Write([]byte(e.SellerID.String()))
    }
    h.Write([]byte(e.Action))
    h.Write([]byte(e.Target.Kind))
    h.Write([]byte(e.Target.Ref))
    h.Write([]byte(e.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000000Z")))
    h.Write(canonicalJSON(e.Payload))
    h.Write(canonicalJSON(map[string]any{
        "kind": e.Actor.Kind,
        "ref":  e.Actor.Ref,
        // Note: ImpersonatedBy intentionally NOT in hash to allow late discovery
        //       of impersonation provenance without breaking chain. Decide at LLD review.
    }))
    return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// canonicalJSON returns sorted-keys JSON; deterministic for hashing.
func canonicalJSON(v any) []byte {
    if v == nil {
        return []byte("null")
    }
    // Marshal then re-marshal sorted (json.Marshal sorts map keys for maps,
    // but for nested objects we need a custom sort).
    raw, _ := json.Marshal(v)
    var anyVal any
    _ = json.Unmarshal(raw, &anyVal)
    sorted, _ := json.Marshal(sortDeep(anyVal))
    return sorted
}

func sortDeep(v any) any {
    switch t := v.(type) {
    case map[string]any:
        keys := make([]string, 0, len(t))
        for k := range t {
            keys = append(keys, k)
        }
        sort.Strings(keys)
        out := make(map[string]any, len(t))
        for _, k := range keys {
            out[k] = sortDeep(t[k])
        }
        return out
    case []any:
        out := make([]any, len(t))
        for i, x := range t {
            out[i] = sortDeep(x)
        }
        return out
    default:
        return v
    }
}

// VerifyChain recomputes hashes for the seller's chain and reports any mismatch.
func VerifyChain(events []eventRow) error {
    var prev string
    for i, e := range events {
        // Re-fabricate Event from row
        evt := rowToEvent(e)
        expected := computeEventHash(evt, prev)
        if expected != e.EventHash {
            return fmt.Errorf("audit chain broken at index %d (id=%s): expected %s, got %s",
                i, e.ID, expected, e.EventHash)
        }
        prev = e.EventHash
    }
    return nil
}
```

## DB schema

```sql
-- migrations/00NN_create_audit.up.sql

CREATE TABLE audit_event (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id    UUID,                          -- NULL for platform events
    actor_jsonb  JSONB NOT NULL,
    action       TEXT NOT NULL,
    target_jsonb JSONB NOT NULL,
    payload_jsonb JSONB NOT NULL DEFAULT '{}',
    occurred_at  TIMESTAMPTZ NOT NULL,
    recorded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    prev_hash    TEXT NOT NULL DEFAULT '',
    event_hash   TEXT NOT NULL,

    -- Linear order per seller (or platform-wide for NULL seller)
    seq          BIGINT NOT NULL
);

CREATE INDEX audit_event_seller_seq_idx ON audit_event (seller_id, seq);
CREATE INDEX audit_event_action_idx     ON audit_event (action);
CREATE INDEX audit_event_occurred_idx   ON audit_event (occurred_at);

-- Sequence per seller for chain ordering
-- Implemented via subquery in INSERT (see queries below)

ALTER TABLE audit_event ENABLE ROW LEVEL SECURITY;
CREATE POLICY audit_event_seller ON audit_event
    FOR SELECT TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT ON audit_event TO pikshipp_app;
GRANT SELECT ON audit_event TO pikshipp_reports;
GRANT ALL ON audit_event TO pikshipp_admin;

-- Operator action audit (cross-seller; separate chain)
CREATE TABLE operator_action_audit (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_jsonb  JSONB NOT NULL,
    action       TEXT NOT NULL,
    target_jsonb JSONB NOT NULL,
    payload_jsonb JSONB NOT NULL DEFAULT '{}',
    occurred_at  TIMESTAMPTZ NOT NULL,
    affected_seller_count INT,
    prev_hash    TEXT NOT NULL DEFAULT '',
    event_hash   TEXT NOT NULL,
    seq          BIGINT NOT NULL
);

CREATE INDEX operator_action_seq_idx ON operator_action_audit (seq);
GRANT SELECT, INSERT ON operator_action_audit TO pikshipp_admin;
GRANT SELECT ON operator_action_audit TO pikshipp_ops;
```

## SQL queries

```sql
-- query/audit.sql

-- name: GetLastSellerChainHash :one
SELECT COALESCE(event_hash, '') AS prev_hash, COALESCE(MAX(seq), 0) AS last_seq
FROM audit_event
WHERE seller_id = $1
ORDER BY seq DESC
LIMIT 1;

-- name: GetLastPlatformChainHash :one
SELECT COALESCE(event_hash, '') AS prev_hash, COALESCE(MAX(seq), 0) AS last_seq
FROM audit_event
WHERE seller_id IS NULL
ORDER BY seq DESC
LIMIT 1;

-- name: InsertSellerAuditEvent :exec
INSERT INTO audit_event (id, seller_id, actor_jsonb, action, target_jsonb, payload_jsonb, occurred_at, prev_hash, event_hash, seq)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
    COALESCE((SELECT MAX(seq) + 1 FROM audit_event WHERE seller_id = $2), 1));

-- name: InsertPlatformAuditEvent :exec
INSERT INTO audit_event (id, actor_jsonb, action, target_jsonb, payload_jsonb, occurred_at, prev_hash, event_hash, seq)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
    COALESCE((SELECT MAX(seq) + 1 FROM audit_event WHERE seller_id IS NULL), 1));

-- name: ListSellerEventsForVerification :many
SELECT id, seller_id, actor_jsonb, action, target_jsonb, payload_jsonb, occurred_at, prev_hash, event_hash, seq
FROM audit_event
WHERE seller_id = $1
ORDER BY seq ASC;
```

## Daily verification job

```go
// internal/audit/jobs.go
package audit

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/riverqueue/river"
)

type VerifyChainsArgs struct{}

func (VerifyChainsArgs) Kind() string { return "audit.verify_chains" }

type VerifyChainsWorker struct {
    river.WorkerDefaults[VerifyChainsArgs]
    repo *repo
    log  *slog.Logger
}

// Work iterates all sellers (and the platform chain) and verifies hash integrity.
//
// Runs weekly. On any failure: P0 alert; chain is "broken" — manual investigation.
func (w *VerifyChainsWorker) Work(ctx context.Context, j *river.Job[VerifyChainsArgs]) error {
    sellers, err := w.repo.ListAllSellerIDsForVerification(ctx)
    if err != nil {
        return fmt.Errorf("audit verify: list sellers: %w", err)
    }

    var failures []string
    for _, sid := range sellers {
        events, err := w.repo.ListSellerEventsForVerification(ctx, sid)
        if err != nil {
            w.log.WarnContext(ctx, "audit verify list events failed", slog.Any("seller", sid))
            continue
        }
        if err := VerifyChain(events); err != nil {
            failures = append(failures, fmt.Sprintf("seller=%s: %v", sid, err))
        }
    }

    if len(failures) > 0 {
        w.log.ErrorContext(ctx, "audit chain verification failures",
            slog.Int("count", len(failures)),
            slog.Any("failures", failures))
        return fmt.Errorf("%d chains failed verification", len(failures))
    }

    w.log.InfoContext(ctx, "audit chains verified", slog.Int("sellers", len(sellers)))
    return nil
}
```

## Outbox consumer

```go
// internal/audit/outbox_consumer.go
package audit

// AuditWriteArgs is the river job that writes async audit events.
type AuditWriteArgs struct {
    Event Event
}

func (AuditWriteArgs) Kind() string { return "audit.write" }

type AuditWriteWorker struct {
    river.WorkerDefaults[AuditWriteArgs]
    repo *repo
}

func (w *AuditWriteWorker) Work(ctx context.Context, j *river.Job[AuditWriteArgs]) error {
    return w.repo.WithTx(ctx, func(tx pgx.Tx) error {
        e := j.Args.Event
        prevHash, err := w.repo.GetLastChainHashTx(ctx, tx, e.SellerID)
        if err != nil { return err }
        eventHash := computeEventHash(e, prevHash)
        return w.repo.InsertEventTx(ctx, tx, eventRow{...})
    })
}
```

## Testing

```go
func TestEmitter_HighValueSyncEmit_SLT(t *testing.T) {
    p := testdb.New(t)
    e := audit.New(p.App, mockOutbox{}, core.NewFakeClock(time.Now()), slog.Default())

    sid := core.NewSellerID()
    err := dbtx.WithSellerTx(context.Background(), p.App, sid, func(ctx context.Context, tx pgx.Tx) error {
        return e.Emit(ctx, tx, audit.Event{
            SellerID: &sid,
            Actor:    audit.Actor{Kind: audit.ActorPikshippOps, Ref: "u_ops_1"},
            Action:   "wallet.adjusted",
            Target:   audit.Target{Kind: "wallet_account", Ref: sid.String()},
            Payload:  map[string]any{"amount_minor": 5000, "reason": "promo"},
        })
    })
    require.NoError(t, err)

    events := readAuditEvents(t, p.App, sid)
    require.Len(t, events, 1)
    require.Equal(t, "wallet.adjusted", events[0].Action)
    require.NotEmpty(t, events[0].EventHash)
}

func TestEmitter_LowValueRejected(t *testing.T) {
    e := audit.New(...)
    err := e.Emit(ctx, tx, audit.Event{Action: "ui.button_clicked"})
    require.ErrorIs(t, err, audit.ErrNotHighValue)
}

func TestVerifyChain_DetectsTamper_SLT(t *testing.T) {
    // Emit 3 events
    // Manually tamper one row's payload via raw SQL
    // Run VerifyChain
    // Expect error
}

func BenchmarkEmit(b *testing.B) {
    // ... setup ...
    for i := 0; i < b.N; i++ {
        _ = dbtx.WithSellerTx(ctx, pool, sid, func(ctx context.Context, tx pgx.Tx) error {
            return e.Emit(ctx, tx, sampleEvent)
        })
    }
}
```

## Performance

- `Emit` overhead beyond the encompassing tx: 1 SELECT (last hash) + 1 INSERT; ~3ms.
- Hash computation: SHA-256 over <1KB payload; ~10µs.
- `EmitAsync`: 1 outbox INSERT; ~2ms.
- `VerifyChain`: O(N) per seller; for ~10k events at v1, ~500ms per seller. Acceptable for weekly.

## Open questions

- **Impersonation in hash**: should the hash include `Actor.ImpersonatedBy`? Currently no, to allow back-filling. Trade-off: tampering with impersonation field is undetectable; pro: legit impersonation backfill is easy. Decide at LLD review (lean: include).
- **Per-seller seq counter**: implemented via subquery in INSERT. At very high concurrent insert rate per seller (>1000 ops/sec), this serializes. Mitigated: per-seller chain rarely sees that rate. If it does, switch to per-seller sequence object.
- **Cold storage** of old events (>1 year): not at v0. v1 may add S3 archival.

## References

- HLD `05-cross-cutting/06-audit-and-change-log.md`.
- HLD `01-architecture/05-domain-event-catalog.md` (events that go to audit).
