package recordstore

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

// ParticipantWriter persists the mutable participant side of the records repository.
type ParticipantWriter struct {
	pool *pgxpool.Pool
}

func NewParticipantWriter(pool *pgxpool.Pool) *ParticipantWriter {
	return &ParticipantWriter{pool: pool}
}

// FindOrCreate serializes allocation within one session so a provider key and speaker code are
// stable even when multiple consumer instances observe the same new speaker concurrently.
func (w *ParticipantWriter) FindOrCreate(
	ctx context.Context,
	observation recordsv1.SpeakerObservation,
) (recordsv1.Participant, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return recordsv1.Participant{}, fmt.Errorf("begin participant allocation: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, lockParticipantSessionQuery, observation.SessionID); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("lock participant session: %w", err)
	}

	participant, err := scanParticipant(tx.QueryRow(ctx, participantByProviderQuery,
		observation.SessionID,
		observation.ProviderSpeakerID,
	))
	switch {
	case err == nil:
		if err := tx.Commit(ctx); err != nil {
			return recordsv1.Participant{}, fmt.Errorf("commit participant lookup: %w", err)
		}
		return participant, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return recordsv1.Participant{}, fmt.Errorf("find participant by provider key: %w", err)
	}

	var ordinal int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) + 1 FROM voice_session_participants WHERE session_id = $1`,
		observation.SessionID,
	).Scan(&ordinal); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("allocate participant speaker code: %w", err)
	}

	participant, err = scanParticipant(tx.QueryRow(ctx, insertParticipantQuery,
		ulid.Make().String(),
		observation.SessionID,
		fmt.Sprintf("speaker_%02d", ordinal),
		observation.ProviderSpeakerID,
	))
	if err != nil {
		return recordsv1.Participant{}, fmt.Errorf("insert participant: %w", MapError(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("commit participant allocation: %w", err)
	}
	return participant, nil
}

func (w *ParticipantWriter) Update(
	ctx context.Context,
	sessionID string,
	participantID string,
	update participants.Update,
) (recordsv1.Participant, error) {
	participant, err := scanParticipant(w.pool.QueryRow(ctx, updateParticipantQuery,
		sessionID,
		participantID,
		update.DisplayNameSet,
		update.DisplayName,
		update.ProviderSpeakerIDSet,
		update.ProviderSpeakerID,
		update.VoiceProfileIDSet,
		update.VoiceProfileID,
		update.UpdatedAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return recordsv1.Participant{}, participants.ErrParticipantNotFound
	}
	if errors.Is(MapError(err), domain.ErrConflict) {
		return recordsv1.Participant{}, participants.ErrConflict
	}
	if err != nil {
		return recordsv1.Participant{}, fmt.Errorf("update participant mapping: %w", err)
	}
	return participant, nil
}

const lockParticipantSessionQuery = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

const participantByProviderQuery = `
SELECT id, session_id, speaker_code, display_name, provider_speaker_id,
       voice_profile_id, confidence, created_at, updated_at
FROM voice_session_participants
WHERE session_id = $1 AND provider_speaker_id = $2`

const insertParticipantQuery = `
INSERT INTO voice_session_participants (
    id, session_id, speaker_code, provider_speaker_id
) VALUES ($1, $2, $3, $4)
RETURNING id, session_id, speaker_code, display_name, provider_speaker_id,
          voice_profile_id, confidence, created_at, updated_at`

const updateParticipantQuery = `
UPDATE voice_session_participants
SET display_name = CASE WHEN $3 THEN $4 ELSE display_name END,
    provider_speaker_id = CASE WHEN $5 THEN $6 ELSE provider_speaker_id END,
    voice_profile_id = CASE WHEN $7 THEN $8 ELSE voice_profile_id END,
    updated_at = $9
WHERE session_id = $1 AND id = $2
RETURNING id, session_id, speaker_code, display_name, provider_speaker_id,
          voice_profile_id, confidence, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanParticipant(row rowScanner) (recordsv1.Participant, error) {
	var participant recordsv1.Participant
	if err := row.Scan(
		&participant.ID,
		&participant.SessionID,
		&participant.SpeakerCode,
		&participant.DisplayName,
		&participant.ProviderSpeakerID,
		&participant.VoiceProfileID,
		&participant.Confidence,
		&participant.CreatedAt,
		&participant.UpdatedAt,
	); err != nil {
		return recordsv1.Participant{}, err
	}
	participant.CreatedAt = participant.CreatedAt.UTC()
	participant.UpdatedAt = participant.UpdatedAt.UTC()
	return participant, nil
}
