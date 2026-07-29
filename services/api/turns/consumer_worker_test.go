package turns

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestFinalTurnWorkerContinuesAfterNackedDelivery(t *testing.T) {
	first := &deliveryStub{event: validEvent(), nackErr: nil}
	second := &deliveryStub{event: validEvent()}
	second.event.EventID = "event_02"
	source := &deliverySourceStub{deliveries: []FinalTurnDelivery{first, second}}
	consumer := &consumerStub{err: errors.New("temporary store failure")}
	worker := NewFinalTurnWorker(source, NewFinalTurnHandler(consumer))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source.afterSecond = cancel

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if consumer.calls != 2 || first.nacks != 1 || second.nacks != 1 {
		t.Fatalf("consume calls=%d, first nacks=%d, second nacks=%d", consumer.calls, first.nacks, second.nacks)
	}
}

func TestFinalTurnWorkerRejectsPermanentDeliveryAndStopsOnCancellation(t *testing.T) {
	delivery := &deliveryStub{event: validEvent()}
	source := &deliverySourceStub{deliveries: []FinalTurnDelivery{delivery}}
	worker := NewFinalTurnWorker(source, NewFinalTurnHandler(&consumerStub{err: ErrInvalidRequest}))

	ctx, cancel := context.WithCancel(t.Context())
	source.afterFirst = cancel
	defer cancel()

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if delivery.rejects != 1 || delivery.nacks != 0 || delivery.acks != 0 {
		t.Fatalf("settlement ack=%d nack=%d reject=%d", delivery.acks, delivery.nacks, delivery.rejects)
	}
}

func TestFinalTurnWorkerReturnsSourceError(t *testing.T) {
	wantErr := errors.New("queue unavailable")
	worker := NewFinalTurnWorker(&deliverySourceStub{err: wantErr}, NewFinalTurnHandler(&consumerStub{}))

	err := worker.Run(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
}

func TestFinalTurnWorkerReturnsSettlementError(t *testing.T) {
	delivery := &deliveryStub{event: validEvent(), ackErr: errors.New("ack unavailable")}
	source := &deliverySourceStub{deliveries: []FinalTurnDelivery{delivery}}
	worker := NewFinalTurnWorker(source, NewFinalTurnHandler(&consumerStub{}))

	err := worker.Run(t.Context())
	if !errors.Is(err, ErrFinalTurnSettlement) || !errors.Is(err, delivery.ackErr) {
		t.Fatalf("Run() error = %v, want settlement and ack errors", err)
	}
}

func TestFinalTurnWorkerStopsWhenReceiveContextIsCancelled(t *testing.T) {
	source := &deliverySourceStub{blockUntilContextDone: true}
	worker := NewFinalTurnWorker(source, NewFinalTurnHandler(&consumerStub{}))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

type deliverySourceStub struct {
	deliveries            []FinalTurnDelivery
	err                   error
	blockUntilContextDone bool
	afterFirst            func()
	afterSecond           func()
	received              int
}

func (s *deliverySourceStub) Receive(ctx context.Context) (FinalTurnDelivery, error) {
	if s.blockUntilContextDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	if s.received >= len(s.deliveries) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	delivery := s.deliveries[s.received]
	s.received++
	switch s.received {
	case 1:
		if s.afterFirst != nil {
			s.afterFirst()
		}
	case 2:
		if s.afterSecond != nil {
			s.afterSecond()
		}
	}
	return delivery, nil
}

var _ FinalTurnDeliverySource = (*deliverySourceStub)(nil)
var _ recordsv1.FinalTurnConsumer = (*consumerStub)(nil)
