package recordstore

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maximumRecordPageSize = 100

// ParticipantReadRepository applies stable participant ordering and binds cursors to the
// authenticated account and session supplied by the service layer.
type ParticipantReadRepository struct {
	pool    *pgxpool.Pool
	cursors *CursorCodec
}

func NewParticipantReadRepository(
	pool *pgxpool.Pool,
	cursors *CursorCodec,
) (*ParticipantReadRepository, error) {
	if pool == nil || cursors == nil {
		return nil, fmt.Errorf("create participant reader: pool and cursor codec are required")
	}
	return &ParticipantReadRepository{pool: pool, cursors: cursors}, nil
}

func (r *ParticipantReadRepository) List(
	ctx context.Context,
	accountID string,
	sessionID string,
	query recordsv1.ListParticipantsQuery,
) (recordsv1.ParticipantListResponse, error) {
	if accountID == "" || sessionID == "" || !validRecordPageSize(query.Limit) {
		return recordsv1.ParticipantListResponse{}, participants.ErrInvalidRequest
	}
	scope := participantCursorScope(accountID, sessionID, query.Limit)
	var after Cursor
	if query.Cursor != "" {
		decoded, err := r.cursors.Decode(query.Cursor, CursorParticipants, scope)
		if err != nil {
			return recordsv1.ParticipantListResponse{}, fmt.Errorf("decode participant cursor: %w: %w", participants.ErrInvalidRequest, err)
		}
		after = decoded
	}

	rows, err := r.pool.Query(ctx, listParticipantsQuery,
		sessionID,
		after.SpeakerCode,
		after.ID,
		query.Limit+1,
	)
	if err != nil {
		return recordsv1.ParticipantListResponse{}, fmt.Errorf("query session participants: %w", err)
	}
	defer rows.Close()

	items := make([]recordsv1.Participant, 0, query.Limit)
	for rows.Next() {
		participant, err := scanReadParticipant(rows)
		if err != nil {
			return recordsv1.ParticipantListResponse{}, fmt.Errorf("scan session participant: %w", err)
		}
		items = append(items, participant)
	}
	if err := rows.Err(); err != nil {
		return recordsv1.ParticipantListResponse{}, fmt.Errorf("read session participant rows: %w", err)
	}

	response := recordsv1.ParticipantListResponse{Items: items}
	if len(items) <= query.Limit {
		return response, nil
	}

	last := items[query.Limit-1]
	nextCursor, err := r.cursors.Encode(Cursor{
		Kind:        CursorParticipants,
		Scope:       scope,
		SpeakerCode: last.SpeakerCode,
		ID:          last.ID,
	})
	if err != nil {
		return recordsv1.ParticipantListResponse{}, fmt.Errorf("encode participant cursor: %w", err)
	}
	response.Items = items[:query.Limit]
	response.NextCursor = &nextCursor
	return response, nil
}

func participantCursorScope(accountID, sessionID string, limit int) string {
	return CursorScope(CursorParticipants, url.Values{
		"account_id": {accountID},
		"session_id": {sessionID},
		"limit":      {strconv.Itoa(limit)},
	})
}

func validRecordPageSize(limit int) bool {
	return limit >= 1 && limit <= maximumRecordPageSize
}

const listParticipantsQuery = `
SELECT id, session_id, speaker_code, display_name, provider_speaker_id,
       voice_profile_id, confidence, created_at, updated_at
FROM voice_session_participants
WHERE session_id = $1
  AND ($2 = '' OR (speaker_code, id) > ($2, $3))
ORDER BY speaker_code ASC, id ASC
LIMIT $4`

type participantReadScanner interface {
	Scan(dest ...any) error
}

func scanReadParticipant(row participantReadScanner) (recordsv1.Participant, error) {
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
