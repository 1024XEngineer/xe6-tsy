package outbox

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

func TestAdapterCanonicalizesFrozenEvents(t *testing.T) {
	writer := &recordingWriter{ack: Ack{Accepted: true}}
	adapter := NewAdapter(writer)
	final := validFinalTurn()
	fact := validUsageFact()
	mode := validModeChangedEvent()

	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, final.EventID, final); err != nil {
		t.Fatalf("FinalTurn Append() error = %v", err)
	}
	if err := adapter.Append(context.Background(), "usage.recorded", fact.IdempotencyKey, fact); err != nil {
		t.Fatalf("UsageFact Append() error = %v", err)
	}
	if err := adapter.Append(context.Background(), realtimev1.ModeChangedTopic, mode.EventID, mode); err != nil {
		t.Fatalf("ModeChangedEvent Append() error = %v", err)
	}
	if len(writer.entries) != 3 {
		t.Fatalf("writer entries = %d, want 3", len(writer.entries))
	}
	if writer.entries[0].Topic != recordsv1.FinalTurnTopic || writer.entries[0].IdempotencyKey != final.EventID {
		t.Fatalf("FinalTurn entry = %#v", writer.entries[0])
	}
	if writer.entries[1].Topic != "usage.recorded" || writer.entries[1].IdempotencyKey != fact.IdempotencyKey {
		t.Fatalf("UsageFact entry = %#v", writer.entries[1])
	}
	if writer.entries[2].Topic != realtimev1.ModeChangedTopic || writer.entries[2].IdempotencyKey != mode.EventID {
		t.Fatalf("ModeChangedEvent entry = %#v", writer.entries[2])
	}
	if len(writer.entries[0].Payload) == 0 || writer.entries[0].PayloadHash == [32]byte{} {
		t.Fatal("FinalTurn payload was not canonicalized")
	}
}

func TestAdapterRequiresDurableAcceptance(t *testing.T) {
	writer := &recordingWriter{ack: Ack{Accepted: false}}
	adapter := NewAdapter(writer)

	err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, "event-1", validFinalTurn())
	if !errors.Is(err, ErrNotAccepted) {
		t.Fatalf("Append() error = %v, want ErrNotAccepted", err)
	}
}

func TestAdapterPropagatesWriterFailureAndAllowsRetry(t *testing.T) {
	writer := &recordingWriter{err: errTemporary}
	adapter := NewAdapter(writer)
	event := validFinalTurn()

	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); !errors.Is(err, errTemporary) {
		t.Fatalf("first Append() error = %v, want %v", err, errTemporary)
	}
	writer.err = nil
	writer.ack = Ack{Accepted: true}
	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("retry Append() error = %v", err)
	}
	if len(writer.entries) != 1 {
		t.Fatalf("accepted entries = %d, want 1", len(writer.entries))
	}
}

func TestAdapterRejectsFrozenKeyMismatchAndPayloadConflict(t *testing.T) {
	writer := &recordingWriter{ack: Ack{Accepted: true}}
	adapter := NewAdapter(writer)
	event := validFinalTurn()

	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, "other-key", event); !errors.Is(err, ErrIdempotencyKeyMismatch) {
		t.Fatalf("key mismatch error = %v, want ErrIdempotencyKeyMismatch", err)
	}
	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	conflict := event
	conflict.TranslatedText = "changed"
	writer.err = errConflict
	if err := adapter.Append(context.Background(), recordsv1.FinalTurnTopic, event.EventID, conflict); !errors.Is(err, errConflict) {
		t.Fatalf("conflicting payload error = %v, want writer conflict", err)
	}
}

func TestAdapterHonorsCanceledContextBeforeWriter(t *testing.T) {
	writer := &recordingWriter{ack: Ack{Accepted: true}}
	adapter := NewAdapter(writer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := adapter.Append(ctx, recordsv1.FinalTurnTopic, "event-1", validFinalTurn()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append() error = %v, want context.Canceled", err)
	}
	if len(writer.entries) != 0 {
		t.Fatalf("writer entries = %d, want 0", len(writer.entries))
	}
}

type recordingWriter struct {
	entries []Entry
	ack     Ack
	err     error
}

func (w *recordingWriter) Accept(_ context.Context, entry Entry) (Ack, error) {
	if w.err != nil {
		return Ack{}, w.err
	}
	w.entries = append(w.entries, cloneEntry(entry))
	return w.ack, nil
}

var _ Writer = (*recordingWriter)(nil)

var (
	errTemporary = errors.New("temporary outbox failure")
	errConflict  = errors.New("payload conflict")
)

func validFinalTurn() pipeline.FinalTurnEvent {
	return pipeline.FinalTurnEvent{
		EventVersion: recordsv1.FinalTurnEventVersion,
		EventID:      "event-1", TraceID: "trace-1", TurnID: "turn-1", SessionID: "session-1",
		SequenceNo: 1, SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		LanguageConfigVersion: 1, SourceText: "你好", TranslatedText: "hello",
		SpeakerCode: recordsv1.PendingSpeakerCode, AttributionStatus: recordsv1.AttributionPending,
		StartedAt: time.Unix(1700000000, 0).UTC(), EndedAt: time.Unix(1700000001, 0).UTC(),
		OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}

func validUsageFact() pipeline.UsageFact {
	return pipeline.UsageFact{
		EventVersion: pipeline.UsageEventVersion, ID: "usage-turn-translation", TraceID: "trace-1",
		IdempotencyKey: "usage:turn-1:translation", AccountID: "account-1", SessionID: "session-1",
		TurnID: "turn-1", ServiceType: "translation", Provider: "fake", Model: "fake-model",
		OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}

func cloneTestEntry(entry Entry) Entry {
	entry.Payload = append([]byte(nil), entry.Payload...)
	return entry
}

func TestEntryCopiesPayload(t *testing.T) {
	entry := Entry{Payload: []byte("payload")}
	clone := cloneTestEntry(entry)
	clone.Payload[0] = 'P'
	if reflect.DeepEqual(entry.Payload, clone.Payload) {
		t.Fatal("cloneEntry shares payload backing array")
	}
}
