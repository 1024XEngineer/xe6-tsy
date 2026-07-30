package languages

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestRecordsSessionOwnerMapsNotFound(t *testing.T) {
	owner := NewRecordsSessionOwner(sessionOwnerFake{err: domain.ErrNotFound})

	_, err := owner.GetOwnerAccountID(t.Context(), "session-missing")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetOwnerAccountID() error = %v, want ErrSessionNotFound", err)
	}
}

func TestRecordsSessionOwnerReturnsCanonicalOwner(t *testing.T) {
	owner := NewRecordsSessionOwner(sessionOwnerFake{accountID: "acct_canonical"})

	got, err := owner.GetOwnerAccountID(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("GetOwnerAccountID() error = %v", err)
	}
	if got != "acct_canonical" {
		t.Fatalf("GetOwnerAccountID() = %q, want acct_canonical", got)
	}
}

func TestRecordsSessionOwnerRejectsEmptySessionID(t *testing.T) {
	owner := NewRecordsSessionOwner(sessionOwnerFake{accountID: "acct_1"})

	_, err := owner.GetOwnerAccountID(t.Context(), "")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("GetOwnerAccountID() error = %v, want ErrInvalidRequest", err)
	}
}

func TestRecordsSessionOwnerNilInnerIsNotImplemented(t *testing.T) {
	owner := NewRecordsSessionOwner(nil)

	_, err := owner.GetOwnerAccountID(t.Context(), "session-1")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("GetOwnerAccountID() error = %v, want ErrNotImplemented", err)
	}
}

type sessionOwnerFake struct {
	accountID string
	err       error
}

func (f sessionOwnerFake) AccountIDForSession(context.Context, string) (string, error) {
	return f.accountID, f.err
}
