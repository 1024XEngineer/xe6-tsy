package sessions

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ClaimPendingEndIntent leases the oldest due unfinished intent. SKIP LOCKED
// lets multiple API instances scan concurrently without processing one row at
// the same time; an expired lease makes an interrupted claim eligible again.
func (r *PostgresRepository) ClaimPendingEndIntent(
	ctx context.Context,
	params ClaimEndIntentParams,
) (EndIntent, bool, error) {
	if err := r.ready(); err != nil {
		return EndIntent{}, false, err
	}
	if params.WorkerID == "" || !validTimestamp(params.ClaimedAt) ||
		!validTimestamp(params.LeaseExpiresAt) ||
		!params.LeaseExpiresAt.After(params.ClaimedAt) {
		return EndIntent{}, false, ErrInvalidRequest
	}
	leaseDuration := params.LeaseExpiresAt.Sub(params.ClaimedAt)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return EndIntent{}, false, postgresError("begin end recovery claim", err)
	}
	defer tx.Rollback(ctx)

	intent, err := scanEndIntent(tx.QueryRow(ctx, `
		SELECT `+endIntentColumns+`
		FROM voice_session_end_intents
		WHERE completed_at IS NULL
		  AND next_attempt_at <= clock_timestamp()
		  AND (
			recovery_lease_expires_at IS NULL
			OR recovery_lease_expires_at <= clock_timestamp()
		  )
		ORDER BY next_attempt_at, requested_at, session_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return EndIntent{}, false, nil
	}
	if err != nil {
		return EndIntent{}, false, postgresError("scan pending end intent", err)
	}
	claimed, err := scanEndIntent(tx.QueryRow(ctx, `
		UPDATE voice_session_end_intents
		SET recovery_owner = $1,
			recovery_lease_expires_at = clock_timestamp() + ($2::double precision * interval '1 second')
		WHERE session_id = $3 AND account_id = $4
		RETURNING `+endIntentColumns,
		params.WorkerID, leaseDuration.Seconds(),
		intent.SessionID, intent.AccountID))
	if err != nil {
		return EndIntent{}, false, postgresError("claim pending end intent", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndIntent{}, false, postgresError("commit end recovery claim", err)
	}
	return claimed, true, nil
}

// RetryClaimedEndIntent records one failed recovery and releases its lease.
// A stale worker cannot overwrite a claim acquired after lease expiry.
func (r *PostgresRepository) RetryClaimedEndIntent(
	ctx context.Context,
	params RetryEndIntentParams,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		params.WorkerID == "" || params.LastError == "" ||
		params.RetryAfter < 0 {
		return ErrInvalidRequest
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE voice_session_end_intents
		SET retry_count = retry_count + 1,
			last_error = $1,
			next_attempt_at = clock_timestamp() + ($2::double precision * interval '1 second'),
			recovery_owner = NULL,
			recovery_lease_expires_at = NULL
		WHERE session_id = $3 AND account_id = $4
		  AND recovery_owner = $5
		  AND recovery_lease_expires_at > clock_timestamp()
		  AND completed_at IS NULL`,
		params.LastError, params.RetryAfter.Seconds(), params.SessionID,
		params.AccountID, params.WorkerID)
	if err != nil {
		return postgresError("record end recovery retry", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConcurrentTransition
	}
	return nil
}

// CompleteClaimedEndIntent marks a recovery complete only for the current
// owner. It also accepts a completion already committed by a request path.
func (r *PostgresRepository) CompleteClaimedEndIntent(
	ctx context.Context,
	params CompleteClaimedEndIntentParams,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		params.WorkerID == "" || !validTimestamp(params.CompletedAt) {
		return ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return postgresError("begin claimed end completion", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionByOwner(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrEndIntentNotFound
	}
	if err != nil {
		return postgresError("lock session for claimed end completion", err)
	}
	if session.Status != StatusEnded && session.Status != StatusFailed {
		return ErrConcurrentTransition
	}
	intent, found, err := endIntentBySession(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if err != nil {
		return postgresError("lock claimed end intent", err)
	}
	if !found {
		return ErrEndIntentNotFound
	}
	if intent.Completed() {
		return postgresError("commit claimed end completion replay", tx.Commit(ctx))
	}
	if intent.RecoveryOwner == nil || *intent.RecoveryOwner != params.WorkerID {
		return ErrConcurrentTransition
	}
	if params.CompletedAt.Before(intent.RequestedAt) {
		return ErrInvalidRequest
	}
	result, err := tx.Exec(ctx, `
		UPDATE voice_session_end_intents
		SET completed_at = $1,
			recovery_owner = NULL,
			recovery_lease_expires_at = NULL
		WHERE session_id = $2 AND account_id = $3
		  AND recovery_owner = $4
		  AND recovery_lease_expires_at > clock_timestamp()
		  AND completed_at IS NULL`,
		params.CompletedAt.UTC(), params.SessionID, params.AccountID,
		params.WorkerID)
	if err != nil {
		return postgresError("complete claimed end intent", err)
	}
	if result.RowsAffected() != 1 {
		return ErrConcurrentTransition
	}
	return postgresError("commit claimed end completion", tx.Commit(ctx))
}
