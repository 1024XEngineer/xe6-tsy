package matter

import (
	"context"
	"errors"
	"testing"
)

func TestServiceImplementsApplicationInterfaces(t *testing.T) {
	var _ MatterSuggestionGenerator = (*service)(nil)
	var _ MatterSuggestionReader = (*service)(nil)
	var _ API = (*service)(nil)
}

func TestService_MatterSuggestionSetByIDDelegatesToStore(t *testing.T) {
	want := MatterSuggestionSetView{
		Set: MatterSuggestionSet{
			SuggestionSetID: "set-1",
			SessionID:       "session-1",
		},
		Freshness:    SuggestionCurrent,
		StateVersion: 1,
	}
	store := &fakeStore{view: want}
	service := NewService(nil, store)

	got, err := service.MatterSuggestionSetByID(context.Background(), "set-1")
	if err != nil {
		t.Fatalf("MatterSuggestionSetByID() error = %v", err)
	}
	if got.Set.SuggestionSetID != want.Set.SuggestionSetID || got.Freshness != want.Freshness {
		t.Fatalf("MatterSuggestionSetByID() = %#v, want %#v", got, want)
	}
	if store.lastID != "set-1" {
		t.Fatalf("store id = %q, want %q", store.lastID, "set-1")
	}
}

func TestService_GenerateMatterSuggestionsIsSkeleton(t *testing.T) {
	service := NewService(nil, nil)

	_, err := service.GenerateMatterSuggestions(context.Background(), GenerateMatterSuggestionsCommand{})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GenerateMatterSuggestions() error = %v, want %v", err, ErrNotImplemented)
	}
}

type fakeStore struct {
	view   MatterSuggestionSetView
	lastID string
}

func (s *fakeStore) SaveResultForOperation(ctx context.Context, operationID string, result MatterSuggestionResult) error {
	return nil
}

func (s *fakeStore) ViewByID(ctx context.Context, suggestionSetID string) (MatterSuggestionSetView, error) {
	s.lastID = suggestionSetID
	return s.view, nil
}

func (s *fakeStore) MarkStaleByTranscript(ctx context.Context, sessionID SessionID, previousDigest, currentDigest string) error {
	return nil
}
