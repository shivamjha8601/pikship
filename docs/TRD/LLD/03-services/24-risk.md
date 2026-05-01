# Risk Service

## Purpose

The risk service is a **lightweight scoring + alerting layer** that watches sellers, orders, and shipments for anomalous patterns and either auto-blocks (with a high bar) or surfaces alerts for ops. It exists primarily to:

- Stop **COD abuse** (fake orders that are placed and then refused at delivery, costing forward freight + RTO).
- Stop **wallet fraud** (sellers manipulating credits, recon disputes patterns).
- Surface **early signs of seller distress** (sudden NDR rate spike).

This service is intentionally **NOT a full fraud platform**. It is heuristic, rule-based, and operates on data the platform already has. ML-based scoring is out of scope for v0.

Out of scope:

- KYC / identity verification — seller (LLD §03-services/09).
- Card / UPI fraud — payment processor's job.
- Network-level abuse — buyer-experience rate limits (LLD §03-services/21).

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, money. |
| `internal/db` | reads via `pikshipp_reports` role for analytics queries. |
| `internal/seller` | suspend on auto-block. |
| `internal/policy` | thresholds per signal. |
| `internal/notifications` | ops alerts. |
| `internal/audit`, `internal/outbox` | standard. |

## Package Layout

```
internal/risk/
├── service.go
├── service_impl.go
├── repo.go
├── signals.go             // per-signal evaluators
├── jobs.go                // RiskScanJob, AutoBlockJob
├── types.go
├── errors.go
└── service_test.go
```

## Signal Catalog

```go
package risk

type SignalKind string

const (
    SignalNDRRateSpike            SignalKind = "ndr_rate_spike"
    SignalCODRefuseRateHigh       SignalKind = "cod_refuse_rate_high"
    SignalNewSellerHighVolume     SignalKind = "new_seller_high_volume"
    SignalReconDisputeBurst       SignalKind = "recon_dispute_burst"
    SignalWalletDrainPattern      SignalKind = "wallet_drain_pattern"
    SignalPickupAddressChurn      SignalKind = "pickup_address_churn"
    SignalDuplicateBuyerPincodes  SignalKind = "duplicate_buyer_pincodes"
)

type Severity string

const (
    SevInfo     Severity = "info"
    SevWarn     Severity = "warn"
    SevCritical Severity = "critical"
)
```

Each signal is a small Go function that runs a SQL query (against `pikshipp_reports` role) and returns a list of (seller, score, evidence) triples. The scan job orchestrates them.

## Public API

```go
package risk

type Service interface {
    // RunDailyScan executes all signals and persists detected events.
    // Triggered by river periodic job.
    RunDailyScan(ctx context.Context) (*ScanReport, error)

    // EvaluateSeller runs all signals for a single seller (used after
    // significant events like wallet adjustments).
    EvaluateSeller(ctx context.Context, sellerID core.SellerID) ([]*Detection, error)

    // ApplyAction handles operator decisions on detections (block, dismiss, snooze).
    ApplyAction(ctx context.Context, req ApplyActionRequest) error

    // List returns active detections for the ops queue.
    ListActive(ctx context.Context, q ListQuery) ([]*Detection, error)
}
```

### Types

```go
type Detection struct {
    ID         core.RiskDetectionID
    SellerID   core.SellerID
    Signal     SignalKind
    Severity   Severity
    Score      float64
    Evidence   map[string]any        // signal-specific
    State      DetectionState        // open | snoozed | dismissed | actioned
    DetectedAt time.Time
    SnoozeUntil *time.Time
    OperatorID  *core.UserID
    OperatorReason string
}

type DetectionState string

const (
    DetectionOpen      DetectionState = "open"
    DetectionSnoozed   DetectionState = "snoozed"
    DetectionDismissed DetectionState = "dismissed"
    DetectionActioned  DetectionState = "actioned"
)

type ApplyActionRequest struct {
    OperatorID core.UserID
    DetectionID core.RiskDetectionID
    Action     string             // "block" | "dismiss" | "snooze:7d"
    Reason     string
}

type ScanReport struct {
    NewDetections     int
    ResolvedAutomatic int
    StillOpen         int
    GeneratedAt       time.Time
}
```

## DB Schema

```sql
CREATE TABLE risk_detection (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id    uuid        NOT NULL REFERENCES seller(id),
    signal       text        NOT NULL,
    severity     text        NOT NULL CHECK (severity IN ('info','warn','critical')),
    score        double precision NOT NULL,
    evidence     jsonb       NOT NULL,
    state        text        NOT NULL CHECK (state IN ('open','snoozed','dismissed','actioned')),
    detected_at  timestamptz NOT NULL DEFAULT now(),
    snooze_until timestamptz,
    operator_id  uuid REFERENCES app_user(id),
    operator_reason text,
    actioned_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),

    -- One open detection per (seller, signal) at a time
    EXCLUDE USING btree (seller_id WITH =, signal WITH =) WHERE (state = 'open')
);

CREATE INDEX risk_detection_seller_state_idx ON risk_detection(seller_id, state);
CREATE INDEX risk_detection_severity_idx ON risk_detection(severity, state) WHERE state = 'open';

-- No RLS: cross-seller view for ops.
GRANT SELECT, INSERT, UPDATE ON risk_detection TO pikshipp_app;
GRANT SELECT ON risk_detection TO pikshipp_reports;
```

## Signal Implementations

Each signal is a `SignalEvaluator` that returns detections. The framework is straightforward enough that adding a new signal is one PR.

```go
type SignalEvaluator interface {
    Kind() SignalKind
    Evaluate(ctx context.Context, q *SignalQueries) ([]*ProposedDetection, error)
}

type ProposedDetection struct {
    SellerID core.SellerID
    Score    float64
    Severity Severity
    Evidence map[string]any
}

var registry = []SignalEvaluator{
    &ndrRateSpike{},
    &codRefuseRateHigh{},
    &newSellerHighVolume{},
    &reconDisputeBurst{},
    &walletDrainPattern{},
    &pickupAddressChurn{},
    &duplicateBuyerPincodes{},
}
```

### Example: NDR Rate Spike

```go
type ndrRateSpike struct{}

func (ndrRateSpike) Kind() SignalKind { return SignalNDRRateSpike }

const ndrRateSpikeQuery = `
WITH last7 AS (
    SELECT seller_id,
           count(*) FILTER (WHERE state = 'open' OR state = 'requested_reattempt' OR state = 'auto_rto_pending') AS open_ndr,
           count(*) AS total_ndr
    FROM ndr_case
    WHERE created_at >= now() - interval '7 days'
    GROUP BY seller_id
),
shipments_last7 AS (
    SELECT seller_id, count(*) AS ships
    FROM shipment
    WHERE booked_at >= now() - interval '7 days'
    GROUP BY seller_id
)
SELECT s.seller_id,
       (l.total_ndr::float / NULLIF(s.ships, 0)) AS ndr_rate,
       l.total_ndr,
       s.ships
FROM shipments_last7 s
JOIN last7 l USING (seller_id)
WHERE s.ships >= 50              -- need enough volume to score
  AND (l.total_ndr::float / s.ships) > 0.25;
`

func (n ndrRateSpike) Evaluate(ctx context.Context, q *SignalQueries) ([]*ProposedDetection, error) {
    rows, err := q.ReportsConn.Query(ctx, ndrRateSpikeQuery)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []*ProposedDetection
    for rows.Next() {
        var sellerID uuid.UUID
        var rate float64
        var totalNDR, ships int
        if err := rows.Scan(&sellerID, &rate, &totalNDR, &ships); err != nil {
            return nil, err
        }
        sev := SevWarn
        if rate > 0.40 {
            sev = SevCritical
        }
        out = append(out, &ProposedDetection{
            SellerID: core.SellerIDFromUUID(sellerID),
            Score:    rate,
            Severity: sev,
            Evidence: map[string]any{
                "ndr_rate":   rate,
                "ndr_count":  totalNDR,
                "ship_count": ships,
                "window":     "7d",
            },
        })
    }
    return out, rows.Err()
}
```

### Example: COD Refuse Rate

A high COD refuse rate means many `ndr.detected → rto_initiated → rto_delivered` for COD shipments. Distinct from generic NDR rate.

```go
const codRefuseRateQuery = `
WITH last30 AS (
    SELECT cs.seller_id,
           count(*) AS cod_total,
           count(*) FILTER (WHERE r.state IS NOT NULL) AS rto_count
    FROM cod_shipment cs
    LEFT JOIN rto_tracking r ON r.shipment_id = cs.shipment_id
    WHERE cs.created_at >= now() - interval '30 days'
    GROUP BY cs.seller_id
)
SELECT seller_id,
       (rto_count::float / NULLIF(cod_total, 0)) AS refuse_rate,
       cod_total, rto_count
FROM last30
WHERE cod_total >= 30
  AND (rto_count::float / cod_total) > 0.20;
`
```

### Example: Wallet Drain Pattern

Detects sellers that credit wallet (top-up), book a burst of shipments, then disappear:

```sql
-- Pseudo: top-up event + 50+ bookings within 6h + no shipments since
WITH topups AS (
    SELECT seller_id, MAX(created_at) AS last_topup_at
    FROM wallet_ledger_entry
    WHERE direction = 'credit' AND ref_type = 'topup'
    GROUP BY seller_id
),
recent_bookings AS (
    SELECT s.seller_id, count(*) AS cnt, MIN(s.booked_at) AS first_booking, MAX(s.booked_at) AS last_booking
    FROM shipment s
    JOIN topups t ON s.seller_id = t.seller_id
    WHERE s.booked_at >= t.last_topup_at AND s.booked_at <= t.last_topup_at + interval '6 hours'
    GROUP BY s.seller_id
)
SELECT * FROM recent_bookings
WHERE cnt >= 50
  AND last_booking < now() - interval '24 hours';
```

## Scan Orchestration

```go
func (s *service) RunDailyScan(ctx context.Context) (*ScanReport, error) {
    queries := &SignalQueries{ReportsConn: s.reportsPool}
    var allProposed []*ProposedDetection
    for _, ev := range registry {
        ds, err := ev.Evaluate(ctx, queries)
        if err != nil {
            slog.Warn("risk: signal failed", "signal", ev.Kind(), "err", err)
            continue
        }
        for _, d := range ds {
            d.signalKind = ev.Kind()
            allProposed = append(allProposed, d)
        }
    }

    var newCount, resolvedCount int
    for _, p := range allProposed {
        inserted, err := s.upsertDetection(ctx, p)
        if err != nil {
            slog.Warn("risk: upsert detection", "err", err)
            continue
        }
        if inserted {
            newCount++
        }
    }

    // Auto-resolve detections that no longer fire
    resolvedCount, _ = s.autoResolveStale(ctx, allProposed)

    open, _ := s.q.RiskDetectionCountOpen(ctx)
    return &ScanReport{NewDetections: newCount, ResolvedAutomatic: resolvedCount, StillOpen: open, GeneratedAt: s.clock.Now()}, nil
}

func (s *service) upsertDetection(ctx context.Context, p *ProposedDetection) (bool, error) {
    inserted := false
    err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        // Use ON CONFLICT via the partial unique index (open detections only)
        _, err := qtx.RiskDetectionInsertIfNotOpen(ctx, sqlcgen.RiskDetectionInsertIfNotOpenParams{
            ID:       core.NewRiskDetectionID().UUID(),
            SellerID: p.SellerID.UUID(),
            Signal:   string(p.signalKind),
            Severity: string(p.Severity),
            Score:    p.Score,
            Evidence: jsonbFrom(p.Evidence),
            State:    string(DetectionOpen),
        })
        if err != nil {
            // Exclude constraint violation = already open; not a new detection.
            if isExcludeViolation(err) {
                return nil
            }
            return err
        }
        inserted = true
        // Audit + notify ops on critical
        if p.Severity == SevCritical {
            if err := s.notif.SendOpsAlert(ctx, notifications.OpsAlertRequest{
                Subject: fmt.Sprintf("Risk detection (critical): %s", p.signalKind),
                Body:    fmt.Sprintf("seller=%s score=%.2f evidence=%v", p.SellerID, p.Score, p.Evidence),
            }); err != nil {
                slog.Warn("risk: notify ops failed", "err", err)
            }
        }
        return s.audit.EmitAsync(ctx, tx, audit.Event{
            SellerID: p.SellerID,
            Action:   "risk.detection." + string(p.signalKind),
            Payload:  map[string]any{"severity": p.Severity, "score": p.Score, "evidence": p.Evidence},
        })
    })
    return inserted, err
}
```

### Auto-Resolution

Each scan also resolves detections that **no longer fire** for the (seller, signal) pair. We move them to `actioned` with `operator_reason="auto_resolved_signal_cleared"`.

```go
func (s *service) autoResolveStale(ctx context.Context, proposed []*ProposedDetection) (int, error) {
    activeKeys := make(map[detectionKey]struct{})
    for _, p := range proposed {
        activeKeys[detectionKey{p.SellerID, p.signalKind}] = struct{}{}
    }
    rows, err := s.q.RiskDetectionListOpen(ctx)
    if err != nil {
        return 0, err
    }
    var resolved int
    for _, r := range rows {
        key := detectionKey{core.SellerIDFromUUID(r.SellerID), SignalKind(r.Signal)}
        if _, stillFires := activeKeys[key]; stillFires {
            continue
        }
        if err := s.q.RiskDetectionResolve(ctx, sqlcgen.RiskDetectionResolveParams{
            ID:             r.ID,
            OperatorReason: pgxNullString("auto_resolved_signal_cleared"),
        }); err != nil {
            slog.Warn("risk: auto-resolve", "id", r.ID, "err", err)
            continue
        }
        resolved++
    }
    return resolved, nil
}
```

## ApplyAction

```go
func (s *service) ApplyAction(ctx context.Context, req ApplyActionRequest) error {
    if len(strings.TrimSpace(req.Reason)) < 5 {
        return fmt.Errorf("risk: reason required")
    }
    return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
        qtx := sqlcgen.New(tx)
        det, err := qtx.RiskDetectionGet(ctx, req.DetectionID.UUID())
        if err != nil {
            return ErrNotFound
        }
        switch {
        case req.Action == "block":
            // Suspend the seller; mark detection actioned.
            if err := s.seller.Suspend(ctx, core.SellerIDFromUUID(det.SellerID), seller.SuspendRequest{
                Reason:     fmt.Sprintf("risk:%s:%s", det.Signal, req.Reason),
                Category:   seller.SuspendCategoryRisk,
                OperatorID: &req.OperatorID,
            }); err != nil {
                return err
            }
            if err := qtx.RiskDetectionAction(ctx, sqlcgen.RiskDetectionActionParams{
                ID: det.ID, OperatorID: pgxNullUUID(&req.OperatorID), OperatorReason: pgxNullString(req.Reason),
            }); err != nil {
                return err
            }
        case req.Action == "dismiss":
            if err := qtx.RiskDetectionDismiss(ctx, sqlcgen.RiskDetectionDismissParams{
                ID: det.ID, OperatorID: pgxNullUUID(&req.OperatorID), OperatorReason: pgxNullString(req.Reason),
            }); err != nil {
                return err
            }
        case strings.HasPrefix(req.Action, "snooze:"):
            dur, err := parseSnooze(strings.TrimPrefix(req.Action, "snooze:"))
            if err != nil {
                return err
            }
            if err := qtx.RiskDetectionSnooze(ctx, sqlcgen.RiskDetectionSnoozeParams{
                ID: det.ID, SnoozeUntil: pgxNullTimestamp(s.clock.Now().Add(dur)),
                OperatorID: pgxNullUUID(&req.OperatorID), OperatorReason: pgxNullString(req.Reason),
            }); err != nil {
                return err
            }
        default:
            return fmt.Errorf("risk: unknown action %q", req.Action)
        }
        return s.audit.Emit(ctx, tx, audit.Event{
            SellerID: core.SellerIDFromUUID(det.SellerID),
            Actor:    audit.ActorUser(req.OperatorID),
            Action:   "risk.detection.action",
            Payload:  map[string]any{"detection_id": det.ID, "action": req.Action, "reason": req.Reason},
        })
    })
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `RunDailyScan` (10k sellers) | 90 s | 240 s | reports-pool queries |
| `EvaluateSeller` | 800 ms | 3 s | run all signals for one seller |
| `upsertDetection` | 5 ms | 18 ms | tx + audit + maybe notify |
| `ApplyAction` (block) | 12 ms | 35 ms | suspend + UPDATE + audit |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| One signal query slow / errors | per-signal try/catch | log + skip; other signals continue. |
| Reports pool exhausted | timeouts | scan partially fails; metrics show which signals didn't run; ops investigates. |
| Critical detection but notification down | notif Send fails | detection persisted; ops dashboard surfaces; not critical-loss. |
| Operator blocks seller incorrectly | recoverable: Reinstate via admin API | Audit trail records both the block and reinstate. |
| Same evidence triggers another detection right after auto-resolve | partial unique index allows new row when `state != open` | re-opens cleanly. |

## Testing

```go
func TestNDRRateSpike_Detects_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    seller := slt.NewSeller(t, pg)
    slt.SeedShipments(t, pg, seller.ID, 100)
    slt.SeedNDRCases(t, pg, seller.ID, 30) // 30%
    detected, _ := slt.Risk(pg).EvaluateSeller(ctx, seller.ID)
    found := false
    for _, d := range detected {
        if d.Signal == SignalNDRRateSpike {
            found = true
            require.Equal(t, SevWarn, d.Severity)
        }
    }
    require.True(t, found)
}

func TestApplyAction_Block_SuspendsSeller_SLT(t *testing.T) { /* ... */ }
func TestApplyAction_Snooze_ReducesQueueSize_SLT(t *testing.T) { /* ... */ }
func TestAutoResolve_StaleDetectionsClosed_SLT(t *testing.T) { /* ... */ }
func TestExclusionConstraint_NoTwoOpenForSameSignal_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **ML / behavioral models.** The signals here are crude. Even simple logistic regression on these features would help. **Decision: deferred to v1+.**
2. **Per-seller-allowlist.** Trusted enterprise sellers shouldn't trigger the new-seller-high-volume signal. **Decision:** add `risk.bypass_signals` policy at v0.5.
3. **Cross-tenant patterns.** Sellers using the same buyer phone numbers across accounts. **Decision:** out of scope; potential future signal.
4. **Risk scores for individual orders (not sellers).** Useful for COD-block-on-checkout. **Decision:** v0 doesn't gate booking; out of scope.
5. **PII in evidence.** Evidence may contain phone numbers. **Decision:** redact in API responses; store full in DB (RLS-protected via grants).

## References

- HLD §03-services: data sources we read from.
- LLD §03-services/09-seller: Suspend hook on auto-block.
- LLD §03-services/02-audit: every detection + action audited.
- LLD §03-services/19-reports: reports-pool reuse.
- LLD §03-services/20-notifications: ops alerts.
