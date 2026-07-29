//go:build integration

package recordstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUsagePostgresRecordIdempotency(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertUsageFixtures(t, pool, "usage_record_account", "usage_record_session", "usage_record_turn")

	repository := usage.NewPostgresRepository(pool)
	owners := accounts.NewPostgresRepository(pool)
	service := usage.NewPersistentUseCases(repository, owners)
	input := usage.RecordInput{
		EventVersion: usage.UsageEventVersion, ID: "usage_event_1", TraceID: "trace_1",
		IdempotencyKey: "usage-key-1", AccountID: "usage_record_account", SessionID: "usage_record_session",
		TurnID: "usage_record_turn", ServiceType: usage.StageTranslation,
		Provider: "mock-llm", Model: "qwen", InputTokens: 12, OutputTokens: 34,
		OccurredAt: time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
	}

	first, err := service.Record(t.Context(), input)
	if err != nil {
		t.Fatalf("first Record() error = %v", err)
	}
	second, err := service.Record(t.Context(), input)
	if err != nil {
		t.Fatalf("replay Record() error = %v", err)
	}
	if first.ID != second.ID || first.RecordedAt != second.RecordedAt {
		t.Fatalf("replay detail = %#v, want %#v", second, first)
	}

	conflict := input
	conflict.InputTokens = 99
	if _, err := service.Record(t.Context(), conflict); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting Record() error = %v, want conflict", err)
	}
}

func TestUsagePostgresSessionAndAccountSummaries(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertUsageFixtures(t, pool, "usage_summary_account", "usage_summary_session", "usage_summary_turn")

	repository := usage.NewPostgresRepository(pool)
	owners := accounts.NewPostgresRepository(pool)
	service := usage.NewPersistentUseCases(repository, owners)
	occurredAt := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	for _, record := range []usage.RecordInput{
		{
			EventVersion: usage.UsageEventVersion, ID: "usage_asr", TraceID: "trace_asr",
			IdempotencyKey: "usage-asr", AccountID: "usage_summary_account", SessionID: "usage_summary_session",
			TurnID: "usage_summary_turn", ServiceType: usage.StageASR,
			Provider: "mock-asr", Model: "v1", AudioDurationMS: 1500, OccurredAt: occurredAt,
		},
		{
			EventVersion: usage.UsageEventVersion, ID: "usage_translation", TraceID: "trace_translation",
			IdempotencyKey: "usage-translation", AccountID: "usage_summary_account", SessionID: "usage_summary_session",
			TurnID: "usage_summary_turn", ServiceType: usage.StageTranslation,
			Provider: "mock-llm", Model: "qwen", InputTokens: 10, OutputTokens: 20, OccurredAt: occurredAt.Add(time.Minute),
		},
	} {
		if _, err := service.Record(t.Context(), record); err != nil {
			t.Fatalf("Record(%s) error = %v", record.ID, err)
		}
	}

	sessionSummary, err := service.SessionUsage(t.Context(), "usage_summary_account", "usage_summary_session")
	if err != nil {
		t.Fatalf("SessionUsage() error = %v", err)
	}
	if len(sessionSummary.Totals) != 2 {
		t.Fatalf("session totals = %#v, want 2 stages", sessionSummary.Totals)
	}

	accountSummary, err := service.AccountUsage(t.Context(), "usage_summary_account", occurredAt.Add(-time.Hour), occurredAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("AccountUsage() error = %v", err)
	}
	if len(accountSummary.Totals) != 2 {
		t.Fatalf("account totals = %#v, want 2 stages", accountSummary.Totals)
	}
}

func TestUsagePostgresRejectsForeignSessionOwner(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertUsageFixtures(t, pool, "usage_owner_account", "usage_owner_session", "usage_owner_turn")

	service := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accounts.NewPostgresRepository(pool))
	_, err := service.Record(t.Context(), usage.RecordInput{
		EventVersion: usage.UsageEventVersion, ID: "usage_foreign", TraceID: "trace_foreign",
		IdempotencyKey: "usage-foreign", AccountID: "other_account", SessionID: "usage_owner_session",
		TurnID: "usage_owner_turn", ServiceType: usage.StageASR,
		Provider: "mock-asr", Model: "v1", OccurredAt: time.Now().UTC(),
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Record() error = %v, want forbidden", err)
	}
}

func TestUsageConsumerPersistsStreamEvent(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	insertUsageFixtures(t, pool, "usage_stream_account", "usage_stream_session", "usage_stream_turn")

	service := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accounts.NewPostgresRepository(pool))
	stream := &usageStreamStub{}
	payload, err := usage.MarshalRecordInput(usage.RecordInput{
		EventVersion: usage.UsageEventVersion, ID: "usage_stream_event", TraceID: "trace_stream",
		IdempotencyKey: "usage-stream-key", AccountID: "usage_stream_account", SessionID: "usage_stream_session",
		TurnID: "usage_stream_turn", ServiceType: usage.StageTTS,
		Provider: "mock-tts", Model: "v1", AudioDurationMS: 900,
		OccurredAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("MarshalRecordInput() error = %v", err)
	}
	stream.messages = []usage.StreamMessage{{Payload: payload, Receipt: "receipt-1"}}

	consumer := usage.NewConsumer(stream, service)
	if processed, err := consumer.ProcessOnce(t.Context()); err != nil || !processed {
		t.Fatalf("ProcessOnce() = (%v, %v)", processed, err)
	}

	summary, err := service.SessionUsage(t.Context(), "usage_stream_account", "usage_stream_session")
	if err != nil {
		t.Fatalf("SessionUsage() error = %v", err)
	}
	if len(summary.Totals) != 1 || summary.Totals[0].ServiceType != usage.StageTTS || summary.Totals[0].AudioDurationMS != 900 {
		t.Fatalf("summary = %#v", summary.Totals)
	}
}

type usageStreamStub struct {
	messages []usage.StreamMessage
	acked    []string
}

func (s *usageStreamStub) Receive(ctx context.Context) (usage.StreamMessage, error) {
	if len(s.messages) == 0 {
		return usage.StreamMessage{}, ctx.Err()
	}
	message := s.messages[0]
	s.messages = s.messages[1:]
	return message, nil
}

func (s *usageStreamStub) Ack(_ context.Context, receipt string) error {
	s.acked = append(s.acked, receipt)
	return nil
}

func (s *usageStreamStub) Nack(context.Context, string) error { return nil }

func insertUsageFixtures(t *testing.T, pool *pgxpool.Pool, accountID, sessionID, turnID string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO lingow_accounts (id,kind,created_at) VALUES ($1,'anonymous',CURRENT_TIMESTAMP)`, accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO voice_sessions (id,account_id,status,audio_config,capabilities) VALUES ($1,$2,'created','{}'::jsonb,'{}'::jsonb)`, sessionID, accountID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_turns (
			id,event_id,event_payload_hash,session_id,speaker_code,sequence_no,
			source_language,target_language,language_config_version,source_text,
			translated_text,attribution_status,started_at,ended_at,created_at
		) VALUES ($1,$2,$3,$4,'speaker_01',1,'zh-CN','en-US',1,'src','dst','pending',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		turnID, "event_"+turnID, make([]byte, 32), sessionID,
	); err != nil {
		t.Fatalf("insert turn: %v", err)
	}
}
