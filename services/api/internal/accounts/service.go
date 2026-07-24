package accounts

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// UseCases reserves the account boundary while persistence and token policy are pending.
type UseCases struct{}

// NewUseCases returns the placeholder account service used until dependencies are wired.
func NewUseCases() *UseCases { return &UseCases{} }

// CreateAnonymous will create a temporary account and its first login session.
func (*UseCases) CreateAnonymous(context.Context) (AuthResult, error) {
	return AuthResult{}, domain.ErrNotImplemented
}

// CreatePhoneChallenge will validate a phone target and initiate verification.
func (*UseCases) CreatePhoneChallenge(context.Context, string) (string, error) {
	return "", domain.ErrNotImplemented
}

// VerifyPhone will consume a challenge, resolve an account, and apply anonymous-account merge policy.
func (*UseCases) VerifyPhone(context.Context, string, string, string) (AuthResult, error) {
	return AuthResult{}, domain.ErrNotImplemented
}

// Refresh will rotate credentials after validating an active refresh-token session.
func (*UseCases) Refresh(context.Context, string) (Tokens, error) {
	return Tokens{}, domain.ErrNotImplemented
}

// Logout will revoke the login session associated with the supplied refresh token.
func (*UseCases) Logout(context.Context, string) error { return domain.ErrNotImplemented }

// Me will read the account identified by trusted request context.
func (*UseCases) Me(context.Context, string) (Account, error) {
	return Account{}, domain.ErrNotImplemented
}
