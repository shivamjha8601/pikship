package secrets

import (
	"context"
	"fmt"
)

// Store retrieves secrets from a backend (env vars at v0; SSM at v1+).
// Implementations must be safe for concurrent use.
type Store interface {
	// Get returns the secret for key, or MissingSecretError if absent.
	Get(ctx context.Context, key string) (Secret, error)
	// GetOptional returns an empty Secret when the key is missing.
	GetOptional(ctx context.Context, key string) Secret
}

// MissingSecretError is returned by Store.Get when the key is not found.
type MissingSecretError struct {
	Key string
}

func (e MissingSecretError) Error() string {
	return fmt.Sprintf("secrets: missing required secret %q", e.Key)
}
