package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	latePhraseUsageQueueSize      = 64
	latePhraseUsagePublishTimeout = 2 * time.Second
	latePhraseUsageInitialBackoff = 100 * time.Millisecond
	latePhraseUsageMaximumBackoff = 5 * time.Second
)

// latePhraseUsageQueue is the PipelineService-owned recovery path for usage
// facts returned by a provider after Finalize has already completed.
type latePhraseUsageQueue struct {
	usage   UsageFactSink
	latency LatencyLogger
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan UsageFact
	done    chan struct{}
	retry   func(int) time.Duration
	mu      sync.Mutex
	started bool
	closed  bool
}

func newLatePhraseUsageQueue(usage UsageFactSink, latency LatencyLogger) *latePhraseUsageQueue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &latePhraseUsageQueue{
		usage: usage, latency: latency, ctx: ctx, cancel: cancel,
		queue: make(chan UsageFact, latePhraseUsageQueueSize), done: make(chan struct{}),
		retry: latePhraseUsageRetryDelay,
	}
	return q
}

// Enqueue applies backpressure instead of dropping a durable usage fact. Its
// callers report late usage from detached goroutines, so VAD finalization and
// provider completion remain independent of outbox recovery.
func (q *latePhraseUsageQueue) Enqueue(fact UsageFact) {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.report(fact, context.Canceled)
		return
	}
	if !q.started {
		q.started = true
		go q.run()
	}
	q.mu.Unlock()
	select {
	case q.queue <- fact:
	case <-q.ctx.Done():
		q.report(fact, q.ctx.Err())
	}
}

func (q *latePhraseUsageQueue) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	started := q.started
	q.mu.Unlock()
	q.cancel()
	if !started {
		close(q.done)
		return
	}
	<-q.done
}

func (q *latePhraseUsageQueue) run() {
	defer close(q.done)
	for {
		select {
		case <-q.ctx.Done():
			return
		case fact := <-q.queue:
			q.publish(fact)
		}
	}
}

func (q *latePhraseUsageQueue) publish(fact UsageFact) {
	for attempt := 0; ; attempt++ {
		publishCtx, cancel := context.WithTimeout(q.ctx, latePhraseUsagePublishTimeout)
		err := q.usage.Publish(publishCtx, fact)
		cancel()
		if err == nil {
			return
		}
		if q.ctx.Err() != nil || permanentLatePhraseUsageError(err) {
			q.report(fact, err)
			return
		}
		wait := time.NewTimer(q.retry(attempt))
		select {
		case <-q.ctx.Done():
			wait.Stop()
			q.report(fact, q.ctx.Err())
			return
		case <-wait.C:
		}
	}
}

func (q *latePhraseUsageQueue) report(fact UsageFact, err error) {
	q.latency.ProviderFailure("phrase_usage", TurnContext{ID: fact.TurnID, SessionID: fact.SessionID, TraceID: fact.TraceID}, fact.Provider, fact.Model, err)
}

func permanentLatePhraseUsageError(err error) bool {
	return errors.Is(err, ErrOutboxRequired) || errors.Is(err, ErrUsageIdentityRequired)
}

func latePhraseUsageRetryDelay(attempt int) time.Duration {
	delay := latePhraseUsageInitialBackoff
	for attempt > 0 && delay < latePhraseUsageMaximumBackoff {
		delay *= 2
		attempt--
	}
	if delay > latePhraseUsageMaximumBackoff {
		return latePhraseUsageMaximumBackoff
	}
	return delay
}
