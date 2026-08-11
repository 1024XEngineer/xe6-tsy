package outbox

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/redis/go-redis/v9"
)

const usageRecordedTopic = "usage.recorded"

// ValkeyWriter publishes canonical outbox entries to a Redis/Valkey stream.
type ValkeyWriter struct {
	client  *redis.Client
	streams map[string]string
}

// NewValkeyWriter constructs a writer for usage and mode-change streams. The optional mode stream
// keeps existing usage-only callers source-compatible while production wiring configures both.
func NewValkeyWriter(client *redis.Client, usageStream string, modeStreams ...string) (*ValkeyWriter, error) {
	if client == nil {
		return nil, ErrWriterRequired
	}
	if len(modeStreams) > 1 {
		return nil, fmt.Errorf("configure valkey writer: at most one mode stream is supported")
	}
	if usageStream == "" {
		usageStream = "lingow:usage:recorded"
	}
	modeStream := "lingow:realtime:mode:changed"
	if len(modeStreams) == 1 && modeStreams[0] != "" {
		modeStream = modeStreams[0]
	}
	return &ValkeyWriter{client: client, streams: map[string]string{
		usageRecordedTopic:          usageStream,
		realtimev1.ModeChangedTopic: modeStream,
	}}, nil
}

// Accept publishes one durable entry to the configured stream.
func (w *ValkeyWriter) Accept(ctx context.Context, entry Entry) (Ack, error) {
	if err := ctx.Err(); err != nil {
		return Ack{}, err
	}
	if w == nil || w.client == nil {
		return Ack{}, ErrWriterRequired
	}
	stream, ok := w.streams[entry.Topic]
	if !ok || stream == "" {
		return Ack{}, fmt.Errorf("%w: topic %q", ErrUnsupportedPayload, entry.Topic)
	}
	dedupKey := w.dedupKey(stream, entry)
	hashHex := hex.EncodeToString(entry.PayloadHash[:])
	for {
		err := w.client.Watch(ctx, func(tx *redis.Tx) error {
			stored, err := tx.Get(ctx, dedupKey).Result()
			switch {
			case err == nil && stored == hashHex:
				return nil
			case err == nil:
				return ErrConflict
			case !errors.Is(err, redis.Nil):
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, dedupKey, hashHex, 0)
				pipe.XAdd(ctx, &redis.XAddArgs{
					Stream: stream,
					Values: map[string]any{"payload": entry.Payload},
				})
				return nil
			})
			return err
		}, dedupKey)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return Ack{}, err
		}
		return Ack{Accepted: true}, nil
	}
}

func (w *ValkeyWriter) dedupKey(stream string, entry Entry) string {
	return stream + ":dedup:" + entry.Topic + "\x00" + entry.IdempotencyKey
}

var _ Writer = (*ValkeyWriter)(nil)
