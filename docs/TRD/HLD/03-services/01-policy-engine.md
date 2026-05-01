# Service: Policy engine

> **Module:** `internal/policy/`
> **Maps to PRD:** [`PRD/03-product-architecture/05-policy-engine.md`](../../../PRD/03-product-architecture/05-policy-engine.md)
>
> The runtime configuration substrate. Every other service reads from it. If this is wrong or slow, everything else suffers.

## Responsibility

Resolve `(seller_id, key) → value` according to:
1. Pikshipp lock (global or seller-type) — wins if present.
2. Seller-level override.
3. Seller-type default.
4. Pikshipp global default.

Plus: emit audit events on writes; emit cache-invalidation NOTIFYs on mutations; provide a sensitive-key read-audit hook.

## Public interface

```go
package policy

type Engine interface {
    // Resolve returns the effective value for this key for this seller.
    // Per-request cached. Reads NOTIFY-invalidated cache; falls back to TTL.
    Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error)

    // ResolveBatch is for hot paths that need multiple keys in one go (allocation, pricing).
    ResolveBatch(ctx context.Context, sellerID core.SellerID, keys ...Key) (map[Key]Value, error)

    // SetSellerOverride is for seller self-service or ops; emits audit + NOTIFY.
    SetSellerOverride(ctx context.Context, sellerID core.SellerID, key Key, value Value, src Source, reason string) error

    // SetLock is for Pikshipp Admin only.
    SetLock(ctx context.Context, scope LockScope, key Key, value Value, reason string) error

    // SetSellerTypeDefault is for Pikshipp Admin only (rare; affects all sellers in type).
    SetSellerTypeDefault(ctx context.Context, sellerType SellerType, key Key, value Value, reason string) error
}
```

## Data model

```sql
-- Seeded at startup from Go-defined registry (NOT user-editable in DB)
CREATE TABLE policy_setting_definition (
  key                TEXT PRIMARY KEY,                 -- e.g., 'wallet.credit_limit_inr'
  type               TEXT NOT NULL,                    -- 'int64', 'string', 'bool', 'enum:...'
  default_global     JSONB NOT NULL,
  defaults_by_type   JSONB NOT NULL DEFAULT '{}',      -- {seller_type → value}
  lock_capable       BOOLEAN NOT NULL DEFAULT true,
  override_allowed   BOOLEAN NOT NULL DEFAULT true,
  description        TEXT NOT NULL,
  registered_in      TEXT NOT NULL,                    -- module that owns this key
  added_in_version   TEXT NOT NULL,
  audit_on_read      BOOLEAN NOT NULL DEFAULT false    -- sensitive keys emit read events
);

CREATE TABLE policy_seller_override (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id    UUID NOT NULL,
  key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
  value        JSONB NOT NULL,
  source       TEXT NOT NULL,                          -- 'seller_self' | 'ops' | 'contract:<id>'
  reason       TEXT,
  set_by       UUID NOT NULL,
  set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ,
  UNIQUE (seller_id, key)
);

CREATE TABLE policy_lock (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_kind   TEXT NOT NULL,                          -- 'global' | 'seller_type'
  scope_value  TEXT,                                   -- e.g., 'mid_market'; NULL for global
  key          TEXT NOT NULL REFERENCES policy_setting_definition(key),
  value        JSONB NOT NULL,
  reason       TEXT NOT NULL,
  set_by       UUID NOT NULL,
  set_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (scope_kind, COALESCE(scope_value, ''), key)
);
```

RLS on `policy_seller_override` (seller-scoped). The other tables are platform; only `pikshipp_admin` writes them.

## Resolution algorithm

```go
func (e *engineImpl) Resolve(ctx context.Context, sellerID core.SellerID, key Key) (Value, error) {
    // 0. Per-request cache
    if cached, ok := requestCache(ctx).get(sellerID, key); ok {
        return cached, nil
    }

    // 1. Pikshipp global lock
    if v, ok := e.lockCache.GlobalLock(key); ok {
        return e.afterResolve(ctx, sellerID, key, v, "global_lock"), nil
    }

    // 2. Seller-type lock
    sellerType := e.resolveSellerType(ctx, sellerID)  // typically cached
    if v, ok := e.lockCache.SellerTypeLock(sellerType, key); ok {
        return e.afterResolve(ctx, sellerID, key, v, "type_lock"), nil
    }

    // 3. Seller-level override
    if v, ok := e.sellerOverrideCache.Get(sellerID, key); ok {
        return e.afterResolve(ctx, sellerID, key, v, "seller_override"), nil
    }

    // 4. Seller-type default
    if v, ok := e.definitions.SellerTypeDefault(key, sellerType); ok {
        return e.afterResolve(ctx, sellerID, key, v, "type_default"), nil
    }

    // 5. Global default
    return e.afterResolve(ctx, sellerID, key, e.definitions.GlobalDefault(key), "global_default"), nil
}
```

`afterResolve` writes the value into the request cache and, if `definition.audit_on_read`, emits an async audit event.

## Caches

Three caches in process:

- **Request cache** (per-request map). Cleared at request end.
- **Seller override cache** (process-wide, keyed by `seller_id`). Invalidated by NOTIFY on `policy_seller_override` write.
- **Lock cache** (process-wide, small). Invalidated by NOTIFY on `policy_lock` write.

All caches have a 5s TTL fallback in case the NOTIFY was missed (network blip, listener disconnect).

## Cache invalidation

```sql
NOTIFY policy_invalidation, '<seller_id|*>:<key>';
```

A goroutine in the policy module subscribes via `LISTEN policy_invalidation`. On message:
- `<seller_id>:<key>` → drop entry from override cache.
- `*:<key>` → drop entire entry (lock changed; affects everyone).
- `*:*` → flush all caches (rare; only on bulk re-seeding).

The TTL on caches is 5s, ensuring eventual consistency even if NOTIFY is missed.

## Setting definitions registry (in Go code)

```go
package policy

var Definitions = []SettingDefinition{
    {
        Key:           "wallet.credit_limit_inr",
        Type:          TypeInt64,
        DefaultGlobal: 0,
        DefaultsByType: map[SellerType]Value{
            SellerTypeMidMarket: 100_000_00,   // ₹1L
            SellerTypeEnterprise: 500_000_00,  // ₹5L
        },
        LockCapable:    true,
        OverrideAllowed: true,
        Description:    "Maximum negative balance permitted for credit-line sellers",
        RegisteredIn:   "wallet",
        AddedInVersion: "v0",
        AuditOnRead:    true,  // sensitive
    },
    {
        Key:           "cod.remittance_cycle_days",
        Type:          TypeInt64,
        DefaultGlobal: 5,
        DefaultsByType: map[SellerType]Value{
            SellerTypeMidMarket: 3,
            SellerTypeEnterprise: 2,
        },
        LockCapable:    true,
        OverrideAllowed: true,
        // ...
    },
    // ... ~30 more keys
}
```

On startup:
1. Load `Definitions` from code.
2. UPSERT into `policy_setting_definition`.
3. Refuse to start if any DB row references a key not in `Definitions` (catch-deletes).

This makes adding a new setting a code change + a migration that runs the upsert.

## Performance budget

- `Resolve`: P95 < 1ms (cache hit). P99 < 5ms (cache miss with DB read).
- `ResolveBatch(N keys)`: P95 < 2ms. Hot paths (allocation, pricing) use this with N=5–10.

Microbenchmark in `bench_test.go` exercises both paths.

## Failure modes

| Failure | Behavior |
|---|---|
| DB unreachable on cache miss | Use last-known cached value with stale flag; if no cached value, return error (caller must handle) |
| LISTEN connection drops | Reconnect with backoff; cache TTL ensures eventual freshness |
| Seller-override write succeeds but NOTIFY fails | TTL fallback; max 5s staleness on other instances |
| Definition missing for queried key | Programming error; log + return error; should be caught at startup |

## Integration with audit

- `SetSellerOverride` → emits audit event with `(actor, key, before, after, reason)`.
- `SetLock` → emits audit event.
- `Resolve` for `audit_on_read=true` keys → async audit event with `(actor, key, accessed_at)`.

## Integration with contracts (Feature 27)

A contract's machine-readable terms become `policy_seller_override` rows with `source='contract:<id>'`. Contract amendments delete old rows and insert new ones inside a single transaction; emits NOTIFY.

## Test coverage

- **Unit**: resolution algorithm exhaustive — all 4 layers, all combinations.
- **SLT**: real PG; verify NOTIFY-driven invalidation and TTL fallback (with NOTIFY blocked); verify audit events emitted; verify multi-instance behavior (two policy engines on same DB).
- **Bench**: `Resolve` cache-hit, cache-miss; `ResolveBatch` for various N.

## Observability

- Counter: cache hit rate.
- Counter: TTL fallback rate (high = NOTIFY broken; alert).
- Histogram: resolution latency.
- Log: every override write.

## Open questions

- **Q-PE-1.** Should the request cache survive across nested transactions in the same request? Probably yes (same request = same effective config). Confirm in code review.
- **Q-PE-2.** When a contract terminates and overrides drop, is there a window where caches return stale values? Yes (≤5s). Acceptable.
- **Q-PE-3.** Reading a `audit_on_read=true` key in a hot loop: do we emit one event per read or rate-limit? Rate-limit per (request, key) — at most one event per request per key.
