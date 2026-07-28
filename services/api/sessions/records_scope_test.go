package sessions

import (
	"context"
	"errors"
	"testing"
)

func TestRecordsScopeReaderPaginatesAccountSessions(t *testing.T) {
	next := "page_2"
	repository := &recordsScopeRepository{
		pages: map[string]ListPage{
			"": {
				Sessions:   []VoiceSessionListItem{{ID: "session_01"}},
				NextCursor: &next,
			},
			"page_2": {Sessions: []VoiceSessionListItem{{ID: "session_02"}}},
		},
	}
	reader, err := NewRecordsScopeReader(repository)
	if err != nil {
		t.Fatalf("NewRecordsScopeReader() error = %v", err)
	}

	ids, err := reader.SessionIDsForAccount(context.Background(), "account_01")
	if err != nil {
		t.Fatalf("SessionIDsForAccount() error = %v", err)
	}
	if len(ids) != 2 || ids[0] != "session_01" || ids[1] != "session_02" {
		t.Fatalf("SessionIDsForAccount() = %#v, want two ordered sessions", ids)
	}
	if len(repository.filters) != 2 || repository.filters[0].AccountID != "account_01" || repository.filters[1].Cursor != "page_2" {
		t.Fatalf("repository filters = %#v", repository.filters)
	}
}

func TestRecordsScopeReaderRejectsInvalidScopeResponses(t *testing.T) {
	next := "next"
	a := "a"
	b := "b"
	tests := []struct {
		name  string
		pages map[string]ListPage
	}{
		{name: "empty session ID", pages: map[string]ListPage{"": {Sessions: []VoiceSessionListItem{{}}}}},
		{name: "empty next cursor", pages: map[string]ListPage{"": {NextCursor: new(string)}}},
		{name: "repeated next cursor", pages: map[string]ListPage{"": {NextCursor: &next}, "next": {NextCursor: &next}}},
		{name: "cursor cycle", pages: map[string]ListPage{"": {NextCursor: &a}, "a": {NextCursor: &b}, "b": {NextCursor: &a}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &recordsScopeRepository{pages: test.pages}
			reader, err := NewRecordsScopeReader(repository)
			if err != nil {
				t.Fatalf("NewRecordsScopeReader() error = %v", err)
			}
			if _, err := reader.SessionIDsForAccount(context.Background(), "account_01"); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("SessionIDsForAccount() error = %v, want invalid request", err)
			}
		})
	}
}

func TestRecordsScopeReaderPropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("list failed")
	repository := &recordsScopeRepository{err: wantErr}
	reader, err := NewRecordsScopeReader(repository)
	if err != nil {
		t.Fatalf("NewRecordsScopeReader() error = %v", err)
	}
	if _, err := reader.SessionIDsForAccount(context.Background(), "account_01"); !errors.Is(err, wantErr) {
		t.Fatalf("SessionIDsForAccount() error = %v, want %v", err, wantErr)
	}
}

func TestRecordsScopeReaderRejectsEmptyAccount(t *testing.T) {
	repository := &recordsScopeRepository{}
	reader, err := NewRecordsScopeReader(repository)
	if err != nil {
		t.Fatalf("NewRecordsScopeReader() error = %v", err)
	}
	if _, err := reader.SessionIDsForAccount(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("SessionIDsForAccount() error = %v, want unauthorized", err)
	}
	if len(repository.filters) != 0 {
		t.Fatalf("repository filters = %#v, want no calls", repository.filters)
	}
}

type recordsScopeRepository struct {
	pages   map[string]ListPage
	filters []ListFilter
	err     error
}

func (r *recordsScopeRepository) List(_ context.Context, filter ListFilter) (ListPage, error) {
	r.filters = append(r.filters, filter)
	return r.pages[filter.Cursor], r.err
}
