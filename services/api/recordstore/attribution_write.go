package recordstore

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5"
)

// CorrectAttribution locks the turn and verifies the target participant in the same transaction.
// The database trigger independently rejects any update to the immutable translation snapshot.
func (w *TurnWriter) CorrectAttribution(
	ctx context.Context,
	update turns.AttributionUpdate,
) (recordsv1.VoiceTurn, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("begin attribution correction: %w", err)
	}
	defer tx.Rollback(ctx)

	var sessionID string
	if err := tx.QueryRow(ctx, lockTurnForAttributionQuery, update.TurnID).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
		return recordsv1.VoiceTurn{}, turns.ErrTurnNotFound
	} else if err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("lock turn for attribution correction: %w", err)
	}

	var participantExists bool
	if err := tx.QueryRow(ctx, participantBelongsQuery, update.ParticipantID, sessionID).Scan(&participantExists); err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("check attribution participant: %w", err)
	}
	if !participantExists {
		return recordsv1.VoiceTurn{}, turns.ErrInvalidAttribution
	}

	turn, err := scanVoiceTurn(tx.QueryRow(ctx, correctAttributionQuery,
		update.TurnID,
		update.ParticipantID,
		update.AttributionStatus,
		update.SpeakerConfidence,
		update.CorrectedBy,
		update.CorrectedAt.UTC(),
	))
	if err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("update turn attribution: %w", MapError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("commit attribution correction: %w", err)
	}
	return turn, nil
}

const participantBelongsQuery = `
SELECT EXISTS (
    SELECT 1
    FROM voice_session_participants
    WHERE id = $1 AND session_id = $2
)`

const lockTurnForAttributionQuery = `
SELECT session_id
FROM voice_turns
WHERE id = $1
FOR UPDATE`

const correctAttributionQuery = `
UPDATE voice_turns
SET participant_id = $2,
    attribution_status = $3,
    speaker_confidence = $4,
    corrected_by = $5,
    corrected_at = $6
WHERE id = $1
RETURNING id, session_id, participant_id, speaker_code, display_name,
          provider_speaker_id, voice_profile_id, sequence_no, source_language,
          target_language, language_config_version, source_text, translated_text,
          speaker_confidence, attribution_status, corrected_by, started_at, ended_at,
          corrected_at, created_at`

func scanVoiceTurn(row rowScanner) (recordsv1.VoiceTurn, error) {
	var turn recordsv1.VoiceTurn
	if err := row.Scan(
		&turn.ID,
		&turn.SessionID,
		&turn.ParticipantID,
		&turn.SpeakerCode,
		&turn.DisplayName,
		&turn.ProviderSpeakerID,
		&turn.VoiceProfileID,
		&turn.SequenceNo,
		&turn.SourceLanguage,
		&turn.TargetLanguage,
		&turn.LanguageConfigVersion,
		&turn.SourceText,
		&turn.TranslatedText,
		&turn.SpeakerConfidence,
		&turn.AttributionStatus,
		&turn.CorrectedBy,
		&turn.StartedAt,
		&turn.EndedAt,
		&turn.CorrectedAt,
		&turn.CreatedAt,
	); err != nil {
		return recordsv1.VoiceTurn{}, err
	}
	turn.StartedAt = turn.StartedAt.UTC()
	turn.EndedAt = turn.EndedAt.UTC()
	turn.CreatedAt = turn.CreatedAt.UTC()
	if turn.CorrectedAt != nil {
		correctedAt := turn.CorrectedAt.UTC()
		turn.CorrectedAt = &correctedAt
	}
	return turn, nil
}
