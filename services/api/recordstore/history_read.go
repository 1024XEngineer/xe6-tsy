package recordstore

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func (r *TurnReadRepository) ListHistory(
	ctx context.Context,
	accountID string,
	query recordsv1.ListTurnsQuery,
) (recordsv1.VoiceTurnListResponse, error) {
	if accountID == "" || !validRecordPageSize(query.Limit) {
		return recordsv1.VoiceTurnListResponse{}, turns.ErrInvalidRequest
	}
	ownedSessionIDs, err := r.sessions.SessionIDsForAccount(ctx, accountID)
	if err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("read account session scope: %w", err)
	}

	scope := historyCursorScope(accountID, query)
	var after Cursor
	var afterCreatedAt *time.Time
	if query.Cursor != "" {
		decoded, err := r.cursors.Decode(query.Cursor, CursorHistory, scope)
		if err != nil {
			return recordsv1.VoiceTurnListResponse{}, domain.NewFieldError("cursor", fmt.Errorf("decode history cursor: %w: %w", turns.ErrInvalidRequest, err))
		}
		after = decoded
		afterCreatedAt = &after.CreatedAt
	}

	rows, err := r.pool.Query(ctx, listHistoryQuery,
		ownedSessionIDs,
		query.SessionID,
		query.ParticipantID,
		query.SourceLanguage,
		query.TargetLanguage,
		query.CreatedFrom,
		query.CreatedTo,
		afterCreatedAt,
		after.ID,
		query.Limit+1,
	)
	if err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("query translation history: %w", err)
	}
	defer rows.Close()

	items := make([]recordsv1.VoiceTurn, 0, query.Limit)
	for rows.Next() {
		turn, err := scanReadVoiceTurn(rows)
		if err != nil {
			return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("scan translation history: %w", err)
		}
		items = append(items, turn)
	}
	if err := rows.Err(); err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("read translation history rows: %w", err)
	}

	response := recordsv1.VoiceTurnListResponse{Items: items}
	if len(items) <= query.Limit {
		return response, nil
	}

	last := items[query.Limit-1]
	nextCursor, err := r.cursors.Encode(Cursor{
		Kind:      CursorHistory,
		Scope:     scope,
		CreatedAt: last.CreatedAt,
		ID:        last.ID,
	})
	if err != nil {
		return recordsv1.VoiceTurnListResponse{}, fmt.Errorf("encode history cursor: %w", err)
	}
	response.Items = items[:query.Limit]
	response.NextCursor = &nextCursor
	return response, nil
}

func historyCursorScope(accountID string, query recordsv1.ListTurnsQuery) string {
	return CursorScope(CursorHistory, url.Values{
		"account_id":      {accountID},
		"session_id":      {query.SessionID},
		"participant_id":  {query.ParticipantID},
		"source_language": {query.SourceLanguage},
		"target_language": {query.TargetLanguage},
		"created_from":    {cursorTimeValue(query.CreatedFrom)},
		"created_to":      {cursorTimeValue(query.CreatedTo)},
		"limit":           {strconv.Itoa(query.Limit)},
	})
}

func cursorTimeValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

const listHistoryQuery = `
SELECT ` + voiceTurnColumns + `
FROM voice_turns
WHERE session_id = ANY($1::text[])
  AND ($2 = '' OR session_id = $2)
  AND ($3 = '' OR participant_id = $3)
  AND ($4 = '' OR source_language = $4)
  AND ($5 = '' OR target_language = $5)
  AND ($6::timestamptz IS NULL OR created_at >= $6)
  AND ($7::timestamptz IS NULL OR created_at <= $7)
  AND ($8::timestamptz IS NULL OR (created_at, id) < ($8, $9))
ORDER BY created_at DESC, id DESC
LIMIT $10`
