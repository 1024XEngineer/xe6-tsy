package delivery

import "context"

// Worker owns the cancellable lifecycle of asynchronous delivery consumption.
type Worker struct {
	queue Queue
}

// NewWorker binds the queue boundary without starting background work.
func NewWorker(queue Queue) *Worker { return &Worker{queue: queue} }

// Run owns no goroutines; cancellation cleanly ends the worker lifecycle.
// Message consumption is intentionally deferred until retry semantics are implemented.
func (w *Worker) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
