package delivery

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// UseCases reserves delivery orchestration while repository and provider adapters are pending.
type UseCases struct{}

// NewUseCases returns the placeholder delivery service used by the foundation server.
func NewUseCases() *UseCases { return &UseCases{} }

// Create will authorize a destination and persist an immutable final-Turn snapshot.
func (*UseCases) Create(context.Context, CreateInput) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

// Get will return a message only when it belongs to the trusted account context.
func (*UseCases) Get(context.Context, string, string) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

// Retry will idempotently create the next attempt for an eligible failed message.
func (*UseCases) Retry(context.Context, string, string, string) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

// Preferences will list channel settings for the trusted account context.
func (*UseCases) Preferences(context.Context, string) ([]Preference, error) {
	return nil, domain.ErrNotImplemented
}

// PutPreference will update a user-controlled channel setting without changing verification.
func (*UseCases) PutPreference(context.Context, string, Channel, bool) (Preference, error) {
	return Preference{}, domain.ErrNotImplemented
}
