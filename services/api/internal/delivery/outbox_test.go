package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

type outboxRepositoryStub struct {
	records   []OutboxRecord
	markError error
	marked    bool
}

func (r *outboxRepositoryStub) ClaimOutbox(context.Context, int) ([]OutboxRecord, error) {
	return r.records, nil
}

func (r *outboxRepositoryStub) MarkOutboxPublished(context.Context, string) error {
	r.marked = true
	return nil
}

func (r *outboxRepositoryStub) MarkOutboxFailed(context.Context, string, string) error {
	return r.markError
}

type outboxQueueStub struct{ enqueueError error }

func (q outboxQueueStub) Enqueue(context.Context, string, string) error { return q.enqueueError }
func (outboxQueueStub) Receive(context.Context) (QueueMessage, error)   { return QueueMessage{}, nil }
func (outboxQueueStub) Ack(context.Context, string) error               { return nil }
func (outboxQueueStub) Nack(context.Context, string, time.Time) error   { return nil }

func TestDispatchOnceReturnsOutboxFailure(t *testing.T) {
	markError := errors.New("database unavailable")
	repository := &outboxRepositoryStub{
		records:   []OutboxRecord{{ID: "outbox-1", AttemptID: "attempt-1", Key: "key-1"}},
		markError: markError,
	}
	dispatcher := NewOutboxDispatcher(repository, outboxQueueStub{enqueueError: errors.New("queue unavailable")}, time.Second)

	if err := dispatcher.DispatchOnce(context.Background()); !errors.Is(err, markError) {
		t.Fatalf("DispatchOnce() error = %v, want %v", err, markError)
	}
}

func TestDispatchOnceMarksPublishedAfterQueueAccepts(t *testing.T) {
	repository := &outboxRepositoryStub{
		records: []OutboxRecord{{ID: "outbox-1", AttemptID: "attempt-1", Key: "key-1"}},
	}
	dispatcher := NewOutboxDispatcher(repository, outboxQueueStub{}, time.Second)

	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("DispatchOnce() error = %v", err)
	}
	if !repository.marked {
		t.Fatal("DispatchOnce() did not mark the accepted outbox record")
	}
}

func TestOutboxDispatcherRunContinuesAfterTransientError(t *testing.T) {
	repository := &outboxRepositoryStub{
		records:   []OutboxRecord{{ID: "outbox-1", AttemptID: "attempt-1", Key: "key-1"}},
		markError: errors.New("database unavailable"),
	}
	dispatcher := NewOutboxDispatcher(repository, outboxQueueStub{enqueueError: errors.New("queue unavailable")}, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("Run() returned early with %v", err)
	case <-time.After(20 * time.Millisecond):
		cancel()
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}
