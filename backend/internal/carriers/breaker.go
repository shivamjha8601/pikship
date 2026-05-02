package carriers

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// BreakerState is the circuit breaker state.
type BreakerState int

const (
	BreakerClosed   BreakerState = iota // normal; calls pass through
	BreakerOpen                         // tripped; calls fail fast
	BreakerHalfOpen                     // testing if service recovered
)

// Breaker is a simple count-based circuit breaker per carrier.
type Breaker struct {
	mu            sync.Mutex
	state         BreakerState
	failures      int
	successes     int
	inFlight      int // probes in flight while half-open; cap at halfOpenProbe
	lastTrip      time.Time
	threshold     int
	resetTimeout  time.Duration
	halfOpenProbe int
}

// DefaultBreaker returns a Breaker with production defaults.
func DefaultBreaker() *Breaker {
	return &Breaker{
		threshold:     5,
		resetTimeout:  30 * time.Second,
		halfOpenProbe: 2,
	}
}

// Allow returns true if the call should proceed. In half-open state we
// admit at most halfOpenProbe concurrent probes; further calls fail fast
// until earlier probes resolve via RecordSuccess/RecordFailure.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if time.Since(b.lastTrip) > b.resetTimeout {
			b.state = BreakerHalfOpen
			b.successes = 0
			b.inFlight = 1
			return true
		}
		return false
	case BreakerHalfOpen:
		if b.inFlight >= b.halfOpenProbe {
			return false
		}
		b.inFlight++
		return true
	}
	return false
}

// RecordSuccess records a successful call.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.state == BreakerHalfOpen {
		if b.inFlight > 0 {
			b.inFlight--
		}
		b.successes++
		if b.successes >= b.halfOpenProbe {
			b.state = BreakerClosed
			b.inFlight = 0
		}
	}
}

// RecordFailure records a failed call.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == BreakerHalfOpen && b.inFlight > 0 {
		b.inFlight--
	}
	b.failures++
	if b.failures >= b.threshold {
		b.state = BreakerOpen
		b.lastTrip = time.Now()
		b.inFlight = 0
	}
}

// State returns the current breaker state.
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// breakerAdapter wraps an Adapter with circuit breaking on mutating calls.
type breakerAdapter struct {
	inner   Adapter
	breaker *Breaker
}

// WithBreaker wraps an Adapter with a Breaker.
func WithBreaker(a Adapter, b *Breaker) Adapter {
	return &breakerAdapter{inner: a, breaker: b}
}

func (ba *breakerAdapter) Code() string               { return ba.inner.Code() }
func (ba *breakerAdapter) DisplayName() string        { return ba.inner.DisplayName() }
func (ba *breakerAdapter) Capabilities() Capabilities { return ba.inner.Capabilities() }

func (ba *breakerAdapter) CheckServiceability(ctx context.Context, q ServiceabilityQuery) (bool, error) {
	return ba.inner.CheckServiceability(ctx, q)
}

func (ba *breakerAdapter) Book(ctx context.Context, req BookRequest) Result[BookResponse] {
	if !ba.breaker.Allow() {
		return Result[BookResponse]{Err: fmt.Errorf("carrier %s circuit open", ba.inner.Code()), ErrClass: ErrClassTransient}
	}
	r := ba.inner.Book(ctx, req)
	if r.OK() {
		ba.breaker.RecordSuccess()
	} else if r.ErrClass == ErrClassTransient {
		ba.breaker.RecordFailure()
	}
	return r
}

func (ba *breakerAdapter) Cancel(ctx context.Context, req CancelRequest) Result[CancelResponse] {
	if !ba.breaker.Allow() {
		return Result[CancelResponse]{Err: fmt.Errorf("carrier %s circuit open", ba.inner.Code()), ErrClass: ErrClassTransient}
	}
	r := ba.inner.Cancel(ctx, req)
	if r.OK() {
		ba.breaker.RecordSuccess()
	} else if r.ErrClass == ErrClassTransient {
		ba.breaker.RecordFailure()
	}
	return r
}

func (ba *breakerAdapter) FetchLabel(ctx context.Context, req LabelRequest) Result[LabelResponse] {
	return ba.inner.FetchLabel(ctx, req)
}

func (ba *breakerAdapter) FetchTrackingEvents(ctx context.Context, awb string, since time.Time) Result[[]TrackingEvent] {
	return ba.inner.FetchTrackingEvents(ctx, awb, since)
}

func (ba *breakerAdapter) RaiseNDRAction(ctx context.Context, req NDRActionRequest) Result[NDRActionResponse] {
	return ba.inner.RaiseNDRAction(ctx, req)
}
