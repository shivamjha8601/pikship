package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const insertOutboxEventSQL = `
    INSERT INTO outbox_event (id, seller_id, kind, version, payload, occurred_at)
    VALUES ($1, $2, $3, $4, $5::jsonb, $6)
`

// Emitter writes outbox_event rows inside a caller-provided transaction.
// The row is committed atomically with the domain change in the same tx.
type Emitter struct{}

// NewEmitter returns an Emitter. It is stateless; the pool is not needed
// here — rows are written through the caller's tx.
func NewEmitter() *Emitter { return &Emitter{} }

// Emit writes one outbox_event row inside tx. payload must be JSON-marshallable.
// sellerID may be nil for platform-level events.
func (em *Emitter) Emit(ctx context.Context, tx pgx.Tx, kind string, sellerID *core.SellerID, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox.Emit marshal: %w", err)
	}

	id := uuid.New()
	var sellerUUID *uuid.UUID
	if sellerID != nil {
		u := sellerID.UUID()
		sellerUUID = &u
	}

	_, err = tx.Exec(ctx, insertOutboxEventSQL,
		id, sellerUUID, kind, 1, raw, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("outbox.Emit insert: %w", err)
	}
	return nil
}
