//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeliveryPostgresRetryIdempotencyLookup(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := delivery.NewPostgresRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertDeliveryAccount(t, pool, "delivery_retry_account", "anonymous", nil)
	seedDeliveryMessage(t, repository, "delivery_retry_account", "delivery_retry_message", "create-key", now)

	if _, err := repository.GetMessageByDeliveryIdempotency(t.Context(), "delivery_retry_account", "create-key"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetMessageByDeliveryIdempotency(create key) error = %v, want not found", err)
	}

	if _, err := repository.CreateRetry(t.Context(), delivery.CreateRetryRecord{
		AccountID: "delivery_retry_account", MessageID: "delivery_retry_message",
		Attempt:        delivery.DeliveryAttempt{ID: "delivery_retry_attempt_2", MessageID: "delivery_retry_message", AttemptNumber: 2, Status: delivery.AttemptStatusQueued, CreatedAt: now.Add(time.Second)},
		IdempotencyKey: "retry-key",
	}); err != nil {
		t.Fatalf("CreateRetry() error = %v", err)
	}

	message, err := repository.GetMessageByDeliveryIdempotency(t.Context(), "delivery_retry_account", "retry-key")
	if err != nil {
		t.Fatalf("GetMessageByDeliveryIdempotency(retry key) error = %v", err)
	}
	if message.ID != "delivery_retry_message" || message.Attempts != 2 {
		t.Fatalf("retry lookup message = %#v, want message at attempt 2", message)
	}

	var attempts, outboxRows int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM delivery_attempts WHERE message_id=$1`, "delivery_retry_message").Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM delivery_outbox o JOIN delivery_attempts a ON a.id=o.attempt_id WHERE a.message_id=$1`, "delivery_retry_message").Scan(&outboxRows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if attempts != 2 || outboxRows != 2 {
		t.Fatalf("stored attempts=%d outbox_rows=%d, want 2 and 2", attempts, outboxRows)
	}
}

func TestDeliveryPostgresRetryIdempotencyViaUseCases(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := delivery.NewPostgresRepository(pool)
	useCases := delivery.NewPersistentUseCases(repository, nil, nil, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	insertDeliveryAccount(t, pool, "delivery_retry_usecase_account", "anonymous", nil)
	seedDeliveryMessage(t, repository, "delivery_retry_usecase_account", "delivery_retry_usecase_message", "create-key", now)

	first, err := useCases.Retry(t.Context(), "delivery_retry_usecase_account", "delivery_retry_usecase_message", "retry-key")
	if err != nil {
		t.Fatalf("first Retry() error = %v", err)
	}
	second, err := useCases.Retry(t.Context(), "delivery_retry_usecase_account", "delivery_retry_usecase_message", "retry-key")
	if err != nil {
		t.Fatalf("replayed Retry() error = %v", err)
	}
	if first.ID != second.ID || first.Attempts != 2 || second.Attempts != 2 {
		t.Fatalf("retry replay results = %#v and %#v, want same message at attempt 2", first, second)
	}

	var attempts int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM delivery_attempts WHERE message_id=$1`, "delivery_retry_usecase_message").Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("stored attempts=%d, want 2", attempts)
	}
}

func TestDeliveryPostgresRetryKeyIsAccountWideViaUseCases(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := delivery.NewPostgresRepository(pool)
	useCases := delivery.NewPersistentUseCases(repository, nil, nil, nil)
	insertDeliveryAccount(t, pool, "delivery_retry_concurrent_account", "anonymous", nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedDeliveryMessage(t, repository, "delivery_retry_concurrent_account", "delivery_retry_message_a", "create-a", now)
	seedDeliveryMessage(t, repository, "delivery_retry_concurrent_account", "delivery_retry_message_b", "create-b", now.Add(time.Second))

	if _, err := useCases.Retry(t.Context(), "delivery_retry_concurrent_account", "delivery_retry_message_a", "shared-retry-key"); err != nil {
		t.Fatalf("Retry(message_a) error = %v", err)
	}
	if _, err := useCases.Retry(t.Context(), "delivery_retry_concurrent_account", "delivery_retry_message_b", "shared-retry-key"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Retry(message_b) error = %v, want conflict", err)
	}
}

func TestDeliveryPostgresReadsMergedAccountLineage(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := delivery.NewPostgresRepository(pool)
	root := "delivery_lineage_root"
	child := "delivery_lineage_child"
	insertDeliveryAccount(t, pool, root, "registered", stringPtr("lineage-phone"))
	insertDeliveryAccount(t, pool, child, "anonymous", nil)
	if _, err := pool.Exec(t.Context(), `UPDATE lingow_accounts SET merged_into=$2 WHERE id=$1`, child, root); err != nil {
		t.Fatalf("merge lineage account: %v", err)
	}
	seedDeliveryMessage(t, repository, child, "delivery_lineage_message", "lineage-create-key", time.Now().UTC().Truncate(time.Microsecond))

	message, err := repository.GetMessage(t.Context(), root, "delivery_lineage_message")
	if err != nil {
		t.Fatalf("GetMessage() through lineage error = %v", err)
	}
	if message.AccountID != child {
		t.Fatalf("lineage message account = %q, want child account %q", message.AccountID, child)
	}
}

func insertDeliveryAccount(t *testing.T, pool *pgxpool.Pool, id, kind string, phoneHash *string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO lingow_accounts (id,kind,phone_hash,created_at) VALUES ($1,$2,$3,CURRENT_TIMESTAMP)`, id, kind, phoneHash)
	if err != nil {
		t.Fatalf("insert delivery account %q: %v", id, err)
	}
}

func seedDeliveryMessage(t *testing.T, repository *delivery.PostgresRepository, accountID, messageID, key string, createdAt time.Time) {
	t.Helper()
	turn := delivery.FinalTurnSnapshot{TurnID: "turn_" + messageID, SessionID: "session_" + messageID, SourceLanguage: "zh-CN", TargetLanguage: "en-US", SourceText: "hello", TranslatedText: "nihao", CreatedAt: createdAt}
	message := delivery.Message{ID: messageID, AccountID: accountID, Channel: delivery.ChannelEmail, DestinationRef: "verified", SnapshotVersion: 1, Turns: []delivery.FinalTurnSnapshot{turn}, Status: delivery.MessageStatusQueued, Attempts: 1, CreatedAt: createdAt, UpdatedAt: createdAt}
	attempt := delivery.DeliveryAttempt{ID: "attempt_" + messageID, MessageID: messageID, AttemptNumber: 1, Status: delivery.AttemptStatusQueued, CreatedAt: createdAt}
	if err := repository.CreateMessage(t.Context(), delivery.CreateMessageRecord{Message: message, InitialAttempt: attempt, IdempotencyKey: key}); err != nil {
		t.Fatalf("CreateMessage(%q) error = %v", messageID, err)
	}
	if _, err := repository.ClaimAttempt(t.Context(), attempt.ID); err != nil {
		t.Fatalf("ClaimAttempt(%q) error = %v", attempt.ID, err)
	}
	if err := repository.CompleteAttempt(t.Context(), attempt.ID, messageID, delivery.AttemptStatusFailed, delivery.MessageStatusFailed, stringPtr("provider_failed")); err != nil {
		t.Fatalf("CompleteAttempt(%q) error = %v", attempt.ID, err)
	}
}

func stringPtr(value string) *string { return &value }
