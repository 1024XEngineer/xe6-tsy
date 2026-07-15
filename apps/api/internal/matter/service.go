package matter

import (
	"context"
	"errors"
)

// ErrNotImplemented marks module 5 entrypoints that only exist as architecture contracts in this skeleton.
var ErrNotImplemented = errors.New("matter service not implemented")

// Service is the module 5 contract exposed to application wiring and other backend modules.
type Service interface {
	GenerateMatterSuggestions(ctx context.Context, cmd GenerateMatterSuggestionsCommand) (MatterSuggestionResult, error)
	MatterSuggestionSetByID(ctx context.Context, setID string) (MatterSuggestionSetView, error)
}

type service struct{}

var _ Service = (*service)(nil)

// NewService returns a minimal module 5 service with no external dependencies wired yet.
func NewService() Service {
	return &service{}
}

// GenerateMatterSuggestions reserves the module 5 generation contract without executing provider logic.
func (s *service) GenerateMatterSuggestions(ctx context.Context, cmd GenerateMatterSuggestionsCommand) (MatterSuggestionResult, error) {
	_ = ctx
	_ = cmd
	return MatterSuggestionResult{}, ErrNotImplemented
}

// MatterSuggestionSetByID reserves the module 5 read contract without adding repository access.
func (s *service) MatterSuggestionSetByID(ctx context.Context, setID string) (MatterSuggestionSetView, error) {
	_ = ctx
	_ = setID
	return MatterSuggestionSetView{}, ErrNotImplemented
}
