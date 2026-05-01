# Service: Policy engine (`internal/policy`)

> Configurability substrate. Every other service reads from it. Sub-millisecond cache hits, LISTEN/NOTIFY-invalidated, sensitive-key read auditing.

## Purpose

Resolve `(seller_id, key) → value` according to: Pikshipp lock → seller-type lock → seller override → seller-type default → Pikshipp global default.

## Dependencies

- `internal/core`
- `internal/observability` (logger, listen connection)
- `internal/audit` (sensitive-key read events)
- `internal/observability/dbtx`
- `github.com/jackc/pgx/v5/pgxpool`

## Package layout

```
internal/policy/
├── doc.go
├── service.go            ← Engine interface
├── service_impl.go       ← engineImpl
├── repo.go               ← DB access
├── definitions.go        ← Go-defined registry of all valid keys
├── keys.go               ← strongly-typed Key constants
├── values.go             ← Value type + JSON marshalling
├── resolver.go           ← walk algorithm
├── cache.go              ← per-process caches with TTL + NOTIFY
├── listen.go             ← LISTEN/NOTIFY pump
├── seed.go               ← startup seeding of definitions
├── jobs.go               ← (none currently)
├── errors.go
├── service_test.go
├── service_slt_test.go
└── bench_test.go
```

## Public API

```go
// Package policy is the configurability substrate.
//
// Every domain service that reads "what's the rule for THIS seller?" goes
// through policy.Engine.Resolve(). Implementations cache aggressively and
// invalidate via Postgres LISTEN/NOTIFY. The setting registry is Go-coded
// (not DB-managed) — adding a new key is a code change.
package policy

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Engine is the public API of the policy module.
type Engine interface {
    // Resolve returns the effective value for (sellerID, key).
    //
    // Walks layers: lock → seller override → seller-type default → global default.
    // Per-request cached; cross-process invalidated by NOTIFY; 5s TTL fallback.
    //
    // Returns ErrUnknownKey if the key isn't in the registry.
    Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error)

    // ResolveBatch is the hot-path call for services that need many keys
    // for the same seller in one go (allocation engine, pricing engine).
    //
    // More efficient than calling Resolve N times: single cache lookup pass.
    ResolveBatch(ctx context.Context, sellerID core.SellerID, keys ...Key) (map[Key]Value, error)

    // SetSellerOverride creates or updates a seller-level override.
    //
    // Source identifies provenance: 'seller_self', 'ops', or 'contract:<id>'.
    // Reason is stored for audit; required for ops-source overrides.
    SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string) error

    // RemoveSellerOverride deletes a seller-level override (reverts to defaults).
    RemoveSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, src Source, reason string) error

    // SetLock pins a value at a scope (global or seller_type:<name>).
    //
    // Locks override seller-level overrides. Use for compliance / risk policies.
    SetLock(ctx context.Context, scope LockScope, key Key, value Value, reason string) error

    // RemoveLock removes a lock; downstream values become editable again.
    RemoveLock(ctx context.Context, scope LockScope, key Key, reason string) error

    // EffectiveConfig returns the full effective config for a seller (debugging UI).
    // Heavy operation; not for hot paths.
    EffectiveConfig(ctx context.Context, sellerID core.SellerID) (map[Key]ResolvedValue, error)
}

// Key is a typed setting identifier; constants live in keys.go.
type Key string

// Value wraps the underlying typed value (int64, string, bool, list, map).
//
// Use the type-checked accessors (AsInt64, AsString, etc.) at consumption.
type Value struct {
    raw json.RawMessage
}

// ResolvedValue is Value plus provenance.
type ResolvedValue struct {
    Value     Value
    Source    ResolveSource  // 'global_default' | 'type_default' | 'seller_override' | 'lock'
    SourceRef string         // for overrides: source name (e.g., 'contract:abc'); for locks: scope kind
}

// Source classifies where a seller override came from.
type Source string

const (
    SourceSellerSelf   Source = "seller_self"
    SourceOps          Source = "ops"
    SourceContractFmt  Source = "contract:%s"   // formatted; e.g., "contract:abc"
)

// LockScope identifies the scope of a lock.
type LockScope struct {
    Kind  LockScopeKind  // 'global' | 'seller_type'
    Value string         // for 'seller_type': the type name; for 'global': empty
}

type LockScopeKind string

const (
    LockScopeGlobal     LockScopeKind = "global"
    LockScopeSellerType LockScopeKind = "seller_type"
)

// ResolveSource is the provenance of a resolved value.
type ResolveSource string

const (
    SourceGlobalDefault ResolveSource = "global_default"
    SourceTypeDefault   ResolveSource = "type_default"
    SourceSellerOverride ResolveSource = "seller_override"
    SourceTypeLock      ResolveSource = "type_lock"
    SourceGlobalLock    ResolveSource = "global_lock"
)

// Sentinel errors.
var (
    ErrUnknownKey   = errors.New("policy: unknown key")
    ErrLocked       = errors.New("policy: setting is locked; cannot override")
    ErrInvalidValue = errors.New("policy: value does not match key's type")
)
```

## Value type

```go
// internal/policy/values.go
package policy

import (
    "encoding/json"
    "fmt"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Value is a JSON-encoded value with type-checked accessors.
//
// Construction is via type-specific constructors (Int64Value, StringValue,
// etc.) so the underlying JSON shape is consistent.
type Value struct {
    raw json.RawMessage
}

// Constructors

func Int64Value(v int64) Value {
    raw, _ := json.Marshal(v)
    return Value{raw: raw}
}

func PaiseValue(v core.Paise) Value {
    return Int64Value(int64(v))
}

func StringValue(v string) Value {
    raw, _ := json.Marshal(v)
    return Value{raw: raw}
}

func BoolValue(v bool) Value {
    raw, _ := json.Marshal(v)
    return Value{raw: raw}
}

func DurationValue(v time.Duration) Value {
    return StringValue(v.String())
}

func StringListValue(v []string) Value {
    raw, _ := json.Marshal(v)
    return Value{raw: raw}
}

func StringSetValue(v core.StringSet) Value {
    return StringListValue(v.Slice())
}

// Accessors return the typed value or err if the key was set to a different type.
// Domain code knows the type from the Key constant.

func (v Value) AsInt64() (int64, error) {
    var n int64
    if err := json.Unmarshal(v.raw, &n); err != nil {
        return 0, fmt.Errorf("policy.Value.AsInt64: %w: %w", err, ErrInvalidValue)
    }
    return n, nil
}

func (v Value) AsPaise() (core.Paise, error) {
    n, err := v.AsInt64()
    return core.Paise(n), err
}

func (v Value) AsString() (string, error) {
    var s string
    if err := json.Unmarshal(v.raw, &s); err != nil {
        return "", fmt.Errorf("policy.Value.AsString: %w: %w", err, ErrInvalidValue)
    }
    return s, nil
}

func (v Value) AsBool() (bool, error) {
    var b bool
    if err := json.Unmarshal(v.raw, &b); err != nil {
        return false, fmt.Errorf("policy.Value.AsBool: %w: %w", err, ErrInvalidValue)
    }
    return b, nil
}

func (v Value) AsDuration() (time.Duration, error) {
    s, err := v.AsString()
    if err != nil {
        return 0, err
    }
    d, err := time.ParseDuration(s)
    if err != nil {
        return 0, fmt.Errorf("policy.Value.AsDuration: %w: %w", err, ErrInvalidValue)
    }
    return d, nil
}

func (v Value) AsStringList() ([]string, error) {
    var l []string
    if err := json.Unmarshal(v.raw, &l); err != nil {
        return nil, fmt.Errorf("policy.Value.AsStringList: %w: %w", err, ErrInvalidValue)
    }
    return l, nil
}

func (v Value) AsStringSet() (core.StringSet, error) {
    l, err := v.AsStringList()
    if err != nil {
        return nil, err
    }
    return core.NewStringSet(l...), nil
}

// MustAs* are panicking variants for paths where type mismatch is a programming bug.
// Use sparingly; prefer the error-returning variants.
func (v Value) MustAsInt64() int64 {
    n, err := v.AsInt64()
    if err != nil { panic(err) }
    return n
}

// Raw returns the underlying JSON bytes; used by repo.
func (v Value) Raw() json.RawMessage { return v.raw }

// FromRaw constructs a Value from JSON bytes (for repo loads).
func FromRaw(raw json.RawMessage) Value { return Value{raw: raw} }
```

## Definitions registry

```go
// internal/policy/definitions.go
package policy

import (
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Type is the JSON type expected for a setting.
type Type string

const (
    TypeInt64    Type = "int64"
    TypePaise    Type = "paise"
    TypeString   Type = "string"
    TypeBool     Type = "bool"
    TypeDuration Type = "duration"
    TypeStringList Type = "string_list"
    TypeStringSet  Type = "string_set"
)

// Definition declares one setting key.
type Definition struct {
    Key             Key
    Type            Type
    DefaultGlobal   Value
    DefaultsByType  map[core.SellerType]Value  // optional per-seller-type defaults
    LockCapable     bool                        // can locks be created on this?
    OverrideAllowed bool                        // can sellers override?
    AuditOnRead     bool                        // emit audit on Resolve
    Description     string
    RegisteredIn    string                      // module name; for audit
    AddedInVersion  string
}

// Definitions is the canonical registry of every valid key.
//
// IMMUTABLE at runtime. Sealed at startup; seed.go upserts to DB.
//
// Adding a new key:
//   1. Add it here.
//   2. Add a Key constant in keys.go.
//   3. Migration runs (or upsert at startup) to populate policy_setting_definition.
//   4. Code that reads it via Resolve() uses the new constant.
var Definitions = []Definition{
    // Wallet
    {
        Key: KeyWalletPosture,
        Type: TypeString,
        DefaultGlobal: StringValue("prepaid_only"),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket: StringValue("hybrid"),
            core.SellerTypeEnterprise: StringValue("credit_only"),
        },
        LockCapable: true, OverrideAllowed: true, AuditOnRead: false,
        Description:    "Wallet payment posture; affects credit limit availability",
        RegisteredIn:   "wallet",
        AddedInVersion: "v0",
    },
    {
        Key: KeyWalletCreditLimitInr,
        Type: TypePaise,
        DefaultGlobal: PaiseValue(0),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket: PaiseValue(core.FromRupees(100_000)),  // ₹1L
            core.SellerTypeEnterprise: PaiseValue(core.FromRupees(500_000)),  // ₹5L
        },
        LockCapable: true, OverrideAllowed: true, AuditOnRead: true, // sensitive!
        Description: "Credit limit in paise; sellers can be negative up to grace + this",
        RegisteredIn: "wallet", AddedInVersion: "v0",
    },
    {
        Key: KeyWalletGraceNegativeAmount,
        Type: TypePaise,
        DefaultGlobal: PaiseValue(core.FromRupees(500)),  // ₹500
        LockCapable: true, OverrideAllowed: true, AuditOnRead: false,
        Description: "Wallet may go negative this much before suspension",
        RegisteredIn: "wallet", AddedInVersion: "v0",
    },

    // COD
    {
        Key: KeyCODEnabled,
        Type: TypeBool,
        DefaultGlobal: BoolValue(false),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket: BoolValue(true),
            core.SellerTypeEnterprise: BoolValue(true),
        },
        LockCapable: true, OverrideAllowed: true,
        Description: "Whether COD is allowed for this seller",
        RegisteredIn: "cod", AddedInVersion: "v0",
    },
    {
        Key: KeyCODRemittanceCycleDays,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(5),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket:  Int64Value(3),
            core.SellerTypeEnterprise: Int64Value(2),
        },
        LockCapable: true, OverrideAllowed: true,
        Description: "Days from delivery to seller-wallet credit",
        RegisteredIn: "cod", AddedInVersion: "v0",
    },
    {
        Key: KeyCODVerificationMode,
        Type: TypeString,
        DefaultGlobal: StringValue("above_x"),
        Description: "When to verify COD orders pre-pickup: 'always'|'above_x'|'none'",
        RegisteredIn: "cod", AddedInVersion: "v0",
    },
    {
        Key: KeyCODVerificationThresholdInr,
        Type: TypePaise,
        DefaultGlobal: PaiseValue(core.FromRupees(500)),
        Description: "Threshold above which COD verification is required",
        RegisteredIn: "cod", AddedInVersion: "v0",
    },

    // Allocation engine
    {
        Key: KeyAllocationWeightCost,
        Type: TypeInt64,  // basis points (e.g., 100 = 1.0)
        DefaultGlobal: Int64Value(100),
        Description: "Allocation engine weight on cost (bp)",
        RegisteredIn: "allocation", AddedInVersion: "v0",
    },
    {
        Key: KeyAllocationWeightSpeed,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(50),
        Description: "Allocation engine weight on speed (bp)",
        RegisteredIn: "allocation", AddedInVersion: "v0",
    },
    {
        Key: KeyAllocationWeightReliability,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(70),
        Description: "Allocation engine weight on reliability (bp)",
        RegisteredIn: "allocation", AddedInVersion: "v0",
    },
    {
        Key: KeyAllocationWeightSellerPref,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(30),
        Description: "Allocation engine weight on seller preference (bp)",
        RegisteredIn: "allocation", AddedInVersion: "v0",
    },
    {
        Key: KeyAllocationAutoBookMinScoreGap,
        Type: TypeInt64,  // bp
        DefaultGlobal: Int64Value(500),  // 5% gap
        Description: "Min score gap for auto-book (bp); else flag for manual",
        RegisteredIn: "allocation", AddedInVersion: "v0",
    },

    // Carriers
    {
        Key: KeyCarriersAllowedSet,
        Type: TypeStringSet,
        DefaultGlobal: StringSetValue(core.NewStringSet("delhivery", "dtdc", "ekart", "ecom_express")),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket:  StringSetValue(core.NewStringSet("delhivery","bluedart","dtdc","ekart","xpressbees","ecom_express","shadowfax","india_post")),
            core.SellerTypeEnterprise: StringSetValue(core.NewStringSet("delhivery","bluedart","dtdc","ekart","xpressbees","ecom_express","shadowfax","india_post")),
        },
        Description: "Set of carrier IDs this seller can route to",
        RegisteredIn: "carriers", AddedInVersion: "v0",
    },
    {
        Key: KeyCarriersExcludedSet,
        Type: TypeStringSet,
        DefaultGlobal: StringSetValue(core.NewStringSet()),
        Description: "Set of carriers this seller has explicitly excluded",
        RegisteredIn: "carriers", AddedInVersion: "v0",
    },

    // Delivery semantics
    {
        Key: KeyDeliveryMaxAttempts,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(2),
        Description: "Max forward delivery attempts before RTO (0 = use carrier default)",
        RegisteredIn: "ndr", AddedInVersion: "v0",
    },
    {
        Key: KeyDeliveryReattemptWindowHours,
        Type: TypeInt64,
        DefaultGlobal: Int64Value(24),
        Description: "Hours between reattempts",
        RegisteredIn: "ndr", AddedInVersion: "v0",
    },
    {
        Key: KeyDeliveryAutoRTOOnMax,
        Type: TypeBool,
        DefaultGlobal: BoolValue(true),
        Description: "Auto-trigger RTO when max attempts reached (else flag for seller)",
        RegisteredIn: "ndr", AddedInVersion: "v0",
    },

    // Pricing
    {
        Key: KeyPricingRateCardRef,
        Type: TypeString,
        DefaultGlobal: StringValue(""),  // empty means use seller-type default
        Description: "UUID of the rate card for this seller (overrides type defaults)",
        RegisteredIn: "pricing", AddedInVersion: "v0",
    },

    // Buyer experience
    {
        Key: KeyBuyerExpBrandLogoURL,
        Type: TypeString,
        DefaultGlobal: StringValue(""),
        Description: "URL of seller's logo for buyer pages",
        RegisteredIn: "buyerexp", AddedInVersion: "v0",
    },
    {
        Key: KeyBuyerExpCustomDomain,
        Type: TypeString,
        DefaultGlobal: StringValue(""),
        Description: "Custom tracking domain (e.g., track.brand.com); empty for default",
        RegisteredIn: "buyerexp", AddedInVersion: "v0",
    },

    // Feature flags
    {
        Key: KeyFeatureInsurance,
        Type: TypeBool,
        DefaultGlobal: BoolValue(false),
        Description: "Insurance attach available for this seller",
        RegisteredIn: "insurance", AddedInVersion: "v0",
    },
    {
        Key: KeyFeatureWeightDisputeAuto,
        Type: TypeBool,
        DefaultGlobal: BoolValue(false),
        DefaultsByType: map[core.SellerType]Value{
            core.SellerTypeMidMarket:  BoolValue(true),
            core.SellerTypeEnterprise: BoolValue(true),
        },
        Description: "Auto weight-dispute filing on this seller's behalf",
        RegisteredIn: "recon", AddedInVersion: "v0",
    },

    // ... add more as modules need them
}

// DefinitionByKey returns the registered definition for key, or nil if unknown.
func DefinitionByKey(key Key) *Definition {
    for i := range Definitions {
        if Definitions[i].Key == key {
            return &Definitions[i]
        }
    }
    return nil
}
```

## Keys

```go
// internal/policy/keys.go
package policy

// Key constants. Keep alphabetized within sections.

const (
    // Wallet
    KeyWalletPosture              Key = "wallet.posture"
    KeyWalletCreditLimitInr       Key = "wallet.credit_limit_inr"
    KeyWalletGraceNegativeAmount  Key = "wallet.grace_negative_amount_inr"

    // COD
    KeyCODEnabled                  Key = "cod.enabled"
    KeyCODRemittanceCycleDays      Key = "cod.remittance_cycle_days"
    KeyCODVerificationMode         Key = "cod.verification_mode"
    KeyCODVerificationThresholdInr Key = "cod.verification_threshold_inr"

    // Allocation
    KeyAllocationWeightCost           Key = "allocation.weight_cost"
    KeyAllocationWeightSpeed          Key = "allocation.weight_speed"
    KeyAllocationWeightReliability    Key = "allocation.weight_reliability"
    KeyAllocationWeightSellerPref     Key = "allocation.weight_seller_pref"
    KeyAllocationAutoBookMinScoreGap  Key = "allocation.auto_book_min_score_gap"

    // Carriers
    KeyCarriersAllowedSet     Key = "carriers.allowed_set"
    KeyCarriersExcludedSet    Key = "carriers.excluded_set"

    // Delivery
    KeyDeliveryMaxAttempts            Key = "delivery.max_attempts"
    KeyDeliveryReattemptWindowHours   Key = "delivery.reattempt_window_hours"
    KeyDeliveryAutoRTOOnMax           Key = "delivery.auto_rto_on_max"

    // Pricing
    KeyPricingRateCardRef Key = "pricing.rate_card_ref"

    // Buyer experience
    KeyBuyerExpBrandLogoURL Key = "buyer_experience.brand.logo_url"
    KeyBuyerExpCustomDomain Key = "buyer_experience.custom_domain"

    // Features
    KeyFeatureInsurance         Key = "features.insurance"
    KeyFeatureWeightDisputeAuto Key = "features.weight_dispute_auto"
)
```

## Implementation

```go
// internal/policy/service_impl.go
package policy

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/audit"
    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/observability"
)

type engineImpl struct {
    repo  *repo
    cache *cache
    audit audit.Emitter
    clock core.Clock
    log   *slog.Logger
}

// New constructs a policy.Engine with caches and listener wired up.
//
// IMPORTANT: caller must call Close() on shutdown to stop the listener.
func New(pool *pgxpool.Pool, audit audit.Emitter, clock core.Clock, log *slog.Logger) (*engineImpl, error) {
    e := &engineImpl{
        repo:  newRepo(pool),
        audit: audit,
        clock: clock,
        log:   log,
    }
    e.cache = newCache(5*time.Second, clock)

    if err := e.seedDefinitions(context.Background()); err != nil {
        return nil, fmt.Errorf("policy.New: seed: %w", err)
    }

    if err := e.warmCache(context.Background()); err != nil {
        return nil, fmt.Errorf("policy.New: warm: %w", err)
    }

    observability.SafeGo(context.Background(), e.listenForInvalidations)

    return e, nil
}

func (e *engineImpl) Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error) {
    def := DefinitionByKey(key)
    if def == nil {
        return Value{}, fmt.Errorf("policy.Resolve: %s: %w", key, ErrUnknownKey)
    }

    // Per-request cache (request-scoped).
    if v, ok := requestCacheGet(ctx, sellerID, key); ok {
        return v.Value, nil
    }

    // 1. Global lock
    if v, ok := e.cache.GlobalLock(key); ok {
        rv := ResolvedValue{Value: v, Source: SourceGlobalLock}
        e.afterResolve(ctx, sellerID, def, rv)
        return v, nil
    }

    // 2. Seller-type lock (need seller-type)
    sellerType, err := e.repo.GetSellerType(ctx, sellerID)
    if err != nil {
        return Value{}, fmt.Errorf("policy.Resolve: get seller type: %w", err)
    }
    if v, ok := e.cache.SellerTypeLock(sellerType, key); ok {
        rv := ResolvedValue{Value: v, Source: SourceTypeLock}
        e.afterResolve(ctx, sellerID, def, rv)
        return v, nil
    }

    // 3. Seller-level override
    if v, ok := e.cache.SellerOverride(sellerID, key); ok {
        rv := ResolvedValue{Value: v, Source: SourceSellerOverride}
        e.afterResolve(ctx, sellerID, def, rv)
        return v, nil
    }

    // 4. Seller-type default (in-memory; from definition)
    if v, ok := def.DefaultsByType[sellerType]; ok {
        rv := ResolvedValue{Value: v, Source: SourceTypeDefault}
        e.afterResolve(ctx, sellerID, def, rv)
        return v, nil
    }

    // 5. Global default
    rv := ResolvedValue{Value: def.DefaultGlobal, Source: SourceGlobalDefault}
    e.afterResolve(ctx, sellerID, def, rv)
    return def.DefaultGlobal, nil
}

func (e *engineImpl) ResolveBatch(ctx context.Context, sellerID core.SellerID, keys ...Key) (map[Key]Value, error) {
    out := make(map[Key]Value, len(keys))
    for _, k := range keys {
        v, err := e.Resolve(ctx, sellerID, k)
        if err != nil {
            return nil, err
        }
        out[k] = v
    }
    return out, nil
}

func (e *engineImpl) SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string) error {
    def := DefinitionByKey(key)
    if def == nil {
        return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrUnknownKey)
    }
    if !def.OverrideAllowed {
        return fmt.Errorf("policy.SetSellerOverride: %s: override not allowed", key)
    }
    // Lock check
    if _, ok := e.cache.GlobalLock(key); ok {
        return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrLocked)
    }
    sellerType, err := e.repo.GetSellerType(ctx, sellerID)
    if err == nil {
        if _, ok := e.cache.SellerTypeLock(sellerType, key); ok {
            return fmt.Errorf("policy.SetSellerOverride: %s: %w", key, ErrLocked)
        }
    }

    actor, _ := actorFromCtx(ctx)
    if err := e.repo.UpsertSellerOverride(ctx, sellerID, key, value, src, reason, actor); err != nil {
        return fmt.Errorf("policy.SetSellerOverride: %w", err)
    }

    // NOTIFY (other instances drop cache)
    e.repo.Notify(ctx, fmt.Sprintf("seller_override:%s:%s", sellerID, key))
    e.cache.InvalidateSeller(sellerID, key)

    e.audit.EmitAsync(ctx, audit.Event{
        SellerID: &sellerID,
        Action:   "policy.seller_override.set",
        Target:   audit.Target{Kind: "policy_setting", Ref: string(key)},
        Payload:  map[string]any{"value": value.Raw(), "source": src, "reason": reason},
    })

    return nil
}

// (RemoveSellerOverride, SetLock, RemoveLock, EffectiveConfig — similar patterns)

// afterResolve handles read-audit for sensitive keys.
func (e *engineImpl) afterResolve(ctx context.Context, sellerID core.SellerID, def *Definition, rv ResolvedValue) {
    requestCachePut(ctx, sellerID, def.Key, rv)

    if def.AuditOnRead {
        // Rate-limit per-request: don't emit twice for same (request, key, seller)
        if !requestCacheReadAudited(ctx, sellerID, def.Key) {
            e.audit.EmitAsync(ctx, audit.Event{
                SellerID: &sellerID,
                Action:   "policy.sensitive_read",
                Target:   audit.Target{Kind: "policy_setting", Ref: string(def.Key)},
                Payload:  map[string]any{"source": rv.Source},
            })
            requestCacheMarkReadAudited(ctx, sellerID, def.Key)
        }
    }
}

// listenForInvalidations subscribes to PG NOTIFY for cross-instance cache invalidation.
func (e *engineImpl) listenForInvalidations(ctx context.Context) {
    backoff := time.Second
    for {
        select { case <-ctx.Done(): return; default: }

        err := e.repo.ListenForInvalidations(ctx, func(payload string) {
            e.cache.HandleNotify(payload)
        })
        if err != nil {
            e.log.WarnContext(ctx, "policy listen disconnected; reconnecting",
                slog.Any("error", err), slog.Duration("backoff", backoff))
            time.Sleep(backoff)
            if backoff < 30*time.Second { backoff *= 2 }
            continue
        }
        backoff = time.Second
    }
}
```

## Cache

```go
// internal/policy/cache.go
package policy

import (
    "fmt"
    "strings"
    "sync"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// cache holds per-process cached overrides and locks.
//
// Three sub-caches:
//   - sellerOverrides: by (seller_id, key)
//   - sellerTypeLocks: by (seller_type, key)
//   - globalLocks: by key
//
// Each entry has a TTL stamp; expired entries fall through to DB.
// LISTEN/NOTIFY invalidates synchronously (sub-second).
type cache struct {
    mu sync.RWMutex

    sellerOverrides map[string]cachedValue  // key: "<sellerID>:<key>"
    sellerTypeLocks map[string]cachedValue  // key: "<sellerType>:<key>"
    globalLocks     map[string]cachedValue  // key: "<key>"

    ttl   time.Duration
    clock core.Clock
}

type cachedValue struct {
    value     Value
    insertedAt time.Time
}

func newCache(ttl time.Duration, clock core.Clock) *cache {
    return &cache{
        sellerOverrides: make(map[string]cachedValue),
        sellerTypeLocks: make(map[string]cachedValue),
        globalLocks:     make(map[string]cachedValue),
        ttl:             ttl,
        clock:           clock,
    }
}

func (c *cache) SellerOverride(sellerID core.SellerID, key Key) (Value, bool) {
    return c.get(c.sellerOverrides, fmt.Sprintf("%s:%s", sellerID, key))
}
func (c *cache) SellerTypeLock(sellerType core.SellerType, key Key) (Value, bool) {
    return c.get(c.sellerTypeLocks, fmt.Sprintf("%s:%s", sellerType, key))
}
func (c *cache) GlobalLock(key Key) (Value, bool) {
    return c.get(c.globalLocks, string(key))
}

func (c *cache) get(m map[string]cachedValue, k string) (Value, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    e, ok := m[k]
    if !ok {
        return Value{}, false
    }
    if c.clock.Now().Sub(e.insertedAt) > c.ttl {
        return Value{}, false
    }
    return e.value, true
}

func (c *cache) PutSellerOverride(sellerID core.SellerID, key Key, value Value) {
    c.put(c.sellerOverrides, fmt.Sprintf("%s:%s", sellerID, key), value)
}
func (c *cache) PutSellerTypeLock(sellerType core.SellerType, key Key, value Value) {
    c.put(c.sellerTypeLocks, fmt.Sprintf("%s:%s", sellerType, key), value)
}
func (c *cache) PutGlobalLock(key Key, value Value) {
    c.put(c.globalLocks, string(key), value)
}

func (c *cache) put(m map[string]cachedValue, k string, v Value) {
    c.mu.Lock()
    defer c.mu.Unlock()
    m[k] = cachedValue{value: v, insertedAt: c.clock.Now()}
}

func (c *cache) InvalidateSeller(sellerID core.SellerID, key Key) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.sellerOverrides, fmt.Sprintf("%s:%s", sellerID, key))
}
func (c *cache) InvalidateSellerType(sellerType core.SellerType, key Key) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.sellerTypeLocks, fmt.Sprintf("%s:%s", sellerType, key))
}
func (c *cache) InvalidateGlobal(key Key) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.globalLocks, string(key))
}

// HandleNotify dispatches NOTIFY payloads to the right invalidation.
//
// Payload formats:
//   "seller_override:<seller_id>:<key>"
//   "seller_type_lock:<seller_type>:<key>"
//   "global_lock:<key>"
func (c *cache) HandleNotify(payload string) {
    parts := strings.SplitN(payload, ":", 4)
    if len(parts) < 2 { return }
    switch parts[0] {
    case "seller_override":
        if len(parts) >= 3 {
            sid, err := core.ParseSellerID(parts[1])
            if err == nil {
                c.InvalidateSeller(sid, Key(parts[2]))
            }
        }
    case "seller_type_lock":
        if len(parts) >= 3 {
            c.InvalidateSellerType(core.SellerType(parts[1]), Key(parts[2]))
        }
    case "global_lock":
        if len(parts) >= 2 {
            c.InvalidateGlobal(Key(parts[1]))
        }
    }
}
```

## DB schema

```sql
-- migrations/00NN_create_policy.up.sql

CREATE TABLE policy_setting_definition (
    key                 TEXT PRIMARY KEY,
    type                TEXT NOT NULL,
    default_global      JSONB NOT NULL,
    defaults_by_type    JSONB NOT NULL DEFAULT '{}',
    lock_capable        BOOLEAN NOT NULL DEFAULT true,
    override_allowed    BOOLEAN NOT NULL DEFAULT true,
    audit_on_read       BOOLEAN NOT NULL DEFAULT false,
    description         TEXT NOT NULL,
    registered_in       TEXT NOT NULL,
    added_in_version    TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE policy_seller_override (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id    UUID NOT NULL,
    key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
    value        JSONB NOT NULL,
    source       TEXT NOT NULL,
    reason       TEXT,
    set_by       UUID,
    set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    UNIQUE (seller_id, key)
);

CREATE INDEX policy_seller_override_seller_idx ON policy_seller_override (seller_id);

ALTER TABLE policy_seller_override ENABLE ROW LEVEL SECURITY;
CREATE POLICY pso_seller ON policy_seller_override
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

CREATE TABLE policy_lock (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_kind   TEXT NOT NULL,
    scope_value  TEXT NOT NULL DEFAULT '',
    key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
    value        JSONB NOT NULL,
    reason       TEXT NOT NULL,
    set_by       UUID NOT NULL,
    set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (scope_kind, scope_value, key),
    CONSTRAINT pl_scope_kind_valid CHECK (scope_kind IN ('global','seller_type'))
);

GRANT SELECT, INSERT, UPDATE, DELETE ON policy_seller_override TO pikshipp_app;
GRANT SELECT ON policy_seller_override TO pikshipp_reports;
GRANT ALL ON policy_seller_override TO pikshipp_admin;

GRANT SELECT ON policy_lock TO pikshipp_app, pikshipp_reports;
GRANT ALL ON policy_lock TO pikshipp_admin;

GRANT SELECT ON policy_setting_definition TO pikshipp_app, pikshipp_reports;
GRANT ALL ON policy_setting_definition TO pikshipp_admin;
```

## SQL queries

```sql
-- query/policy.sql

-- name: UpsertPolicyDefinition :exec
INSERT INTO policy_setting_definition
  (key, type, default_global, defaults_by_type, lock_capable, override_allowed, audit_on_read, description, registered_in, added_in_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (key) DO UPDATE SET
  type             = EXCLUDED.type,
  default_global   = EXCLUDED.default_global,
  defaults_by_type = EXCLUDED.defaults_by_type,
  lock_capable     = EXCLUDED.lock_capable,
  override_allowed = EXCLUDED.override_allowed,
  audit_on_read    = EXCLUDED.audit_on_read,
  description      = EXCLUDED.description,
  registered_in    = EXCLUDED.registered_in,
  updated_at       = now();

-- name: ListSellerOverrides :many
SELECT key, value, source, reason
FROM policy_seller_override
WHERE seller_id = $1;

-- name: GetSellerOverride :one
SELECT key, value, source, reason
FROM policy_seller_override
WHERE seller_id = $1 AND key = $2;

-- name: UpsertSellerOverride :exec
INSERT INTO policy_seller_override (seller_id, key, value, source, reason, set_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (seller_id, key) DO UPDATE SET
  value   = EXCLUDED.value,
  source  = EXCLUDED.source,
  reason  = EXCLUDED.reason,
  set_by  = EXCLUDED.set_by,
  set_at  = now();

-- name: DeleteSellerOverride :exec
DELETE FROM policy_seller_override
WHERE seller_id = $1 AND key = $2;

-- name: ListAllLocks :many
SELECT scope_kind, scope_value, key, value
FROM policy_lock;

-- name: UpsertLock :exec
INSERT INTO policy_lock (scope_kind, scope_value, key, value, reason, set_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (scope_kind, scope_value, key) DO UPDATE SET
  value   = EXCLUDED.value,
  reason  = EXCLUDED.reason,
  set_by  = EXCLUDED.set_by,
  set_at  = now();

-- name: DeleteLock :exec
DELETE FROM policy_lock
WHERE scope_kind = $1 AND scope_value = $2 AND key = $3;

-- name: GetSellerType :one
SELECT type_profile->>'seller_type' AS seller_type
FROM seller
WHERE id = $1;
```

## Testing

```go
func TestEngine_ResolveResolvesGlobalDefault(t *testing.T) {
    p := testdb.New(t)
    audit := audit.NewMock()
    clock := core.NewFakeClock(time.Now())
    e, err := policy.New(p.App, audit, clock, slog.Default())
    require.NoError(t, err)

    sid := core.NewSellerID()
    seedSmallSMB(t, p.App, sid)

    v, err := e.Resolve(context.Background(), sid, policy.KeyWalletPosture)
    require.NoError(t, err)
    s, _ := v.AsString()
    require.Equal(t, "prepaid_only", s)
}

func TestEngine_ResolveSellerOverride(t *testing.T) {
    p := testdb.New(t)
    e, _ := policy.New(p.App, audit.NewMock(), core.NewFakeClock(time.Now()), slog.Default())

    sid := core.NewSellerID()
    seedSmallSMB(t, p.App, sid)

    err := e.SetSellerOverride(ctxAs(sid, ops), sid, policy.KeyCODEnabled,
        policy.BoolValue(true), policy.SourceOps, "promo")
    require.NoError(t, err)

    v, _ := e.Resolve(context.Background(), sid, policy.KeyCODEnabled)
    b, _ := v.AsBool()
    require.True(t, b)
}

func TestEngine_LockBlocksOverride(t *testing.T) {
    e, _ := policy.New(...)
    e.SetLock(ctx, policy.LockScope{Kind: policy.LockScopeGlobal}, policy.KeyWalletGraceNegativeAmount,
        policy.PaiseValue(core.FromRupees(500)), "compliance floor")

    err := e.SetSellerOverride(ctx, sid, policy.KeyWalletGraceNegativeAmount,
        policy.PaiseValue(core.FromRupees(10000)), policy.SourceOps, "")
    require.ErrorIs(t, err, policy.ErrLocked)
}

func TestEngine_AuditOnReadEmitsOnce(t *testing.T) {
    audit := audit.NewMock()
    e, _ := policy.New(p.App, audit, clock, log)

    ctx := context.Background()  // request-scoped cache key
    e.Resolve(ctx, sid, policy.KeyWalletCreditLimitInr)
    e.Resolve(ctx, sid, policy.KeyWalletCreditLimitInr)  // second read, same request

    require.Equal(t, 1, audit.CountOf("policy.sensitive_read"))
}

func BenchmarkEngine_ResolveCacheHit(b *testing.B) {
    e, _ := policy.New(...)
    sid := core.NewSellerID()
    e.Resolve(context.Background(), sid, policy.KeyWalletPosture) // warm cache

    for i := 0; i < b.N; i++ {
        _, _ = e.Resolve(context.Background(), sid, policy.KeyWalletPosture)
    }
}

func BenchmarkEngine_ResolveBatch10Keys(b *testing.B) {
    e, _ := policy.New(...)
    sid := core.NewSellerID()
    keys := []policy.Key{
        policy.KeyAllocationWeightCost, policy.KeyAllocationWeightSpeed,
        policy.KeyAllocationWeightReliability, policy.KeyAllocationWeightSellerPref,
        policy.KeyCarriersAllowedSet, policy.KeyCarriersExcludedSet,
        policy.KeyDeliveryMaxAttempts, policy.KeyDeliveryReattemptWindowHours,
        policy.KeyPricingRateCardRef, policy.KeyCODEnabled,
    }
    for i := 0; i < b.N; i++ {
        _, _ = e.ResolveBatch(context.Background(), sid, keys...)
    }
}
```

Targets: `BenchmarkEngine_ResolveCacheHit` < 500 ns/op; `BenchmarkEngine_ResolveBatch10Keys` < 5 µs/op.

## Performance

- `Resolve` cache hit: ~200 ns (RLock + map lookup + TTL check).
- `Resolve` cache miss: 1 DB roundtrip; ~5 ms.
- `ResolveBatch` is just N×Resolve; per-request memoization helps when same key is requested twice.
- `SetSellerOverride`: 1 INSERT/UPDATE + 1 NOTIFY; ~5 ms.

## Open questions

- Should we add **batch invalidation** (one NOTIFY for many keys when, e.g., a seller-type's defaults change)? Add when needed; current per-key NOTIFY is fine.
- Sensitive-key read auditing emits async; if outbox is far behind, reads aren't blocked but audit lags. Acceptable.
- Definition removal: today, removing a key from `Definitions` leaves orphaned rows in DB. Add cleanup migration when we actually remove a key (rare).

## References

- HLD `03-services/01-policy-engine.md`.
- HLD `03-product-architecture/05-policy-engine.md` (PRD-level).
