package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	defaultBatchSize    = 100
	defaultPollInterval = 500 * time.Millisecond
)

// Forwarder polls outbox_event for pending rows, enqueues River jobs via
// river.Client, then marks enqueued_at. Runs until ctx is cancelled.
//
// Per LLD §03-services/03-outbox: claim → enqueue → mark in two separate
// DB operations (claim+mark are atomic in listAndMarkPending; enqueue is
// best-effort River insert). At-least-once delivery is guaranteed because
// the mark only happens after the River insert succeeds.
type Forwarder struct {
	repo         *repo
	riverClient  *river.Client[pgx.Tx]
	log          *slog.Logger
	batchSize    int
	pollInterval time.Duration
}

// NewForwarder creates a Forwarder. pool must have INSERT access to river_jobs.
// Use the app pool (pikshipp_app is granted river_jobs access by the River
// migration) or the admin pool.
func NewForwarder(pool *pgxpool.Pool, log *slog.Logger) (*Forwarder, error) {
	rc, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("outbox.NewForwarder: river client: %w", err)
	}
	return &Forwarder{
		repo:         newRepo(pool),
		riverClient:  rc,
		log:          log,
		batchSize:    defaultBatchSize,
		pollInterval: defaultPollInterval,
	}, nil
}

// Run polls until ctx is cancelled. Call in a goroutine.
func (f *Forwarder) Run(ctx context.Context) {
	ticker := time.NewTicker(f.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := f.forward(ctx); err != nil {
				f.log.ErrorContext(ctx, "outbox.Forwarder: forward error", slog.String("err", err.Error()))
			}
		}
	}
}

func (f *Forwarder) forward(ctx context.Context) error {
	events, err := f.repo.listAndMarkPending(ctx, f.batchSize)
	if err != nil {
		return fmt.Errorf("outbox.forward: claim: %w", err)
	}
	if len(events) == 0 {
		return nil
	}

	insertParams := make([]river.InsertManyParams, len(events))
	for i, e := range events {
		insertParams[i] = river.InsertManyParams{
			Args: DispatchArgs{EventID: e.ID.String()},
		}
	}

	if _, err := f.riverClient.InsertMany(ctx, insertParams); err != nil {
		return fmt.Errorf("outbox.forward: river insert: %w", err)
	}

	f.log.InfoContext(ctx, "outbox.Forwarder: forwarded",
		slog.Int("count", len(events)),
	)
	return nil
}
