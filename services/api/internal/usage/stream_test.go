package usage

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestValkeyUsageStreamNackLeavesPendingEntry(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	const stream = "lingow:usage:recorded"
	const group = "lingow-usage-test"

	queue, err := NewValkeyUsageStream(t.Context(), client, stream, group, "worker")
	if err != nil {
		t.Fatalf("NewValkeyUsageStream() error = %v", err)
	}
	if err := queue.Publish(t.Context(), []byte(`{"event_version":1}`)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	message, err := queue.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := queue.Nack(t.Context(), message.Receipt); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if pending := pendingCount(t, client, stream, group); pending != 1 {
		t.Fatalf("pending after Nack = %d, want 1", pending)
	}
}

func TestValkeyUsageStreamAckClearsPendingEntry(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	const stream = "lingow:usage:recorded"
	const group = "lingow-usage-test"

	queue, err := NewValkeyUsageStream(context.Background(), client, stream, group, "worker")
	if err != nil {
		t.Fatalf("NewValkeyUsageStream() error = %v", err)
	}
	if err := queue.Publish(context.Background(), []byte(`{"event_version":1}`)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	message, err := queue.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if err := queue.Ack(context.Background(), message.Receipt); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	if pending := pendingCount(t, client, stream, group); pending != 0 {
		t.Fatalf("pending after Ack = %d, want 0", pending)
	}
}

func pendingCount(t *testing.T, client *redis.Client, stream, group string) int64 {
	t.Helper()
	summary, err := client.XPending(context.Background(), stream, group).Result()
	if err != nil {
		t.Fatalf("XPending() error = %v", err)
	}
	return summary.Count
}

func TestValkeyUsageStreamReceiveReclaimsIdlePendingWithoutNewTraffic(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	const stream = "lingow:usage:recorded"
	const group = "lingow-usage-retry"

	queue, err := NewValkeyUsageStream(t.Context(), client, stream, group, "worker")
	if err != nil {
		t.Fatalf("NewValkeyUsageStream() error = %v", err)
	}
	queue.claimIdle = time.Millisecond
	queue.block = time.Millisecond

	payload := []byte(`{"event_version":1}`)
	if err := queue.Publish(t.Context(), payload); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	first, err := queue.Receive(t.Context())
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	if err := queue.Nack(t.Context(), first.Receipt); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	server.FastForward(2 * time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	second, err := queue.Receive(ctx)
	if err != nil {
		t.Fatalf("second Receive() error = %v", err)
	}
	if second.Receipt != first.Receipt {
		t.Fatalf("reclaimed receipt = %q, want %q", second.Receipt, first.Receipt)
	}
}
