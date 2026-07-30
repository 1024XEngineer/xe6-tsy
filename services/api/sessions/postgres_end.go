package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) SaveEndIntent(
	ctx context.Context,
	intent EndIntent,
) (EndIntent, bool, error) {
	if err := r.ready(); err != nil {
		return EndIntent{}, false, err
	}
	if intent.SessionID == "" || intent.AccountID == "" ||
		intent.IdempotencyKey == "" || intent.RequestHash == "" ||
		intent.TraceID == "" || !intent.Reason.Valid() ||
		!validTimestamp(intent.RequestedAt) ||
		intent.CompletedAt != nil || intent.RetryCount != 0 ||
		intent.LastError != nil || !intent.NextAttemptAt.IsZero() ||
		intent.RecoveryOwner == nil || *intent.RecoveryOwner == "" ||
		intent.LeaseExpiresAt == nil ||
		!intent.LeaseExpiresAt.After(intent.RequestedAt) {
		return EndIntent{}, false, ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return EndIntent{}, false, postgresError("begin end intent", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, intent.AccountID, intent.SessionID, true,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EndIntent{}, false, ErrVoiceSessionNotFound
		}
		return EndIntent{}, false, postgresError("lock session for end intent", err)
	}
	intent.AccountID = session.AccountID
	var unresolvedStart bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM voice_session_start_operations
			WHERE account_id = $1 AND session_id = $2
			  AND status IN ('pending', 'compensating', 'compensation_failed')
		)`, intent.AccountID, intent.SessionID).Scan(&unresolvedStart); err != nil {
		return EndIntent{}, false, postgresError("check start interlock for end", err)
	}
	if unresolvedStart {
		return EndIntent{}, false, ErrSessionStartInProgress
	}

	existing, found, err := endIntentBySession(
		ctx, tx, intent.AccountID, intent.SessionID, true,
	)
	if err != nil {
		return EndIntent{}, false, postgresError("read end intent", err)
	}
	if found {
		if !existing.MatchesRequest(intent.IdempotencyKey, intent.RequestHash) {
			return EndIntent{}, false, ErrIdempotencyKeyConflict
		}
		if existing.Completed() {
			return existing, true, nil
		}
		if existing.LeaseExpiresAt != nil &&
			existing.LeaseExpiresAt.After(intent.RequestedAt) {
			return EndIntent{}, false, ErrConcurrentTransition
		}
		updated, err := scanEndIntent(tx.QueryRow(ctx, `
			UPDATE voice_session_end_intents
			SET recovery_owner = $1, recovery_lease_expires_at = $2
			WHERE session_id = $3 AND account_id = $4
			RETURNING `+endIntentColumns,
			*intent.RecoveryOwner, intent.LeaseExpiresAt.UTC(),
			intent.SessionID, intent.AccountID))
		if err != nil {
			return EndIntent{}, false, postgresError("claim replayed end intent", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return EndIntent{}, false, postgresError("commit replayed end intent claim", err)
		}
		return updated, true, nil
	}

	saved, err := scanEndIntent(tx.QueryRow(ctx, `
		INSERT INTO voice_session_end_intents (
			session_id, account_id, reason, idempotency_key, request_hash,
			trace_id, requested_at, next_attempt_at, recovery_owner,
			recovery_lease_expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, $9)
		RETURNING `+endIntentColumns,
		intent.SessionID, intent.AccountID, intent.Reason, intent.IdempotencyKey,
		intent.RequestHash, intent.TraceID, intent.RequestedAt.UTC(),
		*intent.RecoveryOwner, intent.LeaseExpiresAt.UTC()))
	if err != nil {
		if constraintName(err) == "voice_session_end_intents_key_unique" {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				return EndIntent{}, false, postgresError(
					"rollback end intent race", rollbackErr,
				)
			}
			return r.resolveEndIntentRace(ctx, intent)
		}
		return EndIntent{}, false, postgresError("insert end intent", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndIntent{}, false, postgresError("commit end intent", err)
	}
	return saved, false, nil
}

func (r *PostgresRepository) resolveEndIntentRace(
	ctx context.Context,
	intent EndIntent,
) (EndIntent, bool, error) {
	stored, err := scanEndIntent(r.pool.QueryRow(ctx, `
		SELECT `+endIntentColumns+`
		FROM voice_session_end_intents
		WHERE account_id = $1 AND idempotency_key = $2`,
		intent.AccountID, intent.IdempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return EndIntent{}, false, fmt.Errorf(
			"sessions postgres resolve end intent race: %w", ErrConcurrentTransition,
		)
	}
	if err != nil {
		return EndIntent{}, false, postgresError("resolve end intent race", err)
	}
	if stored.SessionID != intent.SessionID ||
		stored.RequestHash != intent.RequestHash ||
		!stored.Reason.Valid() {
		return EndIntent{}, false, ErrIdempotencyKeyConflict
	}
	return stored, true, nil
}

func (r *PostgresRepository) GetEndIntent(
	ctx context.Context,
	accountID string,
	sessionID string,
) (EndIntent, error) {
	if err := r.ready(); err != nil {
		return EndIntent{}, err
	}
	if accountID == "" || sessionID == "" {
		return EndIntent{}, ErrInvalidRequest
	}
	session, err := getSessionForActor(
		ctx, r.pool, accountID, sessionID, false,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EndIntent{}, ErrEndIntentNotFound
	}
	if err != nil {
		return EndIntent{}, postgresError("verify end intent session ownership", err)
	}
	intent, found, err := endIntentBySession(
		ctx, r.pool, session.AccountID, sessionID, false,
	)
	if err != nil {
		return EndIntent{}, postgresError("get end intent", err)
	}
	if !found {
		return EndIntent{}, ErrEndIntentNotFound
	}
	return intent, nil
}

func (r *PostgresRepository) CompleteEndIntent(
	ctx context.Context,
	accountID string,
	sessionID string,
	completedAt time.Time,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	if accountID == "" || sessionID == "" || !validTimestamp(completedAt) {
		return ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return postgresError("begin end intent completion", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, accountID, sessionID, true,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEndIntentNotFound
		}
		return postgresError("lock session for end intent completion", err)
	}
	ownerAccountID := session.AccountID
	if session.Status != StatusEnded && session.Status != StatusFailed {
		return ErrConcurrentTransition
	}
	intent, found, err := endIntentBySession(
		ctx, tx, ownerAccountID, sessionID, true,
	)
	if err != nil {
		return postgresError("lock end intent for completion", err)
	}
	if !found {
		return ErrEndIntentNotFound
	}
	if completedAt.Before(intent.RequestedAt) {
		return ErrInvalidRequest
	}
	if intent.CompletedAt == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE voice_session_end_intents
			SET completed_at = $1,
				recovery_owner = NULL,
				recovery_lease_expires_at = NULL
			WHERE account_id = $2 AND session_id = $3`,
			completedAt.UTC(), ownerAccountID, sessionID); err != nil {
			return postgresError("complete end intent", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return postgresError("commit end intent completion", err)
	}
	return nil
}

func endIntentBySession(
	ctx context.Context,
	db queryRower,
	accountID string,
	sessionID string,
	forUpdate bool,
) (EndIntent, bool, error) {
	query := `SELECT ` + endIntentColumns + `
		FROM voice_session_end_intents
		WHERE account_id = $1 AND session_id = $2`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	intent, err := scanEndIntent(db.QueryRow(ctx, query, accountID, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return EndIntent{}, false, nil
	}
	return intent, err == nil, err
}

func (r *PostgresRepository) TransitionToActive(
	ctx context.Context,
	params StartTransitionParams,
) (VoiceSession, bool, error) {
	if err := r.ready(); err != nil {
		return VoiceSession{}, false, err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		params.OperationID == "" || params.IdempotencyKey == "" ||
		params.RequestHash == "" || params.Expected != StatusCreated ||
		!validTimestamp(params.StartedAt) {
		return VoiceSession{}, false, ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VoiceSession{}, false, postgresError("begin active transition", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return VoiceSession{}, false, ErrVoiceSessionNotFound
	}
	if err != nil {
		return VoiceSession{}, false, postgresError("lock session for activation", err)
	}
	ownerAccountID := session.AccountID
	operation, err := scanStartOperation(tx.QueryRow(ctx, `
		SELECT `+startOperationColumns+`
		FROM voice_session_start_operations
		WHERE operation_id = $1 AND account_id = $2 AND session_id = $3
		FOR UPDATE`, params.OperationID, ownerAccountID, params.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if err != nil {
		return VoiceSession{}, false, postgresError("lock operation for activation", err)
	}
	if operation.AccountID != ownerAccountID ||
		operation.SessionID != params.SessionID ||
		operation.IdempotencyKey != params.IdempotencyKey {
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if operation.RequestHash != params.RequestHash {
		return VoiceSession{}, false, ErrIdempotencyKeyConflict
	}
	if operation.Status == StartOperationCompleted && session.Status == StatusActive {
		if err := tx.Commit(ctx); err != nil {
			return VoiceSession{}, false, postgresError("commit activation replay", err)
		}
		return session, true, nil
	}
	if operation.Status != StartOperationPending ||
		session.Status != params.Expected ||
		params.StartedAt.Before(session.CreatedAt) ||
		params.StartedAt.Before(operation.CreatedAt) {
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if _, err := tx.Exec(ctx, `
		UPDATE voice_sessions
		SET status = 'active', started_at = $1
		WHERE id = $2 AND account_id = $3`,
		params.StartedAt.UTC(), params.SessionID, ownerAccountID); err != nil {
		return VoiceSession{}, false, postgresError("activate session", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE voice_session_start_operations
		SET status = 'completed', started_at = $1, updated_at = $1
		WHERE operation_id = $2`,
		params.StartedAt.UTC(), params.OperationID); err != nil {
		return VoiceSession{}, false, postgresError("complete start operation", err)
	}
	session, err = getSessionByOwner(
		ctx, tx, ownerAccountID, params.SessionID, false,
	)
	if err != nil {
		return VoiceSession{}, false, postgresError("read activated session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VoiceSession{}, false, postgresError("commit active transition", err)
	}
	return session, false, nil
}

func (r *PostgresRepository) TransitionToEnded(
	ctx context.Context,
	params EndTransitionParams,
) (VoiceSession, error) {
	if err := r.ready(); err != nil {
		return VoiceSession{}, err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		(params.Expected != StatusCreated && params.Expected != StatusActive) ||
		!params.EndReason.Valid() || !validTimestamp(params.EndedAt) {
		return VoiceSession{}, ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VoiceSession{}, postgresError("begin ended transition", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	if err != nil {
		return VoiceSession{}, postgresError("lock session for end", err)
	}
	ownerAccountID := session.AccountID
	if session.Status != params.Expected ||
		params.EndedAt.Before(session.CreatedAt) ||
		(session.StartedAt != nil && params.EndedAt.Before(*session.StartedAt)) {
		return VoiceSession{}, ErrConcurrentTransition
	}
	if _, err := tx.Exec(ctx, `
		UPDATE voice_sessions
		SET status = 'ended', ended_at = $1, failure_error_code = NULL
		WHERE id = $2 AND account_id = $3`,
		params.EndedAt.UTC(), params.SessionID, ownerAccountID); err != nil {
		return VoiceSession{}, postgresError("end session", err)
	}
	session, err = getSessionByOwner(
		ctx, tx, ownerAccountID, params.SessionID, false,
	)
	if err != nil {
		return VoiceSession{}, postgresError("read ended session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VoiceSession{}, postgresError("commit ended transition", err)
	}
	return session, nil
}

func (r *PostgresRepository) TransitionToFailed(
	ctx context.Context,
	params FailureTransitionParams,
) (VoiceSession, error) {
	if err := r.ready(); err != nil {
		return VoiceSession{}, err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		params.Expected != StatusActive || params.ErrorCode == "" ||
		!validTimestamp(params.FailedAt) {
		return VoiceSession{}, ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return VoiceSession{}, postgresError("begin failed transition", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	if err != nil {
		return VoiceSession{}, postgresError("lock session for failure", err)
	}
	ownerAccountID := session.AccountID
	if session.Status != StatusActive || session.StartedAt == nil ||
		params.FailedAt.Before(*session.StartedAt) {
		return VoiceSession{}, ErrConcurrentTransition
	}
	if _, err := tx.Exec(ctx, `
		UPDATE voice_sessions
		SET status = 'failed', failure_error_code = $1, ended_at = $2
		WHERE id = $3 AND account_id = $4`,
		params.ErrorCode, params.FailedAt.UTC(), params.SessionID, ownerAccountID); err != nil {
		return VoiceSession{}, postgresError("fail session", err)
	}
	session, err = getSessionByOwner(
		ctx, tx, ownerAccountID, params.SessionID, false,
	)
	if err != nil {
		return VoiceSession{}, postgresError("read failed session", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return VoiceSession{}, postgresError("commit failed transition", err)
	}
	return session, nil
}
