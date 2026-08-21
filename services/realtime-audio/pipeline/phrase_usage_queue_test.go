package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLatePhraseUsageQueueRetriesTransientOutboxFailure(t *testing.T) {
	sink := &retryingLateUsageSink{failures: 1}
	queue := newLatePhraseUsageQueue(sink, LatencyLogger{})
	queue.retry = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(queue.Close)

	queue.Enqueue(UsageFact{ID: "usage-late", TurnID: "turn-1"})
	deadline := time.Now().Add(time.Second)
	for len(sink.Facts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if facts := sink.Facts(); len(facts) != 1 || facts[0].ID != "usage-late" || sink.Attempts() != 2 {
		t.Fatalf("facts = %#v, attempts = %d; want one fact after retry", facts, sink.Attempts())
	}
}

func TestLatePhraseUsageQueueBackpressuresInsteadOfDroppingWhenFull(t *testing.T) {
	sink := newBlockingLateUsageSink()
	queue := newLatePhraseUsageQueue(sink, LatencyLogger{})
	t.Cleanup(queue.Close)

	queue.Enqueue(UsageFact{ID: "usage-1", TurnID: "turn-1"})
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("first late usage fact did not reach the sink")
	}
	for index := 2; index <= latePhraseUsageQueueSize+1; index++ {
		queue.Enqueue(UsageFact{ID: fmt.Sprintf("usage-%d", index), TurnID: "turn-1"})
	}
	queued := make(chan struct{})
	go func() {
		queue.Enqueue(UsageFact{ID: "usage-overflow", TurnID: "turn-1"})
		close(queued)
	}()
	select {
	case <-queued:
		t.Fatal("late usage fact returned while the bounded queue was full")
	case <-time.After(20 * time.Millisecond):
	}

	close(sink.release)
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("late usage fact did not resume after the sink recovered")
	}
	deadline := time.Now().Add(time.Second)
	for sink.Count() != latePhraseUsageQueueSize+2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if count := sink.Count(); count != latePhraseUsageQueueSize+2 {
		t.Fatalf("published late usage facts = %d, want %d", count, latePhraseUsageQueueSize+2)
	}
}

type retryingLateUsageSink struct {
	mu       sync.Mutex
	failures int
	attempts int
	facts    []UsageFact
}

func (s *retryingLateUsageSink) Publish(_ context.Context, fact UsageFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.failures > 0 {
		s.failures--
		return errors.New("outbox temporarily unavailable")
	}
	s.facts = append(s.facts, fact)
	return nil
}

func (s *retryingLateUsageSink) Facts() []UsageFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UsageFact(nil), s.facts...)
}

func (s *retryingLateUsageSink) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type blockingLateUsageSink struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	facts   []UsageFact
}

func newBlockingLateUsageSink() *blockingLateUsageSink {
	return &blockingLateUsageSink{started: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingLateUsageSink) Publish(_ context.Context, fact UsageFact) error {
	select {
	case <-s.started:
	default:
		close(s.started)
		<-s.release
	}
	s.mu.Lock()
	s.facts = append(s.facts, fact)
	s.mu.Unlock()
	return nil
}

func (s *blockingLateUsageSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.facts)
}
