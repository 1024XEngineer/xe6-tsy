package modeprojection

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type consumerStreamStub struct {
	mu       sync.Mutex
	messages []StreamMessage
	acked    []string
	nacked   []string
	ackErr   error
	nackErr  error
}

func (s *consumerStreamStub) Receive(ctx context.Context) (StreamMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return StreamMessage{}, ctx.Err()
	}
	message := s.messages[0]
	s.messages = s.messages[1:]
	return message, nil
}

func (s *consumerStreamStub) Ack(_ context.Context, receipt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked = append(s.acked, receipt)
	return s.ackErr
}

func (s *consumerStreamStub) Nack(_ context.Context, receipt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nacked = append(s.nacked, receipt)
	return s.nackErr
}

type projectorStub struct {
	err    error
	events []realtimev1.ModeChangedEvent
}

type receiveErrorStream struct {
	mu     sync.Mutex
	calls  int
	called chan struct{}
	err    error
}

type canceledReceiveStream struct{}

func (canceledReceiveStream) Receive(context.Context) (StreamMessage, error) {
	return StreamMessage{}, context.Canceled
}

func (canceledReceiveStream) Ack(context.Context, string) error  { return nil }
func (canceledReceiveStream) Nack(context.Context, string) error { return nil }

func (s *receiveErrorStream) Receive(context.Context) (StreamMessage, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	select {
	case s.called <- struct{}{}:
	default:
	}
	return StreamMessage{}, s.err
}

func (*receiveErrorStream) Ack(context.Context, string) error  { return nil }
func (*receiveErrorStream) Nack(context.Context, string) error { return nil }

func (s *receiveErrorStream) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *projectorStub) Project(_ context.Context, event realtimev1.ModeChangedEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func TestConsumerProjectsValidEventAndAcknowledgesReplay(t *testing.T) {
	payload := marshalConsumerEvent(t, consumerEvent())
	stream := &consumerStreamStub{messages: []StreamMessage{
		{Payload: payload, Receipt: "receipt-1"},
		{Payload: payload, Receipt: "receipt-2"},
	}}
	projector := &projectorStub{}
	consumer := NewConsumer(stream, projector)

	for range 2 {
		processed, err := consumer.ProcessOnce(t.Context())
		if err != nil || !processed {
			t.Fatalf("ProcessOnce() = (%v, %v), want (true, nil)", processed, err)
		}
	}
	if len(projector.events) != 2 {
		t.Fatalf("projected events = %d, want replay passed to idempotent repository", len(projector.events))
	}
	if len(stream.acked) != 2 || len(stream.nacked) != 0 {
		t.Fatalf("acked=%v nacked=%v", stream.acked, stream.nacked)
	}
}

func TestConsumerAcknowledgesInvalidPayloadWithoutProjecting(t *testing.T) {
	valid := string(marshalConsumerEvent(t, consumerEvent()))
	tests := []struct {
		name    string
		payload string
	}{
		{name: "malformed", payload: "{"},
		{name: "unknown field", payload: valid[:len(valid)-1] + `,"unexpected":true}`},
		{name: "trailing value", payload: valid + `{}`},
		{name: "invalid contract", payload: `{"event_version":1}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := &consumerStreamStub{messages: []StreamMessage{{Payload: []byte(test.payload), Receipt: "receipt"}}}
			projector := &projectorStub{}

			processed, err := NewConsumer(stream, projector).ProcessOnce(t.Context())

			if err != nil || !processed {
				t.Fatalf("ProcessOnce() = (%v, %v), want (true, nil)", processed, err)
			}
			if len(projector.events) != 0 || len(stream.acked) != 1 || len(stream.nacked) != 0 {
				t.Fatalf("events=%d acked=%v nacked=%v", len(projector.events), stream.acked, stream.nacked)
			}
		})
	}
}

func TestConsumerSettlesProjectionFailuresByPermanence(t *testing.T) {
	transientErr := errors.New("postgres unavailable")
	tests := []struct {
		name       string
		projectErr error
		wantErr    bool
		wantAck    int
		wantNack   int
	}{
		{name: "invalid", projectErr: domain.ErrInvalidArgument, wantAck: 1},
		{name: "event id conflict", projectErr: domain.ErrConflict, wantAck: 1},
		{name: "session missing", projectErr: domain.ErrNotFound, wantAck: 1},
		{name: "transient storage failure", projectErr: transientErr, wantErr: true, wantNack: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := &consumerStreamStub{messages: []StreamMessage{{Payload: marshalConsumerEvent(t, consumerEvent()), Receipt: "receipt"}}}
			processed, err := NewConsumer(stream, &projectorStub{err: test.projectErr}).ProcessOnce(t.Context())
			if processed != true || (err != nil) != test.wantErr {
				t.Fatalf("ProcessOnce() = (%v, %v), want error=%v", processed, err, test.wantErr)
			}
			if len(stream.acked) != test.wantAck || len(stream.nacked) != test.wantNack {
				t.Fatalf("acked=%v nacked=%v", stream.acked, stream.nacked)
			}
		})
	}
}

func TestConsumerReturnsSettlementFailures(t *testing.T) {
	ackErr := errors.New("ack unavailable")
	nackErr := errors.New("nack unavailable")
	transientErr := errors.New("postgres unavailable")
	tests := []struct {
		name       string
		payload    []byte
		projectErr error
		ackErr     error
		nackErr    error
		want       []error
	}{
		{name: "invalid payload ack", payload: []byte("{"), ackErr: ackErr, want: []error{domain.ErrInvalidArgument, ackErr}},
		{name: "permanent error ack", payload: marshalConsumerEvent(t, consumerEvent()), projectErr: domain.ErrConflict, ackErr: ackErr, want: []error{domain.ErrConflict, ackErr}},
		{name: "transient error nack", payload: marshalConsumerEvent(t, consumerEvent()), projectErr: transientErr, nackErr: nackErr, want: []error{transientErr, nackErr}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := &consumerStreamStub{
				messages: []StreamMessage{{Payload: test.payload, Receipt: "receipt"}},
				ackErr:   test.ackErr, nackErr: test.nackErr,
			}
			_, err := NewConsumer(stream, &projectorStub{err: test.projectErr}).ProcessOnce(t.Context())
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Fatalf("ProcessOnce() error = %v, want errors.Is(%v)", err, want)
				}
			}
		})
	}
}

func TestConsumerRunBacksOffAfterStreamFailure(t *testing.T) {
	stream := &receiveErrorStream{
		called: make(chan struct{}, 1),
		err:    errors.New("valkey unavailable"),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- NewConsumer(stream, &projectorStub{}).Run(ctx)
	}()

	<-stream.called
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want nil after cancellation", err)
	}
	if calls := stream.callCount(); calls != 1 {
		t.Fatalf("Receive() calls = %d, want one call before retry backoff was canceled", calls)
	}
}

func TestConsumerRunStopsWhenReceiveIsCanceled(t *testing.T) {
	consumer := NewConsumer(canceledReceiveStream{}, &projectorStub{})

	if err := consumer.Run(t.Context()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func marshalConsumerEvent(t *testing.T, event realtimev1.ModeChangedEvent) []byte {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}

func consumerEvent() realtimev1.ModeChangedEvent {
	return realtimev1.ModeChangedEvent{
		EventVersion: realtimev1.ModeChangedEventVersion, EventID: "event-1", TraceID: "trace-1",
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", OperationID: "operation-1",
		FromMode: realtimev1.ModeAssistant, ToMode: realtimev1.ModeInterpretation,
		ResultingGeneration: 2, OccurredAt: time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC),
	}
}
