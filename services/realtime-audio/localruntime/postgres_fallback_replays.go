package localruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresFallbackPlaybackReplayStore keeps successful fallback playback
// acceptance durable without storing translated text or other message content.
type PostgresFallbackPlaybackReplayStore struct {
	Pool *pgxpool.Pool
}

// Accepted reports whether the exact operation payload completed playback.
func (s PostgresFallbackPlaybackReplayStore) Accepted(ctx context.Context, sessionID, operationID, payloadHash string) (bool, error) {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" {
		return false, fmt.Errorf("fallback playback replay store dependency is required")
	}
	var storedHash string
	err := s.Pool.QueryRow(ctx, `SELECT payload_hash FROM realtime_fallback_playback_operations WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(&storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read fallback playback operation: %w", err)
	}
	if storedHash != payloadHash {
		return false, webrtc.ErrIdempotencyPayloadConflict
	}
	return true, nil
}

// RecordAccepted persists a completed playback or accepts an identical replay.
func (s PostgresFallbackPlaybackReplayStore) RecordAccepted(ctx context.Context, sessionID, operationID, payloadHash string) error {
	if s.Pool == nil || sessionID == "" || operationID == "" || payloadHash == "" {
		return fmt.Errorf("fallback playback replay store dependency is required")
	}
	result, err := s.Pool.Exec(ctx, `INSERT INTO realtime_fallback_playback_operations (session_id,operation_id,payload_hash) VALUES ($1,$2,$3) ON CONFLICT (session_id,operation_id) DO NOTHING`, sessionID, operationID, payloadHash)
	if err != nil {
		return fmt.Errorf("record fallback playback operation: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	accepted, err := s.Accepted(ctx, sessionID, operationID, payloadHash)
	if err != nil {
		return err
	}
	if !accepted {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	return nil
}

var _ controlplane.FallbackPlaybackReplayStore = PostgresFallbackPlaybackReplayStore{}
