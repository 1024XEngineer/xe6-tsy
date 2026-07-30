package languages

import (
	"context"
	"errors"
	"fmt"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// RecordsSessionOwner adapts the records SessionOwnerReader port to the
// language module's ownership contract. Missing sessions become
// ErrSessionNotFound so HTTP mapping stays 404 instead of a generic 500.
type RecordsSessionOwner struct {
	inner recordsv1.SessionOwnerReader
}

// NewRecordsSessionOwner wraps a records ownership reader. Callers typically
// pass recordstore.NewCanonicalSessionOwner so merged accounts authorize the
// same way as turns and participants.
func NewRecordsSessionOwner(inner recordsv1.SessionOwnerReader) SessionOwnerReader {
	if inner == nil {
		return NotImplementedSessionOwner{}
	}
	return RecordsSessionOwner{inner: inner}
}

func (r RecordsSessionOwner) GetOwnerAccountID(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("%w: session_id is required", ErrInvalidRequest)
	}
	accountID, err := r.inner.AccountIDForSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", ErrSessionNotFound
		}
		return "", fmt.Errorf("read session owner: %w", err)
	}
	if accountID == "" {
		return "", ErrSessionNotFound
	}
	return accountID, nil
}
