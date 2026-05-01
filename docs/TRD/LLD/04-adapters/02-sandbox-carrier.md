# Sandbox Carrier Adapter

## Purpose

The sandbox carrier is an **in-memory, deterministic** carrier adapter used by tests, dev environments, and friendly-seller demos. It implements the full `framework.Adapter` contract but never makes a network call.

Goals:

- Deterministic outputs for testability.
- Programmable behaviors (latency, failure injection, capability quirks).
- Safe to run in dev environments and during integration tests.

It is **never** registered in production builds; the `cfg.SandboxCarrierEnabled` flag must be true and `wire.go` panics if seen alongside `cfg.Env == "prod"`.

## Package Layout

```
internal/carriers/sandbox/
├── adapter.go             // Adapter implementation
├── behaviors.go           // Behavior injection
├── store.go               // In-memory shipment store
└── adapter_test.go
```

## Adapter Implementation

```go
package sandbox

type Adapter struct {
    code string
    behaviors map[string]Behavior
    store     *store
    clock     core.Clock
    mu        sync.Mutex
}

type Behavior struct {
    LatencyMs   int
    Class       framework.ErrorClass
    HTTPStatus  int
    Retryable   bool
    Override    any                       // optional: typed override response
}

func New(code string) *Adapter {
    return &Adapter{
        code:      code,
        behaviors: make(map[string]Behavior),
        store:     newStore(),
        clock:     core.SystemClock{},
    }
}

func (a *Adapter) Code() string { return a.code }
func (a *Adapter) DisplayName() string { return "Sandbox " + a.code }

func (a *Adapter) Capabilities() framework.Capabilities {
    return framework.Capabilities{
        Services: []framework.ServiceType{
            framework.ServiceSurface, framework.ServiceExpress, framework.ServiceReverse,
        },
        SupportsCOD: true, SupportsAddressChange: true,
        MaxDeclaredValuePaise: 5_000_000,
        MinWeightG: 1, MaxWeightG: 50_000,
        MaxLengthMM: 2000, MaxWidthMM: 2000, MaxHeightMM: 2000,
        SupportsFragile: true, SupportsBattery: true, SupportsPerishable: true,
        PushesWebhookEvents: true, PullsTrackingEvents: true,
        LabelFormat: "pdf-a4",
    }
}
```

## Behavior Injection

Tests configure behaviors per operation. Default = success with 0 latency.

```go
// SetBehavior overrides the response for a specific op.
func (a *Adapter) SetBehavior(op string, b Behavior) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.behaviors[op] = b
}

func (a *Adapter) Behavior(op string) Behavior {
    a.mu.Lock()
    defer a.mu.Unlock()
    return a.behaviors[op]
}

// Convenience helpers used in tests:

func (a *Adapter) SetBookResult(res framework.Result[framework.BookResponse]) {
    var b Behavior
    if res.OK {
        b.Override = res.Value
    } else {
        b.Class = res.ErrorClass
        b.Retryable = res.Retryable
        b.Override = res.Err.Error()
    }
    a.SetBehavior("book", b)
}

func (a *Adapter) FailNextBookWith(class framework.ErrorClass, retryable bool, msg string) {
    a.SetBehavior("book", Behavior{Class: class, Retryable: retryable, Override: msg})
}
```

## Book Implementation

```go
func (a *Adapter) Book(ctx context.Context, req framework.BookRequest) framework.Result[framework.BookResponse] {
    return framework.Call(ctx, a.fw, a.Code(), "book", func(ctx context.Context) framework.Result[framework.BookResponse] {
        b := a.Behavior("book")
        if b.LatencyMs > 0 {
            select {
            case <-time.After(time.Duration(b.LatencyMs) * time.Millisecond):
            case <-ctx.Done():
                return framework.Result[framework.BookResponse]{OK: false, Err: ctx.Err(), ErrorClass: framework.ErrTimeout, Retryable: true}
            }
        }
        if b.Class != framework.ErrNone {
            return framework.Result[framework.BookResponse]{
                OK: false,
                Err: errors.New(asString(b.Override, "sandbox failure")),
                ErrorClass: b.Class,
                Retryable: b.Retryable,
            }
        }
        if v, ok := b.Override.(framework.BookResponse); ok {
            // Use the override; persist in store for later FetchLabel/Track
            a.store.PutShipment(req.ShipmentID, sandboxShipment{
                AWB: v.AWB, BookedAt: a.clock.Now(),
            })
            return framework.Result[framework.BookResponse]{OK: true, Value: v}
        }
        // Default: deterministic AWB from shipment id
        awb := "SBX" + strings.ToUpper(string(req.ShipmentID)[:8])
        a.store.PutShipment(req.ShipmentID, sandboxShipment{AWB: awb, BookedAt: a.clock.Now()})
        return framework.Result[framework.BookResponse]{
            OK: true,
            Value: framework.BookResponse{
                AWB:               awb,
                CarrierShipmentID: "CSID-" + awb,
                EstimatedDelivery: a.clock.Now().Add(72 * time.Hour),
            },
        }
    })
}
```

The same pattern applies to `Cancel`, `FetchLabel`, `FetchTrackingEvents`, `RaiseNDRAction`. Default-success for happy paths; `SetBehavior` for failure injection.

## Tracking Event Injection

Tests can push tracking events into the store:

```go
func (a *Adapter) PushTrackingEvent(awb, status string, occurred time.Time, location string) {
    a.store.AppendEvent(awb, sandboxEvent{Status: status, OccurredAt: occurred, Location: location})
}

func (a *Adapter) doFetchTrackingEvents(ctx context.Context, awb string, since time.Time) framework.Result[[]framework.TrackingEvent] {
    events := a.store.GetEvents(awb, since)
    out := make([]framework.TrackingEvent, len(events))
    for i, e := range events {
        out[i] = framework.TrackingEvent{
            AWB: awb, Status: e.Status, Location: e.Location, OccurredAt: e.OccurredAt,
        }
    }
    return framework.Result[[]framework.TrackingEvent]{OK: true, Value: out}
}
```

## Status Map

The sandbox uses canonical status strings directly (no carrier-specific dialect):

```go
var statusMap = map[string]tracking.CanonicalStatus{
    "booking_confirmed": tracking.StatusBookingConfirmed,
    "picked_up":         tracking.StatusPickedUp,
    "in_transit":        tracking.StatusInTransit,
    "out_for_delivery":  tracking.StatusOutForDelivery,
    "delivered":         tracking.StatusDelivered,
    "delivery_attempted": tracking.StatusDeliveryAttempted,
    "rto_initiated":     tracking.StatusRTOInitiated,
    "rto_delivered":     tracking.StatusRTODelivered,
}

func (a *Adapter) RegisterStatusMappings(n *tracking.Normalizer) {
    n.Register(a.Code(), statusMap)
}
```

## Production Guard

```go
// In cmd/server/wire.go:
if cfg.Env == "prod" && cfg.SandboxCarrierEnabled {
    panic("refusing to start: sandbox carrier enabled in prod env")
}
```

## Helpers Used in SLTs

```go
package slt

func SandboxCarrier(t *testing.T, pg *PG, code string) *sandbox.Adapter {
    a := sandbox.New(code)
    pg.Registry.Install(a)
    a.RegisterStatusMappings(pg.Normalizer)
    return a
}

func PushDelivered(t *testing.T, a *sandbox.Adapter, awb string) {
    a.PushTrackingEvent(awb, "delivered", time.Now(), "BLR")
}
```

## Testing

```go
func TestSandbox_DefaultBookSucceeds(t *testing.T) {
    a := sandbox.New("sb")
    res := a.Book(ctx, sampleReq())
    require.True(t, res.OK)
    require.True(t, strings.HasPrefix(res.Value.AWB, "SBX"))
}

func TestSandbox_FailureInjection(t *testing.T) {
    a := sandbox.New("sb")
    a.FailNextBookWith(framework.ErrTimeout, true, "injected timeout")
    res := a.Book(ctx, sampleReq())
    require.False(t, res.OK)
    require.True(t, res.Retryable)
    require.Equal(t, framework.ErrTimeout, res.ErrorClass)
}

func TestSandbox_LatencyInjection(t *testing.T) {
    a := sandbox.New("sb")
    a.SetBehavior("book", sandbox.Behavior{LatencyMs: 200})
    start := time.Now()
    a.Book(ctx, sampleReq())
    require.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
}

func TestSandbox_PushEventThenFetch(t *testing.T) { /* ... */ }
```

## References

- LLD §03-services/12-carriers-framework: implements the `Adapter` interface.
- LLD §03-services/14-tracking: status map registration.
- LLD §04-adapters/01-delhivery: production reference adapter.
