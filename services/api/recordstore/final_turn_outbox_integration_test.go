//go:build integration

package recordstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestFinalTurnOutboxAppendsReceivesAndAcks(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)

	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
UPDATE final_turn_outbox
SET payload = jsonb_set(payload, '{translated_text}', '"changed"')
WHERE event_id = $1`, event.EventID); err == nil {
		t.Fatal("updating durable final-turn payload succeeded")
	}
	delivery, err := outbox.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if got := delivery.Event(); !reflect.DeepEqual(got, event) {
		t.Fatalf("Receive() event = %#v, want %#v", got, event)
	}
	if err := delivery.Ack(); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := outbox.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive() after Ack error = %v, want deadline exceeded", err)
	}
}

func TestFinalTurnOutboxReplaysIdenticalPayloadAndRejectsConflict(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("replay Append() error = %v", err)
	}
	event.TranslatedText = "different translation"
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); !errors.Is(err, ErrFinalTurnOutboxConflict) {
		t.Fatalf("conflicting Append() error = %v, want outbox conflict", err)
	}
}

func TestFinalTurnOutboxNackReleasesDeliveryForRetry(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	event := finalTurnEvent("event_01", "turn_01", "session_01", 1)
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	delivery, err := outbox.Receive(t.Context())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	if err := delivery.Nack(); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	retry, err := outbox.Receive(ctx)
	if err != nil {
		t.Fatalf("retry Receive() error = %v", err)
	}
	if retry.Event().EventID != event.EventID {
		t.Fatalf("retry event ID = %q, want %q", retry.Event().EventID, event.EventID)
	}
	if err := retry.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
}
