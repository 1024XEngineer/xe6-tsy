package outbox

import (
	"context"
	"encoding/hex"
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
	dedupKey := w.dedupKey(entry)
	hashHex := hex.EncodeToString(entry.PayloadHash[:])
	inserted, err := w.client.SetNX(ctx, dedupKey, hashHex, 0).Result()
	if err != nil {
		return Ack{}, err
	}
	if !inserted {
		stored, err := w.client.Get(ctx, dedupKey).Result()
		if err != nil {
			return Ack{}, err
		}
		if stored != hashHex {
			return Ack{}, ErrConflict
		}
		return Ack{Accepted: true}, nil
	}
	if err := w.client.XAdd(ctx, &redis.XAddArgs{
		Stream: w.stream,
		Values: map[string]any{"payload": entry.Payload},
	}).Err(); err != nil {
		_ = w.client.Del(ctx, dedupKey).Err()
		return Ack{}, err
	}
	return Ack{Accepted: true}, nil
}

func (w *ValkeyWriter) dedupKey(entry Entry) string {
	return w.stream + ":dedup:" + entry.Topic + "\x00" + entry.IdempotencyKey
}

var _ Writer = (*ValkeyWriter)(nil)
