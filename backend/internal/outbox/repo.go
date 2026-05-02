package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

const (
	listPendingSQL = `
        SELECT id, seller_id, kind, version, payload, occurred_at, created_at
        FROM outbox_event
        WHERE enqueued_at IS NULL
        ORDER BY created_at ASC
        LIMIT $1
        FOR UPDATE SKIP LOCKED
    `

	markEnqueuedSQL = `
        UPDATE outbox_event SET enqueued_at = $1 WHERE id = ANY($2)
    `

	getByIDSQL = `
        SELECT id, seller_id, kind, version, payload, occurred_at, created_at
        FROM outbox_event
        WHERE id = $1
    `
)

type repo struct {
	pool *pgxpool.Pool
}

func newRepo(pool *pgxpool.Pool) *repo { return &repo{pool: pool} }

// listAndMarkPending atomically fetches up to n pending rows and marks them
// as enqueued inside a single transaction. Returns the claimed rows.
func (r *repo) listAndMarkPending(ctx context.Context, n int) ([]Event, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("outbox.listAndMark begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, listPendingSQL, n)
	if err != nil {
		return nil, fmt.Errorf("outbox.listAndMark query: %w", err)
	}

	var events []Event
	var ids []uuid.UUID
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("outbox.listAndMark scan: %w", err)
		}
		events = append(events, e)
		ids = append(ids, e.ID.UUID())
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox.listAndMark iter: %w", err)
	}

	if len(ids) == 0 {
		return nil, tx.Rollback(ctx)
	}

	if _, err := tx.Exec(ctx, markEnqueuedSQL, time.Now().UTC(), ids); err != nil {
		return nil, fmt.Errorf("outbox.markEnqueued: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("outbox.listAndMark commit: %w", err)
	}
	return events, nil
}

// getByID loads a single outbox_event for the dispatch worker.
func (r *repo) getByID(ctx context.Context, id core.OutboxEventID) (Event, error) {
	row := r.pool.QueryRow(ctx, getByIDSQL, id.UUID())
	e, err := scanEvent(row)
	if err != nil {
		return Event{}, fmt.Errorf("outbox.getByID: %w", err)
	}
	return e, nil
}

// scanEvent reads one outbox_event row from any pgx Row/Rows.
func scanEvent(s interface {
	Scan(...any) error
}) (Event, error) {
	var (
		id          uuid.UUID
		sellerIDRaw *uuid.UUID
		kind        string
		version     int
		payload     []byte
		occurredAt  time.Time
		createdAt   time.Time
	)
	if err := s.Scan(&id, &sellerIDRaw, &kind, &version, &payload, &occurredAt, &createdAt); err != nil {
		return Event{}, err
	}

	e := Event{
		ID:         core.OutboxEventIDFromUUID(id),
		Kind:       kind,
		Version:    version,
		Payload:    payload,
		OccurredAt: occurredAt,
		CreatedAt:  createdAt,
	}
	if sellerIDRaw != nil {
		sid := core.SellerIDFromUUID(*sellerIDRaw)
		e.SellerID = &sid
	}
	return e, nil
}

