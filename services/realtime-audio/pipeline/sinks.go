package pipeline

import (
	"context"
	"errors"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

const (
	usageTopic = "usage.recorded"
)

var (
	// ErrUsageIdentityRequired indicates that a UsageFact cannot be deduplicated.
	ErrUsageIdentityRequired = errors.New("usage fact identity is required")
	// ErrOutboxRequired indicates that a production sink has no durable publisher.
	ErrOutboxRequired = errors.New("durable outbox is required")
)

// DurableOutbox accepts an event before a sink reports successful publication. Append returns nil
// only after the entry can survive process failure. Replaying the same topic, key, and payload is
// idempotent; reusing a key with a different payload must return a conflict without overwriting it.
type DurableOutbox interface {
	Append(ctx context.Context, topic, idempotencyKey string, payload any) error
}

// UsageFactSink publishes durable UsageFact events.
type UsageFactSink interface {
	Publish(ctx context.Context, fact UsageFact) error
}

// OutboxFinalTurnSink adapts FinalTurn events to a durable outbox.
type OutboxFinalTurnSink struct {
	outbox DurableOutbox
}

// NewOutboxFinalTurnSink constructs a reliable FinalTurn adapter.
func NewOutboxFinalTurnSink(outbox DurableOutbox) *OutboxFinalTurnSink {
	return &OutboxFinalTurnSink{outbox: outbox}
}

// Publish reports success only after one durable append. Retry and uncertain-commit handling belong
// to the outbox implementation so this adapter never submits a mutated replacement event.
func (s *OutboxFinalTurnSink) Publish(ctx context.Context, event FinalTurnEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if s == nil || s.outbox == nil {
		return ErrOutboxRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.outbox.Append(ctx, recordsv1.FinalTurnTopic, event.EventID, event)
}

// OutboxUsageFactSink adapts UsageFact events to a durable outbox.
type OutboxUsageFactSink struct {
	outbox DurableOutbox
}

// NewOutboxUsageFactSink constructs a reliable UsageFact adapter.
func NewOutboxUsageFactSink(outbox DurableOutbox) *OutboxUsageFactSink {
	return &OutboxUsageFactSink{outbox: outbox}
}

// Publish reports success only after the outbox accepts the typed fact.
func (s *OutboxUsageFactSink) Publish(ctx context.Context, fact UsageFact) error {
	if fact.ID == "" || fact.IdempotencyKey == "" {
		return ErrUsageIdentityRequired
	}
	if err := fact.Validate(); err != nil {
		return err
	}
	if s == nil || s.outbox == nil {
		return ErrOutboxRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.outbox.Append(ctx, usageTopic, fact.IdempotencyKey, fact)
}

var _ recordsv1.FinalTurnSink = (*OutboxFinalTurnSink)(nil)
