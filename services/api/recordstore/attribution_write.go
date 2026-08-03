package recordstore

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5"
)

// CorrectAttribution locks the turn and verifies account ownership, session membership, and the
// target participant in the same transaction. The database trigger independently rejects any
// update to the immutable translation snapshot; the speaker snapshot fields follow the target
// participant so a corrected turn never shows a participant that contradicts its speaker labels.
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

	var sessionOwned bool
	if err := tx.QueryRow(ctx, accountOwnsSessionQuery, sessionID, update.AccountID).Scan(&sessionOwned); err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("check attribution session ownership: %w", err)
	}
	if !sessionOwned {
		return recordsv1.VoiceTurn{}, turns.ErrTurnNotFound
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
		update.SpeakerConfidenceSet,
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

const accountOwnsSessionQuery = `
SELECT EXISTS (
    SELECT 1
    FROM voice_sessions AS sessions
    JOIN lingow_accounts AS owner ON owner.id = sessions.account_id
    WHERE sessions.id = $1
      AND COALESCE(owner.merged_into, owner.id) = $2
)`

const lockTurnForAttributionQuery = `
SELECT session_id
FROM voice_turns
WHERE id = $1
FOR UPDATE`

const correctAttributionQuery = `
UPDATE voice_turns AS turn
SET participant_id = $2,
    speaker_code = target.speaker_code,
    display_name = target.display_name,
    provider_speaker_id = target.provider_speaker_id,
    voice_profile_id = target.voice_profile_id,
    attribution_status = $3,
    speaker_confidence = CASE WHEN $4 THEN $5 ELSE turn.speaker_confidence END,
    corrected_by = $6,
    corrected_at = $7
FROM voice_session_participants AS target
WHERE turn.id = $1
  AND target.id = $2
  AND target.session_id = turn.session_id
RETURNING turn.id, turn.session_id, turn.participant_id, turn.speaker_code, turn.display_name,
          turn.provider_speaker_id, turn.voice_profile_id, turn.sequence_no, turn.source_language,
          turn.target_language, turn.language_config_version, turn.source_text, turn.translated_text,
          turn.speaker_confidence, turn.attribution_status, turn.corrected_by, turn.started_at,
          turn.ended_at, turn.corrected_at, turn.created_at`

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
