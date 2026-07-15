package reference

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotImplemented         = errors.New("reference service generation is not implemented")
	ErrAnalysisStoreRequired  = errors.New("analysis context store is required")
	ErrReferenceStoreRequired = errors.New("reference suggestion store is required")
)

// service is the private module 6 implementation behind the exported API. It
// owns the wiring of retrieval, knowledge, matter-read, and store ports while
// keeping those dependencies hidden from handlers and other modules.
type service struct {
	retriever      Retriever
	knowledge      PublishedKnowledgeReader
	matterReader   MatterSuggestionSetReader
	analysisStore  AnalysisContextStore
	referenceStore ReferenceSuggestionStore
}

// API is the outward-facing application contract for module 6. It groups
// pre-retrieval, reference generation, and recovery reads without exposing the
// internal service skeleton or provider adapters.
type API interface {
	AnalysisContextRetriever
	ReferenceSuggestionGenerator
	ReferenceSuggestionReader
}

// NewService assembles the module 6 application boundary from read-only module
// ports, retrieval provider ports, and stores. Nil dependencies are accepted for
// the current skeleton so Gin can register module boundaries before business
// logic and persistence are implemented.
func NewService(
	retriever Retriever,
	knowledge PublishedKnowledgeReader,
	matterReader MatterSuggestionSetReader,
	analysisStore AnalysisContextStore,
	referenceStore ReferenceSuggestionStore,
) API {
	return &service{
		retriever:      retriever,
		knowledge:      knowledge,
		matterReader:   matterReader,
		analysisStore:  analysisStore,
		referenceStore: referenceStore,
	}
}

// RetrieveAnalysisContext is intentionally a skeleton boundary today. It returns
// a stable sentinel error instead of a no-match result so missing implementation
// cannot be confused with a real provider response during Handler integration.
func (s *service) RetrieveAnalysisContext(ctx context.Context, cmd RetrieveAnalysisContextCommand) (AnalysisContextResult, error) {
	return AnalysisContextResult{}, ErrNotImplemented
}

// GenerateReferenceSuggestions is intentionally a skeleton boundary today. The
// future implementation must validate scope, publication, basis freshness, and
// provider hits before returning a typed result.
func (s *service) GenerateReferenceSuggestions(ctx context.Context, cmd GenerateReferenceSuggestionsCommand) (ReferenceSuggestionResult, error) {
	return ReferenceSuggestionResult{}, ErrNotImplemented
}

// ReferenceSuggestionSetByID delegates recovery reads to the reference store and
// wraps failures with the requested set ID. The transport layer can later map
// not-found or forbidden errors while logs retain the failed lookup context.
func (s *service) ReferenceSuggestionSetByID(ctx context.Context, setID string) (ReferenceSuggestionSetView, error) {
	if s.referenceStore == nil {
		return ReferenceSuggestionSetView{}, ErrReferenceStoreRequired
	}
	view, err := s.referenceStore.ViewByID(ctx, setID)
	if err != nil {
		return ReferenceSuggestionSetView{}, fmt.Errorf("reading reference suggestion set %s: %w", setID, err)
	}
	return view, nil
}
