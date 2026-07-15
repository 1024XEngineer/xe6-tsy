package reference

import (
	"context"
	"time"
)

// Module is the public boundary that other backend modules should depend on.
type Module interface {
	RetrieveAnalysisContext(context.Context, RetrieveAnalysisContextCommand) (AnalysisContextResult, error)
	GenerateReferenceSuggestions(context.Context, GenerateReferenceSuggestionsCommand) (ReferenceSuggestionResult, error)
	ReferenceSuggestionSetByID(context.Context, string) (ReferenceSuggestionSetView, error)
}

// Deps groups optional module-six ports without binding this skeleton to any
// concrete storage, provider, or event adapter.
type Deps struct {
	Clock           Clock
	Knowledge       KnowledgeBundleReader
	Analysis        AnalysisRetriever
	Reference       ReferenceGenerator
	Events          EventPublisher
	SuggestionStore SuggestionStore
}

// Clock supplies time for generation metadata and expiry calculations.
type Clock interface {
	Now() time.Time
}

// KnowledgeBundleReader is the read-only module-two boundary used by module six.
type KnowledgeBundleReader interface {
	KnowledgeBundle(context.Context, KnowledgePublicationRef, ApplicableScope) (PublishedKnowledgeBundle, error)
}

// AnalysisRetriever is the provider port for short-lived pre-analysis context retrieval.
type AnalysisRetriever interface {
	RetrieveAnalysisItems(context.Context, AnalysisRetrievalQuery) ([]AnalysisContextItem, error)
}

// ReferenceGenerator is the provider port for policy, material, path, and FAQ suggestions.
type ReferenceGenerator interface {
	GenerateReferences(context.Context, ReferenceGenerationQuery) (ReferenceGeneration, error)
}

// EventPublisher publishes module-six notification events after state changes.
type EventPublisher interface {
	PublishReferenceEvent(context.Context, ReferenceEvent) error
}

// SuggestionStore is the future persistence port for immutable module-six results.
type SuggestionStore interface {
	AnalysisContextByID(context.Context, string) (AnalysisRetrievalContext, error)
	ReferenceSuggestionSetByID(context.Context, string) (ReferenceSuggestionSet, error)
}

type service struct {
	deps Deps
}

var _ Module = (*service)(nil)

// New returns the module-six boundary backed by a service skeleton.
func New(deps Deps) (Module, error) {
	return &service{deps: deps}, nil
}

func (s *service) RetrieveAnalysisContext(context.Context, RetrieveAnalysisContextCommand) (AnalysisContextResult, error) {
	return AnalysisContextResult{}, ErrNotImplemented
}

func (s *service) GenerateReferenceSuggestions(context.Context, GenerateReferenceSuggestionsCommand) (ReferenceSuggestionResult, error) {
	return ReferenceSuggestionResult{}, ErrNotImplemented
}

func (s *service) ReferenceSuggestionSetByID(context.Context, string) (ReferenceSuggestionSetView, error) {
	return ReferenceSuggestionSetView{}, ErrNotImplemented
}
