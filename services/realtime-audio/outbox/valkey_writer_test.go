package outbox

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestValkeyWriterPublishesUsageRecordedPayload(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	writer, err := NewValkeyWriter(client, "lingow:usage:recorded")
	if err != nil {
		t.Fatalf("NewValkeyWriter() error = %v", err)
	}
	adapter := NewAdapter(writer)
	fact := validUsageFact()

	if err := adapter.Append(context.Background(), "usage.recorded", fact.IdempotencyKey, fact); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	messages, err := client.XRange(context.Background(), "lingow:usage:recorded", "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stream length = %d, want 1", len(messages))
	}
	if messages[0].Values["payload"] == nil {
		t.Fatalf("stream message = %#v, want payload field", messages[0].Values)
	}
}
