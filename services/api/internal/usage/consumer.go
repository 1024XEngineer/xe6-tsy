package usage

import (
	"context"
	"errors"
	"log/slog"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/metrics"
)

// Consumer records usage.recorded events from a durable stream.
type Consumer struct {
	stream  StreamConsumer
	service Service
}

func NewConsumer(stream StreamConsumer, service Service) *Consumer {
	return &Consumer{stream: stream, service: service}
}

func (c *Consumer) Run(ctx context.Context) error {
	if c == nil || c.stream == nil || c.service == nil {
		<-ctx.Done()
		return nil
	}
	for {
		processed, err := c.ProcessOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			slog.Error("usage consumer iteration failed", "error", err)
			continue
		}
		if !processed && ctx.Err() != nil {
			return nil
		}
	}
}

// ProcessOnce handles at most one stream message.
func (c *Consumer) ProcessOnce(ctx context.Context) (bool, error) {
	message, err := c.stream.Receive(ctx)
	if err != nil {
		return false, err
	}
	if len(message.Payload) == 0 {
		if err := c.stream.Ack(ctx, message.Receipt); err != nil {
			return false, err
		}
		return false, nil
	}

	input, err := ParseRecordInput(message.Payload)
	if err != nil {
		if ackErr := c.stream.Ack(ctx, message.Receipt); ackErr != nil {
			return true, errors.Join(err, ackErr)
		}
		slog.Warn("usage consumer rejected invalid payload", "error", err, "receipt", message.Receipt)
		metrics.RecordUsageRejected()
		return true, nil
	}

	if _, err := c.service.Record(ctx, input); err != nil {
		if isPermanentUsageError(err) {
			if ackErr := c.stream.Ack(ctx, message.Receipt); ackErr != nil {
				return true, errors.Join(err, ackErr)
			}
			slog.Warn("usage consumer rejected event", "error", err, "idempotency_key", input.IdempotencyKey)
			metrics.RecordUsageRejected()
			return true, nil
		}
		if nackErr := c.stream.Nack(ctx, message.Receipt); nackErr != nil {
			return true, errors.Join(err, nackErr)
		}
		return true, err
	}
	if err := c.stream.Ack(ctx, message.Receipt); err != nil {
		return true, err
	}
	metrics.RecordUsageRecorded()
	return true, nil
}

func isPermanentUsageError(err error) bool {
	return errors.Is(err, domain.ErrInvalidArgument) ||
		errors.Is(err, domain.ErrForbidden) ||
		errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrNotFound)
}
