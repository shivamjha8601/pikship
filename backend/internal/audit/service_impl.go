package audit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

type emitterImpl struct {
	repo   *repo
	pool   *pgxpool.Pool
	outbox OutboxEmitter
	clock  core.Clock
	log    *slog.Logger
}

// New constructs the production Emitter. pool MUST be the app pool
// (RLS-enforced); outbox is the real outbox emitter; clock is the
// system clock in prod and a FakeClock in tests.
func New(pool *pgxpool.Pool, ob OutboxEmitter, clock core.Clock, log *slog.Logger) Emitter {
	return &emitterImpl{
		repo:   newRepo(pool),
		pool:   pool,
		outbox: ob,
		clock:  clock,
		log:    log,
	}
}

func (e *emitterImpl) Emit(ctx context.Context, tx pgx.Tx, event Event) error {
	if !IsHighValue(event.Action) {
		return fmt.Errorf("audit.Emit %q: %w", event.Action, ErrNotHighValue)
	}
	e.fillDefaults(ctx, &event)

	prevHash, err := e.repo.getLastChainHashTx(ctx, tx, event.SellerID)
	if err != nil {
		return fmt.Errorf("audit.Emit get prev hash: %w", err)
	}
	eventHash := computeEventHash(event, prevHash)

	if err := e.repo.insertEventTx(ctx, tx, event, eventHash, prevHash); err != nil {
		return fmt.Errorf("audit.Emit insert: %w", err)
	}
	return nil
}

func (e *emitterImpl) EmitAsync(ctx context.Context, event Event) error {
	e.fillDefaults(ctx, &event)

	if e.outbox == nil {
		// Defensive fallback: log + drop. This branch should never run in
		// prod (main wires a real outbox), but during partial bring-up of
		// the system we'd rather lose the async event than crash the
		// caller. Make the loss visible.
		e.log.WarnContext(ctx, "audit.EmitAsync: outbox not wired; event dropped",
			slog.String("action", event.Action),
		)
		return nil
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("audit.EmitAsync begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if err := e.outbox.Emit(ctx, tx, "audit.write", event.SellerID, event); err != nil {
		return fmt.Errorf("audit.EmitAsync outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit.EmitAsync commit: %w", err)
	}
	return nil
}

// fillDefaults populates the fields domain code is allowed to leave zero.
// Mutates *event in place. ctx is the original caller's context — when
// the auth middleware has populated it via WithActor, that Actor wins.
func (e *emitterImpl) fillDefaults(ctx context.Context, event *Event) {
	if event.ID.IsZero() {
		event.ID = core.AuditEventIDFromUUID(uuid.New())
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = e.clock.Now().UTC()
	}
	if event.Actor.Kind == "" {
		event.Actor = ActorFromContext(ctx)
	}
}
