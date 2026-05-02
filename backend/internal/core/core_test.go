package core

import (
	"encoding/json"
	"testing"
)

// --- Paise money ---

func TestFromRupees(t *testing.T) {
	if got := FromRupees(100); got != 10000 {
		t.Errorf("FromRupees(100) = %d, want 10000", got)
	}
}

func TestPaise_string(t *testing.T) {
	p := Paise(10050)
	if got := p.String(); got != "₹100.50" {
		t.Errorf("String() = %q, want ₹100.50", got)
	}
}

func TestPaise_arithmetic(t *testing.T) {
	a := Paise(1000)
	b := Paise(500)
	sum, ok := a.Add(b)
	if !ok || sum != 1500 {
		t.Errorf("Add failed: %d %v", sum, ok)
	}
	diff, ok := a.Sub(b)
	if !ok || diff != 500 {
		t.Errorf("Sub failed: %d %v", diff, ok)
	}
}

func TestPaise_marshalJSON(t *testing.T) {
	p := Paise(9999)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Paise
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("JSON roundtrip: got %d want %d", got, p)
	}
}

// --- Typed IDs ---

func TestSellerID_roundtrip(t *testing.T) {
	id := NewSellerID()
	if id.IsZero() {
		t.Error("NewSellerID must not produce zero value")
	}
	s := id.String()
	parsed, err := ParseSellerID(s)
	if err != nil {
		t.Fatalf("ParseSellerID: %v", err)
	}
	if parsed != id {
		t.Errorf("roundtrip failed: %s != %s", parsed, id)
	}
}

func TestUserID_roundtrip(t *testing.T) {
	id := NewUserID()
	parsed, err := ParseUserID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != id {
		t.Error("UserID roundtrip failed")
	}
}

func TestOrderID_roundtrip(t *testing.T) {
	id := NewOrderID()
	parsed, err := ParseOrderID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != id {
		t.Error("OrderID roundtrip failed")
	}
}

func TestShipmentID_roundtrip(t *testing.T) {
	id := NewShipmentID()
	parsed, err := ParseShipmentID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != id {
		t.Error("ShipmentID roundtrip failed")
	}
}

func TestSellerID_isZero(t *testing.T) {
	var id SellerID
	if !id.IsZero() {
		t.Error("zero SellerID must be IsZero")
	}
}

// --- SellerRole / HasAnyRole ---

func TestHasAnyRole(t *testing.T) {
	roles := []SellerRole{RoleOwner, RoleManager}

	if !HasAnyRole(roles, []SellerRole{RoleOwner}) {
		t.Error("owner should match")
	}
	if !HasAnyRole(roles, []SellerRole{RoleFinance, RoleManager}) {
		t.Error("manager should match in the required list")
	}
	if HasAnyRole(roles, []SellerRole{RoleFinance, RoleViewer}) {
		t.Error("finance/viewer not in roles — should not match")
	}
	if HasAnyRole(nil, []SellerRole{RoleOwner}) {
		t.Error("empty roles should never match")
	}
}

// --- StringSet ---

func TestNewStringSet(t *testing.T) {
	s := NewStringSet("a", "b", "c")
	if !s.Has("a") || !s.Has("b") || !s.Has("c") {
		t.Error("NewStringSet: missing members")
	}
	if s.Has("d") {
		t.Error("NewStringSet: unexpected member")
	}
}

func TestStringSet_slice(t *testing.T) {
	s := NewStringSet("x", "y")
	sl := s.Slice()
	if len(sl) != 2 {
		t.Errorf("Slice len=%d want 2", len(sl))
	}
}

// --- Pincode ---

func TestPincode_valid(t *testing.T) {
	if !Pincode("400001").IsValid() {
		t.Error("400001 should be valid")
	}
	if Pincode("0ABCDE").IsValid() {
		t.Error("0ABCDE should be invalid (leading zero / alpha)")
	}
	if Pincode("12345").IsValid() {
		t.Error("12345 (5 digits) should be invalid")
	}
}

// --- FakeClock ---

func TestFakeClock_advance(t *testing.T) {
	fc := &FakeClock{}
	t0 := fc.Now()
	fc.Advance(5 * 1e9) // 5 seconds in nanoseconds via Duration
	if !fc.Now().After(t0) {
		t.Error("FakeClock.Now should advance after Advance()")
	}
}
