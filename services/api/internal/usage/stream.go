package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultUsageStreamBlock = 5 * time.Second

// StreamMessage is one broker-delivered usage.recorded payload and its receipt.
type StreamMessage struct {
	Payload []byte
	Receipt string
}

// StreamConsumer receives usage.recorded events and settles broker receipts.
type StreamConsumer interface {
	Receive(context.Context) (StreamMessage, error)
	Ack(context.Context, string) error
	Nack(context.Context, string) error
}

// ValkeyUsageStream consumes usage.recorded events from a Redis/Valkey stream group.
type ValkeyUsageStream struct {
	client   *redis.Client
	stream   string
	group    string
	consumer string
	block    time.Duration
}

func NewValkeyUsageStream(ctx context.Context, client *redis.Client, stream, group, consumer string) (*ValkeyUsageStream, error) {
	if client == nil {
		return nil, fmt.Errorf("valkey client is required")
	}
	if stream == "" {
		stream = "lingow:usage:recorded"
	}
	if group == "" {
		group = "lingow-usage"
	}
	if consumer == "" {
		consumer = "usage-worker"
	}
	queue := &ValkeyUsageStream{client: client, stream: stream, group: group, consumer: consumer, block: defaultUsageStreamBlock}
	if err := client.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil && !isBusyGroup(err) {
		return nil, err
	}
	return queue, nil
}

// Publish enqueues one usage.recorded payload for tests and local publishers.
func (q *ValkeyUsageStream) Publish(ctx context.Context, payload []byte) error {
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{"payload": payload},
	}).Err()
}

func (q *ValkeyUsageStream) Receive(ctx context.Context) (StreamMessage, error) {
	if message, ok, err := q.autoclaim(ctx); err != nil {
		return StreamMessage{}, err
	} else if ok {
		return message, nil
	}
	for {
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
		message, ok, err := streamMessageFromStreams(streams)
		if err != nil {
			return StreamMessage{}, err
		}
		if ok {
			return message, nil
		}
	}
}

func (q *ValkeyUsageStream) Ack(ctx context.Context, receipt string) error {
	if receipt == "" {
		return nil
	}
	return q.client.XAck(ctx, q.stream, q.group, receipt).Err()
}

func (q *ValkeyUsageStream) Nack(ctx context.Context, receipt string) error {
	return q.Ack(ctx, receipt)
}

func (q *ValkeyUsageStream) autoclaim(ctx context.Context) (StreamMessage, bool, error) {
	result, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  30 * time.Second,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return StreamMessage{}, false, err
	}
	if len(result) == 0 {
		return StreamMessage{}, false, nil
	}
	message, err := streamMessageFromEntry(result[0])
	if err != nil {
		_ = q.Ack(ctx, result[0].ID)
		return StreamMessage{}, false, nil
	}
	return message, true, nil
}

func streamMessageFromStreams(streams []redis.XStream) (StreamMessage, bool, error) {
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return StreamMessage{}, false, nil
	}
	message, err := streamMessageFromEntry(streams[0].Messages[0])
	if err != nil {
		return StreamMessage{}, false, err
	}
	return message, true, nil
}

func streamMessageFromEntry(entry redis.XMessage) (StreamMessage, error) {
	payload := bytesField(entry.Values, "payload")
	if len(payload) == 0 {
		return StreamMessage{}, fmt.Errorf("stream entry missing payload")
	}
	return StreamMessage{Payload: payload, Receipt: entry.ID}, nil
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
