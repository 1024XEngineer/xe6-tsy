package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) GetStartOperation(
	ctx context.Context,
	accountID string,
	sessionID string,
	idempotencyKey string,
) (StartOperation, error) {
	if err := r.ready(); err != nil {
		return StartOperation{}, err
	}
	if accountID == "" || sessionID == "" || idempotencyKey == "" {
		return StartOperation{}, ErrInvalidRequest
	}
	session, err := getSessionForActor(
		ctx, r.pool, accountID, sessionID, false,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StartOperation{}, ErrVoiceSessionNotFound
		}
		return StartOperation{}, postgresError("verify start session ownership", err)
	}
	ownerAccountID := session.AccountID
	operation, err := scanStartOperation(r.pool.QueryRow(ctx, `
		SELECT `+startOperationColumns+`
		FROM voice_session_start_operations
		WHERE account_id = $1 AND session_id = $2 AND idempotency_key = $3`,
		ownerAccountID, sessionID, idempotencyKey))
	if err == nil {
		if operation.Status == StartOperationCompensated {
			return StartOperation{}, ErrStartOperationNotFound
		}
		return operation, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StartOperation{}, postgresError("get start operation", err)
	}

	var occupied bool
	err = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM voice_session_start_operations
			WHERE account_id = $1 AND session_id = $2
			  AND status IN ('pending', 'compensating', 'compensation_failed')
		)`, ownerAccountID, sessionID).Scan(&occupied)
	if err != nil {
		return StartOperation{}, postgresError("check unresolved start operation", err)
	}
	if occupied {
		return StartOperation{}, ErrSessionStartInProgress
	}
	return StartOperation{}, ErrStartOperationNotFound
}

func (r *PostgresRepository) BeginStartOperation(
	ctx context.Context,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	if err := r.ready(); err != nil {
		return BeginStartOperationResult{}, err
	}
	if params.OperationID == "" || params.SessionID == "" || params.AccountID == "" ||
		params.IdempotencyKey == "" || params.RequestHash == "" ||
		!validTimestamp(params.CreatedAt) {
		return BeginStartOperationResult{}, ErrInvalidRequest
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return BeginStartOperationResult{}, postgresError("begin start operation", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BeginStartOperationResult{}, ErrVoiceSessionNotFound
	}
	if err != nil {
		return BeginStartOperationResult{}, postgresError("lock session for start", err)
	}
	ownerAccountID := session.AccountID

	var incompleteEnd bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM voice_session_end_intents
			WHERE account_id = $1 AND session_id = $2 AND completed_at IS NULL
		)`, ownerAccountID, params.SessionID).Scan(&incompleteEnd); err != nil {
		return BeginStartOperationResult{}, postgresError("check end interlock for start", err)
	}
	if incompleteEnd {
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}

	existing, found, err := startOperationByAccountKey(
		ctx, tx, ownerAccountID, params.IdempotencyKey,
	)
	if err != nil {
		return BeginStartOperationResult{}, postgresError("read start idempotency key", err)
	}
	if found {
		ownerParams := params
		ownerParams.AccountID = ownerAccountID
		return classifyBeginReplay(existing, ownerParams)
	}
	if session.Status != StatusCreated {
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}
	var unresolved bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM voice_session_start_operations
			WHERE account_id = $1 AND session_id = $2
			  AND status IN ('pending', 'compensating', 'compensation_failed')
		)`, ownerAccountID, params.SessionID).Scan(&unresolved); err != nil {
		return BeginStartOperationResult{}, postgresError("check start interlock", err)
	}
	if unresolved {
		return BeginStartOperationResult{}, ErrSessionStartInProgress
	}

	operation, err := scanStartOperation(tx.QueryRow(ctx, `
		INSERT INTO voice_session_start_operations (
			operation_id, session_id, account_id, idempotency_key, request_hash,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', $6, $6)
		RETURNING `+startOperationColumns,
		params.OperationID, params.SessionID, ownerAccountID,
		params.IdempotencyKey, params.RequestHash, params.CreatedAt.UTC()))
	if err != nil {
		constraint := constraintName(err)
		if constraint == "voice_session_start_operations_key_unique" ||
			constraint == "voice_session_start_operations_one_unfinished_per_session" {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				return BeginStartOperationResult{}, postgresError(
					"rollback start operation race", rollbackErr,
				)
			}
			ownerParams := params
			ownerParams.AccountID = ownerAccountID
			return r.resolveBeginStartRace(ctx, ownerParams, constraint)
		}
		return BeginStartOperationResult{}, postgresError("insert start operation", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BeginStartOperationResult{}, postgresError("commit start operation", err)
	}
	return BeginStartOperationResult{Operation: operation}, nil
}

func (r *PostgresRepository) resolveBeginStartRace(
	ctx context.Context,
	params BeginStartOperationParams,
	constraint string,
) (BeginStartOperationResult, error) {
	if constraint == "voice_session_start_operations_one_unfinished_per_session" {
		return BeginStartOperationResult{}, ErrSessionStartInProgress
	}
	existing, found, err := startOperationByAccountKey(
		ctx, r.pool, params.AccountID, params.IdempotencyKey,
	)
	if err != nil {
		return BeginStartOperationResult{}, postgresError("resolve start key race", err)
	}
	if !found {
		return BeginStartOperationResult{}, fmt.Errorf(
			"sessions postgres resolve start key race: %w", ErrConcurrentTransition,
		)
	}
	return classifyBeginReplay(existing, params)
}

func classifyBeginReplay(
	existing StartOperation,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	if existing.SessionID != params.SessionID ||
		existing.AccountID != params.AccountID ||
		existing.RequestHash != params.RequestHash {
		return BeginStartOperationResult{}, ErrIdempotencyKeyConflict
	}
	switch existing.Status {
	case StartOperationPending, StartOperationCompensating, StartOperationCompleted:
		return BeginStartOperationResult{Operation: existing, Replayed: true}, nil
	case StartOperationCompensationFailed:
		return BeginStartOperationResult{}, ErrSessionStartInProgress
	case StartOperationCompensated:
		return BeginStartOperationResult{}, ErrIdempotencyKeyConflict
	default:
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}
}

func startOperationByAccountKey(
	ctx context.Context,
	db queryRower,
	accountID string,
	idempotencyKey string,
) (StartOperation, bool, error) {
	operation, err := scanStartOperation(db.QueryRow(ctx, `
		SELECT `+startOperationColumns+`
		FROM voice_session_start_operations
		WHERE account_id = $1 AND idempotency_key = $2`,
		accountID, idempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return StartOperation{}, false, nil
	}
	return operation, err == nil, err
}

func (r *PostgresRepository) ClaimStartCompensation(
	ctx context.Context,
	params ClaimStartCompensationParams,
) (ClaimStartCompensationResult, error) {
	if err := r.ready(); err != nil {
		return ClaimStartCompensationResult{}, err
	}
	if params.SessionID == "" || params.AccountID == "" ||
		params.OperationID == "" || params.ClaimID == "" ||
		!validTimestamp(params.ClaimedAt) {
		return ClaimStartCompensationResult{}, ErrInvalidRequest
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ClaimStartCompensationResult{}, postgresError("begin compensation claim", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, params.AccountID, params.SessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimStartCompensationResult{}, ErrVoiceSessionNotFound
	}
	if err != nil {
		return ClaimStartCompensationResult{}, postgresError("lock session for compensation claim", err)
	}
	ownerAccountID := session.AccountID
	if session.Status != StatusCreated {
		return ClaimStartCompensationResult{
			Reason: StartCompensationSessionNotCreated,
		}, nil
	}
	operation, err := scanStartOperation(tx.QueryRow(ctx, `
		SELECT `+startOperationColumns+`
		FROM voice_session_start_operations
		WHERE account_id = $1 AND session_id = $2 AND operation_id = $3
		FOR UPDATE`,
		ownerAccountID, params.SessionID, params.OperationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationMismatch,
		}, nil
	}
	if err != nil {
		return ClaimStartCompensationResult{}, postgresError("lock compensation operation", err)
	}
	if params.ClaimedAt.Before(operation.CreatedAt) {
		return ClaimStartCompensationResult{}, ErrInvalidRequest
	}
	switch operation.Status {
	case StartOperationPending:
		_, err = tx.Exec(ctx, `
			UPDATE voice_session_start_operations
			SET status = 'compensating', compensation_claim_id = $1, updated_at = $2
			WHERE operation_id = $3`,
			params.ClaimID, params.ClaimedAt.UTC(), params.OperationID)
		if err != nil {
			return ClaimStartCompensationResult{}, postgresError("claim compensation", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return ClaimStartCompensationResult{}, postgresError("commit compensation claim", err)
		}
		return ClaimStartCompensationResult{Claimed: true}, nil
	case StartOperationCompensating:
		if operation.CompensationClaimID != nil &&
			*operation.CompensationClaimID == params.ClaimID {
			if err := tx.Commit(ctx); err != nil {
				return ClaimStartCompensationResult{}, postgresError(
					"commit compensation reentry", err,
				)
			}
			return ClaimStartCompensationResult{Claimed: true}, nil
		}
	}
	return ClaimStartCompensationResult{
		Reason: StartCompensationOperationNotPending,
	}, nil
}

func (r *PostgresRepository) CompleteStartCompensation(
	ctx context.Context,
	params CompleteStartCompensationParams,
) error {
	if params.SessionID == "" || params.AccountID == "" ||
		params.OperationID == "" || params.ClaimID == "" ||
		!validTimestamp(params.CompletedAt) {
		return ErrInvalidRequest
	}
	return r.finishStartCompensation(
		ctx, params.AccountID, params.SessionID, params.OperationID,
		params.ClaimID, params.CompletedAt.UTC(), true,
	)
}

func (r *PostgresRepository) FailStartCompensation(
	ctx context.Context,
	params FailStartCompensationParams,
) error {
	if params.SessionID == "" || params.AccountID == "" ||
		params.OperationID == "" || params.ClaimID == "" ||
		!validTimestamp(params.FailedAt) {
		return ErrInvalidRequest
	}
	return r.finishStartCompensation(
		ctx, params.AccountID, params.SessionID, params.OperationID,
		params.ClaimID, params.FailedAt.UTC(), false,
	)
}

func (r *PostgresRepository) finishStartCompensation(
	ctx context.Context,
	actorAccountID string,
	sessionID string,
	operationID string,
	claimID string,
	at time.Time,
	complete bool,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return postgresError("begin compensation outcome", err)
	}
	defer tx.Rollback(ctx)
	session, err := getSessionForActor(
		ctx, tx, actorAccountID, sessionID, true,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVoiceSessionNotFound
	}
	if err != nil {
		return postgresError("lock session for compensation outcome", err)
	}
	ownerAccountID := session.AccountID
	if session.Status != StatusCreated {
		return ErrConcurrentTransition
	}
	operation, err := scanStartOperation(tx.QueryRow(ctx, `
		SELECT `+startOperationColumns+`
		FROM voice_session_start_operations
		WHERE account_id = $1 AND session_id = $2 AND operation_id = $3
		FOR UPDATE`, ownerAccountID, sessionID, operationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConcurrentTransition
	}
	if err != nil {
		return postgresError("lock compensation outcome operation", err)
	}
	if operation.CompensationClaimID == nil ||
		*operation.CompensationClaimID != claimID {
		return ErrConcurrentTransition
	}
	if at.Before(operation.CreatedAt) {
		return ErrInvalidRequest
	}

	target := StartOperationCompensationFailed
	allowed := operation.Status == StartOperationCompensating
	if complete {
		target = StartOperationCompensated
		allowed = operation.Status == StartOperationCompensating ||
			operation.Status == StartOperationCompensationFailed ||
			operation.Status == StartOperationCompensated
	} else if operation.Status == StartOperationCompensationFailed {
		allowed = true
	}
	if !allowed {
		return ErrConcurrentTransition
	}
	if operation.Status != target {
		_, err = tx.Exec(ctx, `
			UPDATE voice_session_start_operations
			SET status = $1, updated_at = $2
			WHERE operation_id = $3`,
			target, at, operationID)
		if err != nil {
			return postgresError("update compensation outcome", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return postgresError("commit compensation outcome", err)
	}
	return nil
}
