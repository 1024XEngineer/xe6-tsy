package outbox

import (
	"context"
	"errors"
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
