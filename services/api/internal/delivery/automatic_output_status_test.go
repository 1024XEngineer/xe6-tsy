package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type automaticOutputStatusRepositoryStub struct {
	retryRepositoryStub
	statuses  []AutomaticOutputStatus
	err       error
	accountID string
	sessionID string
	limit     int
}

func (r *automaticOutputStatusRepositoryStub) ListAutomaticOutputStatus(_ context.Context, accountID, sessionID string, limit int) ([]AutomaticOutputStatus, error) {
	r.accountID = accountID
	r.sessionID = sessionID
	r.limit = limit
	if r.err != nil {
		return nil, r.err
	}
	return r.statuses, nil
}

type automaticOutputSessionReaderStub struct {
	accountID string
	sessionID string
	err       error
}

func (r *automaticOutputSessionReaderStub) RequireOwnedSession(_ context.Context, accountID, sessionID string) error {
	r.accountID = accountID
	r.sessionID = sessionID
	return r.err
}

func TestListAutomaticOutputStatusScopesAccountAndSession(t *testing.T) {
	repository := &automaticOutputStatusRepositoryStub{statuses: []AutomaticOutputStatus{{
		TurnID: "turn-1", Status: AutomaticTurnRunRestored, UpdatedAt: time.Now().UTC(),
	}}}
	reader := &automaticOutputSessionReaderStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticOutputSessionReader(reader)

	statuses, err := service.ListAutomaticOutputStatus(t.Context(), "account-1", "session-1", 0)
	if err != nil {
		t.Fatalf("ListAutomaticOutputStatus() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != AutomaticTurnRunRestored {
		t.Fatalf("ListAutomaticOutputStatus() = %#v", statuses)
	}
	if repository.accountID != "account-1" || repository.sessionID != "session-1" || repository.limit != defaultAutomaticOutputStatusLimit {
		t.Fatalf("repository input = (%q, %q, %d)", repository.accountID, repository.sessionID, repository.limit)
	}
	if reader.accountID != "account-1" || reader.sessionID != "session-1" {
		t.Fatalf("session reader input = (%q, %q)", reader.accountID, reader.sessionID)
	}
}

func TestListAutomaticOutputStatusAllowsOwnedSessionWithoutRuns(t *testing.T) {
	repository := &automaticOutputStatusRepositoryStub{statuses: []AutomaticOutputStatus{}}
	reader := &automaticOutputSessionReaderStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticOutputSessionReader(reader)

	statuses, err := service.ListAutomaticOutputStatus(t.Context(), "account-1", "session-1", 20)
	if err != nil {
		t.Fatalf("ListAutomaticOutputStatus() error = %v", err)
	}
	if statuses == nil || len(statuses) != 0 {
		t.Fatalf("ListAutomaticOutputStatus() = %#v, want empty statuses", statuses)
	}
}

func TestListAutomaticOutputStatusRejectsUnownedSessionBeforeListing(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "missing session"},
		{name: "other account session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &automaticOutputStatusRepositoryStub{}
			reader := &automaticOutputSessionReaderStub{err: domain.ErrNotFound}
			service := NewPersistentUseCases(repository, nil, nil, nil)
			service.ConfigureAutomaticOutputSessionReader(reader)

			_, err := service.ListAutomaticOutputStatus(t.Context(), "account-1", "session-1", 20)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("ListAutomaticOutputStatus() error = %v, want not found", err)
			}
			if repository.accountID != "" {
				t.Fatalf("repository was called with account %q", repository.accountID)
			}
		})
	}
}

func TestListAutomaticOutputStatusRejectsInvalidScopeAndMissingRepository(t *testing.T) {
	repository := &automaticOutputStatusRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	if _, err := service.ListAutomaticOutputStatus(t.Context(), "", "session-1", 1); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("missing account error = %v, want unauthorized", err)
	}
	if _, err := service.ListAutomaticOutputStatus(t.Context(), "account-1", " ", 1); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("missing session error = %v, want invalid argument", err)
	}
	if _, err := service.ListAutomaticOutputStatus(t.Context(), "account-1", "session-1", 1); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("missing session reader error = %v, want not implemented", err)
	}
	if _, err := NewPersistentUseCases(&retryRepositoryStub{}, nil, nil, nil).ListAutomaticOutputStatus(t.Context(), "account-1", "session-1", 1); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("missing repository error = %v, want not implemented", err)
	}
}
