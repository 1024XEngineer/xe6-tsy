package delivery

import (
	"context"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const defaultAutomaticOutputStatusLimit = 20

type automaticOutputStatusRepository interface {
	ListAutomaticOutputStatus(context.Context, string, string, int) ([]AutomaticOutputStatus, error)
}

// AutomaticOutputSessionReader verifies that an automatic-output status request
// names a voice session visible to the authenticated account.
type AutomaticOutputSessionReader interface {
	RequireOwnedSession(context.Context, string, string) error
}

// ConfigureAutomaticOutputSessionReader enables account-scoped automatic-output
// status reads for persistent deployments.
func (u *UseCases) ConfigureAutomaticOutputSessionReader(reader AutomaticOutputSessionReader) {
	u.outputSessions = reader
}

func (u *UseCases) ListAutomaticOutputStatus(ctx context.Context, accountID, sessionID string, limit int) ([]AutomaticOutputStatus, error) {
	repository, ok := u.repository.(automaticOutputStatusRepository)
	if !ok {
		return nil, domain.ErrNotImplemented
	}
	if strings.TrimSpace(accountID) == "" {
		return nil, domain.ErrUnauthorized
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, domain.ErrInvalidArgument
	}
	if u.outputSessions == nil {
		return nil, domain.ErrNotImplemented
	}
	if err := u.outputSessions.RequireOwnedSession(ctx, accountID, sessionID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = defaultAutomaticOutputStatusLimit
	}
	return repository.ListAutomaticOutputStatus(ctx, accountID, sessionID, limit)
}

var _ AutomaticOutputStatusService = (*UseCases)(nil)
