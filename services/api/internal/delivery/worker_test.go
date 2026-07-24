package delivery

import (
	"context"
	"testing"
	"time"
)

type queueStub struct{}

func (queueStub) Enqueue(context.Context, string) error   { return nil }
func (queueStub) Receive(context.Context) (string, error) { return "", nil }

func TestWorkerStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewWorker(queueStub{})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}
