package modeprojection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultStreamBlock = 5 * time.Second

// StreamMessage is one broker-delivered realtime.mode.changed payload and its receipt.
type StreamMessage struct {
	Payload []byte
	Receipt string
}

// StreamConsumer receives durable events and settles their broker receipts.
type StreamConsumer interface {
	Receive(context.Context) (StreamMessage, error)
	Ack(context.Context, string) error
	Nack(context.Context, string) error
}

// ValkeyStream consumes realtime.mode.changed events from one Redis/Valkey consumer group.
type ValkeyStream struct {
	client     *redis.Client
	stream     string
	group      string
	consumer   string
	block      time.Duration
	claimIdle  time.Duration
	claimStart string
}

func NewValkeyStream(ctx context.Context, client *redis.Client, stream, group, consumer string) (*ValkeyStream, error) {
	if client == nil {
		return nil, fmt.Errorf("valkey client is required")
	}
	if stream == "" {
		stream = "lingow:realtime:mode:changed"
	}
	if group == "" {
		group = "lingow-mode-projection"
	}
	if consumer == "" {
		consumer = "mode-projection-worker"
	}
	queue := &ValkeyStream{
		client:     client,
		stream:     stream,
		group:      group,
		consumer:   consumer,
		block:      defaultStreamBlock,
		claimIdle:  30 * time.Second,
		claimStart: "0-0",
	}
	if err := client.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil && !isBusyGroup(err) {
		return nil, err
	}
	return queue, nil
}

// Publish enqueues one event payload for local publishers and broker-boundary tests.
func (q *ValkeyStream) Publish(ctx context.Context, payload []byte) error {
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{"payload": payload},
	}).Err()
}

func (q *ValkeyStream) Receive(ctx context.Context) (StreamMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return StreamMessage{}, err
		}
		if message, ok, err := q.autoclaim(ctx); err != nil {
			return StreamMessage{}, err
		} else if ok {
			return message, nil
		}
		streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    q.group,
			Consumer: q.consumer,
			Streams:  []string{q.stream, ">"},
			Count:    1,
			Block:    q.block,
		}).Result()
		if errors.Is(err, redis.Nil) {
			if ctx.Err() != nil {
				return StreamMessage{}, ctx.Err()
			}
			continue
		}
		if err != nil {
			return StreamMessage{}, err
		}
		if message, ok := streamMessageFromStreams(streams); ok {
			return message, nil
		}
	}
}

func (q *ValkeyStream) Ack(ctx context.Context, receipt string) error {
	if receipt == "" {
		return nil
	}
	return q.client.XAck(ctx, q.stream, q.group, receipt).Err()
}

func (q *ValkeyStream) Nack(_ context.Context, _ string) error {
	// Redis Streams have no explicit negative acknowledgement. Leaving the receipt pending lets
	// this or another consumer reclaim it after claimIdle without publishing a duplicate entry.
	return nil
}

func (q *ValkeyStream) autoclaim(ctx context.Context) (StreamMessage, bool, error) {
	start := q.claimStart
	if start == "" {
		start = "0-0"
	}
	result, nextStart, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  q.claimIdle,
		Start:    start,
		Count:    1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return StreamMessage{}, false, err
	}
	if nextStart == "" {
		nextStart = "0-0"
	}
	// XAUTOCLAIM returns a continuation cursor even when the current scan window has
	// no eligible entry. Keep it so a recently claimed entry at the front of the PEL
	// cannot permanently hide older idle entries in later scan windows.
	q.claimStart = nextStart
	if len(result) == 0 {
		return StreamMessage{}, false, nil
	}
	return streamMessageFromEntry(result[0]), true, nil
}

func streamMessageFromStreams(streams []redis.XStream) (StreamMessage, bool) {
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return StreamMessage{}, false
	}
	return streamMessageFromEntry(streams[0].Messages[0]), true
}

func streamMessageFromEntry(entry redis.XMessage) StreamMessage {
	// Keep the receipt even when payload is absent or has an unexpected broker type. The consumer
	// can then acknowledge the permanently malformed entry instead of leaving a poison message in
	// the pending list forever.
	return StreamMessage{Payload: bytesField(entry.Values, "payload"), Receipt: entry.ID}
}

func bytesField(values map[string]any, key string) []byte {
	switch value := values[key].(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		return nil
	}
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}
