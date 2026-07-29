package turns

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

var ErrFinalTurnSettlement = errors.New("final turn delivery settlement failed")

// FinalTurnDelivery hides transport receipt details while preserving explicit Ack/Nack control.
type FinalTurnDelivery interface {
	Event() recordsv1.FinalTurnEvent
	Ack() error
	Nack() error
	Reject() error
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
		if isPermanentFinalTurnError(err) {
			if rejectErr := delivery.Reject(); rejectErr != nil {
				return fmt.Errorf("%w: reject invalid final turn: %w", ErrFinalTurnSettlement, errors.Join(err, rejectErr))
			}
			return fmt.Errorf("reject invalid final turn: %w", err)
		}
		if nackErr := delivery.Nack(); nackErr != nil {
			return fmt.Errorf("%w: nack final turn after consume error: %w", ErrFinalTurnSettlement, errors.Join(err, nackErr))
		}
		return fmt.Errorf("consume final turn: %w", err)
	}
	if err := delivery.Ack(); err != nil {
		return fmt.Errorf("%w: ack final turn: %w", ErrFinalTurnSettlement, err)
	}
	return nil
}

func isPermanentFinalTurnError(err error) bool {
	return errors.Is(err, ErrInvalidRequest) || errors.Is(err, domain.ErrConflict)
}
