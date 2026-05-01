# Service: Wallet & ledger

> **Module:** `internal/wallet/`
> **Maps to PRD:** [`PRD/04-features/13-wallet-and-billing.md`](../../../PRD/04-features/13-wallet-and-billing.md)
>
> Money is sacred. Double-entry ledger, two-phase ops, daily invariant check, paise-only arithmetic.

## Responsibility

- Two-phase wallet operations: Reserve / Confirm / Release.
- Direct posting for non-booking flows (recharge, refund, RTO, weight adjust).
- Daily ledger ↔ cached-balance invariant verification.
- Reverse-leg charge handling (RTO) with grace cap.
- Auto-recharge orchestration (deferred; not v0).

## Public interface

```go
package wallet

type Service interface {
    // Two-phase
    Reserve(ctx context.Context, sellerID core.SellerID, amount core.Paise, ttl time.Duration) (HoldID, error)
    Confirm(ctx context.Context, holdID HoldID, ref Ref) (LedgerEntryID, error)
    Release(ctx context.Context, holdID HoldID) error

    // Direct post (idempotent on ref)
    Post(ctx context.Context, sellerID core.SellerID, entry LedgerPost) (LedgerEntryID, error)

    // Reads
    Balance(ctx context.Context, sellerID core.SellerID) (Balance, error)
    Statement(ctx context.Context, sellerID core.SellerID, period Period) ([]LedgerEntry, error)
}

type LedgerPost struct {
    Direction Direction         // credit | debit
    Amount    core.Paise
    Ref       Ref               // (RefType, RefID)
    Actor     audit.Actor
    Reason    string
}

type Ref struct {
    Type  RefType   // shipment_charge | recharge | rto_charge | refund | ...
    ID    string
}
```

## Data model

```sql
CREATE TABLE wallet_account (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id                   UUID NOT NULL UNIQUE,
  currency                    TEXT NOT NULL DEFAULT 'INR',
  balance_minor               BIGINT NOT NULL DEFAULT 0,        -- cached; canonical = sum of ledger
  hold_total_minor            BIGINT NOT NULL DEFAULT 0,
  credit_limit_minor          BIGINT NOT NULL DEFAULT 0,
  grace_negative_amount_minor BIGINT NOT NULL DEFAULT 0,
  status                      TEXT NOT NULL DEFAULT 'active',   -- 'active' | 'frozen' | 'wound_down'
  created_at, updated_at
);
-- Rebuilt cache: SELECT seller_id, SUM(...) FROM wallet_ledger_entry GROUP BY seller_id

CREATE TABLE wallet_ledger_entry (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id     UUID NOT NULL,
  wallet_id     UUID NOT NULL REFERENCES wallet_account(id),
  direction     TEXT NOT NULL,                                  -- 'credit' | 'debit'
  amount_minor  BIGINT NOT NULL,
  ref_type      TEXT NOT NULL,
  ref_id        TEXT NOT NULL,                                   -- shipment_id, recharge_id, etc.
  reverses_id   UUID,                                            -- if this entry reverses another
  actor_jsonb   JSONB NOT NULL,
  reason        TEXT,
  posted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- The contract: idempotency
  UNIQUE (ref_type, ref_id, direction)
);

CREATE INDEX wallet_ledger_seller_posted_idx ON wallet_ledger_entry (seller_id, posted_at DESC);

CREATE TABLE wallet_hold (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  seller_id       UUID NOT NULL,
  wallet_id       UUID NOT NULL REFERENCES wallet_account(id),
  amount_minor    BIGINT NOT NULL,
  hold_token      TEXT NOT NULL UNIQUE,                          -- caller-supplied or generated
  status          TEXT NOT NULL,                                  -- 'active' | 'confirmed' | 'released' | 'expired'
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  resolved_at     TIMESTAMPTZ,
  resolved_to_ledger_entry_id UUID
);

CREATE INDEX wallet_hold_active_idx ON wallet_hold (wallet_id, status) WHERE status='active';

CREATE TABLE wallet_idempotency_key (
  seller_id     UUID NOT NULL,
  key           TEXT NOT NULL,
  response      JSONB NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (seller_id, key)
);

CREATE TABLE wallet_invariant_check_result (
  id              UUID PRIMARY KEY,
  seller_id       UUID NOT NULL,
  computed_minor  BIGINT NOT NULL,
  cached_minor    BIGINT NOT NULL,
  diff_minor      BIGINT NOT NULL,                                -- 0 if invariant holds
  checked_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Reserve (two-phase, phase 1)

```go
func (s *svc) Reserve(ctx, sellerID, amount, ttl) (HoldID, error) {
    return withTx(ctx, sellerID, func(tx) {
        // Lock the wallet row
        wa := tx.QueryRow("SELECT id, balance_minor, hold_total_minor, credit_limit_minor, grace_negative_amount_minor FROM wallet_account WHERE seller_id=$1 FOR UPDATE")

        available := wa.balance + wa.credit_limit - wa.hold_total
        if available < amount {
            // Check grace (only relevant for negative-balance pre-emption; here we're additive, so fail clean)
            return ErrInsufficientFunds
        }

        // Insert hold + bump hold_total
        holdID := uuid.New()
        tx.Exec("INSERT INTO wallet_hold (id, wallet_id, seller_id, amount_minor, hold_token, status, expires_at) VALUES (...)", ...)
        tx.Exec("UPDATE wallet_account SET hold_total_minor = hold_total_minor + $1 WHERE id = $2", amount, wa.id)

        return holdID, nil
    })
}
```

## Confirm (phase 2 success)

```go
func (s *svc) Confirm(ctx, holdID, ref) (LedgerEntryID, error) {
    return withTx(ctx, sellerID, func(tx) {
        hold := tx.QueryRow("SELECT * FROM wallet_hold WHERE id=$1 AND status='active' FOR UPDATE")

        // Idempotent insert
        var entryID UUID
        err := tx.QueryRow(`
            INSERT INTO wallet_ledger_entry (seller_id, wallet_id, direction, amount_minor, ref_type, ref_id, actor_jsonb)
            VALUES ($1, $2, 'debit', $3, $4, $5, $6)
            ON CONFLICT (ref_type, ref_id, direction) DO UPDATE SET ref_id=EXCLUDED.ref_id  -- no-op upsert
            RETURNING id
        `, ...).Scan(&entryID)

        // Bump cached balance and release hold
        tx.Exec("UPDATE wallet_account SET balance_minor = balance_minor - $1, hold_total_minor = hold_total_minor - $1 WHERE id=$2", hold.amount, hold.wallet_id)
        tx.Exec("UPDATE wallet_hold SET status='confirmed', resolved_at=now(), resolved_to_ledger_entry_id=$1 WHERE id=$2", entryID, holdID)

        return entryID, nil
    })
}
```

## Release (phase 2 fail)

```go
func (s *svc) Release(ctx, holdID) error {
    return withTx(ctx, sellerID, func(tx) {
        hold := tx.QueryRow("SELECT * FROM wallet_hold WHERE id=$1 AND status='active' FOR UPDATE")
        tx.Exec("UPDATE wallet_account SET hold_total_minor = hold_total_minor - $1 WHERE id=$2", hold.amount, hold.wallet_id)
        tx.Exec("UPDATE wallet_hold SET status='released', resolved_at=now() WHERE id=$1", holdID)
        return nil
    })
}
```

## Hold expiry (river cron, every minute)

```go
func ExpireHolds(ctx, db) error {
    rows := db.Query(`
        SELECT id, wallet_id, amount_minor FROM wallet_hold
        WHERE status='active' AND expires_at < now()
        FOR UPDATE SKIP LOCKED
        LIMIT 100
    `)
    for rows.Next() {
        // Transactionally release each
        // (Same as Release, but actor=system)
    }
}
```

## Direct Post (recharge, RTO charge, refund, manual adjustment)

```go
func (s *svc) Post(ctx, sellerID, entry LedgerPost) (LedgerEntryID, error) {
    return withTx(ctx, sellerID, func(tx) {
        // Reverse-leg charge: check grace cap
        if entry.Direction == DebitDir {
            wa := tx.QueryRow("SELECT * FROM wallet_account WHERE seller_id=$1 FOR UPDATE")
            new_balance := wa.balance - entry.Amount
            if new_balance < -wa.grace_negative_amount {
                if wa.credit_limit > 0 && new_balance >= -(wa.grace_negative_amount + wa.credit_limit) {
                    // Within credit line; OK
                } else {
                    return ErrGraceCapExceeded   // Suspends seller; raises alert
                }
            }
        }

        // Idempotent insert
        var entryID UUID
        // ... (same as Confirm pattern) ...

        // Update cached balance
        // ... (sign based on direction) ...

        return entryID, nil
    })
}
```

## Daily invariant check

A river cron at 02:00 IST:
```sql
WITH computed AS (
  SELECT seller_id,
    SUM(CASE WHEN direction='credit' THEN amount_minor ELSE -amount_minor END) AS bal
  FROM wallet_ledger_entry
  GROUP BY seller_id
)
INSERT INTO wallet_invariant_check_result (seller_id, computed_minor, cached_minor, diff_minor)
SELECT c.seller_id, c.bal, wa.balance_minor, c.bal - wa.balance_minor
FROM computed c
JOIN wallet_account wa ON wa.seller_id = c.seller_id;
```

If any `diff_minor != 0` → P0 alert. Audit event. Block all wallet ops on the affected seller until resolved.

## Reverse-leg flow

When carrier raises RTO charge (post-pickup):
1. RTO event from tracking → outbox event `rto_charge.due`.
2. River job: `wallet.charge_rto`:
   - Compute charge from rate card (RTO surcharge).
   - Call `Post(direction=debit, amount, ref={type: rto_charge, id: shipment_id})`.
   - Idempotent on (`rto_charge`, shipment_id).
3. If grace cap hit: emit `seller.suspension_required` event; ops alerted; seller suspended.

## Performance

- `Reserve`/`Confirm`/`Release`: P95 < 30ms each.
- `Post`: P95 < 30ms.
- Benchmark gate: at v1 traffic (50/sec sustained), P95 stays < 30ms. If broken, partition wallet account.

## Failure modes

| Failure | Behavior |
|---|---|
| Reserve fails (insufficient balance) | Caller (booking) handles; user-visible error |
| Confirm fails (hold expired) | Recompute current balance; if can absorb, post directly; else release |
| Post hits grace cap | Suspension; ops queue |
| DB unreachable | Booking fails fast; idempotent retry on resume |
| Two confirms for same hold | Second is no-op (UNIQUE constraint) |
| Two posts with same ref | Second is no-op |
| Invariant check fails | P0 alert; freeze seller; ops investigation |

## Test coverage

- **Unit**: arithmetic, paise overflow guards, sign validations.
- **SLT**: end-to-end Reserve/Confirm/Release with concurrent ops; race tests; expiry sweeper; idempotency replay.
- **Bench**: each operation type at concurrent loads.

## Observability

- Counter: ops by type and outcome.
- Histogram: latency per op type.
- Gauge: total wallets with negative balance.
- Gauge: invariant check fails (cumulative).
- Log: every grace-cap breach.

## Open questions

- **Q-WL-1.** Auto-recharge implementation: feature gate at v0; build at v1 when sellers ask.
- **Q-WL-2.** Currency multi-support: deferred; v3+.
- **Q-WL-3.** Wallet partitioning if FOR UPDATE contention becomes real: monitor; act at v2 if needed.
