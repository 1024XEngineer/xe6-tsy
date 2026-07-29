package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestHMACIssuerRequiresSessionLifecycleValidation(t *testing.T) {
	secret := strings.Repeat("s", 32)
	if _, err := NewHMACIssuer(secret, "lingow-api", "lingow-client", nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("NewHMACIssuer() error = %v, want invalid argument", err)
	}
	if _, err := NewHMACIssuerWithAccount(secret, "lingow-api", "lingow-client", nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("NewHMACIssuerWithAccount() error = %v, want invalid argument", err)
	}
}

func TestHMACIssuerWithAccountBindsSessionToSubject(t *testing.T) {
	var gotSessionID, gotAccountID string
	issuer, err := NewHMACIssuerWithAccount(strings.Repeat("s", 32), "lingow-api", "lingow-client", func(_ context.Context, sessionID, accountID string) (bool, error) {
		gotSessionID = sessionID
		gotAccountID = accountID
		return sessionID == "auths-1" && accountID == "acct-1", nil
	})
	if err != nil {
		t.Fatalf("NewHMACIssuerWithAccount() error = %v", err)
	}
	tokens, err := issuer.Issue(context.Background(), Account{ID: "acct-1"}, Session{ID: "auths-1"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := issuer.VerifyAccessToken(context.Background(), tokens.AccessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}
	if claims.AccountID != "acct-1" || claims.SessionID != "auths-1" {
		t.Fatalf("claims = %#v", claims)
	}
	if gotSessionID != "auths-1" || gotAccountID != "acct-1" {
		t.Fatalf("active callback = (%q, %q)", gotSessionID, gotAccountID)
	}
}

func TestHMACIssuerWithAccountRejectsMovedSession(t *testing.T) {
	issuer, err := NewHMACIssuerWithAccount(strings.Repeat("s", 32), "lingow-api", "lingow-client", func(_ context.Context, _, accountID string) (bool, error) {
		// The durable session now belongs to the registered account, while the
		// token subject remains the pre-bind anonymous account.
		return accountID == "acct-registered", nil
	})
	if err != nil {
		t.Fatalf("NewHMACIssuerWithAccount() error = %v", err)
	}
	tokens, err := issuer.Issue(context.Background(), Account{ID: "acct-anonymous"}, Session{ID: "auths-1"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := issuer.VerifyAccessToken(context.Background(), tokens.AccessToken); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("VerifyAccessToken() error = %v, want unauthorized", err)
	}
}

func TestLegacyHMACIssuerStillChecksSessionActivity(t *testing.T) {
	issuer, err := NewHMACIssuer(strings.Repeat("s", 32), "lingow-api", "lingow-client", func(_ context.Context, sessionID string) (bool, error) {
		return sessionID == "auths-1", nil
	})
	if err != nil {
		t.Fatalf("NewHMACIssuer() error = %v", err)
	}
	tokens, err := issuer.Issue(context.Background(), Account{ID: "acct-1"}, Session{ID: "auths-1"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := issuer.VerifyAccessToken(context.Background(), tokens.AccessToken); err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}
}
