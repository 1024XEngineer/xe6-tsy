package delivery

import (
	"context"
	"errors"
	"sync"
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

type retryingOutboxRepositoryStub struct {
	mu           sync.Mutex
	calls        int
	secondCall   chan struct{}
	transientErr error
}

func (r *retryingOutboxRepositoryStub) ClaimOutbox(context.Context, int) ([]OutboxRecord, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	if call == 2 {
		close(r.secondCall)
	}
	r.mu.Unlock()
	if call == 1 {
		return nil, r.transientErr
	}
	return nil, nil
}

func (r *retryingOutboxRepositoryStub) MarkOutboxPublished(context.Context, string) error {
	return nil
}

func (r *retryingOutboxRepositoryStub) MarkOutboxFailed(context.Context, string, string) error {
	return nil
}

func TestRunRetriesAfterTransientDispatchFailure(t *testing.T) {
	repository := &retryingOutboxRepositoryStub{
		secondCall:   make(chan struct{}),
		transientErr: errors.New("database unavailable"),
	}
	dispatcher := NewOutboxDispatcher(repository, outboxQueueStub{}, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()

	select {
	case <-repository.secondCall:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("dispatcher did not retry after transient failure")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil on cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop after cancellation")
	}
}
