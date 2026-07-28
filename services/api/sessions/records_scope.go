package sessions

import (
	"context"
	"fmt"
)

// RecordsScopeReader adapts the session repository's account-scoped list to the
// records module's SQL ownership filter without exposing session storage types.
type RecordsScopeReader struct {
	repository RecordsScopeRepository
}

// RecordsScopeRepository is the persistent list capability required by the records adapter.
type RecordsScopeRepository interface {
	List(ctx context.Context, filter ListFilter) (ListPage, error)
}

func NewRecordsScopeReader(repository RecordsScopeRepository) (*RecordsScopeReader, error) {
	if repository == nil {
		return nil, fmt.Errorf("create records session scope reader: repository is required")
	}
	return &RecordsScopeReader{repository: repository}, nil
}

// SessionIDsForAccount returns every persistent session currently owned by the account.
// The records repository uses this complete scope in SQL; it must not be replaced by a
// single-session lookup or by filtering records after they have been read.
func (r *RecordsScopeReader) SessionIDsForAccount(ctx context.Context, accountID string) ([]string, error) {
	if accountID == "" {
		return nil, ErrUnauthorized
	}

	ids := make([]string, 0)
	cursor := ""
	seenCursors := map[string]struct{}{"": {}}
	for {
		page, err := r.repository.List(ctx, ListFilter{
			AccountID: accountID,
			Cursor:    cursor,
			Limit:     maxListLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("list sessions for records scope: %w", err)
		}
		for _, session := range page.Sessions {
			if session.ID == "" {
				return nil, fmt.Errorf("list sessions for records scope: %w", ErrInvalidRequest)
			}
			ids = append(ids, session.ID)
		}
		if page.NextCursor == nil {
			return ids, nil
		}
		nextCursor := *page.NextCursor
		if nextCursor == "" {
			return nil, fmt.Errorf("list sessions for records scope: invalid pagination cursor: %w", ErrInvalidRequest)
		}
		if _, seen := seenCursors[nextCursor]; seen {
			return nil, fmt.Errorf("list sessions for records scope: pagination cursor cycle: %w", ErrInvalidRequest)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
}
