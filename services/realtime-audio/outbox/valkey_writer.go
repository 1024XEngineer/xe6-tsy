package outbox

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const usageRecordedTopic = "usage.recorded"

// ValkeyWriter publishes canonical outbox entries to a Redis/Valkey stream.
type ValkeyWriter struct {
	client *redis.Client
	stream string
}

// NewValkeyWriter constructs a writer for usage.recorded events.
func NewValkeyWriter(client *redis.Client, stream string) (*ValkeyWriter, error) {
	if client == nil {
		return nil, ErrWriterRequired
	}
	if stream == "" {
		stream = "lingow:usage:recorded"
	}
	return &ValkeyWriter{client: client, stream: stream}, nil
}

// Accept publishes one durable entry to the configured stream.
func (w *ValkeyWriter) Accept(ctx context.Context, entry Entry) (Ack, error) {
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	if w == nil || w.client == nil {
		return Ack{}, ErrWriterRequired
	}
	if entry.Topic != usageRecordedTopic {
		return Ack{}, fmt.Errorf("%w: topic %q", ErrUnsupportedPayload, entry.Topic)
	}
	if err := w.client.XAdd(ctx, &redis.XAddArgs{
		Stream: w.stream,
		Values: map[string]any{"payload": entry.Payload},
	}).Err(); err != nil {
		return Ack{}, err
	}
	return Ack{Accepted: true}, nil
}

var _ Writer = (*ValkeyWriter)(nil)
