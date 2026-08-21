package localruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const fallbackPlaybackClaimLease = 5 * time.Minute

// PostgresFallbackPlaybackReplayStore keeps accepted fallback operations
// durable without storing translated text or other message content.
type PostgresFallbackPlaybackReplayStore struct {
	Pool fallbackPlaybackQueryer
}

type fallbackPlaybackQueryer interface {
	Begin(context.Context) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func newFallbackPlaybackClaimToken() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate fallback playback claim token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// Claim durably reserves an operation before media I/O. Expired processing
// claims become reclaimable and are retried by the new owner.
func (s PostgresFallbackPlaybackReplayStore) Claim(ctx context.Context, sessionID, operationID, payloadHash string) (controlplane.FallbackPlaybackClaim, error) {
	var claim controlplane.FallbackPlaybackClaim
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" {
		return claim, fmt.Errorf("fallback playback replay store dependency is required")
	}
	claimToken, err := newFallbackPlaybackClaimToken()
	if err != nil {
		return claim, err
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `
			INSERT INTO realtime_fallback_playback_operations
				(session_id,operation_id,payload_hash,status,processing_started_at,processing_token)
			VALUES ($1,$2,$3,'processing',CURRENT_TIMESTAMP,$4)
			ON CONFLICT (session_id,operation_id) DO NOTHING`, sessionID, operationID, payloadHash, claimToken)
		if err != nil {
			return fmt.Errorf("claim fallback playback operation: %w", err)
		}
		if result.RowsAffected() == 1 {
			claim.Status = controlplane.FallbackPlaybackClaimed
			claim.Token = claimToken
			return nil
		}

		var storedHash, status string
		var storedToken *string
		var processingStartedAt *time.Time
		var databaseNow time.Time
		if err := tx.QueryRow(ctx, `
			SELECT payload_hash,status,processing_started_at,processing_token,CURRENT_TIMESTAMP
			FROM realtime_fallback_playback_operations
			WHERE session_id=$1 AND operation_id=$2 FOR UPDATE`, sessionID, operationID).
			Scan(&storedHash, &status, &processingStartedAt, &storedToken, &databaseNow); err != nil {
			return fmt.Errorf("read claimed fallback playback operation: %w", err)
		}
		if storedHash != payloadHash {
			return webrtc.ErrIdempotencyPayloadConflict
		}
		switch status {
		case "accepted":
			claim.Status = controlplane.FallbackPlaybackAccepted
		case "reclaimable":
			if _, err := tx.Exec(ctx, `
				UPDATE realtime_fallback_playback_operations
				SET status='processing',accepted_at=NULL,processing_started_at=CURRENT_TIMESTAMP,processing_token=$3
				WHERE session_id=$1 AND operation_id=$2 AND status='reclaimable'`, sessionID, operationID, claimToken); err != nil {
				return fmt.Errorf("reclaim fallback playback claim: %w", err)
			}
			claim.Status = controlplane.FallbackPlaybackClaimed
			claim.Token = claimToken
		case "processing":
			if processingStartedAt == nil || databaseNow.Before(processingStartedAt.Add(fallbackPlaybackClaimLease)) {
				claim.Status = controlplane.FallbackPlaybackProcessing
				return nil
			}
			if _, err := tx.Exec(ctx, `
				UPDATE realtime_fallback_playback_operations
				SET status='reclaimable',accepted_at=NULL,processing_started_at=NULL,processing_token=NULL
				WHERE session_id=$1 AND operation_id=$2 AND status='processing'`, sessionID, operationID); err != nil {
				return fmt.Errorf("mark expired fallback playback claim reclaimable: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE realtime_fallback_playback_operations
				SET status='processing',processing_started_at=CURRENT_TIMESTAMP,processing_token=$3
				WHERE session_id=$1 AND operation_id=$2 AND status='reclaimable'`, sessionID, operationID, claimToken); err != nil {
				return fmt.Errorf("reclaim expired fallback playback claim: %w", err)
			}
			claim.Status = controlplane.FallbackPlaybackClaimed
			claim.Token = claimToken
		default:
			return fmt.Errorf("fallback playback operation has unsupported status %q", status)
		}
		return nil
	})
	return claim, err
}

func (s PostgresFallbackPlaybackReplayStore) Renew(ctx context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" || claimToken == "" {
		return fmt.Errorf("fallback playback replay store dependency is required")
	}
	result, err := s.Pool.Exec(ctx, `
		UPDATE realtime_fallback_playback_operations
		SET processing_started_at=CURRENT_TIMESTAMP
		WHERE session_id=$1 AND operation_id=$2 AND payload_hash=$3
			AND status='processing' AND processing_token=$4`, sessionID, operationID, payloadHash, claimToken)
	if err != nil {
		return fmt.Errorf("renew fallback playback operation: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	var storedHash, status string
	var storedToken *string
	err = s.Pool.QueryRow(ctx, `
		SELECT payload_hash,status,processing_token
		FROM realtime_fallback_playback_operations
		WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(&storedHash, &status, &storedToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("renew fallback playback operation: operation was not claimed")
	}
	if err != nil {
		return fmt.Errorf("read renewed fallback playback operation: %w", err)
	}
	if storedHash != payloadHash {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	if storedToken == nil || *storedToken != claimToken {
		return fmt.Errorf("renew fallback playback operation: claim is no longer owned")
	}
	return fmt.Errorf("renew fallback playback operation: status is %q", status)
}

// Complete records successful playback and accepts repeated completion of an
// operation that is already accepted.
func (s PostgresFallbackPlaybackReplayStore) Complete(ctx context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" || claimToken == "" {
		return fmt.Errorf("fallback playback replay store dependency is required")
	}
	result, err := s.Pool.Exec(ctx, `
		UPDATE realtime_fallback_playback_operations
		SET status='accepted',accepted_at=CURRENT_TIMESTAMP,processing_started_at=NULL,processing_token=NULL
		WHERE session_id=$1 AND operation_id=$2 AND payload_hash=$3 AND status='processing' AND processing_token=$4`, sessionID, operationID, payloadHash, claimToken)
	if err != nil {
		return fmt.Errorf("complete fallback playback operation: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	var storedHash, status string
	var storedToken *string
	err = s.Pool.QueryRow(ctx, `
		SELECT payload_hash,status,processing_token
		FROM realtime_fallback_playback_operations
		WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(&storedHash, &status, &storedToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("complete fallback playback operation: operation was not claimed")
	}
	if err != nil {
		return fmt.Errorf("read completed fallback playback operation: %w", err)
	}
	if storedHash != payloadHash {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	if status == "accepted" {
		return nil
	}
	if storedToken == nil || *storedToken != claimToken {
		return fmt.Errorf("complete fallback playback operation: claim is no longer owned")
	}
	return fmt.Errorf("complete fallback playback operation: status is %q", status)
}

// Abort releases a claim only when the caller still owns the processing row.
// A missing or already accepted row is treated as idempotent; a different
// processing token is never modified by a stale caller.
func (s PostgresFallbackPlaybackReplayStore) Abort(ctx context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" || claimToken == "" {
		return fmt.Errorf("fallback playback replay store dependency is required")
	}
	result, err := s.Pool.Exec(ctx, `
		DELETE FROM realtime_fallback_playback_operations
		WHERE session_id=$1 AND operation_id=$2 AND payload_hash=$3 AND status='processing' AND processing_token=$4`, sessionID, operationID, payloadHash, claimToken)
	if err != nil {
		return fmt.Errorf("abort fallback playback operation: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	var storedHash, status string
	var storedToken *string
	err = s.Pool.QueryRow(ctx, `
		SELECT payload_hash,status,processing_token
		FROM realtime_fallback_playback_operations
		WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(&storedHash, &status, &storedToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read aborted fallback playback operation: %w", err)
	}
	if storedHash != payloadHash {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	if status == "accepted" {
		return nil
	}
	if storedToken == nil || *storedToken != claimToken {
		return fmt.Errorf("abort fallback playback operation: claim is no longer owned")
	}
	return fmt.Errorf("abort fallback playback operation: status is %q", status)
}

var _ controlplane.FallbackPlaybackReplayStore = PostgresFallbackPlaybackReplayStore{}
