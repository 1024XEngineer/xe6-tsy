package delivery

import (
	"context"
	"errors"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresTurnReader reads only final rows that are joined to the caller's
// account-owned session. It never trusts a client-provided account field.
type PostgresTurnReader struct{ pool *pgxpool.Pool }

func NewPostgresTurnReader(pool *pgxpool.Pool) *PostgresTurnReader {
	return &PostgresTurnReader{pool: pool}
}

func (r *PostgresTurnReader) ReadFinalTurns(ctx context.Context, accountID string, turnIDs []string) ([]FinalTurnSnapshot, error) {
	if accountID == "" || len(turnIDs) == 0 {
		return nil, domain.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `SELECT t.id,t.session_id,t.participant_id,t.display_name,t.source_language,t.target_language,t.language_config_version,t.source_text,t.translated_text,t.created_at FROM voice_turns t JOIN voice_sessions s ON s.id=t.session_id WHERE s.account_id IN (SELECT account_id FROM lingow_account_lineage($1)) AND t.id=ANY($2::text[]) ORDER BY t.created_at ASC,t.id ASC`, accountID, turnIDs)
	if err != nil {
		return nil, mapTurnError(err)
	}
	defer rows.Close()
	result := make([]FinalTurnSnapshot, 0, len(turnIDs))
	for rows.Next() {
		var turn FinalTurnSnapshot
		if err := rows.Scan(&turn.TurnID, &turn.SessionID, &turn.ParticipantID, &turn.SpeakerLabelSnapshot, &turn.SourceLanguage, &turn.TargetLanguage, &turn.LanguageConfigVersion, &turn.SourceText, &turn.TranslatedText, &turn.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(turnIDs) {
		return nil, domain.ErrNotFound
	}
	return result, nil
}

func mapTurnError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

var _ TurnReader = (*PostgresTurnReader)(nil)
