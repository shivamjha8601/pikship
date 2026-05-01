# Cross-cutting: Postgres configuration & tuning

> Version, extensions, parameter group settings, vacuum strategy, indexing principles, slow query handling.

## Version

**PostgreSQL 16.x** on AWS RDS. We track latest minor; major upgrades on a controlled schedule (typically 6 months after GA).

## Extensions

| Extension | Purpose | When loaded |
|---|---|---|
| `pgcrypto` | `gen_random_uuid()`, `digest()` | v0 |
| `pg_trgm` | Trigram indexes for fuzzy search (buyer name, seller name) | v0 |
| `btree_gin` | GIN indexes on btree types when combined | v1 if needed |
| `pg_stat_statements` | Query performance instrumentation | v0 (load on db parameter group) |
| `auto_explain` | Capture slow query plans to logs | v0 (load on db parameter group, threshold 1s) |

We do **not** use:
- `uuid-ossp` (deprecated; use `gen_random_uuid()` from pgcrypto).
- TimescaleDB (not on RDS by default; not justified).
- PostGIS (no spatial needs at v0/v1).

## Parameter group settings (RDS custom param group)

Defaults are mostly fine. Overrides:

| Parameter | Value | Why |
|---|---|---|
| `shared_buffers` | 25% of RAM (RDS default for db.t4g) | Standard |
| `effective_cache_size` | 50% of RAM | Tells planner about available cache |
| `work_mem` | 16MB | Per-sort/hash; bumped from default 4MB for our reporting queries |
| `maintenance_work_mem` | 256MB | Faster VACUUM and index builds |
| `max_connections` | 200 (default for t4g.medium ~225) | Capped to leave overhead |
| `random_page_cost` | 1.1 | gp3 SSD; default 4 is for spinning disks |
| `effective_io_concurrency` | 200 | gp3 supports parallel I/O |
| `log_min_duration_statement` | 1000 (1s) | Log slow queries |
| `log_lock_waits` | on | Surface long lock waits |
| `log_temp_files` | 10MB | Catch unexpected disk sorts |
| `log_autovacuum_min_duration` | 1000 | Watch autovacuum pressure |
| `auto_explain.log_min_duration` | 1000 | EXPLAIN slow queries |
| `track_io_timing` | on | Per-query I/O timing |
| `track_functions` | pl | Function-level stats |
| `pg_stat_statements.track` | top | Top-level statements |

**Apply**: via Terraform-managed `aws_db_parameter_group`.

## Connection management

### From the app
- pgxpool with config above (capacity-planning doc).
- Statement-level timeout enforced via `SET LOCAL statement_timeout = N` per request (default 5s; bump for known-slow operations).

### RDS settings
- `idle_in_transaction_session_timeout = 60s` — kills idle transactions; protects against runaway tx.
- `statement_timeout = 30s` (cluster-wide hard ceiling).
- `lock_timeout = 5s` — fail fast on lock contention rather than wait forever.

## Indexing principles

1. **Always**: `(seller_id, primary_business_column)` on every seller-scoped table.
2. **Always**: `(seller_id, created_at DESC)` on tables we paginate.
3. **Always**: foreign key indexes on parent table FK columns.
4. **Partial indexes** for filtered queries (e.g., `WHERE enqueued_at IS NULL`).
5. **GIN trgm** for free-text search (`buyer_name`, `notes`).
6. **No covering indexes (INCLUDE)** unless we measure benefit; bloat cost.
7. **Composite over multiple separates**: prefer `(seller_id, status, created_at)` over three single-column indexes if all three appear together.

### Common index patterns for our tables

```sql
-- Tracking events by shipment, time-ordered:
CREATE INDEX tracking_event_shipment_time_idx ON tracking_event (shipment_id, occurred_at);

-- Tracking events idempotency:
CREATE UNIQUE INDEX tracking_event_carrier_evid_idx ON tracking_event (carrier_id, carrier_event_id);

-- Tracking events by seller for RLS:
CREATE INDEX tracking_event_seller_recorded_idx ON tracking_event (seller_id, recorded_at DESC);

-- Outbox pending:
CREATE INDEX outbox_pending_idx ON outbox_event (created_at)
  WHERE enqueued_at IS NULL;

-- Wallet ledger by seller, time:
CREATE INDEX wallet_ledger_seller_posted_idx ON wallet_ledger_entry (seller_id, posted_at DESC);

-- Wallet ledger idempotency:
CREATE UNIQUE INDEX wallet_ledger_idempotency ON wallet_ledger_entry (ref_type, ref_id, direction);
```

## Vacuum & autovacuum

### Autovacuum settings
| Parameter | Value | Why |
|---|---|---|
| `autovacuum_naptime` | 60s | Default; check tables every minute |
| `autovacuum_vacuum_threshold` | 50 | Default |
| `autovacuum_vacuum_scale_factor` | 0.10 (default 0.20) | Trigger autovac at 10% dead rows on hot tables |
| `autovacuum_analyze_scale_factor` | 0.05 | Keep stats fresh |
| `autovacuum_max_workers` | 5 (default 3) | More parallelism |
| `autovacuum_work_mem` | 256MB | Faster vacuum on big tables |

### Per-table tuning for hot tables

```sql
-- High-churn tables get more aggressive autovac
ALTER TABLE outbox_event SET (autovacuum_vacuum_scale_factor = 0.05);
ALTER TABLE tracking_event SET (autovacuum_vacuum_scale_factor = 0.05);
ALTER TABLE session SET (autovacuum_vacuum_scale_factor = 0.05);
ALTER TABLE idempotency_key SET (autovacuum_vacuum_scale_factor = 0.05);
```

### Bloat monitoring

A weekly river job:
- Queries `pg_stat_all_tables` for `n_dead_tup` ratios.
- Audit-emits any table with `dead_tup_ratio > 30%`.
- Manual `VACUUM FULL` with downtime if a table can't keep up (rare).

## Slow query handling

### Detection
- `log_min_duration_statement = 1s` logs all slow queries.
- `auto_explain` logs query plans.
- `pg_stat_statements` aggregates over time; weekly review.

### Triage
1. Identify slow query from logs.
2. `EXPLAIN ANALYZE` against staging.
3. Check missing index, sequential scan, bad statistics.
4. If query in code: optimize SQL, add index, or rewrite.
5. If query in vendor (river, sqlc-generated): file issue or work around.

### Hard rules
- No query may take > 30s (cluster timeout).
- No query in the request path may take > 1s without an explicit reason and approval.
- No query may scan > 100k rows in the request path.

## Backup & restore

### Automated
- RDS automated backups: daily snapshot at 03:00 IST, 7-day retention.
- Transaction logs continuously archived for point-in-time recovery (PITR).
- RPO: 5 minutes.

### Manual
- Pre-release snapshot: `aws rds create-db-snapshot` before any major release.
- Retention: until verified post-release stable, then 30 days.

### Restore drill
- Quarterly: spin up a snapshot copy in staging.
- Verify integrity (`SELECT count(*)` per major table; checksum invariants).
- Verify our binary boots and `/readyz` returns 200.
- Document timing: target 30 minutes from snapshot ID to running.

## Read scaling

At v0/v1: single primary; no read replica.

At v2: add a read replica for the reports module (which uses `pikshipp_reports` role).

Replication lag tolerance: reports may show data that's up to 60 seconds stale. Acceptable.

## Monitoring & alerts

CloudWatch metrics for RDS:
- CPU > 70% sustained (1h) → warn.
- Storage < 20% free → warn.
- Connection count > 80% of `max_connections` → warn.
- Replication lag > 60s → warn.
- Free memory < 10% → warn.
- Failed login count > threshold → alert.

`pg_stat_statements`-based weekly report:
- Top 20 slowest queries by total time.
- Top 20 most-called queries.
- Surfaced to dev team.

## Locks and contention

### What we avoid
- `LOCK TABLE` (never).
- Long transactions holding row locks.
- `SELECT ... FOR UPDATE` without `WHERE` covering an index.
- `ALTER TABLE` in business hours.

### What we accept
- `FOR UPDATE` on `wallet_account` rows (single-row lock, brief tx).
- Advisory locks for migrations and singleton operations.

### Monitoring
- `pg_locks` query in admin console.
- Alert on lock waits > 5s.

## Schema evolution patterns

### Expand / contract for column changes

```sql
-- Old: column `phone TEXT`
-- New: column `phone E164` (validated)

-- Step 1 (release N): add new column, populate from old
ALTER TABLE app_user ADD COLUMN phone_e164 TEXT;
UPDATE app_user SET phone_e164 = normalize_e164(phone) WHERE phone IS NOT NULL;

-- Step 2 (release N): app reads from new, falls back to old; writes to both

-- Step 3 (release N+1): app reads from new only; writes to new only

-- Step 4 (release N+2): drop old column
ALTER TABLE app_user DROP COLUMN phone;
```

This works at multi-instance because the app's read/write behavior changes over multiple releases, never breaking the previous version.

### Adding NOT NULL columns

Always use a default:
```sql
ALTER TABLE foo ADD COLUMN bar TEXT NOT NULL DEFAULT '';
-- Backfill in subsequent migration
-- Drop default if not desired
```

### Adding indexes

`CREATE INDEX CONCURRENTLY` always (avoids locking the table):
```sql
CREATE INDEX CONCURRENTLY idx_name ON table (column);
```

Cannot be inside a transaction; `golang-migrate` supports this via `--no-tx` directive in the migration file.

## Don't-do list

- No stored procedures (use Go).
- No triggers except for `updated_at` auto-update.
- No materialized views at v0 (use plain views or aggregation tables).
- No replication slots without an owner.
- No JSONB schemas without documented payload structure.

## Open questions

- TimescaleDB for `tracking_event` and `audit_event` (time-series-shaped data)? Not on RDS by default. Defer until performance demands.
- Sharding strategy if we ever hit single-DB limits? `seller_id` range partitioning is the obvious choice; v3+ concern.
