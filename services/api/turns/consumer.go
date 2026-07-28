package turns

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// FinalTurnDelivery hides transport receipt details while preserving explicit Ack/Nack control.
type FinalTurnDelivery interface {
	Event() recordsv1.FinalTurnEvent
	Ack(context.Context) error
	Nack(context.Context) error
}

// FinalTurnHandler keeps transport confirmation outside the database transaction. A failed Ack is
// returned without Nack because the transport may already have accepted the acknowledgement.
type FinalTurnHandler struct {
	consumer recordsv1.FinalTurnConsumer
}

func NewFinalTurnHandler(consumer recordsv1.FinalTurnConsumer) *FinalTurnHandler {
	return &FinalTurnHandler{consumer: consumer}
}

func (h *FinalTurnHandler) Handle(ctx context.Context, delivery FinalTurnDelivery) error {
	if err := h.consumer.ConsumeFinalTurn(ctx, delivery.Event()); err != nil {
		if nackErr := delivery.Nack(ctx); nackErr != nil {
			return fmt.Errorf("nack final turn after consume error: %w", errors.Join(err, nackErr))
		}
		return fmt.Errorf("consume final turn: %w", err)
	}
	if err := delivery.Ack(ctx); err != nil {
		return fmt.Errorf("ack final turn: %w", err)
	}
	return nil
}
