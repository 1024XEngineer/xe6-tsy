package modeprojection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// Projector records durable mode-change facts without becoming the authoritative runtime state.
// Implementations must make replay of the same event id and payload side-effect free.
type Projector interface {
	Project(context.Context, realtimev1.ModeChangedEvent) error
}

// Consumer projects realtime.mode.changed events delivered by a durable stream.
type Consumer struct {
	stream    StreamConsumer
	projector Projector
}

func NewConsumer(stream StreamConsumer, projector Projector) *Consumer {
	return &Consumer{stream: stream, projector: projector}
}

// Run consumes until the context is canceled. Per-message failures are settled by ProcessOnce,
// so a transient storage outage cannot terminate the supervised API runtime component.
func (c *Consumer) Run(ctx context.Context) error {
	if c == nil || c.stream == nil || c.projector == nil {
		<-ctx.Done()
		return nil
	}
	for {
		processed, err := c.ProcessOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			slog.Error("mode projection consumer iteration failed", "error", err)
			continue
		}
		if !processed && ctx.Err() != nil {
			return nil
		}
	}
}

// ProcessOnce handles and settles at most one stream message. Invalid events and permanent domain
// conflicts are acknowledged because redelivery cannot repair them; transient failures remain
// pending for autoclaim and retry.
func (c *Consumer) ProcessOnce(ctx context.Context) (bool, error) {
	message, err := c.stream.Receive(ctx)
	if err != nil {
		return false, err
	}
	if len(message.Payload) == 0 {
		if err := c.stream.Ack(ctx, message.Receipt); err != nil {
			return false, err
		}
		slog.Warn("mode projection consumer rejected empty payload", "receipt", message.Receipt)
		return false, nil
	}

	event, err := decodeModeChangedEvent(message.Payload)
	if err != nil {
		if ackErr := c.stream.Ack(ctx, message.Receipt); ackErr != nil {
			return true, errors.Join(err, ackErr)
		}
		slog.Warn("mode projection consumer rejected invalid payload", "error", err, "receipt", message.Receipt)
		return true, nil
	}

	if err := c.projector.Project(ctx, event); err != nil {
		if isPermanentProjectionError(err) {
			if ackErr := c.stream.Ack(ctx, message.Receipt); ackErr != nil {
				return true, errors.Join(err, ackErr)
			}
			slog.Warn("mode projection consumer rejected event", "error", err, "event_id", event.EventID)
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
	return true, nil
}

func decodeModeChangedEvent(payload []byte) (realtimev1.ModeChangedEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	var event realtimev1.ModeChangedEvent
	if err := decoder.Decode(&event); err != nil {
		return realtimev1.ModeChangedEvent{}, fmt.Errorf("%w: decode mode changed event: %v", domain.ErrInvalidArgument, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return realtimev1.ModeChangedEvent{}, err
	}
	if err := event.Validate(); err != nil {
		return realtimev1.ModeChangedEvent{}, fmt.Errorf("%w: %w", domain.ErrInvalidArgument, err)
	}
	return event, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", domain.ErrInvalidArgument)
		}
		return fmt.Errorf("%w: decode trailing JSON: %v", domain.ErrInvalidArgument, err)
	}
	return nil
}

func isPermanentProjectionError(err error) bool {
	return errors.Is(err, domain.ErrInvalidArgument) ||
		errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrNotFound)
}
