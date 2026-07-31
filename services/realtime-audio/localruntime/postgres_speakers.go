package localruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultLocalProviderSpeakerID = "local-mic"

// PostgresSpeakerReader resolves provisional speaker attribution against
// voice_session_participants without importing services/api.
type PostgresSpeakerReader struct {
	Pool *pgxpool.Pool
}

func (r PostgresSpeakerReader) GetProvisionalAttribution(
	ctx context.Context,
	observation recordsv1.SpeakerObservation,
) (recordsv1.SpeakerAttribution, error) {
	if err := ctx.Err(); err != nil {
		return recordsv1.SpeakerAttribution{}, err
	}
	if strings.TrimSpace(observation.SessionID) == "" || strings.TrimSpace(observation.TurnID) == "" {
		return recordsv1.SpeakerAttribution{}, fmt.Errorf("session_id and turn_id are required")
	}
	if r.Pool == nil {
		return recordsv1.SpeakerAttribution{}, fmt.Errorf("postgres speaker reader pool is required")
	}
	providerID := strings.TrimSpace(observation.ProviderSpeakerID)
	if providerID == "" {
		providerID = defaultLocalProviderSpeakerID
	}
	participant, err := r.findOrCreate(ctx, observation.SessionID, providerID)
	if err != nil {
		return recordsv1.SpeakerAttribution{}, err
	}
	participantID := participant.ID
	return recordsv1.SpeakerAttribution{
		ParticipantID:     &participantID,
		SpeakerCode:       participant.SpeakerCode,
		DisplayName:       participant.DisplayName,
		Confidence:        participant.Confidence,
		AttributionStatus: recordsv1.AttributionProvisional,
	}, nil
}

func (r PostgresSpeakerReader) findOrCreate(ctx context.Context, sessionID, providerSpeakerID string) (recordsv1.Participant, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return recordsv1.Participant{}, fmt.Errorf("begin participant allocation: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, sessionID); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("lock participant session: %w", err)
	}

	participant, err := scanSpeakerParticipant(tx.QueryRow(ctx, `
		SELECT id, session_id, speaker_code, display_name, provider_speaker_id,
		       voice_profile_id, confidence, created_at, updated_at
		FROM voice_session_participants
		WHERE session_id = $1 AND provider_speaker_id = $2
	`, sessionID, providerSpeakerID))
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
		sessionID,
	).Scan(&ordinal); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("allocate participant speaker code: %w", err)
	}

	participant, err = scanSpeakerParticipant(tx.QueryRow(ctx, `
		INSERT INTO voice_session_participants (
		    id, session_id, speaker_code, provider_speaker_id
		) VALUES ($1, $2, $3, $4)
		RETURNING id, session_id, speaker_code, display_name, provider_speaker_id,
		          voice_profile_id, confidence, created_at, updated_at
	`, uuid.NewString(), sessionID, fmt.Sprintf("speaker_%02d", ordinal), providerSpeakerID))
	if err != nil {
		return recordsv1.Participant{}, fmt.Errorf("insert participant: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return recordsv1.Participant{}, fmt.Errorf("commit participant allocation: %w", err)
	}
	return participant, nil
}

func scanSpeakerParticipant(row interface{ Scan(dest ...any) error }) (recordsv1.Participant, error) {
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
