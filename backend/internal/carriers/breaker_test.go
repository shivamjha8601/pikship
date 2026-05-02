package carriers

import (
	"testing"
	"time"
)

func TestBreaker_closedByDefault(t *testing.T) {
	b := DefaultBreaker()
	if !b.Allow() {
		t.Error("new breaker should allow calls")
	}
	if b.State() != BreakerClosed {
		t.Errorf("new breaker state=%d want Closed", b.State())
	}
}

func TestBreaker_opensAfterThreshold(t *testing.T) {
	b := &Breaker{threshold: 3, resetTimeout: time.Hour, halfOpenProbe: 2}
	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}
	if b.State() != BreakerOpen {
		t.Error("breaker should open after threshold failures")
	}
	if b.Allow() {
		t.Error("open breaker should reject calls")
	}
}

func TestBreaker_halfOpenAfterTimeout(t *testing.T) {
	b := &Breaker{threshold: 1, resetTimeout: time.Millisecond, halfOpenProbe: 1}
	b.RecordFailure() // opens
	if b.State() != BreakerOpen {
		t.Fatal("should be open")
	}
	time.Sleep(5 * time.Millisecond)
	if !b.Allow() {
		t.Error("should allow probe after reset timeout")
	}
	if b.State() != BreakerHalfOpen {
		t.Errorf("expected HalfOpen after timeout, got %d", b.State())
	}
}

func TestBreaker_closesAfterHalfOpenSuccess(t *testing.T) {
	b := &Breaker{threshold: 1, resetTimeout: time.Millisecond, halfOpenProbe: 2}
	b.RecordFailure()
	time.Sleep(5 * time.Millisecond)
	b.Allow() // transition to half-open
	b.RecordSuccess()
	b.RecordSuccess()
	if b.State() != BreakerClosed {
		t.Errorf("expected Closed after probes, got %d", b.State())
	}
}

func TestBreaker_recoveryResetsFailures(t *testing.T) {
	b := &Breaker{threshold: 3, resetTimeout: time.Hour, halfOpenProbe: 1}
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess() // should reset failure count
	b.RecordFailure()
	b.RecordFailure()
	// Only 2 failures since last success — should still be closed.
	if b.State() != BreakerClosed {
		t.Error("recovery should reset failure count")
	}
}
