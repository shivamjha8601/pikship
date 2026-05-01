# Core: Clock (`internal/core/clock.go`)

> Injected time source. Domain code never calls `time.Now()` directly.

## Purpose

Make time controllable in tests. The `Clock` interface is dependency-injected into every service that cares about time. Tests inject a `FakeClock` for deterministic behavior.

## Dependencies

- `time` (stdlib)
- `sync` (for FakeClock thread safety)

## Public API

```go
package core

import (
    "sync"
    "time"
)

// Clock is the abstraction over the system clock.
//
// Domain code MUST use Clock instead of time.Now() to enable deterministic
// testing. Constructors of services accept a Clock; production code passes
// SystemClock{}; tests pass FakeClock.
type Clock interface {
    // Now returns the current time. Should be monotonic forward in production.
    Now() time.Time

    // Since returns time elapsed since t.
    Since(t time.Time) time.Duration

    // After returns a channel that receives the current time after duration d.
    // Equivalent to time.After but controllable in tests.
    After(d time.Duration) <-chan time.Time

    // Sleep blocks until duration d has elapsed (or context is cancelled,
    // whichever comes first; ctx-aware variant via SleepCtx).
    Sleep(d time.Duration)
}

// SystemClock is the production Clock that delegates to the stdlib time package.
type SystemClock struct{}

func (SystemClock) Now() time.Time                          { return time.Now() }
func (SystemClock) Since(t time.Time) time.Duration         { return time.Since(t) }
func (SystemClock) After(d time.Duration) <-chan time.Time  { return time.After(d) }
func (SystemClock) Sleep(d time.Duration)                   { time.Sleep(d) }

// FakeClock is a controllable Clock for tests.
//
// FakeClock advances time only when Advance is called. After is implemented
// as a sorted timer queue; firing happens on Advance.
//
// Safe for concurrent use.
type FakeClock struct {
    mu     sync.Mutex
    now    time.Time
    timers []*fakeTimer
}

type fakeTimer struct {
    fireAt time.Time
    ch     chan time.Time
}

// NewFakeClock returns a FakeClock initialized to the given time.
func NewFakeClock(start time.Time) *FakeClock {
    return &FakeClock{now: start}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.now
}

// Since returns time elapsed since t in the fake clock.
func (c *FakeClock) Since(t time.Time) time.Duration {
    return c.Now().Sub(t)
}

// After returns a channel that fires when fake time advances past d.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
    c.mu.Lock()
    defer c.mu.Unlock()
    t := &fakeTimer{
        fireAt: c.now.Add(d),
        ch:     make(chan time.Time, 1),
    }
    c.timers = append(c.timers, t)
    return t.ch
}

// Sleep on FakeClock blocks until the corresponding After channel fires.
// In practice, tests should call Advance to trigger sleeps; if Sleep is
// invoked without Advance, the calling goroutine blocks forever.
func (c *FakeClock) Sleep(d time.Duration) {
    <-c.After(d)
}

// Advance moves the fake clock forward by d. Any timers scheduled to fire
// within the new range are fired (in order).
func (c *FakeClock) Advance(d time.Duration) {
    c.mu.Lock()
    c.now = c.now.Add(d)

    var remaining []*fakeTimer
    for _, t := range c.timers {
        if !t.fireAt.After(c.now) {
            // Fire and discard
            select {
            case t.ch <- c.now:
            default:
            }
        } else {
            remaining = append(remaining, t)
        }
    }
    c.timers = remaining
    c.mu.Unlock()
}

// Set sets the clock to the given time directly (does NOT fire any timers
// that should have elapsed; use Advance for that).
//
// Useful only for tests that need to start a fresh point.
func (c *FakeClock) Set(t time.Time) {
    c.mu.Lock()
    c.now = t
    c.mu.Unlock()
}
```

## Implementation notes

### Why an interface vs a function

A `func() time.Time` is simpler but loses `After`, `Sleep`, `Since`. An interface gives one consistent abstraction.

### Why FakeClock is its own type, not part of the interface

Production code consumes `Clock`; tests construct `*FakeClock` and pass it (it satisfies `Clock`). The `Advance`/`Set` methods are on `*FakeClock` only — the production binary never calls them.

### Concurrency

- `SystemClock` is stateless and trivially safe.
- `FakeClock` uses a mutex; `Advance` and `Now` are safe from multiple goroutines.

### Timer fairness

`Advance` fires all expired timers in encounter order. For tests that depend on "fire timer A before timer B", schedule them with explicit times.

### Monotonicity

`SystemClock.Now()` uses Go's monotonic clock under the hood, so subtraction is safe across the wall-clock changes (e.g., NTP sync). `FakeClock` is monotonic by construction.

## Pattern of use

```go
// Production wiring (cmd/pikshipp/main.go):
clock := core.SystemClock{}
walletSvc := wallet.New(db, audit, outbox, clock, logger)

// Test:
clock := core.NewFakeClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
walletSvc := wallet.New(db, audit, outbox, clock, logger)

// Run something that uses Clock internally
holdID, _ := walletSvc.Reserve(ctx, sellerID, paise(100), 5*time.Minute)

// Advance past the TTL
clock.Advance(6 * time.Minute)

// Trigger expiration sweep
err := walletSvc.SweepExpiredHolds(ctx)
// ... assertions
```

## Linter rule

`golangci-lint` rule (custom or `forbidigo`):

```yaml
# .golangci.yml
linters-settings:
  forbidigo:
    forbid:
      - p: '^time\.Now$'
        msg: "use core.Clock.Now() instead of time.Now() in domain code"
        # but allowed in cmd/, observability/, and SystemClock impl
```

Allowlisted paths:
- `cmd/pikshipp/main.go` — for startup time.
- `internal/observability/*` — for log timestamps where Clock isn't injectable.
- `internal/core/clock.go` — the `SystemClock` impl itself.

## Testing

```go
func TestFakeClock_AdvanceFiresTimers(t *testing.T) {
    c := NewFakeClock(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))

    ch := c.After(5 * time.Second)

    // Not fired yet
    select {
    case <-ch:
        t.Fatal("timer fired prematurely")
    default:
    }

    c.Advance(3 * time.Second)
    select {
    case <-ch:
        t.Fatal("timer fired too early after only 3s")
    default:
    }

    c.Advance(2 * time.Second)
    select {
    case <-ch:
        // ok
    case <-time.After(100 * time.Millisecond):
        t.Fatal("timer didn't fire after total 5s")
    }
}

func TestFakeClock_NowAdvances(t *testing.T) {
    start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
    c := NewFakeClock(start)
    c.Advance(time.Hour)
    if got := c.Now(); !got.Equal(start.Add(time.Hour)) {
        t.Errorf("Now() = %v, want %v", got, start.Add(time.Hour))
    }
}

func TestFakeClock_ConcurrentAdvance(t *testing.T) {
    c := NewFakeClock(time.Now())
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            c.Advance(time.Second)
        }()
    }
    wg.Wait()
    // Should not deadlock or race; no specific assertion needed
}
```

## Performance

- `SystemClock.Now()`: ~30ns (one syscall on first call, monotonic afterward).
- `FakeClock.Now()`: ~30ns (mutex + return).

Negligible in practice.

## Open questions

- Should we support deterministic timers (`Tick`) for tests of long-running loops? Maybe add `FakeTicker` if a test needs it.
- Multi-clock isolation in parallel tests: each test creates its own `FakeClock`; no global. Already correct.
