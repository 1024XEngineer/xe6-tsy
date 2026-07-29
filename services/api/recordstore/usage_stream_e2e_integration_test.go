//go:build integration

package recordstore

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/redis/go-redis/v9"
)

func TestUsageStreamValkeyPublishToConsumerPersistsRecord(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertUsageFixtures(t, pool, "usage_e2e_account", "usage_e2e_session", "usage_e2e_turn")

	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	const stream = "lingow:usage:recorded"
	const group = "lingow-usage-e2e"

	publisher, err := usage.NewValkeyUsageStream(t.Context(), client, stream, group, "publisher")
	if err != nil {
		t.Fatalf("NewValkeyUsageStream(publisher) error = %v", err)
	}
	consumerStream, err := usage.NewValkeyUsageStream(t.Context(), client, stream, group, "consumer")
	if err != nil {
		t.Fatalf("NewValkeyUsageStream(consumer) error = %v", err)
	}

	payload, err := usage.MarshalRecordInput(usage.RecordInput{
		EventVersion: usage.UsageEventVersion, ID: "usage_e2e_event", TraceID: "trace_e2e",
		IdempotencyKey: "usage-e2e-key", AccountID: "usage_e2e_account", SessionID: "usage_e2e_session",
		TurnID: "usage_e2e_turn", ServiceType: usage.StageTranslation,
		Provider: "mock-llm", Model: "qwen", InputTokens: 11, OutputTokens: 22,
		OccurredAt: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("MarshalRecordInput() error = %v", err)
	}
	if err := publisher.Publish(t.Context(), payload); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	service := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accounts.NewPostgresRepository(pool))
	consumer := usage.NewConsumer(consumerStream, service)
	if processed, err := consumer.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("ProcessOnce() = (%v, %v)", processed, err)
	}

	summary, err := service.SessionUsage(t.Context(), "usage_e2e_account", "usage_e2e_session")
	if err != nil {
		t.Fatalf("SessionUsage() error = %v", err)
	}
	if len(summary.Totals) != 1 || summary.Totals[0].ServiceType != usage.StageTranslation {
		t.Fatalf("summary = %#v", summary.Totals)
	}
	if summary.Totals[0].InputTokens != 11 || summary.Totals[0].OutputTokens != 22 {
		t.Fatalf("token totals = %#v", summary.Totals[0])
	}
}
