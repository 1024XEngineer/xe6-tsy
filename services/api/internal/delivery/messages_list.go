package delivery

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const defaultMessageListLimit = 20

type messageListRepository interface {
	ListMessages(context.Context, string, int) ([]Message, error)
}

// ListMessages returns the account's newest outbound messages for status display.
func (u *UseCases) ListMessages(ctx context.Context, accountID string, limit int) ([]Message, error) {
	repository, ok := u.repository.(messageListRepository)
	if !ok {
		return nil, domain.ErrNotImplemented
	}
	if accountID == "" {
		return nil, domain.ErrUnauthorized
	}
	if limit <= 0 || limit > 100 {
		limit = defaultMessageListLimit
	}
	return repository.ListMessages(ctx, accountID, limit)
}

var _ MessageListingService = (*UseCases)(nil)
