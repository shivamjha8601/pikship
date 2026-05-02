package secrets

import (
	"context"
	"os"
	"strings"
)

// EnvStore is the v0 Store backed by environment variables.
// Key "session_hmac_key" → env var "PIKSHIPP_SESSION_HMAC_KEY".
type EnvStore struct {
	prefix string
}

// NewEnvStore returns an EnvStore. prefix is prepended to all lookups after
// upper-casing; use "PIKSHIPP_" in production.
func NewEnvStore(prefix string) *EnvStore {
	return &EnvStore{prefix: strings.ToUpper(prefix)}
}

func (s *EnvStore) Get(_ context.Context, key string) (Secret, error) {
	envKey := s.prefix + strings.ToUpper(key)
	val := os.Getenv(envKey)
	if val == "" {
		return Secret{}, MissingSecretError{Key: key}
	}
	return New(val), nil
}

func (s *EnvStore) GetOptional(_ context.Context, key string) Secret {
	envKey := s.prefix + strings.ToUpper(key)
	return New(os.Getenv(envKey))
}
