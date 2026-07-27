package delivery

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct{}

func NewUseCases() *UseCases { return &UseCases{} }

func (*UseCases) Create(context.Context, CreateInput) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

func (*UseCases) Get(context.Context, string, string) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

func (*UseCases) Retry(context.Context, string, string, string) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}

func (*UseCases) Preferences(context.Context, string) ([]Preference, error) {
	return nil, domain.ErrNotImplemented
}

func (*UseCases) PutPreference(context.Context, string, Channel, bool) (Preference, error) {
	return Preference{}, domain.ErrNotImplemented
}
