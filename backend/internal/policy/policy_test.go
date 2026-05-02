package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// --- Value type unit tests ---

func TestInt64Value_roundtrip(t *testing.T) {
	v := Int64Value(42)
	n, err := v.AsInt64()
	if err != nil || n != 42 {
		t.Errorf("Int64Value roundtrip: got %d %v", n, err)
	}
}

func TestStringValue_roundtrip(t *testing.T) {
	v := StringValue("hello")
	s, err := v.AsString()
	if err != nil || s != "hello" {
		t.Errorf("StringValue roundtrip: got %q %v", s, err)
	}
}

func TestBoolValue_roundtrip(t *testing.T) {
	v := BoolValue(true)
	b, err := v.AsBool()
	if err != nil || !b {
		t.Errorf("BoolValue roundtrip: got %v %v", b, err)
	}
}

func TestDurationValue_roundtrip(t *testing.T) {
	d := 5 * time.Minute
	v := DurationValue(d)
	got, err := v.AsDuration()
	if err != nil || got != d {
		t.Errorf("DurationValue roundtrip: got %v %v", got, err)
	}
}

func TestPaiseValue_roundtrip(t *testing.T) {
	v := PaiseValue(core.FromRupees(100))
	p, err := v.AsPaise()
	if err != nil || p != core.FromRupees(100) {
		t.Errorf("PaiseValue roundtrip: got %v %v", p, err)
	}
}

func TestStringSetValue_roundtrip(t *testing.T) {
	original := core.NewStringSet("delhivery", "bluedart")
	v := StringSetValue(original)
	got, err := v.AsStringSet()
	if err != nil {
		t.Fatalf("AsStringSet: %v", err)
	}
	if !got.Has("delhivery") || !got.Has("bluedart") {
		t.Errorf("StringSet roundtrip missing entries: %v", got)
	}
}

func TestValue_wrongType(t *testing.T) {
	v := StringValue("not-a-number")
	_, err := v.AsInt64()
	if err == nil {
		t.Error("expected error when reading string as int64")
	}
}

func TestFromRaw(t *testing.T) {
	raw, _ := json.Marshal(99)
	v := FromRaw(raw)
	n, err := v.AsInt64()
	if err != nil || n != 99 {
		t.Errorf("FromRaw: got %d %v", n, err)
	}
}

// --- Definitions registry ---

func TestDefinitionByKey_found(t *testing.T) {
	def := DefinitionByKey(KeyWalletPosture)
	if def == nil {
		t.Fatal("KeyWalletPosture must be registered")
	}
	if def.ValueType != TypeString {
		t.Errorf("KeyWalletPosture type=%s want string", def.ValueType)
	}
}

func TestDefinitionByKey_notFound(t *testing.T) {
	if DefinitionByKey("nonexistent.key") != nil {
		t.Error("unknown key should return nil")
	}
}

func TestAllDefinitionsHaveDefaults(t *testing.T) {
	for _, def := range Definitions {
		if def.DefaultGlobal.IsZero() {
			t.Errorf("key %s has zero DefaultGlobal", def.Key)
		}
	}
}

func TestAllDefinitionsHaveUniqueKeys(t *testing.T) {
	seen := map[Key]bool{}
	for _, def := range Definitions {
		if seen[def.Key] {
			t.Errorf("duplicate key in Definitions: %s", def.Key)
		}
		seen[def.Key] = true
	}
}

// --- Cache ---

func TestCache_globalLock(t *testing.T) {
	c := newCache(5*time.Second, core.SystemClock{})
	v := Int64Value(999)
	c.SetGlobalLock(KeyCODEnabled, v)

	got, ok := c.GlobalLock(KeyCODEnabled)
	if !ok {
		t.Fatal("expected cache hit")
	}
	n, _ := got.AsInt64()
	if n != 999 {
		t.Errorf("got %d want 999", n)
	}

	c.InvalidateGlobalLock(KeyCODEnabled)
	_, ok = c.GlobalLock(KeyCODEnabled)
	if ok {
		t.Error("expected cache miss after invalidation")
	}
}

func TestCache_sellerOverride(t *testing.T) {
	c := newCache(5*time.Second, core.SystemClock{})
	sid := core.NewSellerID()
	v := StringValue("credit_only")
	c.SetSellerOverride(sid, KeyWalletPosture, v)

	got, ok := c.SellerOverride(sid, KeyWalletPosture)
	if !ok {
		t.Fatal("expected override hit")
	}
	s, _ := got.AsString()
	if s != "credit_only" {
		t.Errorf("got %q want %q", s, "credit_only")
	}

	c.InvalidateSeller(sid, KeyWalletPosture)
	_, ok = c.SellerOverride(sid, KeyWalletPosture)
	if ok {
		t.Error("expected miss after invalidation")
	}
}

func TestCache_ttlExpiry(t *testing.T) {
	// Use a fake clock to control time.
	fc := &core.FakeClock{}
	c := newCache(100*time.Millisecond, fc)
	c.SetGlobalLock(KeyCODEnabled, BoolValue(true))

	_, ok := c.GlobalLock(KeyCODEnabled)
	if !ok {
		t.Fatal("expected hit before TTL")
	}

	fc.Advance(200 * time.Millisecond)
	_, ok = c.GlobalLock(KeyCODEnabled)
	if ok {
		t.Error("expected miss after TTL expired")
	}
}
