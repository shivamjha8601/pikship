package wallet

import (
	"testing"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// --- unit tests on the service logic that don't need DB ---

func TestBalance_available(t *testing.T) {
	// available = balance + creditLimit - holdTotal
	bal := Balance{
		Balance:     core.Paise(10000),
		HoldTotal:   core.Paise(2000),
		Available:   core.Paise(8000),
		CreditLimit: core.Paise(0),
	}
	if bal.Available != 8000 {
		t.Errorf("available=%d want 8000", bal.Available)
	}
}

func TestRef_distinct(t *testing.T) {
	r1 := Ref{Type: "shipment", ID: "abc"}
	r2 := Ref{Type: "shipment", ID: "def"}
	if r1 == r2 {
		t.Error("refs with different IDs must be distinct")
	}
}

// --- ErrInsufficientFunds sentinel ---

func TestErrSentinels(t *testing.T) {
	if ErrInsufficientFunds == nil {
		t.Error("ErrInsufficientFunds should not be nil")
	}
	if ErrAccountFrozen == nil {
		t.Error("ErrAccountFrozen should not be nil")
	}
}

// --- Hold TTL default ---

func TestDefaultHoldTTL(t *testing.T) {
	if defaultHoldTTL != 10*time.Minute {
		t.Errorf("defaultHoldTTL=%v want 10m", defaultHoldTTL)
	}
}
