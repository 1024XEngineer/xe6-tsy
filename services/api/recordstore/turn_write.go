package recordstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TurnWriter persists final translation events and their mutable attribution fields.
type TurnWriter struct {
	pool *pgxpool.Pool
}

func NewTurnWriter(pool *pgxpool.Pool) *TurnWriter {
	return &TurnWriter{pool: pool}
}

// StoreFinalTurn treats all three event identity keys as one immutable replay boundary.
func (w *TurnWriter) StoreFinalTurn(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	payloadHash, err := recordsv1.FinalTurnEventPayloadHash(event)
	if err != nil {
		return err
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin final turn transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	result, err := tx.Exec(ctx, insertFinalTurnQuery,
		event.TurnID,
		event.EventID,
		payloadHash[:],
		event.SessionID,
		event.ParticipantID,
		event.SpeakerCode,
		event.SpeakerLabelSnapshot,
		event.ProviderSpeakerID,
		event.SequenceNo,
		event.SourceLanguage,
		event.TargetLanguage,
		event.LanguageConfigVersion,
		event.SourceText,
		event.TranslatedText,
		event.SpeakerConfidence,
		event.AttributionStatus,
		event.StartedAt.UTC(),
		event.EndedAt.UTC(),
		event.OccurredAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert final turn: %w", MapError(err))
	}
	if result.RowsAffected() == 0 {
		if err := verifyFinalTurnReplay(ctx, tx, event); err != nil {
			return err
		}
	} else if shouldEnqueueAttribution(event) {
		// Enqueue one durable attribution task in the same transaction so a pending or
		// provisional turn with provider evidence is considered by the async resolver.
		if err := w.enqueueAttribution(ctx, tx, event.TurnID, event.SessionID); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit final turn transaction: %w", err)
	}
	return nil
}

// enqueueAttribution inserts one async attribution task for the stored turn. The account is
// resolved from the owning session inside the insert so the task is always account-scoped.
func (w *TurnWriter) enqueueAttribution(ctx context.Context, tx pgx.Tx, turnID, sessionID string) error {
	if err := NewAttributionTaskStore(w.pool).Enqueue(ctx, tx, turnID, sessionID); err != nil {
		return fmt.Errorf("enqueue attribution task: %w", err)
	}
	return nil
}

func needsAsyncAttribution(status recordsv1.AttributionStatus) bool {
	return status == recordsv1.AttributionPending || status == recordsv1.AttributionProvisional
}

func shouldEnqueueAttribution(event recordsv1.FinalTurnEvent) bool {
	return needsAsyncAttribution(event.AttributionStatus) &&
		event.ProviderSpeakerID != nil && strings.TrimSpace(*event.ProviderSpeakerID) != ""
}

func verifyFinalTurnReplay(
	ctx context.Context,
	tx pgx.Tx,
	event recordsv1.FinalTurnEvent,
) error {
	rows, err := tx.Query(ctx, finalTurnReplayQuery,
		event.EventID,
		event.TurnID,
		event.SessionID,
		event.SequenceNo,
	)
	if err != nil {
		return fmt.Errorf("read final turn replay: %w", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		found = true
		var storedHash []byte
		if err := rows.Scan(&storedHash); err != nil {
			return fmt.Errorf("scan final turn replay: %w", err)
		}
		matches, err := recordsv1.FinalTurnEventPayloadHashMatches(event, storedHash)
		if err != nil {
			return fmt.Errorf("hash final turn replay: %w", err)
		}
		if !matches {
			return fmt.Errorf("final turn idempotency key reused with another payload: %w", domain.ErrConflict)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read final turn replay rows: %w", err)
	}
	if !found {
		return errors.New("final turn conflict did not resolve to an existing record")
	}
	return nil
}

const insertFinalTurnQuery = `
INSERT INTO voice_turns (
    id, event_id, event_payload_hash, session_id, participant_id,
    speaker_code, display_name, provider_speaker_id, sequence_no, source_language, target_language,
    language_config_version, source_text, translated_text, speaker_confidence,
    attribution_status, started_at, ended_at, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15,
    $16, $17, $18, $19
)
ON CONFLICT DO NOTHING`

const finalTurnReplayQuery = `
SELECT event_payload_hash
FROM voice_turns
WHERE event_id = $1
   OR id = $2
   OR (session_id = $3 AND sequence_no = $4)
FOR UPDATE`
