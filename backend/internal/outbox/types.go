package outbox

import (
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Event is one row from outbox_event, decoded for dispatch.
type Event struct {
	ID         core.OutboxEventID
	SellerID   *core.SellerID
	Kind       string
	Version    int
	Payload    []byte // raw JSON
	OccurredAt time.Time
	CreatedAt  time.Time
}

// Handler processes a decoded outbox event. Returning a non-nil error
// causes the River job to retry.
type Handler func(e Event) error
