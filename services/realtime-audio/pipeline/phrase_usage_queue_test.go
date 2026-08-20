package pipeline

import (
	"context"
	"errors"
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
