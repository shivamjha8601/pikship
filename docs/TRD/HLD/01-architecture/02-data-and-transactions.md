# Data & transactions

> Single Postgres. Single schema. Seller scoping via RLS. Strict transaction discipline. Money in paise.

## Database setup

- **Engine:** PostgreSQL 16 on AWS RDS, multi-AZ.
- **Schema:** single `public` schema. Tables prefixed by domain.
- **Roles:**
  - `pikshipp_app` — the application role. RLS enforced. No `BYPASSRLS`.
  - `pikshipp_reports` — reports + analytics. Has `BYPASSRLS`. Read-only on most tables.
  - `pikshipp_admin` — Pikshipp ops elevated path. Has `BYPASSRLS`. Write access logged via audit.
  - `pikshipp_migration` — schema migrations only. Used by CI step.
- **Connection:** pgx connection pool. Pool size: starts at 50; tunable.

## Seller scoping with RLS

Every domain table that holds seller-scoped data has:
- A `seller_id UUID NOT NULL` column.
- A `*_seller_id_idx` index.
- An RLS policy that filters on `current_setting('app.seller_id')::uuid`.

```sql
ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;

CREATE POLICY order_items_seller ON order_items
  FOR ALL
  TO pikshipp_app
  USING (seller_id = current_setting('app.seller_id', true)::uuid)
  WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);
```

The `WITH CHECK` clause is critical — without it, an INSERT could write a row with the wrong `seller_id`. With it, RLS enforces both reads and writes.

### Setting the GUC per request

A middleware in `api/http/middleware/seller_scope.go` runs after auth resolves the session:

```go
tx, _ := db.BeginTx(ctx)
defer tx.Rollback()

_, err := tx.ExecContext(ctx, "SET LOCAL app.seller_id = $1", principal.SellerID)
// ... pass tx to handler ...
tx.Commit()
```

`SET LOCAL` scopes the GUC to this transaction; on commit/rollback it's automatically cleared. **No leakage across requests** even within the same connection in the pool.

### Tables NOT seller-scoped

Some tables are platform-level: `carrier`, `carrier_serviceability`, `carrier_health_state`, `policy_setting_definition`, `notification_template` (when `scope=pikshipp`). These do **not** have RLS — they're shared. Their writes are gated by application-layer RBAC (only Pikshipp Admin role).

### When to use BYPASSRLS

- Reports module's aggregations: `pikshipp_reports` role.
- Pikshipp ops "view as" or impersonation: `pikshipp_admin` role; every action audit-logged with elevation reason.
- Cron / batch jobs that span sellers: `pikshipp_admin` role; audit-logged.

The application code uses **separate connection pools** for different roles. The default pool is `pikshipp_app`; reports module has its own pool to `pikshipp_reports`; admin paths have a `pikshipp_admin` pool used only by admin handlers.

## Transaction patterns

### Pattern 1 — Simple mutating request

One transaction wraps the whole handler. RLS scope set at start.

```go
err := db.WithSellerTx(ctx, principal.SellerID, func(tx Transaction) error {
    // domain operations
    return service.DoThing(ctx, tx, ...)
})
```

`WithSellerTx` is a helper that:
1. Begins transaction.
2. `SET LOCAL app.seller_id = ...`.
3. Runs the callback.
4. Commits or rolls back.

### Pattern 2 — Booking (two transactions + external call)

```go
// Tx 1
err := db.WithSellerTx(ctx, sellerID, func(tx Transaction) error {
    rateQuote := pricing.Quote(...)
    decision := allocation.Allocate(..., rateQuote)
    holdID := wallet.Reserve(tx, ...)
    shipmentID := shipments.Create(tx, ..., status=pending_carrier)
    outbox.Emit(tx, "shipment.pending", ...)
    return nil
})

// External call — NO TX
result, err := carrier.Adapter.Book(ctx, ...)

// Tx 2
err := db.WithSellerTx(ctx, sellerID, func(tx Transaction) error {
    if result.Success {
        shipments.SetAWB(tx, shipmentID, result.AWB)
        wallet.Confirm(tx, holdID, ref)
        outbox.Emit(tx, "shipment.booked", ...)
    } else {
        shipments.MarkFailed(tx, shipmentID, result.Error)
        wallet.Release(tx, holdID)
        outbox.Emit(tx, "shipment.booking_failed", ...)
    }
    return nil
})
```

### Pattern 3 — Wallet ops (locked row)

```go
err := db.WithSellerTx(ctx, sellerID, func(tx Transaction) error {
    var balance Paise
    err := tx.QueryRow(`
        SELECT balance_minor FROM wallet_account WHERE seller_id = $1 FOR UPDATE
    `, sellerID).Scan(&balance)

    // compute new balance
    // ...

    _, err = tx.Exec(`
        UPDATE wallet_account SET balance_minor = $1 WHERE seller_id = $2
    `, newBalance, sellerID)

    _, err = tx.Exec(`
        INSERT INTO wallet_ledger_entry (...) VALUES (...) ON CONFLICT (ref_type, ref_id, direction) DO NOTHING
    `, ...)

    return nil
})
```

The `FOR UPDATE` serializes wallet ops per seller — fine for the volumes we expect. Benchmark gates the assumption (see ADR 0006).

### What never happens in a transaction

- HTTP requests to external services (carriers, PG, KYC, comms vendors).
- File uploads to S3.
- Reads/writes to S3.
- Sleeps.
- Anything taking >100ms reliably.

The reason: long transactions hold locks, eat connections, and cascade into tail-latency disasters.

## Money

### Paise int64

All monetary values stored and computed as `int64` paise. NUMERIC in DB, mapped via sqlc custom type:

```go
package core

type Paise int64

func (p Paise) Rupees() float64 { return float64(p) / 100.0 } // for display only
func (p Paise) String() string  { return ... }                // INR formatting
```

Arithmetic uses int64. Float operations in money code are linted out via custom `revive` rule.

### Wallet ledger constraint

`wallet_ledger_entry` has:
```sql
UNIQUE (ref_type, ref_id, direction)
```

This is the contract. Idempotent posting just attempts the INSERT; on UNIQUE violation, it treats as success and returns the existing entry.

### Daily invariant

A river cron at 02:00 IST runs:
```sql
SELECT seller_id,
       SUM(CASE WHEN direction='credit' THEN amount_minor ELSE -amount_minor END) AS computed,
       wa.balance_minor AS cached
FROM wallet_ledger_entry le
JOIN wallet_account wa ON wa.seller_id = le.seller_id
GROUP BY le.seller_id, wa.balance_minor
HAVING SUM(...) <> wa.balance_minor;
```

Any rows = P0 alert. Logged + audit event.

## Migrations

### As a CI step (not on startup)

Migrations are SQL files under `migrations/` versioned by `golang-migrate`:
```
migrations/
├── 0001_create_seller_table.up.sql
├── 0001_create_seller_table.down.sql
├── 0002_add_wallet_account.up.sql
└── ...
```

CI workflow:
1. PR opened with new migration.
2. CI runs `migrate -path ./migrations -database $TEST_DB up` against ephemeral PG.
3. CI runs `migrate down 1; migrate up 1` to verify reversibility.
4. Lint check: every `up.sql` has a corresponding `down.sql`.
5. On merge: deploy pipeline runs `migrate up` against staging, then prod.
6. Then deploys new binary.

Binary on startup checks:
```sql
SELECT version FROM schema_migrations LIMIT 1;
```
And refuses to start if `version < expected_version` (compiled into the binary as a constant updated each release).

### Reversibility

All migrations must be reversible **without data loss for the previous binary version**. Adding a column? Reversible. Renaming a column? Use the expand/contract pattern over two releases. Dropping a column? Only when the previous binary version no longer reads it.

## Schema conventions

### IDs
- All primary keys are `UUID` (`gen_random_uuid()`).
- Foreign keys use the parent's UUID type.
- For human-readable IDs (Order ID like `PSO-lotus-100123`), store the human form in a separate `display_id` column with UNIQUE constraint.

### Timestamps
- Every table has `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
- Mutable rows have `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()` updated via trigger.
- Domain timestamps (e.g., `delivered_at`) are domain-specific.

### Tenancy column
- `seller_id UUID NOT NULL` on every seller-scoped table.
- Optional `sub_seller_id UUID` where applicable.

### Soft delete
- We don't soft-delete by default. Lifecycle states (`status` column) handle the equivalent.
- For records that can be removed (e.g., a deprecated channel connection), we delete; cascading FKs preserve referential integrity.

### Indexing
- Always: `(seller_id, created_at DESC)` on tables we paginate.
- Always: `(seller_id, foreign_key)` on FK columns.
- Always: partial index on outbox `WHERE enqueued_at IS NULL`.
- Idempotency: `UNIQUE (seller_id, idempotency_key)`.

## Backups

- RDS automated backup, daily, 7-day retention.
- Manual snapshot before each major release.
- Test restore quarterly.

## Connection lifecycle

- Pool: pgx `Pool`, max 50 connections at v0.
- Per-request: acquire connection from pool, begin transaction, set GUC, run handler, commit/rollback, release.
- Idle timeout: 30 minutes.
- Connection lifetime: max 1 hour (forces refresh).

## What's at risk in this design

1. **Single-PG bottleneck.** If we hit IO, we add a read replica for reports — already speccable. If we hit write IO at v3+, we partition by seller_id range; that's a v3+ project.
2. **RLS performance.** RLS adds a predicate to every query. Index on `seller_id` makes it fast. Worth benchmarking at v1 traffic; flagging as a benchmark target in the v1-readiness gate.
3. **`FOR UPDATE` contention.** Per the wallet pattern. Benchmark gate.
