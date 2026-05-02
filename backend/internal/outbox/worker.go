package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// DispatchArgs is the River job payload for outbox dispatch.
type DispatchArgs struct {
	EventID string `json:"event_id"`
}

func (DispatchArgs) Kind() string { return "outbox.dispatch" }

// DispatchWorker is the River worker that loads and dispatches outbox events.
// Register with river.AddWorker before the client starts.
type DispatchWorker struct {
	river.WorkerDefaults[DispatchArgs]
	repo     *repo
	registry *Registry
}

// NewDispatchWorker constructs the worker. pool should be the app pool.
func NewDispatchWorker(pool *pgxpool.Pool, registry *Registry) *DispatchWorker {
	return &DispatchWorker{repo: newRepo(pool), registry: registry}
}

func (w *DispatchWorker) Work(ctx context.Context, job *river.Job[DispatchArgs]) error {
	id, err := core.ParseOutboxEventID(job.Args.EventID)
	if err != nil {
		return fmt.Errorf("outbox.DispatchWorker: bad event_id %q: %w", job.Args.EventID, err)
	}

	e, err := w.repo.getByID(ctx, id)
	if err != nil {
		return fmt.Errorf("outbox.DispatchWorker load: %w", err)
	}

	return w.registry.Dispatch(e)
}
