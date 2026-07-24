package pipeline

import (
	"context"
	"errors"
	"testing"
)

func TestOutboxSinksPublishTypedEvents(t *testing.T) {
	outbox := &recordingOutbox{}
	finalSink := NewOutboxFinalTurnSink(outbox)
	usageSink := NewOutboxUsageFactSink(outbox)
	final := FinalTurnEvent{EventID: "event-1", TurnID: "turn-1", SessionID: "session-1"}
	fact := UsageFact{ID: "fact-1", IdempotencyKey: "usage:turn-1:asr", TurnID: "turn-1"}

	if err := finalSink.Publish(context.Background(), final); err != nil {
		t.Fatalf("FinalTurn Publish() error = %v", err)
	}
	if err := usageSink.Publish(context.Background(), fact); err != nil {
		t.Fatalf("UsageFact Publish() error = %v", err)
	}
	if len(outbox.entries) != 2 {
		t.Fatalf("outbox entries = %d, want 2", len(outbox.entries))
	}
	if outbox.entries[0].topic != "final_turn.recorded" || outbox.entries[0].key != final.EventID {
		t.Fatalf("final entry = %#v", outbox.entries[0])
	}
	if got, ok := outbox.entries[0].payload.(FinalTurnEvent); !ok || got.EventID != final.EventID {
		t.Fatalf("final payload = %#v", outbox.entries[0].payload)
	}
	if outbox.entries[1].topic != "usage.recorded" || outbox.entries[1].key != fact.IdempotencyKey {
		t.Fatalf("usage entry = %#v", outbox.entries[1])
	}
}

func TestOutboxSinksPropagateAcceptanceErrors(t *testing.T) {
	wantErr := errors.New("outbox unavailable")
	outbox := &recordingOutbox{err: wantErr}
	finalSink := NewOutboxFinalTurnSink(outbox)
	usageSink := NewOutboxUsageFactSink(outbox)
	if err := finalSink.Publish(context.Background(), FinalTurnEvent{EventID: "event-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("FinalTurn error = %v, want %v", err, wantErr)
	}
	if err := usageSink.Publish(context.Background(), UsageFact{ID: "fact-1", IdempotencyKey: "key-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("UsageFact error = %v, want %v", err, wantErr)
	}
}

type outboxEntry struct {
	topic   string
	key     string
	payload any
}

type recordingOutbox struct {
	entries []outboxEntry
	err     error
}

func (r *recordingOutbox) Append(_ context.Context, topic, key string, payload any) error {
	if r.err != nil {
		return r.err
	}
	r.entries = append(r.entries, outboxEntry{topic: topic, key: key, payload: payload})
	return nil
}

var _ DurableOutbox = (*recordingOutbox)(nil)
