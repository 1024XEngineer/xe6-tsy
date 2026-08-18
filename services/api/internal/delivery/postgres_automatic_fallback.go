package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
)

const automaticFallbackClaimLease = 5 * time.Minute

func (r *PostgresRepository) ListAutomaticTurnRecoveryCandidates(ctx context.Context, limit int) ([]AutomaticTurnRun, error) {
	if r == nil || r.pool == nil || limit <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
		language_config_version,delivery_trigger,status,target_count,settled_count,succeeded_count,
		failed_count,fallback_operation_id,created_at,updated_at
		FROM automatic_turn_runs
		WHERE (target_count=0 AND status IN ('pending','fallback_pending'))
		   OR (target_count>0 AND failed_count=target_count AND status IN ('failed','fallback_pending'))
		ORDER BY updated_at ASC, turn_id ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AutomaticTurnRun, 0)
	for rows.Next() {
		var run AutomaticTurnRun
		if err := rows.Scan(&run.AccountID, &run.TurnID, &run.SessionID, &run.TraceID, &run.TargetLanguage, &run.TranslatedText, &run.LanguageConfigVersion, &run.Trigger, &run.Status, &run.TargetCount, &run.SettledCount, &run.SucceededCount, &run.FailedCount, &run.FallbackOperationID, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) ListAutomaticTurnRestoreCandidates(ctx context.Context, limit int) ([]AutomaticTurnRun, error) {
	if r == nil || r.pool == nil || limit <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `
		SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
		language_config_version,delivery_trigger,status,target_count,settled_count,succeeded_count,
		failed_count,fallback_operation_id,created_at,updated_at
		FROM automatic_turn_runs WHERE status='fallback_played'
		ORDER BY updated_at ASC, turn_id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AutomaticTurnRun, 0)
	for rows.Next() {
		var run AutomaticTurnRun
		if err := rows.Scan(&run.AccountID, &run.TurnID, &run.SessionID, &run.TraceID, &run.TargetLanguage, &run.TranslatedText, &run.LanguageConfigVersion, &run.Trigger, &run.Status, &run.TargetCount, &run.SettledCount, &run.SucceededCount, &run.FailedCount, &run.FallbackOperationID, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) ClaimAutomaticTurnFallback(ctx context.Context, accountID, turnID string) (AutomaticTurnRun, bool, error) {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" {
		return AutomaticTurnRun{}, false, domain.ErrInvalidArgument
	}
	var run AutomaticTurnRun
	claimed := false
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT account_id,turn_id,session_id,trace_id,target_language,translated_text,
			language_config_version,delivery_trigger,status,target_count,settled_count,succeeded_count,
			failed_count,fallback_operation_id,created_at,updated_at
			FROM automatic_turn_runs WHERE account_id=$1 AND turn_id=$2 FOR UPDATE`, accountID, turnID).Scan(
			&run.AccountID, &run.TurnID, &run.SessionID, &run.TraceID, &run.TargetLanguage,
			&run.TranslatedText, &run.LanguageConfigVersion, &run.Trigger, &run.Status, &run.TargetCount,
			&run.SettledCount, &run.SucceededCount, &run.FailedCount, &run.FallbackOperationID,
			&run.CreatedAt, &run.UpdatedAt)
		if err != nil {
			return mapDeliveryError(err)
		}
		now := time.Now().UTC()
		if run.Status == AutomaticTurnRunFallbackPending {
			// A fresh pending row is owned by another worker. An expired lease may
			// retry the same operation because realtime durably deduplicates it.
			if now.Before(run.UpdatedAt.Add(automaticFallbackClaimLease)) {
				return nil
			}
		} else {
			eligible := (run.TargetCount == 0 && run.Status == AutomaticTurnRunPending) ||
				(run.TargetCount > 0 && run.Status == AutomaticTurnRunFailed && run.FailedCount == run.TargetCount)
			if !eligible {
				return domain.ErrConflict
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE automatic_turn_runs SET status='fallback_pending',updated_at=$3 WHERE account_id=$1 AND turn_id=$2`, accountID, turnID, now); err != nil {
			return err
		}
		run.Status = AutomaticTurnRunFallbackPending
		run.UpdatedAt = now
		claimed = true
		return nil
	})
	return run, claimed, err
}

func (r *PostgresRepository) MarkAutomaticTurnFallbackPlayed(ctx context.Context, accountID, turnID string) error {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" {
		return domain.ErrInvalidArgument
	}
	result, err := r.pool.Exec(ctx, `UPDATE automatic_turn_runs SET status='fallback_played',updated_at=$3 WHERE account_id=$1 AND turn_id=$2 AND status='fallback_pending'`, accountID, turnID, time.Now().UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var status AutomaticTurnRunStatus
	if err := r.pool.QueryRow(ctx, `SELECT status FROM automatic_turn_runs WHERE account_id=$1 AND turn_id=$2`, accountID, turnID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	} else if status == AutomaticTurnRunFallbackPlayed {
		return nil
	}
	return domain.ErrConflict
}

func (r *PostgresRepository) MarkAutomaticTurnRestored(ctx context.Context, accountID, turnID string) error {
	if r == nil || r.pool == nil || accountID == "" || turnID == "" {
		return domain.ErrInvalidArgument
	}
	result, err := r.pool.Exec(ctx, `UPDATE automatic_turn_runs SET status='restored',updated_at=$3 WHERE account_id=$1 AND turn_id=$2 AND status='fallback_played'`, accountID, turnID, time.Now().UTC())
	if err != nil {
		return err
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var status AutomaticTurnRunStatus
	if err := r.pool.QueryRow(ctx, `SELECT status FROM automatic_turn_runs WHERE account_id=$1 AND turn_id=$2`, accountID, turnID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	} else if status == AutomaticTurnRunRestored {
		return nil
	}
	return domain.ErrConflict
}

var _ AutomaticTurnFallbackRepository = (*PostgresRepository)(nil)
