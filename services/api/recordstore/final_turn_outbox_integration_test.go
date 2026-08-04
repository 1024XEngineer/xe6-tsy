//go:build integration

package recordstore

import (
	"errors"
	"reflect"
	"testing"

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

	if _, found, err := outbox.receiveOnce(t.Context()); err != nil {
		t.Fatalf("receiveOnce() after Ack error = %v", err)
	} else if found {
		t.Fatal("receiveOnce() returned acknowledged event")
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

func TestFinalTurnOutboxReplaysLegacyHashWithRouteFields(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	legacyEvent := finalTurnEvent("event_legacy", "turn_legacy", "session_legacy", 1)
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, legacyEvent.EventID, legacyEvent); err != nil {
		t.Fatalf("append legacy FinalTurn() error = %v", err)
	}

	currentEvent := legacyEvent
	currentEvent.TTSEnabled = true
	currentEvent.DeliveryEnabled = true
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, currentEvent.EventID, currentEvent); err != nil {
		t.Fatalf("replay current FinalTurn() error = %v", err)
	}
}

func TestFinalTurnOutboxRoutesInvalidPayloadToReject(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
INSERT INTO final_turn_outbox (event_id, turn_id, session_id, sequence_no, payload_hash, payload)
VALUES ('event_invalid', 'turn_invalid', 'session_invalid', 1, decode(repeat('00', 32), 'hex'), '{"event_version":2}'::jsonb)`); err != nil {
		t.Fatalf("insert invalid final-turn payload: %v", err)
	}

	delivery, err := NewFinalTurnOutbox(pool).Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() invalid payload error = %v", err)
	}
	if delivery.Event().EventVersion != 2 {
		t.Fatalf("invalid payload event version = %d, want 2", delivery.Event().EventVersion)
	}
	if err := delivery.Reject(); err != nil {
		t.Fatalf("Reject() invalid payload error = %v", err)
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

	if _, found, err := outbox.receiveOnce(t.Context()); err != nil {
		t.Fatalf("receiveOnce() before retry error = %v", err)
	} else if found {
		t.Fatal("receiveOnce() returned nacked event before retry time")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE final_turn_outbox SET available_at = CURRENT_TIMESTAMP WHERE event_id = $1`, event.EventID); err != nil {
		t.Fatalf("make retry available: %v", err)
	}
	retry, found, err := outbox.receiveOnce(t.Context())
	if err != nil {
		t.Fatalf("retry receiveOnce() error = %v", err)
	}
	if !found {
		t.Fatal("retry receiveOnce() found no event")
	}
	if retry.Event().EventID != event.EventID {
		t.Fatalf("retry event ID = %q, want %q", retry.Event().EventID, event.EventID)
	}
	if err := retry.Reject(); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
}

func TestFinalTurnOutboxAcceptsStaleReceiptAfterAnotherWorkerSettles(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	event := finalTurnEvent("event_stale_receipt", "turn_stale_receipt", "session_stale_receipt", 1)
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	first, err := outbox.Receive(t.Context())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE final_turn_outbox SET locked_until = CURRENT_TIMESTAMP WHERE event_id = $1`, event.EventID); err != nil {
		t.Fatalf("expire first lease: %v", err)
	}
	second, err := outbox.Receive(t.Context())
	if err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if err := second.Ack(); err != nil {
		t.Fatalf("second Ack() error = %v", err)
	}
	if err := first.Ack(); err != nil {
		t.Fatalf("stale first Ack() error = %v", err)
	}
}
