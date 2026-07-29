package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestMemoryOutboxIsIdempotentAndDetectsConflicts(t *testing.T) {
	fake := NewMemoryOutbox()
	event := validFinalTurn()

	if err := fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if err := fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("replay Append() error = %v", err)
	}
	conflict := event
	conflict.TranslatedText = "different"
	if err := fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Append() error = %v, want ErrConflict", err)
	}
	if got := len(fake.Entries()); got != 1 {
		t.Fatalf("stored entries = %d, want 1", got)
	}
}

func TestMemoryOutboxRecoversAfterInjectedFailure(t *testing.T) {
	fake := NewMemoryOutbox()
	fake.FailNext(errTemporary)
	event := validFinalTurn()

	if err := fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); !errors.Is(err, errTemporary) {
		t.Fatalf("first Append() error = %v, want %v", err, errTemporary)
	}
	if err := fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("retry Append() error = %v", err)
	}
	if got := len(fake.Entries()); got != 1 {
		t.Fatalf("stored entries = %d, want 1", got)
	}
}

func TestMemoryOutboxConcurrentReplayStoresOneEntry(t *testing.T) {
	fake := NewMemoryOutbox()
	event := validFinalTurn()
	const workers = 20
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wait.Done()
			errorsCh <- fake.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event)
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent Append() error = %v", err)
		}
	}
	if got := len(fake.Entries()); got != 1 {
		t.Fatalf("stored entries = %d, want 1", got)
	}
}
