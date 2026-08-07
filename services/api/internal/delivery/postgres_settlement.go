package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func settleAutomaticTurnTarget(ctx context.Context, tx pgx.Tx, messageID string, attemptStatus DeliveryAttemptStatus, code *string, now time.Time) error {
	status := AutomaticTurnSettlementFailed
	if attemptStatus == AttemptStatusSucceeded {
		status = AutomaticTurnSettlementSucceeded
	}
	var accountID, turnID string
	err := tx.QueryRow(ctx, `SELECT account_id,turn_id FROM automatic_turn_settlements WHERE message_id=$1`, messageID).Scan(&accountID, &turnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE automatic_turn_settlements SET status=$3,error_code=$4,updated_at=$5 WHERE account_id=$1 AND turn_id=$2 AND message_id=$6 AND status='queued'`, accountID, turnID, status, code, now, messageID); err != nil {
		return err
	}
	var targetCount, settledCount, succeededCount, failedCount int
	if err := tx.QueryRow(ctx, `
		SELECT r.target_count,
			COUNT(*) FILTER (WHERE s.status <> 'queued'),
			COUNT(*) FILTER (WHERE s.status = 'succeeded'),
			COUNT(*) FILTER (WHERE s.status = 'failed')
		FROM automatic_turn_runs r
		LEFT JOIN automatic_turn_settlements s ON s.account_id=r.account_id AND s.turn_id=r.turn_id
		WHERE r.account_id=$1 AND r.turn_id=$2
		GROUP BY r.target_count`, accountID, turnID).Scan(&targetCount, &settledCount, &succeededCount, &failedCount); err != nil {
		return err
	}
	runStatus := automaticTurnRunStatus(targetCount, settledCount, succeededCount, failedCount)
	_, err = tx.Exec(ctx, `UPDATE automatic_turn_runs SET status=$3,settled_count=$4,succeeded_count=$5,failed_count=$6,updated_at=$7 WHERE account_id=$1 AND turn_id=$2`, accountID, turnID, runStatus, settledCount, succeededCount, failedCount, now)
	return err
}
