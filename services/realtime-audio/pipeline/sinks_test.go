package pipeline

import (
	"context"
	"errors"
	"reflect"
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
	if outbox.entries[0].topic != recordsv1.FinalTurnTopic || outbox.entries[0].key != final.EventID {
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

func TestOutboxFinalTurnSinkRequiresDurableOutbox(t *testing.T) {
	tests := []struct {
		name string
		sink *OutboxFinalTurnSink
	}{
		{name: "nil sink"},
		{name: "nil outbox", sink: NewOutboxFinalTurnSink(nil)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.sink.Publish(context.Background(), validFinalTurnEvent()); !errors.Is(err, ErrOutboxRequired) {
				t.Fatalf("Publish() error = %v, want ErrOutboxRequired", err)
			}
		})
	}
}

func TestOutboxFinalTurnSinkHonorsCanceledContextBeforeAppend(t *testing.T) {
	outbox := &recordingOutbox{}
	sink := NewOutboxFinalTurnSink(outbox)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sink.Publish(ctx, validFinalTurnEvent()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v, want context.Canceled", err)
	}
	if len(outbox.entries) != 0 {
		t.Fatalf("outbox entries = %d, want 0", len(outbox.entries))
	}
}

func TestOutboxFinalTurnSinkReplaysUnchangedPayload(t *testing.T) {
	outbox := &recordingOutbox{}
	sink := NewOutboxFinalTurnSink(outbox)
	event := validFinalTurnEvent()

	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("replay Publish() error = %v", err)
	}
	if len(outbox.entries) != 2 {
		t.Fatalf("outbox entries = %d, want 2 attempts", len(outbox.entries))
	}
	if !reflect.DeepEqual(outbox.entries[0], outbox.entries[1]) {
		t.Fatalf("replay entry = %#v, want %#v", outbox.entries[1], outbox.entries[0])
	}
}

func TestOutboxFinalTurnSinkPropagatesPayloadConflict(t *testing.T) {
	wantErr := errors.New("idempotency conflict")
	outbox := newIdempotentFinalTurnOutbox(wantErr)
	sink := NewOutboxFinalTurnSink(outbox)
	event := validFinalTurnEvent()

	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("identical replay Publish() error = %v", err)
	}
	event.TranslatedText = "different"
	if err := sink.Publish(context.Background(), event); !errors.Is(err, wantErr) {
		t.Fatalf("conflicting replay Publish() error = %v, want %v", err, wantErr)
	}
	if outbox.accepted != 1 {
		t.Fatalf("accepted entries = %d, want 1", outbox.accepted)
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

type idempotentFinalTurnOutbox struct {
	hashes      map[string]recordsv1.FinalTurnPayloadHash
	conflictErr error
	accepted    int
}

func newIdempotentFinalTurnOutbox(conflictErr error) *idempotentFinalTurnOutbox {
	return &idempotentFinalTurnOutbox{
		hashes:      make(map[string]recordsv1.FinalTurnPayloadHash),
		conflictErr: conflictErr,
	}
}

func (o *idempotentFinalTurnOutbox) Append(_ context.Context, topic, key string, payload any) error {
	event, ok := payload.(recordsv1.FinalTurnEvent)
	if !ok {
		return errors.New("unexpected outbox payload")
	}
	hash, err := recordsv1.FinalTurnEventPayloadHash(event)
	if err != nil {
		return err
	}
	entryKey := topic + "\x00" + key
	stored, exists := o.hashes[entryKey]
	if exists {
		if stored != hash {
			return o.conflictErr
		}
		return nil
	}
	o.hashes[entryKey] = hash
	o.accepted++
	return nil
}

var _ DurableOutbox = (*idempotentFinalTurnOutbox)(nil)

func validFinalTurnEvent() FinalTurnEvent {
	return FinalTurnEvent{
		EventVersion: recordsv1.FinalTurnEventVersion,
		EventID:      "event-1", TraceID: "trace-1", TurnID: "turn-1", SessionID: "session-1",
		SequenceNo: 1, SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		LanguageConfigVersion: 1, SourceText: "你好", TranslatedText: "hello",
		SpeakerCode:       recordsv1.PendingSpeakerCode,
		AttributionStatus: recordsv1.AttributionPending,
		StartedAt:         time.Unix(1700000000, 0).UTC(), EndedAt: time.Unix(1700000001, 0).UTC(),
		OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}
