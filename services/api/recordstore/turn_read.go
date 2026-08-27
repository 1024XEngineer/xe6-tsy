package recordstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AccountSessionScopeReader returns the complete session scope currently owned by an account.
// Record queries use the scope inside SQL instead of reading rows before checking ownership.
type AccountSessionScopeReader interface {
	SessionIDsForAccount(ctx context.Context, accountID string) ([]string, error)
}

// TurnReadRepository provides account-filtered, final-only reads over voice_turns.
type TurnReadRepository struct {
	pool     *pgxpool.Pool
	cursors  *CursorCodec
	sessions AccountSessionScopeReader
}

func NewTurnReadRepository(
	pool *pgxpool.Pool,
	cursors *CursorCodec,
	sessions AccountSessionScopeReader,
) (*TurnReadRepository, error) {
	if pool == nil || cursors == nil || sessions == nil {
		return nil, fmt.Errorf("create turn reader: pool, cursor codec, and session scope reader are required")
	}
	return &TurnReadRepository{pool: pool, cursors: cursors, sessions: sessions}, nil
}

func (r *TurnReadRepository) ListSession(
	ctx context.Context,
	accountID string,
	sessionID string,
	query recordsv1.ListTurnsQuery,
) (recordsv1.VoiceTurnListResponse, error) {
	if accountID == "" || sessionID == "" || !validRecordPageSize(query.Limit) {
		return recordsv1.VoiceTurnListResponse{}, turns.ErrInvalidRequest
	}
	scope := sessionTurnsCursorScope(accountID, sessionID, query)
	var after Cursor
	if query.Cursor != "" {
		decoded, err := r.cursors.Decode(query.Cursor, CursorSessionTurns, scope)
		if err != nil {
			return recordsv1.VoiceTurnListResponse{}, domain.NewFieldError("cursor", fmt.Errorf("decode session turns cursor: %w: %w", turns.ErrInvalidRequest, err))
		}
		after = decoded
	}

	rows, err := r.pool.Query(ctx, listSessionTurnsQuery,
		sessionID,
		query.ParticipantID,
		query.SpeakerCode,
		query.AttributionStatus,
		query.SourceLanguage,
		query.TargetLanguage,
		after.SequenceNo,
		after.ID,
		query.Limit+1,
	)
	if err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("query session turns: %w", err)
	}
	defer rows.Close()

	items := make([]recordsv1.VoiceTurn, 0, query.Limit)
	for rows.Next() {
		turn, err := scanReadVoiceTurn(rows)
		if err != nil {
			return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("scan session turn: %w", err)
		}
		items = append(items, turn)
	}
	if err := rows.Err(); err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("read session turn rows: %w", err)
	}

	response := recordsv1.VoiceTurnListResponse{Items: items}
	if len(items) > query.Limit {
		last := items[query.Limit-1]
		response.Items = items[:query.Limit]
		nextCursor, err := r.cursors.Encode(Cursor{
			Kind:       CursorSessionTurns,
			Scope:      scope,
			SequenceNo: last.SequenceNo,
			ID:         last.ID,
		})
		if err != nil {
			return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("encode session turns cursor: %w", err)
		}
		response.NextCursor = &nextCursor
		return response, nil
	}

	if len(items) == 0 {
		return response, nil
	}
	// Keep a signed keyset position even on the final non-empty page. This lets
	// incremental consumers advance to the end instead of polling the same tail
	// page forever; the following request returns an empty page and a nil cursor.
	last := items[len(items)-1]
	nextCursor, err := r.cursors.Encode(Cursor{
		Kind:       CursorSessionTurns,
		Scope:      scope,
		SequenceNo: last.SequenceNo,
		ID:         last.ID,
	})
	if err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("encode session turns cursor: %w", err)
	}
	response.NextCursor = &nextCursor
	return response, nil
}

func (r *TurnReadRepository) Find(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error) {
	if accountID == "" || turnID == "" {
		return recordsv1.VoiceTurn{}, turns.ErrInvalidRequest
	}
	turn, err := scanReadVoiceTurn(r.pool.QueryRow(ctx, findTurnQuery, turnID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return recordsv1.VoiceTurn{}, turns.ErrTurnNotFound
	}
	if err != nil {
		return recordsv1.VoiceTurn{}, fmt.Errorf("find account turn: %w", err)
	}
	return turn, nil
}

func sessionTurnsCursorScope(accountID, sessionID string, query recordsv1.ListTurnsQuery) string {
	return CursorScope(CursorSessionTurns, url.Values{
		"account_id":         {accountID},
		"session_id":         {sessionID},
		"participant_id":     {query.ParticipantID},
		"speaker_code":       {query.SpeakerCode},
		"attribution_status": {string(query.AttributionStatus)},
		"source_language":    {query.SourceLanguage},
		"target_language":    {query.TargetLanguage},
		"limit":              {strconv.Itoa(query.Limit)},
	})
}

const voiceTurnColumns = `
id, session_id, participant_id, speaker_code, display_name,
provider_speaker_id, voice_profile_id, sequence_no, source_language,
target_language, language_config_version, source_text, translated_text,
speaker_confidence, attribution_status, corrected_by, started_at, ended_at,
corrected_at, created_at`

const listSessionTurnsQuery = `
SELECT ` + voiceTurnColumns + `
FROM voice_turns
WHERE session_id = $1
  AND ($2 = '' OR participant_id = $2)
  AND ($3 = '' OR speaker_code = $3)
  AND ($4 = '' OR attribution_status = $4)
  AND ($5 = '' OR source_language = $5)
  AND ($6 = '' OR target_language = $6)
  AND ($7 = 0 OR (sequence_no, id) > ($7, $8))
ORDER BY sequence_no ASC, id ASC
LIMIT $9`

const findTurnQuery = `
SELECT ` + voiceTurnColumns + `
FROM voice_turns AS turn
WHERE turn.id = $1
  AND turn.session_id IN (
      SELECT sessions.id
      FROM voice_sessions AS sessions
      WHERE sessions.account_id IN (
          SELECT account_id
          FROM lingow_account_lineage($2)
      )
  )`

type voiceTurnReadScanner interface {
	Scan(dest ...any) error
}

func scanReadVoiceTurn(row voiceTurnReadScanner) (recordsv1.VoiceTurn, error) {
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
