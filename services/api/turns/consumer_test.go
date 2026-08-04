package turns

import (
	"context"
	"errors"
	"fmt"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestFinalTurnHandlerAcknowledgesCommittedEvent(t *testing.T) {
	consumer := &consumerStub{}
	delivery := &deliveryStub{event: validEvent(), attempts: 1}
	handler := NewFinalTurnHandler(consumer)

	if err := handler.Handle(t.Context(), delivery); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if consumer.calls != 1 || delivery.acks != 1 || delivery.nacks != 0 {
		t.Fatalf("calls consume=%d ack=%d nack=%d", consumer.calls, delivery.acks, delivery.nacks)
	}
}

func TestFinalTurnHandlerNacksFailedEvent(t *testing.T) {
	consumeErr := errors.New("database unavailable")
	consumer := &consumerStub{err: consumeErr}
	delivery := &deliveryStub{event: validEvent()}
	handler := NewFinalTurnHandler(consumer)

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, consumeErr) {
		t.Fatalf("Handle() error = %v, want consume error", err)
	}
	if delivery.acks != 0 || delivery.nacks != 1 {
		t.Fatalf("calls ack=%d nack=%d", delivery.acks, delivery.nacks)
	}
	if delivery.lastError != consumeErr.Error() {
		t.Fatalf("last error = %q, want %q", delivery.lastError, consumeErr.Error())
	}
}

func TestFinalTurnHandlerReturnsNackFailure(t *testing.T) {
	consumeErr := errors.New("database unavailable")
	nackErr := errors.New("broker unavailable")
	delivery := &deliveryStub{event: validEvent(), nackErr: nackErr}
	handler := NewFinalTurnHandler(&consumerStub{err: consumeErr})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, consumeErr) || !errors.Is(err, nackErr) {
		t.Fatalf("Handle() error = %v, want consume and nack errors", err)
	}
	if delivery.acks != 0 || delivery.nacks != 1 {
		t.Fatalf("calls ack=%d nack=%d", delivery.acks, delivery.nacks)
	}
}

func TestFinalTurnHandlerRejectsPermanentErrors(t *testing.T) {
	tests := []struct {
		name       string
		consumeErr error
	}{
		{name: "invalid event", consumeErr: ErrInvalidRequest},
		{name: "conflicting replay", consumeErr: domain.ErrConflict},
		{name: "missing dependency", consumeErr: domain.ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &deliveryStub{event: validEvent()}
			handler := NewFinalTurnHandler(&consumerStub{err: test.consumeErr})

			err := handler.Handle(t.Context(), delivery)
			if !errors.Is(err, test.consumeErr) {
				t.Fatalf("Handle() error = %v, want consume error", err)
			}
			if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
				t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
			}
		})
	}
}

func TestFinalTurnHandlerRejectsTransientErrorWhenAttemptLimitReached(t *testing.T) {
	consumeErr := errors.New("database unavailable")
	delivery := &deliveryStub{event: validEvent(), attempts: maxFinalTurnAttempts}
	handler := NewFinalTurnHandler(&consumerStub{err: consumeErr})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, consumeErr) {
		t.Fatalf("Handle() error = %v, want consume error", err)
	}
	if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
		t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
	if delivery.lastError != consumeErr.Error() {
		t.Fatalf("last error = %q, want %q", delivery.lastError, consumeErr.Error())
	}
}

func TestFinalTurnHandlerNacksTransientErrorBelowAttemptLimit(t *testing.T) {
	consumeErr := errors.New("database unavailable")
	delivery := &deliveryStub{event: validEvent(), attempts: maxFinalTurnAttempts - 1}
	handler := NewFinalTurnHandler(&consumerStub{err: consumeErr})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, consumeErr) {
		t.Fatalf("Handle() error = %v, want consume error", err)
	}
	if delivery.acks != 0 || delivery.nacks != 1 || delivery.rejects != 0 {
		t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
}

func TestFinalTurnHandlerReturnsExhaustedRejectFailure(t *testing.T) {
	consumeErr := errors.New("database unavailable")
	rejectErr := errors.New("dead-letter unavailable")
	delivery := &deliveryStub{event: validEvent(), attempts: maxFinalTurnAttempts, rejectErr: rejectErr}
	handler := NewFinalTurnHandler(&consumerStub{err: consumeErr})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, consumeErr) || !errors.Is(err, rejectErr) {
		t.Fatalf("Handle() error = %v, want consume and reject errors", err)
	}
	if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
		t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
}

func TestFinalTurnHandlerRejectsWrappedPermanentErrors(t *testing.T) {
	tests := []struct {
		name       string
		consumeErr error
	}{
		{name: "wrapped invalid event", consumeErr: fmt.Errorf("consume: %w", ErrInvalidRequest)},
		{name: "wrapped conflict", consumeErr: fmt.Errorf("store: %w", domain.ErrConflict)},
		{name: "wrapped missing dependency", consumeErr: fmt.Errorf("scope: %w", domain.ErrNotFound)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &deliveryStub{event: validEvent()}
			handler := NewFinalTurnHandler(&consumerStub{err: test.consumeErr})

			err := handler.Handle(t.Context(), delivery)
			if !errors.Is(err, test.consumeErr) {
				t.Fatalf("Handle() error = %v, want consume error", err)
			}
			if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
				t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
			}
		})
	}
}

func TestFinalTurnHandlerRejectsPermanentErrorWhenRejectFailsAndWrapsConsumeError(t *testing.T) {
	consumeErr := fmt.Errorf("consume: %w", ErrInvalidRequest)
	rejectErr := errors.New("dead-letter unavailable")
	delivery := &deliveryStub{event: validEvent(), rejectErr: rejectErr}
	handler := NewFinalTurnHandler(&consumerStub{err: consumeErr})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, rejectErr) {
		t.Fatalf("Handle() error = %v, want invalid request and reject errors", err)
	}
	if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
		t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
}

func TestFinalTurnHandlerReturnsRejectFailure(t *testing.T) {
	rejectErr := errors.New("dead-letter unavailable")
	delivery := &deliveryStub{event: validEvent(), rejectErr: rejectErr}
	handler := NewFinalTurnHandler(&consumerStub{err: ErrInvalidRequest})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, rejectErr) {
		t.Fatalf("Handle() error = %v, want consume and reject errors", err)
	}
	if delivery.acks != 0 || delivery.nacks != 0 || delivery.rejects != 1 {
		t.Fatalf("calls ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
}

func TestFinalTurnHandlerSettlesAfterConsumeContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	delivery := &deliveryStub{event: validEvent()}
	handler := NewFinalTurnHandler(&consumerStub{err: context.Canceled})

	err := handler.Handle(ctx, delivery)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle() error = %v, want context canceled", err)
	}
	if delivery.nacks != 1 {
		t.Fatalf("Nack() calls = %d, want 1", delivery.nacks)
	}
}

func TestFinalTurnHandlerDoesNotNackAmbiguousAckFailure(t *testing.T) {
	ackErr := errors.New("ack response lost")
	delivery := &deliveryStub{event: validEvent(), ackErr: ackErr}
	handler := NewFinalTurnHandler(&consumerStub{})

	err := handler.Handle(t.Context(), delivery)
	if !errors.Is(err, ackErr) {
		t.Fatalf("Handle() error = %v, want ack error", err)
	}
	if delivery.acks != 1 || delivery.nacks != 0 {
		t.Fatalf("calls ack=%d nack=%d", delivery.acks, delivery.nacks)
	}
}

type consumerStub struct {
	calls int
	err   error
}

func (s *consumerStub) ConsumeFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	s.calls++
	return s.err
}

type deliveryStub struct {
	event     recordsv1.FinalTurnEvent
	attempts  int
	ackErr    error
	nackErr   error
	rejectErr error
	lastError string
	acks      int
	nacks     int
	rejects   int
}

func (d *deliveryStub) Event() recordsv1.FinalTurnEvent {
	return d.event
}

func (d *deliveryStub) Attempts() int {
	return d.attempts
}

func (d *deliveryStub) Ack() error {
	d.acks++
	return d.ackErr
}

func (d *deliveryStub) Nack(lastError string) error {
	d.nacks++
	d.lastError = lastError
	return d.nackErr
}

func (d *deliveryStub) Reject(lastError string) error {
	d.rejects++
	d.lastError = lastError
	return d.rejectErr
}

var (
	_ recordsv1.FinalTurnConsumer = (*consumerStub)(nil)
	_ FinalTurnDelivery           = (*deliveryStub)(nil)
)
