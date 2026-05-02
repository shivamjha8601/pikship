package core

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is the abstraction over the system clock. Domain code MUST use Clock
// instead of time.Now() to enable deterministic testing.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since returns time elapsed since t.
	Since(t time.Time) time.Duration
	// After returns a channel that fires after duration d.
	After(d time.Duration) <-chan time.Time
	// Sleep blocks until d has elapsed.
	Sleep(d time.Duration)
	// SleepCtx blocks until d has elapsed or ctx is cancelled.
	SleepCtx(ctx context.Context, d time.Duration) error
}

// SystemClock is the production Clock backed by the stdlib time package.
type SystemClock struct{}

func (SystemClock) Now() time.Time                         { return time.Now() }
func (SystemClock) Since(t time.Time) time.Duration        { return time.Since(t) }
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (SystemClock) Sleep(d time.Duration)                  { time.Sleep(d) }
func (SystemClock) SleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// FakeClock is a controllable Clock for tests. Time advances only when
// Advance is called; After timers fire on Advance.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	fireAt time.Time
	ch     chan time.Time
}

// NewFakeClock returns a FakeClock starting at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ft := &fakeTimer{fireAt: c.now.Add(d), ch: ch}
	c.timers = append(c.timers, ft)
	return ch
}

// Sleep on FakeClock is a no-op — tests should use Advance instead. Callers
// that need to suspend a goroutine in a test should rely on After.
func (c *FakeClock) Sleep(time.Duration) {}

// SleepCtx is a no-op (returns immediately) in fake mode unless ctx is
// already cancelled.
func (c *FakeClock) SleepCtx(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// Advance moves the fake clock forward by d, firing any timers whose
// deadline has now passed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	due := c.now
	// Fire any timers whose fireAt <= due. Stable order helps tests that rely on it.
	sort.SliceStable(c.timers, func(i, j int) bool { return c.timers[i].fireAt.Before(c.timers[j].fireAt) })
	remaining := c.timers[:0]
	for _, t := range c.timers {
		if !t.fireAt.After(due) {
			select {
			case t.ch <- due:
			default:
			}
			continue
		}
		remaining = append(remaining, t)
	}
	c.timers = remaining
	c.mu.Unlock()
}

// Set hard-resets the fake clock to t (no timers fire).
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}
