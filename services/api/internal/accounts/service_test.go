package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/authcontext"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestMapCredentialLookupErrorHidesMissingSession(t *testing.T) {
	if err := mapCredentialLookupError(domain.ErrNotFound); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("mapCredentialLookupError() = %v, want unauthorized", err)
	}
}

func TestMapCredentialLookupErrorPreservesDependencyFailure(t *testing.T) {
	want := errors.New("database unavailable")
	if got := mapCredentialLookupError(want); !errors.Is(got, want) {
		t.Fatalf("mapCredentialLookupError() = %v, want %v", got, want)
	}
}

func TestVerifyAnonymousBindingOwnershipRequiresExactTrustedAccount(t *testing.T) {
	if err := verifyAnonymousBindingOwnership(context.Background(), "acct-anonymous"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("missing context error = %v, want unauthorized", err)
	}
	wrong := authcontext.WithAccountID(context.Background(), "acct-other")
	if err := verifyAnonymousBindingOwnership(wrong, "acct-anonymous"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("wrong context error = %v, want forbidden", err)
	}
	matching := authcontext.WithAccountID(context.Background(), "acct-anonymous")
	if err := verifyAnonymousBindingOwnership(matching, "acct-anonymous"); err != nil {
		t.Fatalf("matching context error = %v", err)
	}
}

func TestVerifyAnonymousBindingOwnershipAllowsUnboundPhoneLogin(t *testing.T) {
	if err := verifyAnonymousBindingOwnership(context.Background(), ""); err != nil {
		t.Fatalf("empty anonymous account ID error = %v", err)
	}
}
