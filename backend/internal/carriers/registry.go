package carriers

import (
	"fmt"
	"sync"
)

// Registry maps carrier codes to their Adapter implementations.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

// Install registers an adapter. Panics on duplicate code — this is startup wiring.
func (r *Registry) Install(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	code := a.Code()
	if _, ok := r.adapters[code]; ok {
		panic(fmt.Sprintf("carriers: adapter %q already registered", code))
	}
	r.adapters[code] = a
}

// Get returns the adapter for the given carrier code, or false if not found.
func (r *Registry) Get(code string) (Adapter, bool) {
	r.mu.RLock()
	a, ok := r.adapters[code]
	r.mu.RUnlock()
	return a, ok
}

// All returns all registered adapters.
func (r *Registry) All() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a)
	}
	return out
}

// Codes returns all registered carrier codes.
func (r *Registry) Codes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for code := range r.adapters {
		out = append(out, code)
	}
	return out
}
