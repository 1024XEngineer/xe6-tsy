package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestOutboxSinksPublishTypedEvents(t *testing.T) {
	outbox := &recordingOutbox{}
	finalSink := NewOutboxFinalTurnSink(outbox)
	usageSink := NewOutboxUsageFactSink(outbox)
	final := validFinalTurnEvent()
	fact := validUsageFact()

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
	if got, ok := outbox.entries[0].payload.(recordsv1.FinalTurnEvent); !ok || got.EventID != final.EventID {
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
	if err := finalSink.Publish(context.Background(), validFinalTurnEvent()); !errors.Is(err, wantErr) {
		t.Fatalf("FinalTurn error = %v, want %v", err, wantErr)
	}
	if err := usageSink.Publish(context.Background(), validUsageFact()); !errors.Is(err, wantErr) {
		t.Fatalf("UsageFact error = %v, want %v", err, wantErr)
	}
}

func TestOutboxFinalTurnSinkRejectsInvalidEventBeforeAppend(t *testing.T) {
	outbox := &recordingOutbox{}
	sink := NewOutboxFinalTurnSink(outbox)
	event := validFinalTurnEvent()
	event.TargetLanguage = ""

	if err := sink.Publish(context.Background(), event); !errors.Is(err, recordsv1.ErrInvalidFinalTurnEvent) {
		t.Fatalf("Publish() error = %v, want ErrInvalidFinalTurnEvent", err)
	}
	if len(outbox.entries) != 0 {
		t.Fatalf("outbox entries = %d, want 0", len(outbox.entries))
	}
}

func TestOutboxUsageSinkRejectsInvalidFactBeforeAppend(t *testing.T) {
	outbox := &recordingOutbox{}
	sink := NewOutboxUsageFactSink(outbox)
	fact := validUsageFact()
	fact.Provider = ""

	if err := sink.Publish(context.Background(), fact); !errors.Is(err, ErrInvalidUsageFact) {
		t.Fatalf("Publish() error = %v, want ErrInvalidUsageFact", err)
	}
	if len(outbox.entries) != 0 {
		t.Fatalf("outbox entries = %d, want 0", len(outbox.entries))
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

func validFinalTurnEvent() FinalTurnEvent {
	return FinalTurnEvent{
		EventID: "event-1", TraceID: "trace-1", TurnID: "turn-1", SessionID: "session-1",
		SequenceNo: 1, SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		LanguageConfigVersion: 1, SourceText: "你好", TranslatedText: "hello",
		AttributionStatus: recordsv1.AttributionPending,
		StartedAt:         time.Unix(1700000000, 0).UTC(), EndedAt: time.Unix(1700000001, 0).UTC(),
		OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}
