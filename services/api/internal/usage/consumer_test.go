package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type consumerStreamStub struct {
	mu       sync.Mutex
	messages []StreamMessage
	acked    []string
	nacked   []string
}

func (s *consumerStreamStub) Enqueue(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt := "receipt-" + string(rune('a'+len(s.messages)))
	s.messages = append(s.messages, StreamMessage{Payload: payload, Receipt: receipt})
}

func (s *consumerStreamStub) Receive(ctx context.Context) (StreamMessage, error) {
	for {
		s.mu.Lock()
		if len(s.messages) > 0 {
			message := s.messages[0]
			s.messages = s.messages[1:]
			s.mu.Unlock()
			return message, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return StreamMessage{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *consumerStreamStub) Ack(_ context.Context, receipt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acked = append(s.acked, receipt)
	return nil
}

func (s *consumerStreamStub) Nack(_ context.Context, receipt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nacked = append(s.nacked, receipt)
	return nil
}

type consumerServiceStub struct {
	err     error
	records []RecordInput
}

func (s *consumerServiceStub) Record(_ context.Context, input RecordInput) (Detail, error) {
	if s.err != nil {
		return Detail{}, s.err
	}
	s.records = append(s.records, input)
	return Detail{RecordInput: input, RecordedAt: time.Now().UTC()}, nil
}

func (s *consumerServiceStub) SessionUsage(context.Context, string, string) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}

func (s *consumerServiceStub) AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}

func TestConsumerProcessOnceRecordsValidEvent(t *testing.T) {
	stream := &consumerStreamStub{}
	service := &consumerServiceStub{}
	payload, err := MarshalRecordInput(validRecordInput())
	if err != nil {
		t.Fatalf("MarshalRecordInput() error = %v", err)
	}
	stream.Enqueue(payload)

	consumer := NewConsumer(stream, service)
	processed, err := consumer.ProcessOnce(t.Context())
	if err != nil || !processed {
		t.Fatalf("ProcessOnce() = (%v, %v)", processed, err)
	}
	if len(service.records) != 1 || len(stream.acked) != 1 {
		t.Fatalf("records=%d acked=%#v", len(service.records), stream.acked)
	}
}

func TestConsumerProcessOnceAcksInvalidPayload(t *testing.T) {
	stream := &consumerStreamStub{}
	stream.Enqueue([]byte(`{"event_version":2}`))
	consumer := NewConsumer(stream, &consumerServiceStub{})

	if _, err := consumer.ProcessOnce(t.Context()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if len(stream.acked) != 1 || len(stream.nacked) != 0 {
		t.Fatalf("acked=%#v nacked=%#v", stream.acked, stream.nacked)
	}
}

func TestConsumerProcessOnceNacksTransientFailure(t *testing.T) {
	stream := &consumerStreamStub{}
	payload, _ := MarshalRecordInput(validRecordInput())
	stream.Enqueue(payload)
	service := &consumerServiceStub{err: errors.New("postgres unavailable")}
	consumer := NewConsumer(stream, service)

	if _, err := consumer.ProcessOnce(t.Context()); err == nil {
		t.Fatal("ProcessOnce() error = nil, want transient failure")
	}
	if len(stream.nacked) != 1 || len(stream.acked) != 0 {
		t.Fatalf("nacked=%#v acked=%#v", stream.nacked, stream.acked)
	}
}

func TestConsumerProcessOnceAcksForbiddenEvent(t *testing.T) {
	stream := &consumerStreamStub{}
	payload, _ := MarshalRecordInput(validRecordInput())
	stream.Enqueue(payload)
	service := &consumerServiceStub{err: domain.ErrForbidden}
	consumer := NewConsumer(stream, service)

	if _, err := consumer.ProcessOnce(t.Context()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if len(stream.acked) != 1 {
		t.Fatalf("acked=%#v, want permanent reject acked", stream.acked)
	}
}

func TestParseRecordInputRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseRecordInput([]byte("{")); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("ParseRecordInput() error = %v, want invalid argument", err)
	}
}

func TestConsumerRunReturnsNilWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name     string
		consumer *Consumer
	}{
		{name: "nil_consumer"},
		{name: "nil_stream", consumer: &Consumer{service: &runServiceStub{}}},
		{name: "nil_service", consumer: &Consumer{stream: newRunStreamStub(), service: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var consumer *Consumer
			if tt.consumer != nil {
				consumer = tt.consumer
			}
			if err := consumer.Run(ctx); err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
		})
	}
}

func TestConsumerRunIgnoresContextErrors(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(err.Error(), func(t *testing.T) {
			consumer := NewConsumer(&runErrorStreamStub{err: err}, &runServiceStub{})
			if got := consumer.Run(t.Context()); got != nil {
				t.Fatalf("Run() error = %v, want nil", got)
			}
		})
	}
}

func TestConsumerRunRetriesTransientFailureThenContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	payload1, err := MarshalRecordInput(validRecordInput())
	if err != nil {
		t.Fatalf("MarshalRecordInput() error = %v", err)
	}
	payload2, err := MarshalRecordInput(validRecordInput())
	if err != nil {
		t.Fatalf("MarshalRecordInput() error = %v", err)
	}

	stream := newRunStreamStub(
		StreamMessage{Payload: payload1, Receipt: "receipt-1"},
		StreamMessage{Payload: payload2, Receipt: "receipt-2"},
	)
	service := &runServiceStub{results: []error{errors.New("postgres unavailable"), nil}}
	consumer := NewConsumer(stream, service)
	done := make(chan error, 1)
	go func() {
		done <- consumer.Run(ctx)
	}()

	select {
	case receipt := <-stream.nackSignal:
		if receipt != "receipt-1" {
			t.Fatalf("first settlement = %q, want receipt-1", receipt)
		}
	case <-t.Context().Done():
		t.Fatal("timed out waiting for nack")
	}

	select {
	case receipt := <-stream.ackSignal:
		if receipt != "receipt-2" {
			t.Fatalf("second settlement = %q, want receipt-2", receipt)
		}
	case <-t.Context().Done():
		t.Fatal("timed out waiting for ack")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(service.records) != 2 {
		t.Fatalf("records = %d, want 2", len(service.records))
	}
	if len(stream.nacked) != 1 || len(stream.acked) != 1 {
		t.Fatalf("acked=%#v nacked=%#v", stream.acked, stream.nacked)
	}
}

type runStreamStub struct {
	mu         sync.Mutex
	messages   chan StreamMessage
	acked      []string
	nacked     []string
	ackSignal  chan string
	nackSignal chan string
}

func newRunStreamStub(messages ...StreamMessage) *runStreamStub {
	stub := &runStreamStub{
		messages:   make(chan StreamMessage, len(messages)+1),
		ackSignal:  make(chan string, len(messages)+1),
		nackSignal: make(chan string, len(messages)+1),
	}
	for _, message := range messages {
		stub.messages <- message
	}
	return stub
}

func (s *runStreamStub) Receive(ctx context.Context) (StreamMessage, error) {
	select {
	case message := <-s.messages:
		return message, nil
	case <-ctx.Done():
		return StreamMessage{}, ctx.Err()
	}
}

func (s *runStreamStub) Ack(_ context.Context, receipt string) error {
	s.mu.Lock()
	s.acked = append(s.acked, receipt)
	s.mu.Unlock()
	s.ackSignal <- receipt
	return nil
}

func (s *runStreamStub) Nack(_ context.Context, receipt string) error {
	s.mu.Lock()
	s.nacked = append(s.nacked, receipt)
	s.mu.Unlock()
	s.nackSignal <- receipt
	return nil
}

type runErrorStreamStub struct {
	err error
}

func (s *runErrorStreamStub) Receive(context.Context) (StreamMessage, error) {
	return StreamMessage{}, s.err
}

func (s *runErrorStreamStub) Ack(context.Context, string) error {
	return nil
}

func (s *runErrorStreamStub) Nack(context.Context, string) error {
	return nil
}

type runServiceStub struct {
	mu      sync.Mutex
	results []error
	records []RecordInput
}

func (s *runServiceStub) Record(_ context.Context, input RecordInput) (Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, input)
	if len(s.records) <= len(s.results) && s.results[len(s.records)-1] != nil {
		return Detail{}, s.results[len(s.records)-1]
	}
	return Detail{RecordInput: input, RecordedAt: time.Now().UTC()}, nil
}

func (s *runServiceStub) SessionUsage(context.Context, string, string) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}

func (s *runServiceStub) AccountUsage(context.Context, string, time.Time, time.Time) (Summary, error) {
	return Summary{}, domain.ErrNotImplemented
}
