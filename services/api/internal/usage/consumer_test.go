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
