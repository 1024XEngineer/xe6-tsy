package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

const deliveryAttemptTopic = "delivery.attempt.queued"

// deliveryAttemptOutboxPayload is the durable hand-off contract between the
// transaction that creates an attempt and the dispatcher that publishes it.
// The provider destination is deliberately absent: workers resolve that value
// from the account-owned destination at send time.
type deliveryAttemptOutboxPayload struct {
	AttemptID      string `json:"attempt_id"`
	MessageID      string `json:"message_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateMessage(ctx context.Context, record CreateMessageRecord) error {
	turns, err := json.Marshal(record.Message.Turns)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO outbound_messages (id, account_id, channel, destination_ref, snapshot_version, turns, status, attempts, created_at, updated_at, idempotency_key) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11)`, record.Message.ID, record.Message.AccountID, record.Message.Channel, record.Message.DestinationRef, record.Message.SnapshotVersion, turns, record.Message.Status, record.Message.Attempts, record.Message.CreatedAt, record.Message.UpdatedAt, record.IdempotencyKey)
	if err != nil {
		return mapDeliveryError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO delivery_attempts (id, message_id, attempt_number, status, created_at) VALUES ($1,$2,$3,$4,$5)`, record.InitialAttempt.ID, record.InitialAttempt.MessageID, record.InitialAttempt.AttemptNumber, record.InitialAttempt.Status, record.InitialAttempt.CreatedAt)
	if err != nil {
		return mapDeliveryError(err)
	}
	if err := insertDeliveryOutbox(ctx, tx, record.InitialAttempt, record.IdempotencyKey); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) GetMessage(ctx context.Context, accountID, messageID string) (Message, error) {
	if accountID == "" {
		return Message{}, domain.ErrUnauthorized
	}
	return r.scanMessage(ctx, `SELECT id,account_id,channel,destination_ref,snapshot_version,turns,status,attempts,last_error_code,created_at,updated_at FROM outbound_messages WHERE id=$1 AND account_id IN (SELECT account_id FROM lingow_account_lineage($2))`, messageID, accountID)
}

func (r *PostgresRepository) GetMessageForWorker(ctx context.Context, messageID string) (Message, error) {
	return r.scanMessage(ctx, `SELECT id,account_id,channel,destination_ref,snapshot_version,turns,status,attempts,last_error_code,created_at,updated_at FROM outbound_messages WHERE id=$1`, messageID)
}

func (r *PostgresRepository) GetMessageByIdempotency(ctx context.Context, accountID, key string) (Message, error) {
	return r.scanMessage(ctx, `SELECT id,account_id,channel,destination_ref,snapshot_version,turns,status,attempts,last_error_code,created_at,updated_at FROM outbound_messages WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1)) AND idempotency_key=$2`, accountID, key)
}

func (r *PostgresRepository) GetMessageByDeliveryIdempotency(ctx context.Context, accountID, key string) (Message, error) {
	// Retry keys are persisted separately from the create-message key. The
	// attempt-number predicate is retained as a guard for legacy rows and makes
	// the lookup contract explicit: an initial attempt can never satisfy retry
	// idempotency.
	return r.scanMessage(ctx, `SELECT m.id,m.account_id,m.channel,m.destination_ref,m.snapshot_version,m.turns,m.status,m.attempts,m.last_error_code,m.created_at,m.updated_at FROM outbound_messages m JOIN delivery_retry_requests r ON r.message_id=m.id JOIN delivery_attempts a ON a.id=r.attempt_id AND a.attempt_number > 1 WHERE r.account_id IN (SELECT account_id FROM lingow_account_lineage($1)) AND r.idempotency_key=$2 LIMIT 1`, accountID, key)
}

func (r *PostgresRepository) CreateRetry(ctx context.Context, record CreateRetryRecord) (Message, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback(ctx)
	var existingMessageID string
	if err := tx.QueryRow(ctx, `SELECT message_id FROM delivery_retry_requests WHERE account_id=$1 AND idempotency_key=$2`, record.AccountID, record.IdempotencyKey).Scan(&existingMessageID); err == nil {
		if existingMessageID != record.MessageID {
			return Message{}, domain.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Message{}, err
		}
		return r.GetMessage(ctx, record.AccountID, record.MessageID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Message{}, mapDeliveryError(err)
	}
	var status MessageStatus
	var attempts int
	var lastErrorCode *string
	if err := tx.QueryRow(ctx, `SELECT status, attempts, last_error_code FROM outbound_messages WHERE id=$1 AND account_id IN (SELECT account_id FROM lingow_account_lineage($2)) FOR UPDATE`, record.MessageID, record.AccountID).Scan(&status, &attempts, &lastErrorCode); err != nil {
		return Message{}, mapDeliveryError(err)
	}
	if status != MessageStatusFailed || (lastErrorCode != nil && *lastErrorCode == deliveryUnknownErrorCode) {
		return Message{}, domain.ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (id,message_id,attempt_number,status,created_at) VALUES ($1,$2,$3,$4,$5)`, record.Attempt.ID, record.MessageID, record.Attempt.AttemptNumber, record.Attempt.Status, record.Attempt.CreatedAt); err != nil {
		return Message{}, mapDeliveryError(err)
	}
	// The attempt must exist before the retry request can reference it. If
	// another instance wins the account-level key, this transaction rolls back
	// its provisional attempt and returns the committed idempotent result.
	if err := tx.QueryRow(ctx, `INSERT INTO delivery_retry_requests (account_id,idempotency_key,message_id,attempt_id,created_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (account_id,idempotency_key) DO NOTHING RETURNING message_id`, record.AccountID, record.IdempotencyKey, record.MessageID, record.Attempt.ID, record.Attempt.CreatedAt).Scan(&existingMessageID); errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `SELECT message_id FROM delivery_retry_requests WHERE account_id=$1 AND idempotency_key=$2`, record.AccountID, record.IdempotencyKey).Scan(&existingMessageID); err != nil {
			return Message{}, mapDeliveryError(err)
		}
		if existingMessageID != record.MessageID {
			return Message{}, domain.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Message{}, err
		}
		return r.GetMessage(ctx, record.AccountID, record.MessageID)
	} else if err != nil {
		return Message{}, mapDeliveryError(err)
	}
	if err := insertDeliveryOutbox(ctx, tx, record.Attempt, record.IdempotencyKey); err != nil {
		return Message{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_messages SET status=$2, attempts=$3, last_error_code=NULL, updated_at=$4 WHERE id=$1`, record.MessageID, MessageStatusRetrying, attempts+1, time.Now().UTC()); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, err
	}
	return r.GetMessage(ctx, record.AccountID, record.MessageID)
}

func (r *PostgresRepository) GetAttempt(ctx context.Context, id string) (DeliveryAttempt, error) {
	var attempt DeliveryAttempt
	var errorCode *string
	err := r.pool.QueryRow(ctx, `SELECT id,message_id,attempt_number,status,error_code,next_attempt_at,started_at,finished_at,created_at FROM delivery_attempts WHERE id=$1`, id).Scan(&attempt.ID, &attempt.MessageID, &attempt.AttemptNumber, &attempt.Status, &errorCode, &attempt.NextAttemptAt, &attempt.StartedAt, &attempt.FinishedAt, &attempt.CreatedAt)
	attempt.ErrorCode = errorCode
	return attempt, mapDeliveryError(err)
}

func (r *PostgresRepository) ClaimAttempt(ctx context.Context, id string) (DeliveryAttempt, error) {
	var attempt DeliveryAttempt
	var errorCode *string
	now := time.Now().UTC()
	err := r.pool.QueryRow(ctx, `
		UPDATE delivery_attempts
		SET status=$2,error_code=NULL,next_attempt_at=NULL,started_at=$3,finished_at=NULL
		WHERE id=$1 AND status='queued'
		RETURNING id,message_id,attempt_number,status,error_code,next_attempt_at,started_at,finished_at,created_at`,
		id, AttemptStatusSending, now,
	).Scan(&attempt.ID, &attempt.MessageID, &attempt.AttemptNumber, &attempt.Status, &errorCode, &attempt.NextAttemptAt, &attempt.StartedAt, &attempt.FinishedAt, &attempt.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeliveryAttempt{}, domain.ErrConflict
	}
	attempt.ErrorCode = errorCode
	return attempt, mapDeliveryError(err)
}

// RequeueAttempt releases a claim only when the attempt is still owned by a
// sending worker. This is used for failures before provider invocation; a
// provider failure must keep the sending state because its acceptance may be
// unknown and cannot be safely converted back to queued.
func (r *PostgresRepository) RequeueAttempt(ctx context.Context, id string, nextAttemptAt time.Time) error {
	if nextAttemptAt.IsZero() {
		nextAttemptAt = time.Now().UTC()
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE delivery_attempts
		SET status='queued',error_code=NULL,next_attempt_at=$2,started_at=NULL,finished_at=NULL
		WHERE id=$1 AND status='sending'`, id, nextAttemptAt.UTC())
	if err != nil {
		return mapDeliveryError(err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (r *PostgresRepository) CompleteAttempt(ctx context.Context, attemptID, messageID string, attemptStatus DeliveryAttemptStatus, messageStatus MessageStatus, code *string) error {
	if (attemptStatus != AttemptStatusSucceeded && attemptStatus != AttemptStatusFailed) || (messageStatus != MessageStatusSent && messageStatus != MessageStatusFailed) {
		return domain.ErrInvalidArgument
	}
	now := time.Now().UTC()
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE delivery_attempts SET status=$3,error_code=$4,next_attempt_at=NULL,finished_at=$5 WHERE id=$1 AND message_id=$2 AND status='sending'`, attemptID, messageID, attemptStatus, code, now)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		// Never overwrite a terminal message chosen by another worker or by a
		// cancellation path. If the row is no longer eligible, roll back the
		// attempt transition; the next broker delivery will reconcile the stale
		// attempt with the authoritative message state.
		result, err = tx.Exec(ctx, `UPDATE outbound_messages SET status=$2,last_error_code=$3,updated_at=$4 WHERE id=$1 AND status IN ('queued','sending','retrying')`, messageID, messageStatus, code, now)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		return nil
	})
	return mapDeliveryError(err)
}

func (r *PostgresRepository) SetMessageStatus(ctx context.Context, id string, status MessageStatus, code *string) error {
	result, err := r.pool.Exec(ctx, `UPDATE outbound_messages SET status=$2,last_error_code=$3,updated_at=$4 WHERE id=$1`, id, status, code, time.Now().UTC())
	if err != nil {
		return mapDeliveryError(err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) SetAttemptStatus(ctx context.Context, id string, status DeliveryAttemptStatus, code *string) error {
	now := time.Now().UTC()
	result, err := r.pool.Exec(ctx, `UPDATE delivery_attempts SET status=$2,error_code=$3,started_at=CASE WHEN $2='sending' THEN $4 ELSE started_at END,finished_at=CASE WHEN $2 IN ('succeeded','failed') THEN $4 ELSE finished_at END WHERE id=$1`, id, status, code, now)
	if err != nil {
		return mapDeliveryError(err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) ListPreferences(ctx context.Context, accountID string) ([]Preference, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT ON (channel) account_id,channel,enabled,verified,updated_at FROM message_preferences WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1)) ORDER BY channel,(account_id=$1) DESC,updated_at DESC`, accountID)
	if err != nil {
		return nil, mapDeliveryError(err)
	}
	defer rows.Close()
	result := make([]Preference, 0)
	for rows.Next() {
		var preference Preference
		if err := rows.Scan(&preference.AccountID, &preference.Channel, &preference.Enabled, &preference.Verified, &preference.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, preference)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) PutPreference(ctx context.Context, preference Preference) (Preference, error) {
	var stored Preference
	err := r.pool.QueryRow(ctx, `
		INSERT INTO message_preferences (account_id,channel,enabled,verified,updated_at)
		VALUES (
			$1,
			$2,
			$3,
			EXISTS (
				SELECT 1
				FROM account_destinations d
				WHERE d.account_id IN (SELECT account_id FROM lingow_account_lineage($1))
				  AND d.channel=$2
				  AND d.verified_at IS NOT NULL
				  AND d.revoked_at IS NULL
			),
			$4
		)
		ON CONFLICT (account_id,channel) DO UPDATE
		SET enabled=EXCLUDED.enabled,verified=EXCLUDED.verified,updated_at=EXCLUDED.updated_at
		RETURNING account_id,channel,enabled,verified,updated_at`,
		preference.AccountID, preference.Channel, preference.Enabled, preference.UpdatedAt,
	).Scan(&stored.AccountID, &stored.Channel, &stored.Enabled, &stored.Verified, &stored.UpdatedAt)
	if err != nil {
		return Preference{}, mapDeliveryError(err)
	}
	return stored, nil
}

func (r *PostgresRepository) ClaimOutbox(ctx context.Context, limit int) ([]OutboxRecord, error) {
	if limit < 1 {
		limit = 50
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,attempt_id,idempotency_key,attempts FROM delivery_outbox WHERE published_at IS NULL AND available_at <= CURRENT_TIMESTAMP ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OutboxRecord, 0, limit)
	for rows.Next() {
		var record OutboxRecord
		if err := rows.Scan(&record.ID, &record.AttemptID, &record.Key, &record.Attempts); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, record := range result {
		// The row lock above protects this increment until the claim transaction
		// commits. There is intentionally no claimed_at lease: the migration does
		// not expose that column, and duplicate publishes are safe by contract.
		if _, err := tx.Exec(ctx, `UPDATE delivery_outbox SET attempts=attempts+1 WHERE id=$1`, record.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func insertDeliveryOutbox(ctx context.Context, tx pgx.Tx, attempt DeliveryAttempt, idempotencyKey string) error {
	_, _, payload, err := buildDeliveryAttemptOutbox(attempt, idempotencyKey)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO delivery_outbox (id,attempt_id,idempotency_key,topic,event_key,payload,available_at,created_at) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$7)`, "outbox_"+attempt.ID, attempt.ID, idempotencyKey, deliveryAttemptTopic, attempt.ID, payload, attempt.CreatedAt)
	return mapDeliveryError(err)
}

func buildDeliveryAttemptOutbox(attempt DeliveryAttempt, idempotencyKey string) (topic, eventKey string, payload []byte, err error) {
	payload, err = json.Marshal(deliveryAttemptOutboxPayload{
		AttemptID:      attempt.ID,
		MessageID:      attempt.MessageID,
		IdempotencyKey: idempotencyKey,
	})
	return deliveryAttemptTopic, attempt.ID, payload, err
}

func (r *PostgresRepository) MarkOutboxPublished(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE delivery_outbox SET published_at=CURRENT_TIMESTAMP WHERE id=$1`, id)
	return mapDeliveryError(err)
}
func (r *PostgresRepository) MarkOutboxFailed(ctx context.Context, id, reason string) error {
	_, err := r.pool.Exec(ctx, `UPDATE delivery_outbox SET last_error=$2,available_at=CURRENT_TIMESTAMP + INTERVAL '5 seconds' WHERE id=$1`, id, reason)
	return mapDeliveryError(err)
}

func (r *PostgresRepository) scanMessage(ctx context.Context, query string, args ...any) (Message, error) {
	var message Message
	var turns []byte
	var lastError *string
	err := r.pool.QueryRow(ctx, query, args...).Scan(&message.ID, &message.AccountID, &message.Channel, &message.DestinationRef, &message.SnapshotVersion, &turns, &message.Status, &message.Attempts, &lastError, &message.CreatedAt, &message.UpdatedAt)
	if err != nil {
		return Message{}, mapDeliveryError(err)
	}
	if err := json.Unmarshal(turns, &message.Turns); err != nil {
		return Message{}, err
	}
	message.LastErrorCode = lastError
	return message, nil
}

func mapDeliveryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return domain.ErrConflict
		}
		if pgErr.Code == "23503" {
			return domain.ErrNotFound
		}
	}
	return fmt.Errorf("postgres delivery operation: %w", err)
}

var _ Repository = (*PostgresRepository)(nil)
var _ OutboxRepository = (*PostgresRepository)(nil)
var _ IdempotencyReader = (*PostgresRepository)(nil)
var _ WorkerMessageReader = (*PostgresRepository)(nil)
