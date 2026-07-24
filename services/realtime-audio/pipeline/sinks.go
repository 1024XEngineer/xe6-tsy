package pipeline

import (
	"context"
	"errors"
)

const (
	finalTurnTopic = "final_turn.recorded"
	usageTopic     = "usage.recorded"
)

var (
	// ErrFinalTurnIDRequired indicates that a FinalTurn event cannot be deduplicated.
	ErrFinalTurnIDRequired = errors.New("final Turn event id is required")
	// ErrUsageIdentityRequired indicates that a UsageFact cannot be deduplicated.
	ErrUsageIdentityRequired = errors.New("usage fact identity is required")
	// ErrOutboxRequired indicates that a production sink has no durable publisher.
	ErrOutboxRequired = errors.New("durable outbox is required")
)

// DurableOutbox accepts an event before a sink reports successful publication.
type DurableOutbox interface {
	Append(ctx context.Context, topic, idempotencyKey string, payload any) error
}

// FinalTurnSink publishes durable FinalTurn events.
type FinalTurnSink interface {
	Publish(ctx context.Context, event FinalTurnEvent) error
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

// Publish reports success only after the outbox accepts the typed event.
func (s *OutboxFinalTurnSink) Publish(ctx context.Context, event FinalTurnEvent) error {
	if event.EventID == "" {
		return ErrFinalTurnIDRequired
	}
	if s == nil || s.outbox == nil {
		return ErrOutboxRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.outbox.Append(ctx, finalTurnTopic, event.EventID, event)
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
	if s == nil || s.outbox == nil {
		return ErrOutboxRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.outbox.Append(ctx, usageTopic, fact.IdempotencyKey, fact)
}
