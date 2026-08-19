package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestHashRefreshTokenIsStableAndURLSafe(t *testing.T) {
	var issuer HMACIssuer
	first := issuer.HashRefreshToken("refresh-token")
	second := issuer.HashRefreshToken("refresh-token")
	other := issuer.HashRefreshToken("different-token")

	if first != second {
		t.Fatalf("HashRefreshToken() = %q and %q, want stable output", first, second)
	}
	if first == other {
		t.Fatalf("HashRefreshToken() = %q, want different token to change the hash", first)
	}
	if len(first) != 43 {
		t.Fatalf("HashRefreshToken() length = %d, want 43", len(first))
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("HashRefreshToken() = %q, want raw URL-safe base64", first)
	}
}

func TestVerifyAccessTokenRejectsTamperingAndInvalidJWTMetadata(t *testing.T) {
	issuer, err := NewHMACIssuer(strings.Repeat("s", 32), "lingow-api", "lingow-client", func(context.Context, string) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("NewHMACIssuer() error = %v", err)
	}
	tokens, err := issuer.Issue(context.Background(), Account{ID: "acct-1"}, Session{ID: "session-1"})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	now := time.Now().Unix()
	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{name: "signature tampered", token: tokens.AccessToken[:len(tokens.AccessToken)-1] + "x"},
		{name: "wrong algorithm", token: signedAccessToken(issuer, map[string]string{"alg": "HS512", "typ": "JWT"}, validClaims(now))},
		{name: "wrong type", token: signedAccessToken(issuer, map[string]string{"alg": "HS256", "typ": "JWS"}, validClaims(now))},
		{name: "wrong issuer", token: signedAccessToken(issuer, map[string]string{"alg": "HS256", "typ": "JWT"}, claimsWith(validClaims(now), "iss", "other-issuer"))},
		{name: "wrong audience", token: signedAccessToken(issuer, map[string]string{"alg": "HS256", "typ": "JWT"}, claimsWith(validClaims(now), "aud", "other-audience"))},
		{name: "expired", token: signedAccessToken(issuer, map[string]string{"alg": "HS256", "typ": "JWT"}, claimsWith(validClaims(now), "exp", now-1))},
		{name: "issued too far in future", token: signedAccessToken(issuer, map[string]string{"alg": "HS256", "typ": "JWT"}, claimsWith(validClaims(now), "iat", now+61))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := issuer.VerifyAccessToken(context.Background(), tt.token); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("VerifyAccessToken() error = %v, want unauthorized", err)
			}
		})
	}
}

func validClaims(now int64) map[string]any {
	return map[string]any{
		"iss": "lingow-api", "aud": "lingow-client", "sub": "acct-1", "sid": "session-1",
		"iat": now, "exp": now + 3600,
	}
}

func claimsWith(claims map[string]any, key string, value any) map[string]any {
	copy := make(map[string]any, len(claims)+1)
	for name, claim := range claims {
		copy[name] = claim
	}
	copy[key] = value
	return copy
}

func signedAccessToken(issuer *HMACIssuer, header map[string]string, claims map[string]any) string {
	unsigned := encodePart(header) + "." + encodePart(claims)
	return unsigned + "." + issuer.sign(unsigned)
}
