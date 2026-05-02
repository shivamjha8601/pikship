package secrets

import (
	"context"
	"sync"
)

// MemoryStore is a thread-safe in-memory Store for use in tests.
type MemoryStore struct {
	mu      sync.RWMutex
	secrets map[string]string
}

// NewMemoryStore creates a MemoryStore pre-populated with the given map.
func NewMemoryStore(s map[string]string) *MemoryStore {
	m := make(map[string]string, len(s))
	for k, v := range s {
		m[k] = v
	}
	return &MemoryStore{secrets: m}
}

// Set adds or replaces a secret value. Safe to call concurrently.
func (ms *MemoryStore) Set(key, val string) {
	ms.mu.Lock()
	ms.secrets[key] = val
	ms.mu.Unlock()
}

func (ms *MemoryStore) Get(_ context.Context, key string) (Secret, error) {
	ms.mu.RLock()
	val, ok := ms.secrets[key]
	ms.mu.RUnlock()
	if !ok {
		return Secret{}, MissingSecretError{Key: key}
	}
	return New(val), nil
}

func (ms *MemoryStore) GetOptional(_ context.Context, key string) Secret {
	ms.mu.RLock()
	val := ms.secrets[key]
	ms.mu.RUnlock()
	return New(val)
}
