package localruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const fallbackPlaybackClaimLease = 5 * time.Minute

// PostgresFallbackPlaybackReplayStore keeps accepted fallback operations
// durable without storing translated text or other message content.
type PostgresFallbackPlaybackReplayStore struct {
	Pool *pgxpool.Pool
}

// Claim durably reserves an operation before media I/O. Expired processing
// claims are conservatively settled as accepted because playback may already
// have reached the audio device when the owner disappeared.
func (s PostgresFallbackPlaybackReplayStore) Claim(ctx context.Context, sessionID, operationID, payloadHash string) (controlplane.FallbackPlaybackClaim, error) {
	var claim controlplane.FallbackPlaybackClaim
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" {
		return claim, fmt.Errorf("fallback playback replay store dependency is required")
	}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `
			INSERT INTO realtime_fallback_playback_operations
				(session_id,operation_id,payload_hash,status,processing_started_at)
			VALUES ($1,$2,$3,'processing',CURRENT_TIMESTAMP)
			ON CONFLICT (session_id,operation_id) DO NOTHING`, sessionID, operationID, payloadHash)
		if err != nil {
			return fmt.Errorf("claim fallback playback operation: %w", err)
		}
		if result.RowsAffected() == 1 {
			claim.Status = controlplane.FallbackPlaybackClaimed
			return nil
		}

		var storedHash, status string
		var processingStartedAt *time.Time
		var databaseNow time.Time
		if err := tx.QueryRow(ctx, `
			SELECT payload_hash,status,processing_started_at,CURRENT_TIMESTAMP
			FROM realtime_fallback_playback_operations
			WHERE session_id=$1 AND operation_id=$2 FOR UPDATE`, sessionID, operationID).
			Scan(&storedHash, &status, &processingStartedAt, &databaseNow); err != nil {
			return fmt.Errorf("read claimed fallback playback operation: %w", err)
		}
		if storedHash != payloadHash {
			return webrtc.ErrIdempotencyPayloadConflict
		}
		switch status {
		case "accepted":
			claim.Status = controlplane.FallbackPlaybackAccepted
		case "processing":
			if processingStartedAt == nil || databaseNow.Before(processingStartedAt.Add(fallbackPlaybackClaimLease)) {
				claim.Status = controlplane.FallbackPlaybackProcessing
				return nil
			}
			if _, err := tx.Exec(ctx, `
				UPDATE realtime_fallback_playback_operations
				SET status='accepted',accepted_at=CURRENT_TIMESTAMP,processing_started_at=NULL
				WHERE session_id=$1 AND operation_id=$2 AND status='processing'`, sessionID, operationID); err != nil {
				return fmt.Errorf("settle expired fallback playback claim: %w", err)
			}
			claim.Status = controlplane.FallbackPlaybackAccepted
		default:
			return fmt.Errorf("fallback playback operation has unsupported status %q", status)
		}
		return nil
	})
	return claim, err
}

// Complete records successful playback and accepts repeated completion after
// an expired claim has already been conservatively settled.
func (s PostgresFallbackPlaybackReplayStore) Complete(ctx context.Context, sessionID, operationID, payloadHash string) error {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" {
		return fmt.Errorf("fallback playback replay store dependency is required")
	}
	result, err := s.Pool.Exec(ctx, `
		UPDATE realtime_fallback_playback_operations
		SET status='accepted',accepted_at=CURRENT_TIMESTAMP,processing_started_at=NULL
		WHERE session_id=$1 AND operation_id=$2 AND payload_hash=$3 AND status='processing'`, sessionID, operationID, payloadHash)
	if err != nil {
		return fmt.Errorf("complete fallback playback operation: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}

	var storedHash, status string
	err = s.Pool.QueryRow(ctx, `
		SELECT payload_hash,status
		FROM realtime_fallback_playback_operations
		WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(&storedHash, &status)
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
	return fmt.Errorf("complete fallback playback operation: status is %q", status)
}

var _ controlplane.FallbackPlaybackReplayStore = PostgresFallbackPlaybackReplayStore{}
