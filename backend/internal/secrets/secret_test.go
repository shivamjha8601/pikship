package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestSecret_redaction(t *testing.T) {
	s := New("supersecret")
	if got := s.String(); got != "***" {
		t.Errorf("String() = %q, want %q", got, "***")
	}
	if got := fmt.Sprintf("%s", s); got != "***" {
		t.Errorf("%%s = %q, want %q", got, "***")
	}
	if got := fmt.Sprintf("%v", s); got != "***" {
		t.Errorf("%%v = %q, want %q", got, "***")
	}
}

func TestSecret_empty_redaction(t *testing.T) {
	s := New("")
	if got := s.String(); got != "" {
		t.Errorf("empty secret String() = %q, want %q", got, "")
	}
}

func TestSecret_reveal(t *testing.T) {
	val := "mypassword"
	s := New(val)
	if got := s.Reveal(); got != val {
		t.Errorf("Reveal() = %q, want %q", got, val)
	}
}

func TestSecret_isZero(t *testing.T) {
	if !New("").IsZero() {
		t.Error("empty secret should be zero")
	}
	if New("x").IsZero() {
		t.Error("non-empty secret should not be zero")
	}
}

func TestSecret_equal(t *testing.T) {
	a := New("hello")
	b := New("hello")
	c := New("world")
	if !a.Equal(b) {
		t.Error("equal secrets should be equal")
	}
	if a.Equal(c) {
		t.Error("different secrets should not be equal")
	}
}

func TestSecret_marshalJSON(t *testing.T) {
	s := New("real")
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"***"` {
		t.Errorf("JSON = %s, want %q", data, `"***"`)
	}
}

func TestSecret_marshalJSON_empty(t *testing.T) {
	s := New("")
	data, _ := json.Marshal(s)
	if string(data) != `""` {
		t.Errorf("empty secret JSON = %s, want %q", data, `""`)
	}
}

// --- EnvStore ---

func TestEnvStore_get(t *testing.T) {
	t.Setenv("TEST_MYKEY", "testval")
	store := NewEnvStore("TEST_")
	got, err := store.Get(context.Background(), "mykey")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reveal() != "testval" {
		t.Errorf("Reveal() = %q, want %q", got.Reveal(), "testval")
	}
}

func TestEnvStore_missing(t *testing.T) {
	store := NewEnvStore("TEST_")
	_, err := store.Get(context.Background(), "does_not_exist_xyz")
	if err == nil {
		t.Error("expected error for missing key")
	}
}

// --- MemoryStore ---

func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore(map[string]string{"foo": "bar"})

	got, err := store.Get(context.Background(), "foo")
	if err != nil || got.Reveal() != "bar" {
		t.Errorf("Get(foo) = %v, %v", got.Reveal(), err)
	}

	_, err = store.Get(context.Background(), "missing")
	if err == nil {
		t.Error("expected MissingSecretError")
	}

	opt := store.GetOptional(context.Background(), "missing")
	if !opt.IsZero() {
		t.Error("GetOptional missing key should return zero secret")
	}

	store.Set("foo", "updated")
	got2, _ := store.Get(context.Background(), "foo")
	if got2.Reveal() != "updated" {
		t.Errorf("after Set, Reveal() = %q, want %q", got2.Reveal(), "updated")
	}
}
