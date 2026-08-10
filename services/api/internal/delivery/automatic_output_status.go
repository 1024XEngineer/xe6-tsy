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
	if limit <= 0 || limit > 100 {
		limit = defaultAutomaticOutputStatusLimit
	}
	return repository.ListAutomaticOutputStatus(ctx, accountID, sessionID, limit)
}

var _ AutomaticOutputStatusService = (*UseCases)(nil)
