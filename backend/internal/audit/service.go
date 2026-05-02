package audit

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// Emitter is the public API for writing audit events.
//
// Two paths:
//
//   - Emit — synchronous, in the caller's tx. Use for high-value events
//     (financial, KYC, ops privileged, contract changes). The audit row
//     commits with the domain change; if either fails, both roll back.
//
//   - EmitAsync — outbox-routed. Use for everything else. The audit
//     consumer picks up the outbox event and writes the row.
type Emitter interface {
	Emit(ctx context.Context, tx pgx.Tx, event Event) error
	EmitAsync(ctx context.Context, event Event) error
}

// OutboxEmitter is the slice of the outbox service that audit needs.
// Defined here (rather than importing internal/outbox) so audit doesn't
// take a package dependency on outbox; main wires the real outbox impl
// into audit at boot. Per LLD §03-services/03-outbox the outbox row is
// written inside the caller's tx so it commits atomically with the
// domain change.
type OutboxEmitter interface {
	Emit(ctx context.Context, tx pgx.Tx, kind string, sellerID *core.SellerID, payload any) error
}

// TxRunner abstracts running a function inside a transaction. EmitAsync
// opens its own tiny tx to write the outbox row; we accept this rather
// than hard-coding pool.BeginTx so tests can substitute a fake.
type TxRunner interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
}
