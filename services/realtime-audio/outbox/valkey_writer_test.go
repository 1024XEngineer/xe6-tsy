package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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

func TestValkeyWriterPublishesModeChangedToDedicatedStream(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	writer, err := NewValkeyWriter(client, "lingow:usage:recorded", "lingow:realtime:mode:changed")
	if err != nil {
		t.Fatalf("NewValkeyWriter() error = %v", err)
	}
	adapter := NewAdapter(writer)
	event := validModeChangedEvent()

	for range 2 {
		if err := adapter.Append(context.Background(), realtimev1.ModeChangedTopic, event.EventID, event); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	messages, err := client.XRange(context.Background(), "lingow:realtime:mode:changed", "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stream length = %d, want 1", len(messages))
	}
	encoded, ok := messages[0].Values["payload"].(string)
	if !ok {
		t.Fatalf("payload type = %T, want string", messages[0].Values["payload"])
	}
	var published realtimev1.ModeChangedEvent
	if err := json.Unmarshal([]byte(encoded), &published); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if published != event {
		t.Fatalf("published event = %#v, want %#v", published, event)
	}
}

func TestValkeyWriterReplayAppendIsIdempotent(t *testing.T) {
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

	const workers = 20
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			errorsCh <- adapter.Append(context.Background(), "usage.recorded", fact.IdempotencyKey, fact)
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent Append() error = %v", err)
		}
	}
	messages, err := client.XRange(context.Background(), "lingow:usage:recorded", "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stream length = %d, want 1", len(messages))
	}
}

func TestValkeyWriterDetectsPayloadConflict(t *testing.T) {
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
	conflict := fact
	conflict.InputTokens = 999

	if err := adapter.Append(context.Background(), "usage.recorded", fact.IdempotencyKey, fact); err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if err := adapter.Append(context.Background(), "usage.recorded", fact.IdempotencyKey, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Append() error = %v, want ErrConflict", err)
	}
}

func TestValkeyWriterDedupKeyUsesStreamClusterSlot(t *testing.T) {
	writer := &ValkeyWriter{}
	entry := Entry{Topic: realtimev1.ModeChangedTopic, IdempotencyKey: "event-1"}
	for stream, wantPrefix := range map[string]string{
		"lingow:mode":           "{lingow:mode}:dedup:",
		"lingow:{mode}:changed": "{mode}:dedup:",
	} {
		if got := writer.dedupKey(stream, entry); !strings.HasPrefix(got, wantPrefix) {
			t.Fatalf("dedupKey(%q) = %q, want prefix %q", stream, got, wantPrefix)
		}
	}
}

func TestValkeyWriterDoesNotAcknowledgeWhenStreamAppendFails(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	const stream = "lingow:realtime:mode:changed"
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	writer, err := NewValkeyWriter(client, "lingow:usage:recorded", stream)
	if err != nil {
		t.Fatalf("NewValkeyWriter() error = %v", err)
	}
	adapter := NewAdapter(writer)
	event := validModeChangedEvent()
	if err := client.Set(context.Background(), stream, "not-a-stream", 0).Err(); err != nil {
		t.Fatalf("seed wrong stream type: %v", err)
	}

	if err := adapter.Append(context.Background(), realtimev1.ModeChangedTopic, event.EventID, event); err == nil {
		t.Fatal("Append() error = nil, want XADD failure")
	}
	dedupKey := writer.dedupKey(stream, Entry{
		Topic:          realtimev1.ModeChangedTopic,
		IdempotencyKey: event.EventID,
	})
	if exists, err := client.Exists(context.Background(), dedupKey).Result(); err != nil || exists != 0 {
		t.Fatalf("dedup marker after XADD failure = %d, error = %v; want absent", exists, err)
	}
	if err := client.Del(context.Background(), stream).Err(); err != nil {
		t.Fatalf("remove wrong stream key: %v", err)
	}

	if err := adapter.Append(context.Background(), realtimev1.ModeChangedTopic, event.EventID, event); err != nil {
		t.Fatalf("retry Append() error = %v", err)
	}
	messages, err := client.XRange(context.Background(), stream, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stream length after retry = %d, want 1", len(messages))
	}
}

func validModeChangedEvent() realtimev1.ModeChangedEvent {
	return realtimev1.ModeChangedEvent{
		EventVersion: realtimev1.ModeChangedEventVersion,
		EventID:      "mode-event-1", TraceID: "trace-1", SessionID: "session-1",
		RuntimeInstanceID: "runtime-1", OperationID: "operation-1",
		FromMode: realtimev1.ModeInterpretation, ToMode: realtimev1.ModeAssistant,
		ResultingGeneration: 2, OccurredAt: time.Unix(1700000000, 0).UTC(),
	}
}
