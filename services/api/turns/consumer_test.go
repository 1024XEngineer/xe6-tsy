package turns

import (
	"context"
	"errors"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestFinalTurnHandlerAcknowledgesCommittedEvent(t *testing.T) {
	consumer := &consumerStub{}
	delivery := &deliveryStub{event: validEvent()}
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
	event   recordsv1.FinalTurnEvent
	ackErr  error
	nackErr error
	acks    int
	nacks   int
}

func (d *deliveryStub) Event() recordsv1.FinalTurnEvent {
	return d.event
}

func (d *deliveryStub) Ack(context.Context) error {
	d.acks++
	return d.ackErr
}

func (d *deliveryStub) Nack(context.Context) error {
	d.nacks++
	return d.nackErr
}

var (
	_ recordsv1.FinalTurnConsumer = (*consumerStub)(nil)
	_ FinalTurnDelivery           = (*deliveryStub)(nil)
)
