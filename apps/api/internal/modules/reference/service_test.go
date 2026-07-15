package reference

import (
	"context"
	"errors"
	"testing"
)

func TestServiceImplementsApplicationInterfaces(t *testing.T) {
	var _ AnalysisContextRetriever = (*service)(nil)
	var _ ReferenceSuggestionGenerator = (*service)(nil)
	var _ ReferenceSuggestionReader = (*service)(nil)
	var _ API = (*service)(nil)
}

func TestService_ReferenceSuggestionSetByIDDelegatesToStore(t *testing.T) {
	want := ReferenceSuggestionSetView{
		Set: ReferenceSuggestionSet{
			SuggestionSetID: "ref-set-1",
			SessionID:       "session-1",
		},
		Freshness:    SuggestionCurrent,
		StateVersion: 1,
	}
	store := &fakeReferenceStore{view: want}
	service := NewService(nil, nil, nil, nil, store)

	got, err := service.ReferenceSuggestionSetByID(context.Background(), "ref-set-1")
	if err != nil {
		t.Fatalf("ReferenceSuggestionSetByID() error = %v", err)
	}
	if got.Set.SuggestionSetID != want.Set.SuggestionSetID || got.Freshness != want.Freshness {
		t.Fatalf("ReferenceSuggestionSetByID() = %#v, want %#v", got, want)
	}
	if store.lastID != "ref-set-1" {
		t.Fatalf("store id = %q, want %q", store.lastID, "ref-set-1")
	}
}

func TestService_GenerationMethodsAreSkeletons(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil)

	_, analysisErr := service.RetrieveAnalysisContext(context.Background(), RetrieveAnalysisContextCommand{})
	if !errors.Is(analysisErr, ErrNotImplemented) {
		t.Fatalf("RetrieveAnalysisContext() error = %v, want %v", analysisErr, ErrNotImplemented)
	}

	_, referenceErr := service.GenerateReferenceSuggestions(context.Background(), GenerateReferenceSuggestionsCommand{})
	if !errors.Is(referenceErr, ErrNotImplemented) {
		t.Fatalf("GenerateReferenceSuggestions() error = %v, want %v", referenceErr, ErrNotImplemented)
	}
}

type fakeReferenceStore struct {
	view   ReferenceSuggestionSetView
	lastID string
}

func (s *fakeReferenceStore) SaveResultForOperation(ctx context.Context, operationID string, result ReferenceSuggestionResult) error {
	return nil
}

func (s *fakeReferenceStore) ViewByID(ctx context.Context, suggestionSetID string) (ReferenceSuggestionSetView, error) {
	s.lastID = suggestionSetID
	return s.view, nil
}

func (s *fakeReferenceStore) MarkStaleByTranscript(ctx context.Context, sessionID SessionID, previousDigest, currentDigest string) error {
	return nil
}

func (s *fakeReferenceStore) MarkStaleByBasis(ctx context.Context, basisDigest string, reason string) error {
	return nil
}
