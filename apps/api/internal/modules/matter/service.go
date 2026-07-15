package matter

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotImplemented = errors.New("matter service generation is not implemented")
	ErrStoreRequired  = errors.New("matter suggestion store is required")
)

// service is the private module 5 implementation behind the exported API. It
// keeps provider and store dependencies inside the matter package so handlers
// and other modules depend on behavior contracts instead of implementation
// details.
type service struct {
	generator CandidateGenerator
	store     MatterSuggestionStore
}

// API is the outward-facing application contract for module 5. Exposing this
// interface lets Gin handlers, workflow code, and tests wire the module without
// depending on the private service skeleton.
type API interface {
	MatterSuggestionGenerator
	MatterSuggestionReader
}

// NewService assembles the module 5 application boundary from provider and
// storage ports. Passing nil dependencies is allowed for the current skeleton so
// the API process can start before business generation and persistence are
// implemented.
func NewService(generator CandidateGenerator, store MatterSuggestionStore) API {
	return &service{generator: generator, store: store}
}

// GenerateMatterSuggestions is intentionally a skeleton boundary today. It
// returns a stable sentinel error rather than fabricating business output, which
// keeps later Handler work from accidentally treating missing generation logic
// as a valid no-candidate result.
func (s *service) GenerateMatterSuggestions(ctx context.Context, cmd GenerateMatterSuggestionsCommand) (MatterSuggestionResult, error) {
	return MatterSuggestionResult{}, ErrNotImplemented
}

// MatterSuggestionSetByID delegates recovery reads to the store port and wraps
// failures with the requested set ID. The wrapping keeps operational context for
// logs while preserving the original error for future not-found or permission
// mapping at the transport boundary.
func (s *service) MatterSuggestionSetByID(ctx context.Context, setID string) (MatterSuggestionSetView, error) {
	if s.store == nil {
		return MatterSuggestionSetView{}, ErrStoreRequired
	}
	view, err := s.store.ViewByID(ctx, setID)
	if err != nil {
		return MatterSuggestionSetView{}, fmt.Errorf("reading matter suggestion set %s: %w", setID, err)
	}
	return view, nil
}
