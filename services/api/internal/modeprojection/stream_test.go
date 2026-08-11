package modeprojection

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestNewValkeyStreamRequiresClient(t *testing.T) {
	if _, err := NewValkeyStream(t.Context(), nil, "", "", ""); err == nil {
		t.Fatal("NewValkeyStream() error = nil, want required client error")
	}
}

func TestValkeyStreamSettlesPendingEntries(t *testing.T) {
	tests := []struct {
		name        string
		settle      func(*ValkeyStream, context.Context, string) error
		wantPending int64
	}{
		{name: "ack clears pending", settle: (*ValkeyStream).Ack, wantPending: 0},
		{name: "nack leaves pending", settle: (*ValkeyStream).Nack, wantPending: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, client := newTestValkey(t)
			queue := newTestStream(t, client, "settlement-worker")
			if err := queue.Publish(t.Context(), []byte(`{"event_version":1}`)); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			message, err := queue.Receive(t.Context())
			if err != nil {
				t.Fatalf("Receive() error = %v", err)
			}
			if err := test.settle(queue, t.Context(), message.Receipt); err != nil {
				t.Fatalf("settle error = %v", err)
			}
			if got := pendingCount(t, client, queue.stream, queue.group); got != test.wantPending {
				t.Fatalf("pending = %d, want %d", got, test.wantPending)
			}
		})
	}
}

func TestValkeyStreamReclaimsIdleNackWithoutNewTraffic(t *testing.T) {
	server, client := newTestValkey(t)
	queue := newTestStream(t, client, "retry-worker")
	queue.claimIdle = time.Millisecond
	queue.block = time.Millisecond
	if err := queue.Publish(t.Context(), []byte(`{"event_version":1}`)); err != nil {
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

func TestValkeyStreamAdvancesAutoClaimCursorAcrossPendingWindows(t *testing.T) {
	_, client := newTestValkey(t)
	queue := newTestStream(t, client, "retry-worker")
	queue.claimIdle = time.Hour

	for _, payload := range []string{"first", "second", "third"} {
		if err := queue.Publish(t.Context(), []byte(payload)); err != nil {
			t.Fatalf("Publish(%q) error = %v", payload, err)
		}
	}
	for range 3 {
		message, err := queue.Receive(t.Context())
		if err != nil {
			t.Fatalf("Receive() while seeding pending entries: %v", err)
		}
		if err := queue.Nack(t.Context(), message.Receipt); err != nil {
			t.Fatalf("Nack() while seeding pending entries: %v", err)
		}
	}
	queue.claimIdle = 0
	queue.claimStart = "0-0"

	first, ok, err := queue.autoclaim(t.Context())
	if err != nil || !ok {
		t.Fatalf("first autoclaim = (%#v, %v, %v), want an entry", first, ok, err)
	}
	second, ok, err := queue.autoclaim(t.Context())
	if err != nil || !ok {
		t.Fatalf("second autoclaim = (%#v, %v, %v), want the next entry", second, ok, err)
	}
	if first.Receipt == second.Receipt {
		t.Fatalf("autoclaim receipts repeated %q; cursor did not advance", first.Receipt)
	}
}

func TestValkeyStreamReturnsMalformedEntryForAcknowledgement(t *testing.T) {
	_, client := newTestValkey(t)
	queue := newTestStream(t, client, "malformed-worker")
	entryID, err := client.XAdd(t.Context(), &redis.XAddArgs{
		Stream: queue.stream,
		Values: map[string]any{"other": "missing payload"},
	}).Result()
	if err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}

	message, err := queue.Receive(t.Context())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if message.Receipt != entryID || len(message.Payload) != 0 {
		t.Fatalf("message = %#v, want receipt with empty payload", message)
	}
	if err := queue.Ack(t.Context(), message.Receipt); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	if got := pendingCount(t, client, queue.stream, queue.group); got != 0 {
		t.Fatalf("pending = %d, want poison entry acknowledged", got)
	}
}

func newTestValkey(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

func newTestStream(t *testing.T, client *redis.Client, consumer string) *ValkeyStream {
	t.Helper()
	queue, err := NewValkeyStream(t.Context(), client, "lingow:realtime:mode:changed:test", "mode-projection-test", consumer)
	if err != nil {
		t.Fatalf("NewValkeyStream() error = %v", err)
	}
	return queue
}

func pendingCount(t *testing.T, client *redis.Client, stream, group string) int64 {
	t.Helper()
	summary, err := client.XPending(t.Context(), stream, group).Result()
	if err != nil {
		t.Fatalf("XPending() error = %v", err)
	}
	return summary.Count
}
