package delivery

import (
	"context"
	"encoding/json"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func (r *PostgresRepository) GetAutomaticTurnRun(ctx context.Context, accountID, turnID string) (AutomaticTurnRun, error) {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" {
		return AutomaticTurnRun{}, domain.ErrInvalidArgument
	}
	var run AutomaticTurnRun
	err := r.pool.QueryRow(ctx, `
		SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
		language_config_version,delivery_trigger,status,target_count,settled_count,succeeded_count,
		failed_count,fallback_operation_id,created_at,updated_at
		FROM automatic_turn_runs WHERE account_id=$1 AND turn_id=$2`, accountID, turnID).Scan(
		&run.AccountID, &run.TurnID, &run.SessionID, &run.TraceID, &run.TargetLanguage,
		&run.TranslatedText, &run.LanguageConfigVersion, &run.Trigger, &run.Status, &run.TargetCount,
		&run.SettledCount, &run.SucceededCount, &run.FailedCount, &run.FallbackOperationID,
		&run.CreatedAt, &run.UpdatedAt)
	return run, mapDeliveryError(err)
}

func (r *PostgresRepository) ListAutomaticOutputStatus(ctx context.Context, accountID, sessionID string, limit int) ([]AutomaticOutputStatus, error) {
	if r == nil || r.pool == nil || accountID == "" || sessionID == "" || limit <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT turn_id,status,updated_at
		FROM automatic_turn_runs
		WHERE account_id IN (SELECT account_id FROM lingow_account_lineage($1))
		  AND session_id=$2
		ORDER BY updated_at DESC,turn_id DESC
		LIMIT $3`, accountID, sessionID, limit)
	if err != nil {
		return nil, mapDeliveryError(err)
	}
	defer rows.Close()
	statuses := make([]AutomaticOutputStatus, 0, limit)
	for rows.Next() {
		var status AutomaticOutputStatus
		if err := rows.Scan(&status.TurnID, &status.Status, &status.UpdatedAt); err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}

// ScheduleAutomaticTurn persists the aggregate run and all its target work in
// one transaction. A committed run makes FinalTurn scheduling replay-safe even
// when the consumer crashes after the database commit and before Ack.
func (r *PostgresRepository) ScheduleAutomaticTurn(ctx context.Context, record AutomaticTurnScheduleRecord) error {
	if r == nil || r.pool == nil || record.Run.AccountID == "" || record.Run.TurnID == "" ||
		record.Run.SessionID == "" || record.Run.TraceID == "" || record.Run.TargetLanguage == "" ||
		record.Run.TranslatedText == "" || record.Run.LanguageConfigVersion < 1 || record.Run.FallbackOperationID == "" ||
		(record.Run.Trigger != AutomaticTurnTriggerConfiguredRoute && record.Run.Trigger != AutomaticTurnTriggerLongSentence) ||
		record.Run.TargetCount != len(record.Targets) {
		return domain.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, `
		INSERT INTO automatic_turn_runs (
			account_id,turn_id,session_id,trace_id,target_language,translated_text,
			language_config_version,delivery_trigger,status,target_count,fallback_operation_id,created_at,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (account_id,turn_id) DO NOTHING`,
		record.Run.AccountID, record.Run.TurnID, record.Run.SessionID, record.Run.TraceID,
		record.Run.TargetLanguage, record.Run.TranslatedText, record.Run.LanguageConfigVersion,
		record.Run.Trigger, record.Run.Status, record.Run.TargetCount, record.Run.FallbackOperationID,
		record.Run.CreatedAt, record.Run.UpdatedAt)
	if err != nil {
		return mapDeliveryError(err)
	}
	if result.RowsAffected() == 0 {
		var existing AutomaticTurnRun
		err := tx.QueryRow(ctx, `
			SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
			language_config_version,delivery_trigger,status,target_count,settled_count,succeeded_count,
			failed_count,fallback_operation_id,created_at,updated_at
			FROM automatic_turn_runs WHERE account_id=$1 AND turn_id=$2`, record.Run.AccountID, record.Run.TurnID).Scan(
			&existing.AccountID, &existing.TurnID, &existing.SessionID, &existing.TraceID,
			&existing.TargetLanguage, &existing.TranslatedText, &existing.LanguageConfigVersion,
			&existing.Trigger, &existing.Status, &existing.TargetCount, &existing.SettledCount, &existing.SucceededCount,
			&existing.FailedCount, &existing.FallbackOperationID, &existing.CreatedAt, &existing.UpdatedAt)
		if err != nil {
			return mapDeliveryError(err)
		}
		if existing.SessionID != record.Run.SessionID || existing.TraceID != record.Run.TraceID ||
			existing.TargetLanguage != record.Run.TargetLanguage || existing.TranslatedText != record.Run.TranslatedText ||
			existing.LanguageConfigVersion != record.Run.LanguageConfigVersion || existing.Trigger != record.Run.Trigger {
			return domain.ErrConflict
		}
		return tx.Commit(ctx)
	}

	for _, target := range record.Targets {
		turns, err := json.Marshal(target.Message.Turns)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbound_messages (id,account_id,channel,destination_ref,snapshot_version,turns,status,attempts,created_at,updated_at,idempotency_key) VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11)`,
			target.Message.ID, target.Message.AccountID, target.Message.Channel, target.Message.DestinationRef,
			target.Message.SnapshotVersion, turns, target.Message.Status, target.Message.Attempts,
			target.Message.CreatedAt, target.Message.UpdatedAt, target.IdempotencyKey); err != nil {
			return mapDeliveryError(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (id,message_id,attempt_number,status,created_at) VALUES ($1,$2,$3,$4,$5)`,
			target.InitialAttempt.ID, target.InitialAttempt.MessageID, target.InitialAttempt.AttemptNumber,
			target.InitialAttempt.Status, target.InitialAttempt.CreatedAt); err != nil {
			return mapDeliveryError(err)
		}
		if err := insertDeliveryOutbox(ctx, tx, target.InitialAttempt, target.IdempotencyKey); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO automatic_turn_settlements (account_id,turn_id,session_id,target_language,channel,destination_ref,status,message_id,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			target.Settlement.AccountID, target.Settlement.TurnID, target.Settlement.SessionID,
			target.Settlement.TargetLanguage, target.Settlement.Channel, target.Settlement.DestinationRef,
			target.Settlement.Status, target.Message.ID, target.Settlement.CreatedAt, target.Settlement.UpdatedAt); err != nil {
			return mapDeliveryError(err)
		}
	}
	return tx.Commit(ctx)
}

var _ AutomaticTurnSchedulerRepository = (*PostgresRepository)(nil)
