package policy

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// --- Constructors ---

func Int64Value(v int64) Value {
	raw, _ := json.Marshal(v)
	return Value{raw: raw}
}

func PaiseValue(v core.Paise) Value { return Int64Value(int64(v)) }

func StringValue(v string) Value {
	raw, _ := json.Marshal(v)
	return Value{raw: raw}
}

func BoolValue(v bool) Value {
	raw, _ := json.Marshal(v)
	return Value{raw: raw}
}

func DurationValue(v time.Duration) Value { return StringValue(v.String()) }

func StringListValue(v []string) Value {
	raw, _ := json.Marshal(v)
	return Value{raw: raw}
}

func StringSetValue(v core.StringSet) Value { return StringListValue(v.Slice()) }

// --- Accessors ---

func (v Value) AsInt64() (int64, error) {
	var n int64
	if err := json.Unmarshal(v.raw, &n); err != nil {
		return 0, fmt.Errorf("policy.Value.AsInt64: %w: %w", err, ErrInvalidValue)
	}
	return n, nil
}

func (v Value) AsPaise() (core.Paise, error) {
	n, err := v.AsInt64()
	return core.Paise(n), err
}

func (v Value) AsString() (string, error) {
	var s string
	if err := json.Unmarshal(v.raw, &s); err != nil {
		return "", fmt.Errorf("policy.Value.AsString: %w: %w", err, ErrInvalidValue)
	}
	return s, nil
}

func (v Value) AsBool() (bool, error) {
	var b bool
	if err := json.Unmarshal(v.raw, &b); err != nil {
		return false, fmt.Errorf("policy.Value.AsBool: %w: %w", err, ErrInvalidValue)
	}
	return b, nil
}

func (v Value) AsDuration() (time.Duration, error) {
	s, err := v.AsString()
	if err != nil {
		return 0, err
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("policy.Value.AsDuration: %w: %w", err, ErrInvalidValue)
	}
	return d, nil
}

func (v Value) AsStringList() ([]string, error) {
	var l []string
	if err := json.Unmarshal(v.raw, &l); err != nil {
		return nil, fmt.Errorf("policy.Value.AsStringList: %w: %w", err, ErrInvalidValue)
	}
	return l, nil
}

func (v Value) AsStringSet() (core.StringSet, error) {
	l, err := v.AsStringList()
	if err != nil {
		return nil, err
	}
	return core.NewStringSet(l...), nil
}

func (v Value) MustAsInt64() int64 {
	n, err := v.AsInt64()
	if err != nil {
		panic(err)
	}
	return n
}

func (v Value) IsZero() bool { return len(v.raw) == 0 }

// Raw returns the underlying JSON bytes (for repo storage).
func (v Value) Raw() json.RawMessage { return v.raw }

// FromRaw constructs a Value from stored JSON bytes.
func FromRaw(raw json.RawMessage) Value { return Value{raw: raw} }
