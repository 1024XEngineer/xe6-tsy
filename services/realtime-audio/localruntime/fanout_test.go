package localruntime

import (
	"context"
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

type recordingFinalSink struct {
	events []recordsv1.FinalTurnEvent
	err    error
}

func (s *recordingFinalSink) Publish(_ context.Context, event recordsv1.FinalTurnEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func TestFanoutFinalTurnSinkPublishesLiveThenDurable(t *testing.T) {
	live := &recordingFinalSink{}
	durable := &recordingFinalSink{}
	sink := FanoutFinalTurnSink{Live: live, Durable: durable}
	event := recordsv1.FinalTurnEvent{EventID: "final_1", TurnID: "turn_1", SessionID: "vs_1"}
	if err := sink.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(live.events) != 1 || len(durable.events) != 1 {
		t.Fatalf("live=%d durable=%d", len(live.events), len(durable.events))
	}
}

func TestFanoutFinalTurnSinkReturnsDurableError(t *testing.T) {
	want := errors.New("outbox down")
	live := &recordingFinalSink{}
	durable := &recordingFinalSink{err: want}
	sink := FanoutFinalTurnSink{Live: live, Durable: durable}
	err := sink.Publish(context.Background(), recordsv1.FinalTurnEvent{EventID: "final_1"})
	if !errors.Is(err, want) {
		t.Fatalf("Publish() error = %v, want %v", err, want)
	}
	if len(live.events) != 1 {
		t.Fatalf("live events = %d, want 1 best-effort publish", len(live.events))
	}
}
