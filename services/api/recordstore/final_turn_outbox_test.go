package recordstore

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFinalTurnOutboxRejectsInvalidAppend(t *testing.T) {
	event := finalTurnEventForOutboxTest()
	tests := []struct {
		name    string
		outbox  *FinalTurnOutbox
		topic   string
		key     string
		payload any
		wantErr error
	}{
		{name: "missing outbox", topic: recordsv1.FinalTurnTopic, key: event.EventID, payload: event, wantErr: ErrFinalTurnOutboxRequired},
		{name: "wrong topic", outbox: NewFinalTurnOutbox(new(pgxpool.Pool)), topic: "other", key: event.EventID, payload: event, wantErr: ErrFinalTurnOutboxPayload},
		{name: "wrong payload", outbox: NewFinalTurnOutbox(new(pgxpool.Pool)), topic: recordsv1.FinalTurnTopic, key: event.EventID, payload: struct{}{}, wantErr: ErrFinalTurnOutboxPayload},
		{name: "mismatched key", outbox: NewFinalTurnOutbox(new(pgxpool.Pool)), topic: recordsv1.FinalTurnTopic, key: "event_02", payload: event, wantErr: ErrFinalTurnOutboxPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.outbox.Append(t.Context(), test.topic, test.key, test.payload)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Append() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestFinalTurnOutboxHonorsCancelledAppendContext(t *testing.T) {
	event := finalTurnEventForOutboxTest()
	outbox := NewFinalTurnOutbox(new(pgxpool.Pool))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := outbox.Append(ctx, recordsv1.FinalTurnTopic, event.EventID, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append() error = %v, want context canceled", err)
	}
}

func TestFinalTurnOutboxRejectsInvalidEventPayload(t *testing.T) {
	event := finalTurnEventForOutboxTest()
	event.SourceText = ""
	outbox := NewFinalTurnOutbox(new(pgxpool.Pool))

	err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event)
	if err == nil {
		t.Fatal("Append() invalid event payload succeeded")
	}
}

func finalTurnEventForOutboxTest() recordsv1.FinalTurnEvent {
	startedAt := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	return recordsv1.FinalTurnEvent{
		EventVersion:          recordsv1.FinalTurnEventVersion,
		EventID:               "event_01",
		TraceID:               "trace_01",
		TurnID:                "turn_01",
		SessionID:             "session_01",
		SequenceNo:            1,
		SourceLanguage:        "en-US",
		TargetLanguage:        "zh-CN",
		LanguageConfigVersion: 1,
		SourceText:            "Hello",
		TranslatedText:        "Ni hao",
		SpeakerCode:           recordsv1.PendingSpeakerCode,
		AttributionStatus:     recordsv1.AttributionPending,
		StartedAt:             startedAt,
		EndedAt:               startedAt.Add(time.Second),
		OccurredAt:            startedAt.Add(2 * time.Second),
	}
}
