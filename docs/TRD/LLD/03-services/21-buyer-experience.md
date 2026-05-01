# Buyer Experience Service

## Purpose

The buyer-experience service is the **public-facing surface** for the parcel's recipient — the buyer who receives a tracking link via SMS or email and lands on `track.pikshipp.com/<token>`. It serves the buyer-facing tracking page and accepts buyer-side actions (NDR decisions, address change requests).

The buyer is **never authenticated**; possession of the unguessable token is the authorization. This service is consequently designed with strong rate limits, narrow data exposure, and strict input validation.

Responsibilities:

- Serve the tracking page (`GET /track/:token`) returning a sanitized JSON view.
- Accept NDR action submissions from the tracking page (`POST /track/:token/ndr-action`).
- Accept address-change requests with validation.
- Rate-limit all endpoints by token + IP.
- Log every interaction for analytics (conversion funnel: token visit → action taken).

Out of scope:

- Token issuance — tracking (LLD §03-services/14).
- NDR business logic — NDR (LLD §03-services/15).
- Tracking-page front-end (HTML/CSS/JS) — separate frontend repo.

## Dependencies

| Dependency | Why |
|---|---|
| `internal/core` | IDs, errors. |
| `internal/db` | small interaction-log table. |
| `internal/tracking` | `GetByPublicToken`. |
| `internal/ndr` | `SubmitBuyerDecision`. |
| `internal/notifications` | OTP send for address-change confirmation (future). |

## Package Layout

```
internal/buyerexp/
├── service.go             // Service interface
├── service_impl.go
├── handlers.go            // chi handlers
├── ratelimit.go           // per-token + per-IP token bucket
├── repo.go                // interaction-log writes
├── types.go
├── errors.go
└── service_test.go
```

## Public API

```go
package buyerexp

type Service interface {
    // GetTrackingView is the read endpoint for the tracking page.
    // Resolves the token, fetches sanitized data, logs the visit.
    GetTrackingView(ctx context.Context, token string, requestor RequestorMeta) (*PublicView, error)

    // SubmitNDRAction is invoked when the buyer chooses an action on
    // the tracking page during an NDR.
    SubmitNDRAction(ctx context.Context, token string, req NDRActionRequest, requestor RequestorMeta) (*NDRActionResult, error)
}
```

### Types

```go
type RequestorMeta struct {
    IP          string
    UserAgent   string
    AcceptLang  string
}

type PublicView struct {
    OrderRef        string             // sanitized: "ORDER-12345"
    BuyerName       string             // first name only
    Status          string             // canonical status, e.g. "in_transit"
    StatusLabel     string             // human-friendly: "On its way"
    Carrier         string             // display name only ("Delhivery"); no AWB
    Events          []PublicEvent      // chronological events
    EstimatedDelivery time.Time
    LastUpdated     time.Time

    // NDR action prompt (visible only when shipment.state = in_transit
    // AND most recent canonical_status = delivery_attempted)
    ActionPrompt    *ActionPrompt
}

type PublicEvent struct {
    Status     string
    Label      string
    Location   string             // city only; no full address
    OccurredAt time.Time
}

type ActionPrompt struct {
    Reason         string
    AvailableActions []string         // "reattempt" | "change_address" | "rto"
    Deadline       time.Time           // by when buyer must respond
}

type NDRActionRequest struct {
    Action      string                 // "reattempt" | "change_address" | "rto"
    NewAddress  *Address               // required if Action == change_address
    Notes       string
}

type NDRActionResult struct {
    Accepted bool
    Message  string
}
```

### Sentinel Errors

```go
var (
    ErrTokenInvalid    = errors.New("buyerexp: invalid or expired token")
    ErrRateLimited     = errors.New("buyerexp: rate limited")
    ErrNoActiveNDR     = errors.New("buyerexp: no active NDR for this shipment")
    ErrInvalidAction   = errors.New("buyerexp: action not allowed for this shipment")
)
```

## DB Schema

```sql
-- Interaction log: one row per page visit + each action submission.
CREATE TABLE buyer_interaction_log (
    id            bigserial   PRIMARY KEY,
    public_token  text        NOT NULL,
    shipment_id   uuid        REFERENCES shipment(id),
    seller_id     uuid        REFERENCES seller(id),
    kind          text        NOT NULL CHECK (kind IN ('view','action')),
    action_type   text,                    -- "reattempt" | "change_address" | "rto" (when kind=action)
    success       boolean     NOT NULL,
    ip            inet        NOT NULL,
    user_agent    text,
    request_body  jsonb,                  -- only for action submissions
    error         text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX buyer_interaction_log_token_idx ON buyer_interaction_log(public_token, created_at DESC);
CREATE INDEX buyer_interaction_log_shipment_idx ON buyer_interaction_log(shipment_id, created_at DESC);

-- No RLS: this is platform-internal.
GRANT SELECT, INSERT ON buyer_interaction_log TO pikshipp_app;
GRANT USAGE, SELECT ON SEQUENCE buyer_interaction_log_id_seq TO pikshipp_app;
GRANT SELECT ON buyer_interaction_log TO pikshipp_reports;
```

## Rate Limiting

Two token-bucket limits stack:

- Per-IP: 60 requests / minute (covers crawlers, DoS).
- Per-token: 10 requests / minute (a buyer doesn't need more).

```go
package buyerexp

type RateLimiter struct {
    perIP    *tokenBucketMap // 60/minute
    perToken *tokenBucketMap // 10/minute
}

type tokenBucketMap struct {
    mu      sync.Mutex
    buckets map[string]*tokenBucket
    rate    float64           // tokens per second
    burst   float64           // max burst
}

type tokenBucket struct {
    tokens    float64
    lastReset time.Time
}

func (m *tokenBucketMap) Allow(key string) bool {
    m.mu.Lock()
    defer m.mu.Unlock()
    b, ok := m.buckets[key]
    now := time.Now()
    if !ok {
        b = &tokenBucket{tokens: m.burst, lastReset: now}
        m.buckets[key] = b
    }
    elapsed := now.Sub(b.lastReset).Seconds()
    b.tokens = math.Min(m.burst, b.tokens + elapsed*m.rate)
    b.lastReset = now
    if b.tokens < 1 {
        return false
    }
    b.tokens--
    return true
}
```

A periodic GC sweeps stale buckets every 5 minutes.

## Handlers

```go
package buyerexp

func MountRoutes(r chi.Router, svc Service, rl *RateLimiter) {
    r.With(rateLimitMiddleware(rl)).Group(func(r chi.Router) {
        r.Get("/track/{token}", makeGetTrackingHandler(svc))
        r.Post("/track/{token}/ndr-action", makeSubmitNDRHandler(svc))
    })
}

func rateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := clientIP(r)
            token := chi.URLParam(r, "token")
            if !rl.perIP.Allow(ip) || (token != "" && !rl.perToken.Allow(token)) {
                http.Error(w, "rate limited", http.StatusTooManyRequests)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

func makeGetTrackingHandler(svc Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        token := chi.URLParam(r, "token")
        view, err := svc.GetTrackingView(r.Context(), token, RequestorMeta{
            IP:         clientIP(r),
            UserAgent:  r.UserAgent(),
            AcceptLang: r.Header.Get("Accept-Language"),
        })
        if errors.Is(err, ErrTokenInvalid) {
            http.Error(w, "not found", http.StatusNotFound)
            return
        }
        if err != nil {
            http.Error(w, "internal", http.StatusInternalServerError)
            return
        }
        writeJSON(w, http.StatusOK, view)
    }
}
```

## GetTrackingView

```go
func (s *service) GetTrackingView(ctx context.Context, token string, meta RequestorMeta) (*PublicView, error) {
    raw, err := s.tracking.GetByPublicToken(ctx, token)
    if err != nil {
        s.logInteraction(ctx, token, "", "", "view", false, meta, "", "invalid_token")
        return nil, ErrTokenInvalid
    }
    view := s.buildPublicView(raw)
    s.logInteraction(ctx, token, raw.ShipmentID, raw.SellerID, "view", true, meta, "", "")
    return view, nil
}

func (s *service) buildPublicView(raw *tracking.PublicTrackingView) *PublicView {
    view := &PublicView{
        OrderRef:    sanitizeOrderRef(raw.OrderRef),
        BuyerName:   firstName(raw.BuyerName),
        Status:      string(raw.Status),
        StatusLabel: humanLabel(raw.Status),
        Carrier:     raw.CarrierDisplayName,
        Events:      make([]PublicEvent, 0, len(raw.Events)),
        EstimatedDelivery: raw.EstimatedDelivery,
        LastUpdated: raw.LastUpdated,
    }
    for _, e := range raw.Events {
        view.Events = append(view.Events, PublicEvent{
            Status:     string(e.Status),
            Label:      humanLabel(e.Status),
            Location:   cityOnly(e.Location),
            OccurredAt: e.OccurredAt,
        })
    }
    if raw.HasOpenNDR {
        view.ActionPrompt = &ActionPrompt{
            Reason:           humanReason(raw.NDRReason),
            AvailableActions: raw.AvailableNDRActions,
            Deadline:         raw.NDRResponseDeadline,
        }
    }
    return view
}
```

## SubmitNDRAction

```go
func (s *service) SubmitNDRAction(ctx context.Context, token string, req NDRActionRequest, meta RequestorMeta) (*NDRActionResult, error) {
    if req.Action == "" {
        return nil, ErrInvalidAction
    }
    raw, err := s.tracking.GetByPublicToken(ctx, token)
    if err != nil {
        s.logInteraction(ctx, token, "", "", "action", false, meta, req.Action, "invalid_token")
        return nil, ErrTokenInvalid
    }
    if !raw.HasOpenNDR {
        s.logInteraction(ctx, token, raw.ShipmentID, raw.SellerID, "action", false, meta, req.Action, "no_active_ndr")
        return nil, ErrNoActiveNDR
    }

    // Forward to NDR service
    c, err := s.ndr.SubmitBuyerDecision(ctx, ndr.BuyerDecisionRequest{
        PublicToken: token,
        Action:      ndr.Action(req.Action),
        NewAddress:  req.NewAddress,
        Notes:       req.Notes,
    })
    if err != nil {
        s.logInteraction(ctx, token, raw.ShipmentID, raw.SellerID, "action", false, meta, req.Action, err.Error())
        return nil, err
    }

    s.logInteraction(ctx, token, raw.ShipmentID, raw.SellerID, "action", true, meta, req.Action, "")
    return &NDRActionResult{Accepted: true, Message: humanResultMessage(c.State)}, nil
}
```

## Sanitization Helpers

```go
func sanitizeOrderRef(s string) string {
    if s == "" {
        return ""
    }
    if len(s) <= 4 {
        return s
    }
    return s[:4] + strings.Repeat("*", len(s)-4)
}

func firstName(full string) string {
    parts := strings.Fields(full)
    if len(parts) == 0 {
        return ""
    }
    return parts[0]
}

func cityOnly(loc string) string {
    // "Mumbai, MH, 400001" → "Mumbai"
    if i := strings.Index(loc, ","); i > 0 {
        return loc[:i]
    }
    return loc
}

func humanLabel(s tracking.CanonicalStatus) string {
    switch s {
    case tracking.StatusBookingConfirmed: return "Order placed"
    case tracking.StatusPickedUp:         return "Picked up"
    case tracking.StatusInTransit:        return "On its way"
    case tracking.StatusOutForDelivery:   return "Out for delivery"
    case tracking.StatusDelivered:        return "Delivered"
    case tracking.StatusDeliveryAttempted: return "Delivery attempted — couldn't reach you"
    case tracking.StatusRTOInitiated:     return "Returning to seller"
    default:                              return string(s)
    }
}
```

## Interaction Logging

```go
func (s *service) logInteraction(ctx context.Context, token string, shipmentID core.ShipmentID, sellerID core.SellerID, kind string, success bool, meta RequestorMeta, actionType, errMsg string) {
    // Best-effort; log failure must not break the request.
    _ = s.q.BuyerInteractionLogInsert(ctx, sqlcgen.BuyerInteractionLogInsertParams{
        PublicToken: token,
        ShipmentID:  pgxNullUUIDFromShipment(shipmentID),
        SellerID:    pgxNullUUIDFromSeller(sellerID),
        Kind:        kind,
        ActionType:  pgxNullString(actionType),
        Success:     success,
        IP:          netip.MustParseAddr(meta.IP),
        UserAgent:   pgxNullString(meta.UserAgent),
        Error:       pgxNullString(errMsg),
    })
}
```

## Performance Budgets

| Operation | p50 | p99 | Notes |
|---|---|---|---|
| `GET /track/:token` | 8 ms | 30 ms | token lookup + tracking events read + render |
| `POST /track/:token/ndr-action` | 15 ms | 50 ms | + NDR.SubmitBuyerDecision |
| Rate-limit check | 200 ns | 500 ns | in-memory token bucket |

## Failure Modes

| Failure | Detection | Handling |
|---|---|---|
| Invalid token | `tracking.GetByPublicToken` returns ErrNotFound | 404 — uniform response, no enumeration of valid tokens. |
| Expired token | tracking returns ErrNotFound (token+expires_at filter) | Same as invalid: 404. |
| Action submitted but NDR closed in interim | `ndr.SubmitBuyerDecision` returns `ErrNoOpenCase` | 409 with message "this delivery is no longer awaiting your decision". |
| Address-change without address | validation rejects | 422. |
| Rate limit exceeded | bucket empty | 429 + Retry-After. |
| Interaction log insert fails | swallowed (best-effort) | metric increments; doesn't affect UX. |
| User on slow connection times out | server unaffected | normal timeout handling. |

## Testing

```go
func TestSanitizeOrderRef(t *testing.T) {
    require.Equal(t, "ABCD****", sanitizeOrderRef("ABCD1234"))
    require.Equal(t, "AB", sanitizeOrderRef("AB"))
}
func TestRateLimiter_BucketRefill(t *testing.T) { /* ... */ }
func TestGetTrackingView_HappyPath_SLT(t *testing.T) {
    pg := slt.StartPG(t)
    sh := slt.NewBookedShipment(t, pg)
    token, _ := slt.Tracking(pg).IssuePublicToken(ctx, sh.ID, sh.SellerID)
    view, err := slt.BuyerExp(pg).GetTrackingView(ctx, token, RequestorMeta{IP: "1.2.3.4"})
    require.NoError(t, err)
    require.NotEmpty(t, view.StatusLabel)
}
func TestGetTrackingView_InvalidToken_404_SLT(t *testing.T) { /* ... */ }
func TestSubmitNDRAction_Reattempt_SLT(t *testing.T) { /* ... */ }
func TestSubmitNDRAction_NoActiveNDR_409_SLT(t *testing.T) { /* ... */ }
func TestRateLimit_PerToken_429_SLT(t *testing.T) { /* ... */ }
func TestInteractionLog_RowsPersisted_SLT(t *testing.T) { /* ... */ }
```

## Open Questions

1. **OTP for address change.** Buyers can submit a new address without authentication today. **Decision:** v0 trusts token possession; v0.5 adds OTP to buyer phone for change_address actions.
2. **Internationalization.** Buyer phones may be Indian-language speakers. **Decision:** start with English; the `Accept-Language` header is logged so we can see demand.
3. **Bot traffic.** Crawlers will hit `track.pikshipp.com/<random>`. **Decision:** rate limits + 404 with no info; noindex meta tag in front-end.
4. **Tracking page caching.** Front-end cache 60s. Server-side: no.
5. **Buyer feedback (rate-this-delivery).** Out of scope for v0.

## References

- LLD §03-services/14-tracking: `GetByPublicToken`.
- LLD §03-services/15-ndr: `SubmitBuyerDecision`.
- HLD §04-cross-cutting/05-resilience: rate-limit strategy.
- LLD §02-infrastructure/04-http-server: middleware integration.
