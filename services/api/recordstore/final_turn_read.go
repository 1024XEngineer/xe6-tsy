package recordstore

import (
	"context"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func (r *TurnReadRepository) ReadFinalTurns(
	ctx context.Context,
	accountID string,
	turnIDs []string,
) ([]recordsv1.FinalTurnSnapshot, error) {
	if accountID == "" || len(turnIDs) == 0 {
		return nil, turns.ErrInvalidRequest
	}
	ownedSessionIDs, err := r.sessions.SessionIDsForAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("read account session scope: %w", err)
	}

	rows, err := r.pool.Query(ctx, readFinalTurnsQuery, turnIDs, ownedSessionIDs)
	if err != nil {
		return nil, fmt.Errorf("query final turn snapshots: %w", err)
	}
	defer rows.Close()

	snapshots := make([]recordsv1.FinalTurnSnapshot, 0, len(turnIDs))
	for rows.Next() {
		var snapshot recordsv1.FinalTurnSnapshot
		if err := rows.Scan(
			&snapshot.TurnID,
			&snapshot.SessionID,
			&snapshot.ParticipantID,
			&snapshot.SpeakerLabelSnapshot,
			&snapshot.SourceLanguage,
			&snapshot.TargetLanguage,
			&snapshot.LanguageConfigVersion,
			&snapshot.SourceText,
			&snapshot.TranslatedText,
			&snapshot.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan final turn snapshot: %w", err)
		}
		snapshot.CreatedAt = snapshot.CreatedAt.UTC()
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read final turn snapshot rows: %w", err)
	}
	if len(snapshots) != len(turnIDs) {
		return nil, turns.ErrTurnNotFound
	}
	return snapshots, nil
}

const readFinalTurnsQuery = `
WITH requested(turn_id, ordinal) AS (
    SELECT turn_id, ordinal
    FROM unnest($1::text[]) WITH ORDINALITY AS request(turn_id, ordinal)
)
SELECT turn.id, turn.session_id, turn.participant_id, turn.display_name,
       turn.source_language, turn.target_language, turn.language_config_version,
       turn.source_text, turn.translated_text, turn.created_at
FROM requested
JOIN voice_turns AS turn ON turn.id = requested.turn_id
WHERE turn.session_id = ANY($2::text[])
ORDER BY requested.ordinal ASC`
