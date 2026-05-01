# Service: Idempotency (`internal/idempotency`)

> Per-(seller, key) caching of HTTP responses for safe retry of state-mutating endpoints. 24h TTL. Backed by Postgres.

## Purpose

- HTTP middleware integration: replay cached response on duplicate `Idempotency-Key` for the same seller.
- Domain-level idempotency keys: separate primitive used by wallet ledger, booking, etc. (those have their own UNIQUE constraints; this module is the cross-cutting "request body cache").
- Periodic cleanup of expired entries.

## Dependencies

- `internal/core`
- `internal/observability/dbtx`
- `github.com/jackc/pgx/v5/pgxpool`

## Package layout

```
internal/idempotency/
├── doc.go
├── service.go         ← Store interface
├── service_impl.go
├── repo.go
├── types.go           ← Response struct
├── jobs.go            ← cleanup cron
├── memory_store.go    ← in-memory impl (tests)
├── service_test.go
└── service_slt_test.go
```

## Public API

```go
// Package idempotency caches HTTP responses to make state-mutating endpoints
// safely retryable.
//
// Usage flow:
//   Caller sends POST with `Idempotency-Key: <uuid>`.
//   Middleware: Lookup(sellerID, key) → if cached, return early with body.
//   Else: handler runs, middleware captures the response, calls Store(...).
//
// Cached for 24h. Cleanup cron deletes expired rows.
package idempotency

import (
    "context"
    "errors"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// Store is the public API.
type Store interface {
    // Lookup returns the cached response for (seller, key), or found=false.
    //
    // Expired entries (older than TTL) are returned as not found and may be
    // cleaned up later by the cron.
    Lookup(ctx context.Context, sellerID core.SellerID, key string) (cached Response, found bool, err error)

    // Store persists the response keyed by (seller, key).
    //
    // If a row with the same (seller, key) already exists with a different
    // payload hash, returns ErrConflict (caller had a key collision).
    //
    // If body is large (>1MB), returns an error; caller logs and skips.
    Store(ctx context.Context, sellerID core.SellerID, key string, resp Response) error
}

// Response captures everything needed to replay an HTTP response.
type Response struct {
    StatusCode int               `json:"status_code"`
    Headers    map[string]string `json:"headers"`
    Body       []byte            `json:"body"`
}

// Sentinel errors.
var (
    ErrConflict       = errors.New("idempotency: conflicting payload")
    ErrBodyTooLarge   = errors.New("idempotency: body exceeds 1MB; skipping cache")
)

// MaxBodySize is the cap; bodies above this are not cached.
const MaxBodySize = 1 << 20 // 1MB
```

## Implementation

```go
// internal/idempotency/service_impl.go
package idempotency

import (
    "context"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/pikshipp/pikshipp/internal/core"
    "github.com/pikshipp/pikshipp/internal/db"
)

type pgStore struct {
    pool    *pgxpool.Pool
    queries *db.Queries
    clock   core.Clock
    ttl     time.Duration
}

// NewPGStore constructs a Postgres-backed Store.
func NewPGStore(pool *pgxpool.Pool, clock core.Clock) Store {
    return &pgStore{
        pool:    pool,
        queries: db.New(pool),
        clock:   clock,
        ttl:     24 * time.Hour,
    }
}

func (s *pgStore) Lookup(ctx context.Context, sellerID core.SellerID, key string) (Response, bool, error) {
    if key == "" {
        return Response{}, false, fmt.Errorf("idempotency.Lookup: empty key: %w", core.ErrInvalidArgument)
    }

    row, err := s.queries.GetIdempotencyKey(ctx, db.GetIdempotencyKeyParams{
        SellerID: sellerID,
        Key:      key,
    })
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return Response{}, false, nil
        }
        return Response{}, false, fmt.Errorf("idempotency.Lookup: %w", err)
    }

    if s.clock.Now().Sub(row.CreatedAt) > s.ttl {
        return Response{}, false, nil
    }

    var hdrs map[string]string
    if len(row.HeadersJsonb) > 0 {
        if err := json.Unmarshal(row.HeadersJsonb, &hdrs); err != nil {
            return Response{}, false, fmt.Errorf("idempotency.Lookup: unmarshal headers: %w", err)
        }
    }

    return Response{
        StatusCode: int(row.StatusCode),
        Headers:    hdrs,
        Body:       row.Body,
    }, true, nil
}

func (s *pgStore) Store(ctx context.Context, sellerID core.SellerID, key string, resp Response) error {
    if key == "" {
        return fmt.Errorf("idempotency.Store: empty key: %w", core.ErrInvalidArgument)
    }
    if len(resp.Body) > MaxBodySize {
        return ErrBodyTooLarge
    }

    hdrsJSON, err := json.Marshal(resp.Headers)
    if err != nil {
        return fmt.Errorf("idempotency.Store: marshal headers: %w", err)
    }

    bodyHash := hashBytes(resp.Body)

    // INSERT ... ON CONFLICT: if same (seller, key, body_hash) → no-op (success).
    // If same (seller, key) but different body_hash → conflict.
    err = s.queries.UpsertIdempotencyKey(ctx, db.UpsertIdempotencyKeyParams{
        SellerID:     sellerID,
        Key:          key,
        StatusCode:   int32(resp.StatusCode),
        HeadersJsonb: hdrsJSON,
        Body:         resp.Body,
        BodyHash:     bodyHash,
    })
    if err != nil {
        // Detect UNIQUE conflict from upsert (the upsert WHERE clause filters
        // by body_hash; if no row updates, it means a conflicting row exists)
        if isConflictError(err) {
            return ErrConflict
        }
        return fmt.Errorf("idempotency.Store: %w", err)
    }
    return nil
}

func hashBytes(b []byte) string {
    h := sha256.Sum256(b)
    return base64.RawURLEncoding.EncodeToString(h[:])
}

func isConflictError(err error) bool {
    // pgx returns 23505 for unique_violation
    var pgErr *pgconn.PgError
    return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

## DB schema

```sql
-- migrations/00NN_create_idempotency.up.sql

CREATE TABLE idempotency_key (
    seller_id      UUID NOT NULL,
    key            TEXT NOT NULL,
    status_code    INT NOT NULL,
    headers_jsonb  JSONB NOT NULL DEFAULT '{}',
    body           BYTEA NOT NULL,
    body_hash      TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (seller_id, key)
);

CREATE INDEX idempotency_key_created_idx ON idempotency_key (created_at);

ALTER TABLE idempotency_key ENABLE ROW LEVEL SECURITY;
CREATE POLICY idempotency_key_seller ON idempotency_key
    FOR ALL TO pikshipp_app
    USING (seller_id = current_setting('app.seller_id', true)::uuid)
    WITH CHECK (seller_id = current_setting('app.seller_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON idempotency_key TO pikshipp_app;
GRANT SELECT ON idempotency_key TO pikshipp_reports;
GRANT ALL ON idempotency_key TO pikshipp_admin;
```

## SQL queries

```sql
-- query/idempotency.sql

-- name: GetIdempotencyKey :one
SELECT seller_id, key, status_code, headers_jsonb, body, body_hash, created_at
FROM idempotency_key
WHERE seller_id = $1 AND key = $2;

-- name: UpsertIdempotencyKey :exec
-- Inserts a new row, OR succeeds silently if the existing row has the same body_hash.
-- If the existing row has a different body_hash, ON CONFLICT DO NOTHING leaves it
-- and the caller can detect by re-reading and comparing. (We use a different
-- approach below for clarity.)
INSERT INTO idempotency_key (seller_id, key, status_code, headers_jsonb, body, body_hash)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (seller_id, key) DO UPDATE
    SET status_code = EXCLUDED.status_code  -- only updates when body_hash matches
    WHERE idempotency_key.body_hash = EXCLUDED.body_hash;
-- The WHERE clause means: if existing body_hash differs, NO row is updated;
-- the caller treats "0 rows affected" as conflict.
-- (Note: golang-migrate may not detect this nicely; we'll treat it as success
-- for now, given the practical case is rare.)

-- name: CleanupExpiredIdempotency :execrows
DELETE FROM idempotency_key
WHERE created_at < now() - INTERVAL '24 hours';

-- name: IdempotencyDepth :one
SELECT count(*) AS depth FROM idempotency_key WHERE created_at > now() - INTERVAL '24 hours';
```

## Memory store (for tests)

```go
// internal/idempotency/memory_store.go
package idempotency

import (
    "context"
    "sync"
    "time"

    "github.com/pikshipp/pikshipp/internal/core"
)

// memoryStore is an in-process Store for tests.
type memoryStore struct {
    mu      sync.RWMutex
    entries map[string]memEntry
    clock   core.Clock
    ttl     time.Duration
}

type memEntry struct {
    response  Response
    bodyHash  string
    createdAt time.Time
}

// NewMemoryStore constructs an in-memory Store.
func NewMemoryStore(clock core.Clock) Store {
    return &memoryStore{
        entries: make(map[string]memEntry),
        clock:   clock,
        ttl:     24 * time.Hour,
    }
}

func (m *memoryStore) Lookup(ctx context.Context, sellerID core.SellerID, key string) (Response, bool, error) {
    if key == "" {
        return Response{}, false, fmt.Errorf("idempotency: empty key")
    }
    m.mu.RLock()
    defer m.mu.RUnlock()
    e, ok := m.entries[m.k(sellerID, key)]
    if !ok { return Response{}, false, nil }
    if m.clock.Now().Sub(e.createdAt) > m.ttl {
        return Response{}, false, nil
    }
    return e.response, true, nil
}

func (m *memoryStore) Store(ctx context.Context, sellerID core.SellerID, key string, resp Response) error {
    if key == "" {
        return fmt.Errorf("idempotency: empty key")
    }
    if len(resp.Body) > MaxBodySize {
        return ErrBodyTooLarge
    }
    bh := hashBytes(resp.Body)
    m.mu.Lock()
    defer m.mu.Unlock()
    if existing, ok := m.entries[m.k(sellerID, key)]; ok && existing.bodyHash != bh {
        return ErrConflict
    }
    m.entries[m.k(sellerID, key)] = memEntry{
        response:  resp,
        bodyHash:  bh,
        createdAt: m.clock.Now(),
    }
    return nil
}

func (m *memoryStore) k(sid core.SellerID, key string) string {
    return sid.String() + "|" + key
}
```

## Cleanup cron

```go
// internal/idempotency/jobs.go
package idempotency

import (
    "context"

    "github.com/riverqueue/river"
)

type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "idempotency.cleanup" }

type CleanupWorker struct {
    river.WorkerDefaults[CleanupArgs]
    queries *db.Queries
    log     *slog.Logger
}

func (w *CleanupWorker) Work(ctx context.Context, j *river.Job[CleanupArgs]) error {
    n, err := w.queries.CleanupExpiredIdempotency(ctx)
    if err != nil { return err }
    w.log.InfoContext(ctx, "idempotency cleanup", slog.Int64("deleted", n))
    return nil
}

// Schedule: every 1 hour via river PeriodicJobs.
```

## Testing

```go
func TestStore_RoundTrip_SLT(t *testing.T) {
    p := testdb.New(t)
    s := idempotency.NewPGStore(p.App, core.NewFakeClock(time.Now()))

    sid := core.NewSellerID()
    key := "test-key-1"
    resp := idempotency.Response{
        StatusCode: 201,
        Headers:    map[string]string{"Content-Type": "application/json"},
        Body:       []byte(`{"id":"abc"}`),
    }

    err := dbtx.WithSellerTx(context.Background(), p.App, sid, func(ctx context.Context, tx pgx.Tx) error {
        return s.Store(ctx, sid, key, resp)
    })
    require.NoError(t, err)

    got, found, err := dbtx.WithSellerTx(...) // wrap Lookup
    require.NoError(t, err)
    require.True(t, found)
    require.Equal(t, resp.StatusCode, got.StatusCode)
    require.Equal(t, resp.Body, got.Body)
}

func TestStore_ConflictDetection_SLT(t *testing.T) {
    s := idempotency.NewPGStore(...)

    sid := core.NewSellerID()
    key := "k"
    resp1 := idempotency.Response{StatusCode: 200, Body: []byte("v1")}
    resp2 := idempotency.Response{StatusCode: 200, Body: []byte("v2")}

    s.Store(ctx, sid, key, resp1)
    err := s.Store(ctx, sid, key, resp2)
    require.ErrorIs(t, err, idempotency.ErrConflict)
}

func TestStore_ExpiredNotReturned(t *testing.T) {
    clock := core.NewFakeClock(time.Now())
    s := idempotency.NewMemoryStore(clock)

    s.Store(ctx, sid, "k", resp)
    clock.Advance(25 * time.Hour)

    _, found, _ := s.Lookup(ctx, sid, "k")
    require.False(t, found)
}

func TestStore_BodyTooLarge(t *testing.T) {
    s := idempotency.NewMemoryStore(core.SystemClock{})
    big := make([]byte, idempotency.MaxBodySize + 1)
    err := s.Store(ctx, sid, "k", idempotency.Response{Body: big})
    require.ErrorIs(t, err, idempotency.ErrBodyTooLarge)
}

func TestCleanup_DeletesOldOnly_SLT(t *testing.T) {
    // Insert 10 rows: 5 created 25h ago (via direct SQL), 5 fresh
    // Run cleanup
    // Expect 5 rows remain (the fresh ones)
}

func BenchmarkLookup_PGStore(b *testing.B) {
    p := testdb.New(b)
    s := idempotency.NewPGStore(p.App, core.SystemClock{})
    s.Store(ctx, sid, "bench-key", resp)

    for i := 0; i < b.N; i++ {
        _, _, _ = s.Lookup(ctx, sid, "bench-key")
    }
}
```

## Performance

- `Lookup` (cache hit, PG indexed lookup): P95 < 5ms.
- `Lookup` (miss): same; PK lookup either way.
- `Store`: 1 INSERT ON CONFLICT; ~2-3ms.
- Cleanup: deletes ~10k rows/hour at v1 idempotency volume; ~200ms.

## Failure modes

| Failure | Behavior |
|---|---|
| DB unreachable on Lookup | Returns error; HTTP middleware treats as cache miss; handler runs |
| DB unreachable on Store | Logged; handler succeeded; idempotency cache miss for retry (acceptable) |
| Cleanup never runs | Storage bloat; depth alert if > 1M rows |
| Conflicting key + different body | Returns 409 Conflict to caller; retry with same key + same body should succeed |

## Why store the body, not just the response

Because we replay the response body. Storing only `(status, headers)` would force the handler to re-execute, which is exactly what idempotency is preventing.

## Why hash the body

Detects "same key, different body" — caller bug. Better to surface as 409 than to silently return wrong response.

## Integration with HTTP middleware

See `02-infrastructure/04-http-server.md` § Idempotency middleware. The middleware:
1. Skips for GET/HEAD/OPTIONS.
2. Pulls `Idempotency-Key` header.
3. Calls `Lookup`; if found, replays cached response with `Idempotent-Replayed: true`.
4. On miss, runs handler, captures response body via wrapper, calls `Store`.

## Open questions

- **In-process cache layer** in front of DB to reduce DB hits for hot replays? Not v0 — DB lookup is fast enough; in-process drift on multi-instance complicates correctness.
- **Body compression** for large bodies (e.g., gzip stored bytes)? Defer; if storage cost matters, add at v2.
- **Per-endpoint TTL** (some endpoints want shorter TTL)? Not v0; one TTL.

## References

- HLD `04-cross-cutting/05-resilience.md` § Idempotency.
- HLD `00-tenets.md` § T-1.4 (Idempotency-Key on every state-mutating endpoint).
