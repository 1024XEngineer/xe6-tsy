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
