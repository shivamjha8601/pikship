// Package secrets provides a typed Secret value that prevents accidental
// disclosure via logging or JSON serialization. The only way to obtain the
// plaintext is via Reveal(), which is auditable in code review.
//
// Per LLD §02-infrastructure/05-secrets.
package secrets

import (
	"crypto/subtle"
	"fmt"
)

// Secret wraps a sensitive string. Its String/Format/MarshalJSON methods
// all return "***" so the value cannot leak through fmt, slog, or JSON.
type Secret struct {
	val string
}

// New constructs a Secret from a plaintext string.
func New(val string) Secret { return Secret{val: val} }

// Reveal returns the plaintext. Every call site must be justified in review.
func (s Secret) Reveal() string { return s.val }

// String implements fmt.Stringer; returns "***" for non-empty secrets.
func (s Secret) String() string {
	if s.val == "" {
		return ""
	}
	return "***"
}

// Format implements fmt.Formatter so all %v/%s/%q verbs produce "***".
func (s Secret) Format(f fmt.State, verb rune) {
	fmt.Fprint(f, s.String()) //nolint:errcheck
}

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.val == "" }

// Equal performs constant-time comparison to protect against timing attacks
// (important when comparing HMAC keys or webhook secrets).
func (s Secret) Equal(other Secret) bool {
	return subtle.ConstantTimeCompare([]byte(s.val), []byte(other.val)) == 1
}

// HasPrefix reports whether the secret's plaintext begins with prefix.
// Safe to call without exposing the full value.
func (s Secret) HasPrefix(prefix string) bool {
	if len(s.val) < len(prefix) {
		return false
	}
	return s.val[:len(prefix)] == prefix
}

// MarshalJSON always encodes to "***" to prevent JSON serialization leaks.
func (s Secret) MarshalJSON() ([]byte, error) {
	if s.val == "" {
		return []byte(`""`), nil
	}
	return []byte(`"***"`), nil
}

// UnmarshalJSON populates the secret from JSON. Provided so config structs
// can be decoded, but production usage should load via Store, not JSON.
func (s *Secret) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("secrets.Secret: unmarshal: not a JSON string")
	}
	s.val = string(data[1 : len(data)-1])
	return nil
}
