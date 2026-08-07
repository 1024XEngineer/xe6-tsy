package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

func (r *PostgresRepository) ListAutomaticTurnRetryCandidates(ctx context.Context, limit int) ([]AutomaticTurnRun, error) {
	if r == nil || r.pool == nil || limit <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
		language_config_version,status,target_count,settled_count,succeeded_count,
		failed_count,fallback_operation_id,created_at,updated_at
		FROM automatic_turn_runs
		WHERE status='partially_succeeded' AND succeeded_count>0 AND failed_count>0
		  AND EXISTS (
			SELECT 1 FROM automatic_turn_settlements s
			JOIN outbound_messages m ON m.id=s.message_id
			WHERE s.account_id=automatic_turn_runs.account_id AND s.turn_id=automatic_turn_runs.turn_id
			  AND s.status='failed' AND m.attempts < $2
		  )
		ORDER BY updated_at ASC, turn_id ASC
		LIMIT $1`, limit, maxAutomaticTargetAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AutomaticTurnRun, 0)
	for rows.Next() {
		var run AutomaticTurnRun
		if err := rows.Scan(&run.AccountID, &run.TurnID, &run.SessionID, &run.TraceID, &run.TargetLanguage, &run.TranslatedText, &run.LanguageConfigVersion, &run.Status, &run.TargetCount, &run.SettledCount, &run.SucceededCount, &run.FailedCount, &run.FallbackOperationID, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) ListAutomaticTurnSettlements(ctx context.Context, accountID, turnID string) ([]AutomaticTurnSettlement, error) {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT account_id,turn_id,session_id,target_language,channel,destination_ref,
		status,message_id,error_code,created_at,updated_at
		FROM automatic_turn_settlements WHERE account_id=$1 AND turn_id=$2
		ORDER BY channel,destination_ref`, accountID, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AutomaticTurnSettlement, 0)
	for rows.Next() {
		var settlement AutomaticTurnSettlement
		if err := rows.Scan(&settlement.AccountID, &settlement.TurnID, &settlement.SessionID, &settlement.TargetLanguage, &settlement.Channel, &settlement.DestinationRef, &settlement.Status, &settlement.MessageID, &settlement.ErrorCode, &settlement.CreatedAt, &settlement.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, settlement)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) RetryAutomaticTurnTarget(ctx context.Context, accountID, turnID, messageID, idempotencyKey string) (Message, error) {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" || messageID == "" || idempotencyKey == "" {
		return Message{}, domain.ErrInvalidArgument
	}
	now := time.Now().UTC()
	var message Message
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var settlementStatus AutomaticTurnSettlementStatus
		if err := tx.QueryRow(ctx, `SELECT status FROM automatic_turn_settlements WHERE account_id=$1 AND turn_id=$2 AND message_id=$3 FOR UPDATE`, accountID, turnID, messageID).Scan(&settlementStatus); err != nil {
			return mapDeliveryError(err)
		}
		if settlementStatus == AutomaticTurnSettlementSucceeded {
			return domain.ErrConflict
		}
		if settlementStatus == AutomaticTurnSettlementQueued {
			return nil
		}
		var status MessageStatus
		var attempts int
		var lastErrorCode *string
		var channel Channel
		var destinationRef string
		if err := tx.QueryRow(ctx, `SELECT account_id,channel,destination_ref,status,attempts,last_error_code FROM outbound_messages WHERE id=$1 AND account_id=$2 FOR UPDATE`, messageID, accountID).Scan(&message.AccountID, &channel, &destinationRef, &status, &attempts, &lastErrorCode); err != nil {
			return mapDeliveryError(err)
		}
		if status != MessageStatusFailed || attempts >= maxAutomaticTargetAttempts || (lastErrorCode != nil && *lastErrorCode == deliveryUnknownErrorCode) {
			return domain.ErrConflict
		}
		var existingMessageID string
		err := tx.QueryRow(ctx, `SELECT message_id FROM delivery_retry_requests WHERE account_id=$1 AND idempotency_key=$2`, accountID, idempotencyKey).Scan(&existingMessageID)
		if err == nil {
			if existingMessageID != messageID {
				return domain.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return mapDeliveryError(err)
		}
		attempt := DeliveryAttempt{ID: "attempt_" + ulid.Make().String(), MessageID: messageID, AttemptNumber: attempts + 1, Status: AttemptStatusQueued, CreatedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (id,message_id,attempt_number,status,created_at) VALUES ($1,$2,$3,$4,$5)`, attempt.ID, attempt.MessageID, attempt.AttemptNumber, attempt.Status, attempt.CreatedAt); err != nil {
			return mapDeliveryError(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_retry_requests (account_id,idempotency_key,message_id,attempt_id,created_at) VALUES ($1,$2,$3,$4,$5)`, accountID, idempotencyKey, messageID, attempt.ID, now); err != nil {
			return mapDeliveryError(err)
		}
		if err := insertDeliveryOutbox(ctx, tx, attempt, idempotencyKey); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE outbound_messages SET status=$2,attempts=$3,last_error_code=NULL,updated_at=$4 WHERE id=$1`, messageID, MessageStatusRetrying, attempt.AttemptNumber, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE automatic_turn_settlements SET status='queued',error_code=NULL,updated_at=$4 WHERE account_id=$1 AND turn_id=$2 AND message_id=$3`, accountID, turnID, messageID, now); err != nil {
			return err
		}
		if err := refreshAutomaticTurnRun(ctx, tx, accountID, turnID, now); err != nil {
			return err
		}
		message.Channel = channel
		message.DestinationRef = destinationRef
		message.Status = MessageStatusRetrying
		message.Attempts = attempt.AttemptNumber
		message.LastErrorCode = nil
		message.UpdatedAt = now
		return nil
	})
	if err != nil {
		return Message{}, fmt.Errorf("retry automatic target transaction: %w", err)
	}
	return r.GetMessage(ctx, accountID, messageID)
}

var _ AutomaticTurnRetryRepository = (*PostgresRepository)(nil)
