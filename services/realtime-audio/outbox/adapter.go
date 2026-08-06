// Package outbox adapts frozen realtime events to a durable append boundary.
package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

var (
	ErrWriterRequired         = errors.New("outbox writer is required")
	ErrNotAccepted            = errors.New("outbox durable acceptance was not acknowledged")
	ErrConflict               = errors.New("outbox payload conflicts with existing idempotency key")
	ErrUnsupportedPayload     = errors.New("unsupported outbox payload")
	ErrIdempotencyKeyMismatch = errors.New("outbox idempotency key does not match payload")
)

// Entry is the immutable, canonical representation passed to a durable writer.
type Entry struct {
	Topic          string
	IdempotencyKey string
	Payload        []byte
	PayloadHash    [32]byte
}

// Ack confirms that Entry can survive process failure and be replayed safely.
type Ack struct {
	Accepted bool
}

// Writer is the production persistence or broker-facing boundary. It must not
// return an accepted acknowledgement before the entry is durably recorded.
type Writer interface {
	Accept(context.Context, Entry) (Ack, error)
}

// Adapter implements pipeline.DurableOutbox without changing event contracts.
type Adapter struct {
	writer Writer
}

// NewAdapter constructs an adapter around a durable writer.
func NewAdapter(writer Writer) *Adapter {
	return &Adapter{writer: writer}
}

// Append validates and canonicalizes one frozen event before durable acceptance.
func (a *Adapter) Append(ctx context.Context, topic, idempotencyKey string, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.writer == nil {
		return ErrWriterRequired
	}
	entry, err := canonicalEntry(topic, idempotencyKey, payload)
	if err != nil {
		return err
	}
	ack, err := a.writer.Accept(ctx, entry)
	if err != nil {
		return err
	}
	if !ack.Accepted {
		return ErrNotAccepted
	}
	return nil
}

func canonicalEntry(topic, idempotencyKey string, payload any) (Entry, error) {
	if topic == "" || idempotencyKey == "" {
		return Entry{}, fmt.Errorf("%w: topic and idempotency key are required", ErrUnsupportedPayload)
	}
	var expectedTopic, expectedKey string
	switch event := payload.(type) {
	case pipeline.FinalTurnEvent:
		if err := event.Validate(); err != nil {
			return Entry{}, err
		}
		expectedTopic, expectedKey = recordsv1.FinalTurnTopic, event.EventID
	case pipeline.UsageFact:
		if err := event.Validate(); err != nil {
			return Entry{}, err
		}
		expectedTopic, expectedKey = "usage.recorded", event.IdempotencyKey
	default:
		return Entry{}, fmt.Errorf("%w: %T", ErrUnsupportedPayload, payload)
	}
	if topic != expectedTopic || idempotencyKey != expectedKey {
		return Entry{}, fmt.Errorf("%w: got topic=%q key=%q, want topic=%q key=%q", ErrIdempotencyKeyMismatch, topic, idempotencyKey, expectedTopic, expectedKey)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal outbox payload: %w", err)
	}
	return Entry{
		Topic:          topic,
		IdempotencyKey: idempotencyKey,
		Payload:        encoded,
		PayloadHash:    sha256.Sum256(encoded),
	}, nil
}

var _ pipeline.DurableOutbox = (*Adapter)(nil)
