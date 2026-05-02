package outbox

import (
	"fmt"
	"sync"
)

// Registry maps outbox event kinds to their Handler functions.
// Register must be called before the Forwarder or DispatchWorker starts.
// Concurrent reads (Dispatch) are safe after all Register calls complete.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register binds kind to h. Panics if kind is registered twice — that is
// always a programming error at boot time.
func (r *Registry) Register(kind string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.handlers[kind]; dup {
		panic(fmt.Sprintf("outbox: duplicate handler for kind %q", kind))
	}
	r.handlers[kind] = h
}

// Dispatch calls the handler registered for e.Kind. Returns an error if no
// handler is registered (so the River job can retry or DLQ).
func (r *Registry) Dispatch(e Event) error {
	r.mu.RLock()
	h, ok := r.handlers[e.Kind]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("outbox: no handler for kind %q", e.Kind)
	}
	return h(e)
}
