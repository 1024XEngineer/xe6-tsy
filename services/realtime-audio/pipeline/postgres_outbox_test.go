package pipeline

import (
	"context"
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresFinalTurnOutboxRejectsInvalidAppend(t *testing.T) {
	event := validFinalTurnEvent()
	tests := []struct {
		name    string
		outbox  *PostgresFinalTurnOutbox
		topic   string
		key     string
		payload any
		wantErr error
	}{
		{name: "missing outbox", topic: recordsv1.FinalTurnTopic, key: event.EventID, payload: event, wantErr: ErrOutboxRequired},
		{name: "wrong topic", outbox: NewPostgresFinalTurnOutbox(new(pgxpool.Pool)), topic: "other", key: event.EventID, payload: event, wantErr: ErrPostgresFinalTurnOutboxPayload},
		{name: "wrong payload", outbox: NewPostgresFinalTurnOutbox(new(pgxpool.Pool)), topic: recordsv1.FinalTurnTopic, key: event.EventID, payload: struct{}{}, wantErr: ErrPostgresFinalTurnOutboxPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.outbox.Append(t.Context(), test.topic, test.key, test.payload)
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("Append() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && err == nil {
				t.Fatal("Append() error = nil, want invalid payload error")
			}
		})
	}
}

func TestPostgresFinalTurnOutboxHonorsCancelledContext(t *testing.T) {
	outbox := NewPostgresFinalTurnOutbox(new(pgxpool.Pool))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	event := validFinalTurnEvent()
	if err := outbox.Append(ctx, recordsv1.FinalTurnTopic, event.EventID, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append() error = %v, want context canceled", err)
	}
}
