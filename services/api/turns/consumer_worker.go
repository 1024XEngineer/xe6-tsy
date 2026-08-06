package turns

import (
	"context"
	"errors"
	"fmt"
)

// FinalTurnDeliverySource receives durable final-turn deliveries. Implementations own receipt
// state and retry scheduling; the worker only coordinates the handler lifecycle.
type FinalTurnDeliverySource interface {
	Receive(context.Context) (FinalTurnDelivery, error)
}

// FinalTurnWorker drains a durable final-turn source until the source fails or the context ends.
// Handler errors are already settled by Ack, Nack, or Reject and therefore do not stop unrelated
// deliveries. A source error is returned so the process supervisor can restart the worker.
type FinalTurnWorker struct {
	source  FinalTurnDeliverySource
	handler *FinalTurnHandler
}

func NewFinalTurnWorker(source FinalTurnDeliverySource, handler *FinalTurnHandler) *FinalTurnWorker {
	if source == nil {
		panic("final turn delivery source is required")
	}
	if handler == nil {
		panic("final turn handler is required")
	}
	return &FinalTurnWorker{source: source, handler: handler}
}

func (w *FinalTurnWorker) Run(ctx context.Context) error {
	for {
		delivery, err := w.source.Receive(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("receive final turn: %w", err)
		}
		if err := w.handler.Handle(ctx, delivery); err != nil {
			if errors.Is(err, ErrFinalTurnSettlement) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}
