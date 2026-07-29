//go:build integration

package recordstore

import (
	"context"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestFinalTurnWorkerPersistsAndAcknowledgesDurableEvent(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	services, err := NewServices(pool, []byte("cursor-signing-key"), sessionOwnerFake{}, sessionScopeFake{})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	outbox := NewFinalTurnOutbox(pool)
	event := finalTurnEvent("event_worker_01", "turn_worker_01", "session_worker_01", 1)
	event.ParticipantID = nil
	event.SpeakerCode = recordsv1.PendingSpeakerCode
	event.SpeakerLabelSnapshot = nil
	event.SpeakerConfidence = nil
	event.AttributionStatus = recordsv1.AttributionPending
	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := &cancelAfterAckSource{source: outbox, cancel: cancel}
	worker := turns.NewFinalTurnWorker(source, turns.NewFinalTurnHandler(services.Turns))
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var (
		count       int
		participant *string
		status      recordsv1.AttributionStatus
	)
	if err := pool.QueryRow(t.Context(), `
SELECT COUNT(*), MAX(participant_id), MAX(attribution_status)
FROM voice_turns
WHERE id = $1`, event.TurnID).Scan(&count, &participant, &status); err != nil {
		t.Fatalf("read consumed final turn: %v", err)
	}
	if count != 1 || participant != nil || status != recordsv1.AttributionPending {
		t.Fatalf("consumed final turn count=%d participant=%v status=%q", count, participant, status)
	}

	if err := outbox.Append(t.Context(), recordsv1.FinalTurnTopic, event.EventID, event); err != nil {
		t.Fatalf("replay Append() error = %v", err)
	}
	if _, found, err := outbox.receiveOnce(t.Context()); err != nil {
		t.Fatalf("receiveOnce() replay error = %v", err)
	} else if found {
		t.Fatal("receiveOnce() returned replay after Ack")
	}
}

type cancelAfterAckSource struct {
	source turns.FinalTurnDeliverySource
	cancel context.CancelFunc
}

func (s *cancelAfterAckSource) Receive(ctx context.Context) (turns.FinalTurnDelivery, error) {
	delivery, err := s.source.Receive(ctx)
	if err != nil {
		return nil, err
	}
	return &cancelAfterAckDelivery{FinalTurnDelivery: delivery, cancel: s.cancel}, nil
}

type cancelAfterAckDelivery struct {
	turns.FinalTurnDelivery
	cancel context.CancelFunc
}

func (d *cancelAfterAckDelivery) Ack() error {
	if err := d.FinalTurnDelivery.Ack(); err != nil {
		return err
	}
	d.cancel()
	return nil
}

var _ turns.FinalTurnDeliverySource = (*cancelAfterAckSource)(nil)
