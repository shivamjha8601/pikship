# Infrastructure: Database access (`internal/observability/dbtx`, `query/`, sqlc, migrations)

> The single most-used piece of infrastructure. Every domain module depends on this. Get it right.

## Purpose

- pgx connection pool setup.
- Three connection roles: `pikshipp_app`, `pikshipp_reports`, `pikshipp_admin`.
- Transaction helpers that automatically `SET LOCAL app.seller_id` for RLS.
- sqlc configuration and code generation.
- golang-migrate setup.

## Dependencies

- `github.com/jackc/pgx/v5` and `/pgxpool`
- `github.com/jackc/pgx/v5/stdlib` (for sqlc-compatible `database/sql` interface)
- `github.com/sqlc-dev/sqlc` (codegen tool, dev dependency)
- `github.com/golang-migrate/migrate/v4` (CLI tool + library)

## Package layout

```
internal/observability/dbtx/
├── doc.go
├── pool.go              ← pool construction
├── transactions.go      ← WithSellerTx, WithReadOnlyTx, WithAdminTx
├── role.go              ← role enum + connection-string builder
├── timeout.go           ← per-statement timeout helper
└── transactions_test.go
```

```
query/                   ← .sql files; sqlc inputs
├── auth.sql
├── seller.sql
├── wallet.sql
├── orders.sql
├── ...
```

```
internal/<module>/
└── repo.go              ← uses sqlc-generated code from `query/`
```

```
migrations/
├── 0001_init_schema.up.sql
├── 0001_init_schema.down.sql
├── 0002_create_seller.up.sql
├── 0002_create_seller.down.sql
└── ...
```

## Connection pool setup

```go
// internal/observability/dbtx/pool.go
package dbtx

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

// Role distinguishes which Postgres role we connect as.
type Role int

const (
    RoleApp     Role = iota  // pikshipp_app — RLS enforced
    RoleReports              // pikshipp_reports — BYPASSRLS, read-mostly
    RoleAdmin                // pikshipp_admin — BYPASSRLS, audit-elevated writes
)

func (r Role) String() string {
    switch r {
    case RoleApp:     return "pikshipp_app"
    case RoleReports: return "pikshipp_reports"
    case RoleAdmin:   return "pikshipp_admin"
    }
    return "unknown"
}

// Config drives pool construction.
type Config struct {
    URL                string         // base postgres URL; the role overrides user
    MaxConns           int32          // default 50
    MinConns           int32          // default 5
    MaxConnLifetime    time.Duration  // default 60min
    MaxConnIdleTime    time.Duration  // default 30min
    HealthCheckPeriod  time.Duration  // default 1min
    StatementTimeout   time.Duration  // default 5s; per-tx via SET LOCAL
}

// DefaultConfig returns sensible defaults; URL must be set.
func DefaultConfig(url string) Config {
    return Config{
        URL:                url,
        MaxConns:           50,
        MinConns:           5,
        MaxConnLifetime:    60 * time.Minute,
        MaxConnIdleTime:    30 * time.Minute,
        HealthCheckPeriod:  1 * time.Minute,
        StatementTimeout:   5 * time.Second,
    }
}

// NewPool constructs a pgxpool.Pool for the given role.
//
// Each role gets its own pool (caller manages multiple pools). The role's
// credentials must already exist in the URL or via env vars resolved by
// the caller; this constructor doesn't manage credentials.
func NewPool(ctx context.Context, cfg Config, role Role, log *slog.Logger) (*pgxpool.Pool, error) {
    poolCfg, err := pgxpool.ParseConfig(cfg.URL)
    if err != nil {
        return nil, fmt.Errorf("dbtx.NewPool: parse url: %w", err)
    }

    poolCfg.MaxConns          = cfg.MaxConns
    poolCfg.MinConns          = cfg.MinConns
    poolCfg.MaxConnLifetime   = cfg.MaxConnLifetime
    poolCfg.MaxConnIdleTime   = cfg.MaxConnIdleTime
    poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod

    // Per-connection setup: set role + statement timeout
    poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
        // Switch to the configured role for this connection.
        // (Note: using SET ROLE not SET SESSION AUTHORIZATION; the ROLE
        // privilege must be granted to the connecting user by DBA.)
        _, err := conn.Exec(ctx, fmt.Sprintf("SET ROLE %s", role.String()))
        if err != nil {
            return fmt.Errorf("set role: %w", err)
        }
        _, err = conn.Exec(ctx, fmt.Sprintf("SET statement_timeout = '%dms'", cfg.StatementTimeout.Milliseconds()))
        if err != nil {
            return fmt.Errorf("set statement_timeout: %w", err)
        }
        return nil
    }

    pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
    if err != nil {
        return nil, fmt.Errorf("dbtx.NewPool: create pool: %w", err)
    }

    // Smoke test
    if err := pool.Ping(ctx); err != nil {
        return nil, fmt.Errorf("dbtx.NewPool: ping: %w", err)
    }

    log.InfoContext(ctx, "db pool ready",
        slog.String("role", role.String()),
        slog.Int("max_conns", int(cfg.MaxConns)))

    return pool, nil
}
```

### Wiring (in `cmd/pikshipp/main.go`)

```go
poolApp, err     := dbtx.NewPool(ctx, dbtx.DefaultConfig(cfg.DatabaseURL), dbtx.RoleApp, log)
if err != nil { panic(err) }
defer poolApp.Close()

poolReports, err := dbtx.NewPool(ctx, dbtx.DefaultConfig(cfg.DatabaseURL), dbtx.RoleReports, log)
if err != nil { panic(err) }
defer poolReports.Close()

poolAdmin, err   := dbtx.NewPool(ctx, dbtx.DefaultConfig(cfg.DatabaseURL), dbtx.RoleAdmin, log)
if err != nil { panic(err) }
defer poolAdmin.Close()
```

Domain services receive `*pgxpool.Pool` (or a wrapper) of the appropriate role.

## Transaction helpers

```go
// internal/observability/dbtx/transactions.go
package dbtx

import (
    "context"
    "errors"
    "fmt"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/core"
)

// TxFunc is the body of a transactional operation.
//
// The provided pgx.Tx is the active transaction; the seller scope GUC has
// already been applied. The function must NOT call Commit/Rollback; the
// helper handles it.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// WithSellerTx executes fn inside a transaction scoped to sellerID.
//
// Specifically:
//  1. Begins a transaction with default isolation level.
//  2. Sets app.seller_id GUC (used by RLS policies).
//  3. Calls fn.
//  4. If fn returns nil: commits.
//  5. If fn returns an error: rolls back.
//
// If commit itself fails, returns the commit error.
//
// The transaction is automatically rolled back on context cancellation
// (pgx behavior).
//
// This is the canonical entry point for any domain operation that
// touches seller-scoped data. NEVER use pool.Begin() directly.
func WithSellerTx(ctx context.Context, pool *pgxpool.Pool, sellerID core.SellerID, fn TxFunc) error {
    if sellerID.IsZero() {
        return fmt.Errorf("dbtx.WithSellerTx: %w", core.ErrSellerScope)
    }

    tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return fmt.Errorf("dbtx.WithSellerTx: begin: %w", err)
    }
    defer func() {
        // Best-effort rollback if the function panicked or didn't commit.
        // pgx's Rollback after Commit is a no-op.
        _ = tx.Rollback(context.Background())
    }()

    // SET LOCAL is scoped to this transaction; auto-cleared on commit/rollback.
    _, err = tx.Exec(ctx, "SET LOCAL app.seller_id = $1", sellerID.String())
    if err != nil {
        return fmt.Errorf("dbtx.WithSellerTx: set seller_id: %w", err)
    }

    if err := fn(ctx, tx); err != nil {
        return err  // already wrapped by caller; rollback via deferred
    }

    if err := tx.Commit(ctx); err != nil {
        return fmt.Errorf("dbtx.WithSellerTx: commit: %w", err)
    }
    return nil
}

// WithReadOnlyTx is like WithSellerTx but uses a read-only transaction.
// Useful for handlers that only query.
func WithReadOnlyTx(ctx context.Context, pool *pgxpool.Pool, sellerID core.SellerID, fn TxFunc) error {
    if sellerID.IsZero() {
        return fmt.Errorf("dbtx.WithReadOnlyTx: %w", core.ErrSellerScope)
    }

    tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
    if err != nil {
        return fmt.Errorf("dbtx.WithReadOnlyTx: begin: %w", err)
    }
    defer tx.Rollback(context.Background())

    _, err = tx.Exec(ctx, "SET LOCAL app.seller_id = $1", sellerID.String())
    if err != nil {
        return fmt.Errorf("dbtx.WithReadOnlyTx: set seller_id: %w", err)
    }

    return fn(ctx, tx)
}

// WithAdminTx executes fn inside a transaction with BYPASSRLS, intended for
// Pikshipp admin operations across sellers.
//
// Pre-conditions:
//   - Pool must be the admin pool (Role=RoleAdmin).
//   - Caller must have already authorized this access (Pikshipp Admin role check).
//   - Caller must emit an audit event for the cross-seller access.
//
// This helper does NOT enforce these — it's a privileged operation; misuse
// is a code review failure.
func WithAdminTx(ctx context.Context, pool *pgxpool.Pool, fn TxFunc) error {
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return fmt.Errorf("dbtx.WithAdminTx: begin: %w", err)
    }
    defer tx.Rollback(context.Background())

    if err := fn(ctx, tx); err != nil {
        return err
    }

    if err := tx.Commit(ctx); err != nil {
        return fmt.Errorf("dbtx.WithAdminTx: commit: %w", err)
    }
    return nil
}

// IsRetryableError reports whether the error is a transient Postgres failure
// that warrants a retry (e.g., serialization failure, deadlock).
//
// Domain code generally doesn't retry; this is for the framework wrapper
// that runs before or alongside transaction helpers.
func IsRetryableError(err error) bool {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        switch pgErr.Code {
        case "40001": return true  // serialization_failure
        case "40P01": return true  // deadlock_detected
        }
    }
    return false
}
```

### Usage example

```go
// In a domain service:
func (s *serviceImpl) Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error) {
    var holdID HoldID

    err := dbtx.WithSellerTx(ctx, s.pool, sellerID, func(ctx context.Context, tx pgx.Tx) error {
        q := s.queries.WithTx(tx)

        wallet, err := q.GetWalletForUpdate(ctx, sellerID)
        if err != nil {
            return fmt.Errorf("get wallet: %w", err)
        }

        if wallet.Available < int64(amount) {
            return ErrInsufficientFunds
        }

        holdID = HoldID(uuid.New())
        err = q.InsertHold(ctx, db.InsertHoldParams{
            ID: uuid.UUID(holdID),
            WalletID: wallet.ID,
            SellerID: sellerID,
            AmountMinor: int64(amount),
            ExpiresAt: s.clock.Now().Add(ttl),
        })
        if err != nil {
            return fmt.Errorf("insert hold: %w", err)
        }

        err = q.IncrementWalletHold(ctx, db.IncrementWalletHoldParams{
            WalletID: wallet.ID,
            Amount: int64(amount),
        })
        if err != nil {
            return fmt.Errorf("update wallet: %w", err)
        }

        return nil
    })

    if err != nil {
        return HoldID{}, fmt.Errorf("wallet.Reserve: %w", err)
    }

    return holdID, nil
}
```

## sqlc setup

### `sqlc.yaml`

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "query/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        sql_package: "pgx/v5"
        out: "internal/db"
        emit_interface: true
        emit_json_tags: false
        emit_pointers_for_null_types: true
        emit_db_tags: false
        emit_prepared_queries: false
        emit_exact_table_names: false
        emit_empty_slices: true
        emit_methods_with_db_argument: true
        overrides:
          - go_type:
              import: "github.com/pikshipp/pikshipp/internal/core"
              package: "core"
              type: "Paise"
            db_type: "bigint"
            nullable: false
          - go_type:
              import: "github.com/pikshipp/pikshipp/internal/core"
              package: "core"
              type: "SellerID"
            db_type: "uuid"
            nullable: false
            column: "*.seller_id"
          - go_type:
              import: "github.com/google/uuid"
              package: "uuid"
              type: "UUID"
            db_type: "uuid"
            nullable: false
```

(More overrides per typed-ID in core.)

### Query files

Each `query/<module>.sql` follows this convention:

```sql
-- name: GetWalletForUpdate :one
SELECT id, seller_id, balance_minor, hold_total_minor, credit_limit_minor, grace_negative_amount_minor
FROM wallet_account
WHERE seller_id = $1
FOR UPDATE;

-- name: InsertHold :exec
INSERT INTO wallet_hold (id, wallet_id, seller_id, amount_minor, expires_at, status)
VALUES ($1, $2, $3, $4, $5, 'active');

-- name: IncrementWalletHold :exec
UPDATE wallet_account
SET hold_total_minor = hold_total_minor + $2,
    updated_at = now()
WHERE id = $1;
```

Annotations:
- `:one` — query returns exactly one row.
- `:many` — returns multiple rows.
- `:exec` — no return rows.
- `:execrows` — returns rows-affected.

### Generated code

`sqlc generate` produces `internal/db/wallet.sql.go` with typed methods:

```go
package db

func (q *Queries) GetWalletForUpdate(ctx context.Context, sellerID core.SellerID) (GetWalletForUpdateRow, error)
func (q *Queries) InsertHold(ctx context.Context, arg InsertHoldParams) error
func (q *Queries) IncrementWalletHold(ctx context.Context, arg IncrementWalletHoldParams) error
```

The `Queries` struct has a constructor:
```go
func New(db DBTX) *Queries
func (q *Queries) WithTx(tx pgx.Tx) *Queries
```

`DBTX` is an interface that both `*pgxpool.Pool` and `pgx.Tx` satisfy — meaning the same query methods work on either pool-direct or in-tx.

### Repos use sqlc

```go
// internal/wallet/repo.go
package wallet

import (
    "context"

    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/db"
)

type repo struct {
    pool    *pgxpool.Pool
    queries *db.Queries
}

func newRepo(pool *pgxpool.Pool) *repo {
    return &repo{pool: pool, queries: db.New(pool)}
}

// QueryWithTx returns a Queries scoped to the given transaction.
// Used inside dbtx.WithSellerTx callbacks.
func (r *repo) QueryWithTx(tx pgx.Tx) *db.Queries {
    return r.queries.WithTx(tx)
}
```

The domain service holds the repo; the repo's queries are accessed through transactions.

## Migrations with golang-migrate

### File naming

```
migrations/
├── 0001_init_schema.up.sql        ← creates roles, extensions
├── 0001_init_schema.down.sql      ← drops them
├── 0002_create_seller.up.sql
├── 0002_create_seller.down.sql
├── 0003_create_wallet.up.sql
├── 0003_create_wallet.down.sql
└── ...
```

Sequential 4-digit versions. Always paired up/down. Both required; CI lint enforces.

### Init schema migration (the very first)

```sql
-- migrations/0001_init_schema.up.sql

-- Extensions
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Roles
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_app') THEN
        CREATE ROLE pikshipp_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_reports') THEN
        CREATE ROLE pikshipp_reports NOLOGIN BYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pikshipp_admin') THEN
        CREATE ROLE pikshipp_admin NOLOGIN BYPASSRLS;
    END IF;
END
$$;

-- Schema migrations table is created by golang-migrate itself; no action here.
```

```sql
-- migrations/0001_init_schema.down.sql
DROP ROLE IF EXISTS pikshipp_app;
DROP ROLE IF EXISTS pikshipp_reports;
DROP ROLE IF EXISTS pikshipp_admin;
-- Extensions left in place (other migrations may depend).
```

### Standard pattern for table migrations

Every domain table follows this template:

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
    CONSTRAINT wallet_account_status_valid CHECK (status IN ('active', 'frozen', 'wound_down'))
);

CREATE INDEX wallet_account_seller_idx ON wallet_account (seller_id);

-- updated_at trigger
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER wallet_account_updated_at
BEFORE UPDATE ON wallet_account
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- RLS
ALTER TABLE wallet_account ENABLE ROW LEVEL SECURITY;

CREATE POLICY wallet_account_seller ON wallet_account
    FOR ALL
    TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

-- Grants
GRANT SELECT, INSERT, UPDATE ON wallet_account TO pikshipp_app;
GRANT SELECT ON wallet_account TO pikshipp_reports;
GRANT ALL ON wallet_account TO pikshipp_admin;
```

```sql
-- migrations/00NN_create_wallet.down.sql

DROP TRIGGER IF EXISTS wallet_account_updated_at ON wallet_account;
DROP TABLE IF EXISTS wallet_account;
```

### Migration command (manual or CI)

```bash
migrate -path migrations -database "$DATABASE_URL" up
migrate -path migrations -database "$DATABASE_URL" down 1
migrate -path migrations -database "$DATABASE_URL" version
```

### Schema version check on startup

```go
// internal/observability/dbtx/version.go

const RequiredSchemaVersion = 47  // bumped each release; matches latest migration

// CheckSchemaVersion returns an error if the current DB schema is older than required.
// Called from main during startup; binary refuses to start on mismatch.
func CheckSchemaVersion(ctx context.Context, pool *pgxpool.Pool) error {
    var version int
    var dirty bool
    err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty)
    if err != nil {
        return fmt.Errorf("schema_migrations: %w", err)
    }
    if dirty {
        return fmt.Errorf("schema is dirty at version %d; manual cleanup required", version)
    }
    if version < RequiredSchemaVersion {
        return fmt.Errorf("schema at version %d, binary requires %d", version, RequiredSchemaVersion)
    }
    return nil
}
```

`RequiredSchemaVersion` is bumped manually with each release that adds migrations.

## Testing patterns

### testcontainers setup (used by SLTs)

```go
// internal/observability/dbtx/testdb/testdb.go
package testdb

import (
    "context"
    "fmt"
    "testing"

    "github.com/golang-migrate/migrate/v4"
    _ "github.com/golang-migrate/migrate/v4/database/postgres"
    _ "github.com/golang-migrate/migrate/v4/source/file"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// New brings up a fresh Postgres testcontainer, runs migrations,
// and returns connection pools for each role.
func New(t *testing.T) Pools {
    t.Helper()
    ctx := context.Background()

    ctr, err := postgres.Run(ctx, "postgres:16-alpine",
        postgres.WithDatabase("pikshipp_test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
        postgres.WithInitScripts("../../../migrations/0001_init_schema.up.sql"),
        postgres.BasicWaitStrategies(),
    )
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = ctr.Terminate(ctx) })

    url, err := ctr.ConnectionString(ctx, "sslmode=disable")
    if err != nil { t.Fatal(err) }

    // Run all migrations
    m, err := migrate.New("file://../../../migrations", url)
    if err != nil { t.Fatal(err) }
    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        t.Fatal(err)
    }

    // Construct pools (admin pool for tests; tests can switch as needed)
    poolApp, err := pgxpool.New(ctx, url)
    if err != nil { t.Fatal(err) }
    t.Cleanup(poolApp.Close)

    return Pools{App: poolApp, ConnURL: url}
}

type Pools struct {
    App     *pgxpool.Pool
    ConnURL string  // for additional pool construction
}
```

### Per-test data isolation

Each SLT either:
- Creates a fresh seller_id and works under it. Other tests' sellers don't interfere via RLS.
- OR uses `TRUNCATE` between tests (slower; only for tests that span sellers).

### Transaction tests

```go
func TestWithSellerTx_RLS(t *testing.T) {
    p := testdb.New(t)
    sellerA := core.NewSellerID()
    sellerB := core.NewSellerID()

    // Insert as A
    err := dbtx.WithSellerTx(context.Background(), p.App, sellerA, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx, "INSERT INTO foo (seller_id, value) VALUES ($1, 'a')", sellerA.String())
        return err
    })
    require.NoError(t, err)

    // Read as B; should see nothing
    err = dbtx.WithSellerTx(context.Background(), p.App, sellerB, func(ctx context.Context, tx pgx.Tx) error {
        rows, err := tx.Query(ctx, "SELECT * FROM foo")
        require.NoError(t, err)
        if rows.Next() {
            t.Errorf("seller B leaked seller A's row")
        }
        rows.Close()
        return nil
    })
    require.NoError(t, err)
}
```

## Performance budget

- `WithSellerTx` overhead (begin + SET LOCAL + commit): < 2ms. Dominated by network RTT to RDS.
- Single sqlc-generated query: depends on query, but framework adds < 100µs.
- Connection acquisition from pool: typically < 1ms when pool is healthy.

Microbenchmark gate: SLT measures wall-clock for repeated `WithSellerTx`; alerts on regression.

## Failure modes

| Failure | Behavior |
|---|---|
| Pool exhausted | `BeginTx` blocks up to a configurable timeout (default 30s); then returns `ErrPoolTimeout`; handler returns 503 |
| Connection drops mid-tx | pgx returns error; helper rolls back (no-op since connection is dead); caller retries via outer logic |
| `SET LOCAL app.seller_id` fails | Likely role permission issue; returns error; caller sees 500 (config bug) |
| Statement timeout | Returns pg error code `57014`; classify as retryable for SELECT, fatal for write |
| Serialization failure | `IsRetryableError` returns true; caller may retry (max 3) |

## Open questions

- pgx's `RowsAffected` vs sqlc's `:execrows`: ensure consistency. We use sqlc's `:execrows` for delete/update where count matters.
- Connection pool sizing per role: app=50, reports=10, admin=5 — tune at v1 with measurements.
- Logical replication for the `pikshipp_reports` role to a read replica: deferred to v2.

## References

- HLD `00-tenets.md` § 3 (persistence rules).
- HLD `01-architecture/02-data-and-transactions.md` (transaction patterns).
- HLD `04-cross-cutting/08-postgres-tuning.md` (parameter group settings).
- ADR 0005 (RLS).
- ADR 0009 (migrations as CI step).
