package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// OutboxDispatcher publishes committed database outbox rows to Valkey. A
// duplicate publish is safe because message creation and usage consumers are
// idempotent; marking the row happens only after the broker accepts it.
type OutboxDispatcher struct {
	repository OutboxRepository
	queue      Queue
	interval   time.Duration
}

func NewOutboxDispatcher(repository OutboxRepository, queue Queue, interval time.Duration) *OutboxDispatcher {
	if interval <= 0 {
		interval = time.Second
	}
	return &OutboxDispatcher{repository: repository, queue: queue, interval: interval}
}

func (d *OutboxDispatcher) Run(ctx context.Context) error {
	if d == nil || d.repository == nil || d.queue == nil {
		<-ctx.Done()
		return nil
	}
	d.dispatch(ctx)
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			d.dispatch(ctx)
		}
	}
}

// dispatch deliberately keeps the dispatcher alive after a transient storage
// or broker error. The next tick retries the same durable outbox rows; returning
// here would strand committed rows until the process is restarted.
func (d *OutboxDispatcher) dispatch(ctx context.Context) {
	if err := d.DispatchOnce(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		slog.Warn("delivery outbox dispatch failed; will retry", "error", err)
	}
}

func (d *OutboxDispatcher) DispatchOnce(ctx context.Context) error {
	records, err := d.repository.ClaimOutbox(ctx, 50)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := d.queue.Enqueue(ctx, QueueItem{AccountID: record.AccountID, AttemptID: record.AttemptID, IdempotencyKey: record.Key}); err != nil {
			if markErr := d.repository.MarkOutboxFailed(ctx, record.ID, err.Error()); markErr != nil {
				return markErr
			}
			continue
		}
		if err := d.repository.MarkOutboxPublished(ctx, record.ID); err != nil {
			return err
		}
	}
	return nil
}
