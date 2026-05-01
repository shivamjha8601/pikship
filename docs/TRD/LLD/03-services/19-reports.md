# Reports Service

## Purpose

The reports service is the **read-only analytical layer** over the operational database. It exists to:

- Power seller dashboard widgets (today's bookings, this-week's COD, NDR rates).
- Generate scheduled CSV/Excel exports (daily activity, monthly statements).
- Provide ops with cross-seller queries (top movers, carrier health snapshots).
- Insulate the operational tables from heavy aggregation queries.

Reports never **mutate** state; the service holds no domain logic. Its design priorities are:

1. **Fast cached reads** for the small, hot set of widget queries.
2. **Streaming exports** that don't blow up memory for million-row CSVs.
3. **Read-replica friendliness** — every query uses the `pikshipp_reports` role (BYPASSRLS), so when we add a Postgres read replica, this service can route to it without code changes.

Out of scope:

- ETL into a warehouse — out of scope for v0; future v1+ work.
- Complex BI tooling — sellers download CSV; ops use the admin console.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | money, IDs, errors. |
| `internal/db` | a separate `*pgxpool.Pool` configured with the `pikshipp_reports` role. |
| `internal/policy` | a few report toggles (e.g., enable enterprise reports). |

The reports service does NOT depend on any operational service (orders, shipments, wallet). It reads tables directly via SQL.

## Package Layout

```
internal/reports/
├── service.go             // Service interface
├── service_impl.go
├── widgets.go             // Dashboard widget queries
├── exports.go             // Streaming CSV/Excel exports
├── stats.go               // Aggregation helpers
├── repo.go                // pre-built statements
├── types.go               // Widget/Export struct definitions
├── jobs.go                // Scheduled exports (DailyDigestJob, MonthlyStatementJob)
├── service_test.go
└── service_slt_test.go
```

## Public API

```go
package reports

type Service interface {
    // Dashboard widgets — small, fast queries cached for ~30s
    GetSellerDashboard(ctx context.Context, sellerID core.SellerID) (*SellerDashboard, error)
    GetOpsDashboard(ctx context.Context) (*OpsDashboard, error)

    // Exports (streaming)
    ExportShipments(ctx context.Context, w io.Writer, q ShipmentExportQuery) error
    ExportOrders(ctx context.Context, w io.Writer, q OrderExportQuery) error
    ExportWalletStatement(ctx context.Context, w io.Writer, sellerID core.SellerID, period TimeRange) error
    ExportCODSettlement(ctx context.Context, w io.Writer, sellerID core.SellerID, period TimeRange) error

    // Scheduled report enqueuers
    GenerateDailyDigest(ctx context.Context, sellerID core.SellerID, day time.Time) (*DigestArtifact, error)
    GenerateMonthlyStatement(ctx context.Context, sellerID core.SellerID, monthStart time.Time) (*StatementArtifact, error)

    // Ops queries
    GetCarrierHealthSummary(ctx context.Context, since time.Time) ([]*CarrierHealthRow, error)
    GetSellerActivity(ctx context.Context, since time.Time) ([]*SellerActivityRow, error)
}
```

### Widget Types

```go
type SellerDashboard struct {
    Today TodaySnapshot
    Week  WeekSnapshot

    NDR       NDRSnapshot
    COD       CODSnapshot
    Wallet    WalletSnapshot
    Carriers  []CarrierSnapshot

    GeneratedAt time.Time
}

type TodaySnapshot struct {
    OrdersCreated      int
    ShipmentsBooked    int
    Delivered          int
    InTransit          int
    Failed             int
}

type WeekSnapshot struct {
    Days []DailyMetric  // last 7 days
}

type DailyMetric struct {
    Date            time.Time
    OrdersCreated   int
    ShipmentsBooked int
    Delivered       int
}

type NDRSnapshot struct {
    OpenCases         int
    AwaitingDecision  int
    LastWeekRTORate   float64
}

type CODSnapshot struct {
    CollectedAwaitingRemitPaise core.Paise
    RemittedAwaitingSettlePaise core.Paise
    SettledThisMonthPaise       core.Paise
}

type WalletSnapshot struct {
    BalancePaise        core.Paise
    HoldPaise           core.Paise
    LastWeekDebitPaise  core.Paise
    LastWeekCreditPaise core.Paise
}

type CarrierSnapshot struct {
    CarrierCode string
    Bookings7d  int
    Delivered7d int
    SLABreaches int
}
```

## Caching

Dashboard widgets are read on every page-load; we cache per-seller for 30 seconds.

```go
type widgetCache struct {
    mu      sync.RWMutex
    entries map[core.SellerID]widgetCacheEntry
    ttl     time.Duration
}

type widgetCacheEntry struct {
    dash      *SellerDashboard
    fetchedAt time.Time
}

func (c *widgetCache) Get(id core.SellerID) (*SellerDashboard, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    e, ok := c.entries[id]
    if !ok || time.Since(e.fetchedAt) > c.ttl {
        return nil, false
    }
    return e.dash, true
}
func (c *widgetCache) Set(id core.SellerID, d *SellerDashboard) { /* ... */ }
```

The cache is **soft**: stale-on-miss is acceptable; we don't want a thundering herd to compute 50 queries simultaneously, so misses serialize per-seller via `singleflight.Group`.

```go
func (s *service) GetSellerDashboard(ctx context.Context, sellerID core.SellerID) (*SellerDashboard, error) {
    if d, ok := s.cache.Get(sellerID); ok {
        return d, nil
    }
    v, err, _ := s.sf.Do(string(sellerID), func() (any, error) {
        d, err := s.computeSellerDashboard(ctx, sellerID)
        if err != nil {
            return nil, err
        }
        s.cache.Set(sellerID, d)
        return d, nil
    })
    if err != nil {
        return nil, err
    }
    return v.(*SellerDashboard), nil
}
```

## Widget Implementation

Every widget query runs as one SQL statement against `pikshipp_reports` pool. The dashboard uses ~6 queries; we fan them out concurrently.

```go
func (s *service) computeSellerDashboard(ctx context.Context, sellerID core.SellerID) (*SellerDashboard, error) {
    g, gctx := errgroup.WithContext(ctx)

    var today TodaySnapshot
    var week []DailyMetric
    var ndr NDRSnapshot
    var cod CODSnapshot
    var wallet WalletSnapshot
    var carriers []CarrierSnapshot

    g.Go(func() error { return s.queryToday(gctx, sellerID, &today) })
    g.Go(func() error { return s.queryWeek(gctx, sellerID, &week) })
    g.Go(func() error { return s.queryNDR(gctx, sellerID, &ndr) })
    g.Go(func() error { return s.queryCOD(gctx, sellerID, &cod) })
    g.Go(func() error { return s.queryWallet(gctx, sellerID, &wallet) })
    g.Go(func() error { return s.queryCarriers(gctx, sellerID, &carriers) })

    if err := g.Wait(); err != nil {
        return nil, err
    }
    return &SellerDashboard{
        Today: today, Week: WeekSnapshot{Days: week},
        NDR: ndr, COD: cod, Wallet: wallet, Carriers: carriers,
        GeneratedAt: s.clock.Now(),
    }, nil
}
```

### Example Widget Query (Today)

```sql
-- Single query for today's snapshot.
SELECT
    count(*) FILTER (WHERE created_at >= today_start AND created_at < tomorrow_start) AS orders_today,
    count(*) FILTER (WHERE booked_at  >= today_start AND booked_at  < tomorrow_start) AS shipments_today,
    count(*) FILTER (WHERE delivered_at >= today_start AND delivered_at < tomorrow_start) AS delivered_today,
    count(*) FILTER (WHERE state = 'in_transit') AS in_transit,
    count(*) FILTER (WHERE state = 'failed') AS failed
FROM
    (SELECT created_at, NULL::timestamptz AS booked_at, NULL::timestamptz AS delivered_at, NULL::text AS state
       FROM order_record WHERE seller_id = $1
     UNION ALL
     SELECT NULL, booked_at, NULL, state FROM shipment WHERE seller_id = $1
     UNION ALL
     SELECT NULL, NULL, occurred_at, NULL FROM tracking_event
       WHERE seller_id = $1 AND canonical_status = 'delivered') x,
    (SELECT date_trunc('day', now()) AS today_start,
            date_trunc('day', now()) + interval '1 day' AS tomorrow_start) t;
```

For widgets where a single SQL is awkward, we run a few. The point is: every widget is a small, indexed query that executes in < 50ms p99.

## Streaming Exports

Exports must not buffer entire result sets in memory. We use cursor-based pagination via `LIMIT` + `OFFSET` (or keyset for large exports), writing rows directly to the `io.Writer`.

```go
package reports

func (s *service) ExportShipments(ctx context.Context, w io.Writer, q ShipmentExportQuery) error {
    cw := csv.NewWriter(w)
    defer cw.Flush()

    if err := cw.Write(shipmentExportHeaders); err != nil {
        return err
    }

    const pageSize = 1000
    var afterID int64
    for {
        rows, err := s.q.ShipmentExportPage(ctx, sqlcgen.ShipmentExportPageParams{
            SellerID:  q.SellerID.UUID(),
            DateFrom:  q.DateFrom,
            DateTo:    q.DateTo,
            States:    q.States,
            AfterID:   afterID,
            Limit:     pageSize,
        })
        if err != nil {
            return err
        }
        for _, r := range rows {
            if err := cw.Write(shipmentToCSVRow(r)); err != nil {
                return err
            }
            afterID = r.ID
        }
        cw.Flush()
        if err := cw.Error(); err != nil {
            return err
        }
        if len(rows) < pageSize {
            return nil
        }
        // Yield to allow tx idle; pgx pool returns connection between pages.
    }
}

var shipmentExportHeaders = []string{
    "shipment_id","order_id","awb","carrier","state",
    "booked_at","delivered_at","drop_pincode",
    "weight_g","charges_paise","cod_amount_paise",
}
```

Exports run via the **api** binary (not workers) and write to the HTTP response. For scheduled exports (daily digest), the worker streams to S3 and emits a `report.generated` event with the object URL.

### Keyset Pagination Query

```sql
-- name: ShipmentExportPage :many
SELECT id, order_id, awb, carrier_code, state,
       booked_at, drop_pincode, package_weight_g, charges_paise, cod_amount_paise
FROM shipment
WHERE seller_id = $1
  AND ($2::timestamptz IS NULL OR booked_at >= $2)
  AND ($3::timestamptz IS NULL OR booked_at <  $3)
  AND ($4::text[]      IS NULL OR state = ANY($4::text[]))
  AND ($5::bigint      = 0 OR id > $5)
ORDER BY id
LIMIT $6;
```

Keyset (`id > AfterID`) avoids OFFSET cost on large exports.

## Scheduled Reports

```go
type DailyDigestJob struct {
    river.JobArgs
    SellerID core.SellerID
    Day      time.Time
}
func (DailyDigestJob) Kind() string { return "reports.daily_digest" }

type DailyDigestWorker struct {
    river.WorkerDefaults[DailyDigestJob]
    svc *service
    s3  ObjectStore
    notif notifications.Service
}

func (w *DailyDigestWorker) Work(ctx context.Context, j *river.Job[DailyDigestJob]) error {
    artifact, err := w.svc.GenerateDailyDigest(ctx, j.Args.SellerID, j.Args.Day)
    if err != nil {
        return err
    }
    return w.notif.SendDailyDigest(ctx, notifications.DailyDigestRequest{
        SellerID:    j.Args.SellerID,
        ObjectKey:   artifact.ObjectKey,
        Day:         j.Args.Day,
    })
}
```

A scheduler enqueues `DailyDigestJob` for every active seller at 02:00 IST daily.

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `GetSellerDashboard` (cache hit) | 1 ms | 4 ms | map lookup + copy |
| `GetSellerDashboard` (cache miss) | 60 ms | 250 ms | 6 queries fan-out |
| Today widget single query | 8 ms | 35 ms | indexed |
| Week widget query | 15 ms | 60 ms | 7-day group-by |
| `ExportShipments` per 1k rows | 80 ms | 250 ms | mostly network |
| `ExportShipments` 100k rows | 12 s | 35 s | streaming |
| `GenerateDailyDigest` | 600 ms | 2.5 s | several queries + render |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Slow widget query (e.g., trigram search lag) | per-query timeout 1s | Skip that widget; return partial dashboard with `errors: ["carriers"]` field. |
| Reports pool saturated | `pool.Acquire` blocks | Bound concurrency at handler layer (semaphore); reject with 503 + Retry-After. |
| Long-running export hits idle-in-transaction timeout | per-page cursor | Pages are independent statements; no long-lived tx. |
| Cache stampede on dashboard | singleflight serializes | One query runs; others wait. |
| Stale cache after a write | 30s TTL | Acceptable for dashboard; sellers see fresh data within 30s. |

## Testing

```go
func TestSellerDashboard_Today_SLT(t *testing.T) { /* counts orders / shipments / delivered */ }
func TestExportShipments_StreamsAllRows_SLT(t *testing.T) {
    // Insert 5000 shipments; export; count CSV lines.
    // Assert peak memory < 50 MB during export.
}
func TestExportShipments_KeysetPaginationOrdering_SLT(t *testing.T) { /* monotonic IDs */ }
func TestCacheTTL_Honored(t *testing.T) { /* ... */ }
func TestSingleflight_DedupedConcurrentRequests(t *testing.T) { /* ... */ }
```

## Open Questions

1. **Read replica.** Currently `pikshipp_reports` connects to the same primary as `pikshipp_app`. The point of using BYPASSRLS + a separate role is to make replica routing a config change later. **Decision: ship single-replica architecture for v0**; add replica when reports queries exceed 10% of primary CPU.
2. **Materialized views.** A few queries (carrier_health_summary) could benefit. **Decision:** measure first; introduce if a single widget exceeds 200ms p99.
3. **Cross-seller ops dashboard.** v0 returns a single op dashboard with cross-seller aggregates. **Decision: keep as-is**; if we add SLAs/billing per seller, build proper grouping.
4. **Excel exports.** Sellers occasionally ask for .xlsx. **Decision: CSV only for v0**; xlsx via `xuri/excelize` if requested by 3+ sellers.
5. **Long-running exports via async job.** > 100k rows takes > 30s; HTTP timeout risk. **Decision:** add `POST /reports/exports` (returns job_id) + `GET /reports/exports/:id` polling at v0.5; for v0 we cap inline export at 50k rows.

## References

- HLD §02-data-model.md: tables we read from.
- HLD §04-cross-cutting/06-postgres-tuning.md: read replica plan.
- LLD §02-infrastructure/01-database-access: separate pool for reports role.
- LLD §03-services/05-wallet through §18-recon: source data.
- LLD §03-services/20-notifications: scheduled report delivery.
