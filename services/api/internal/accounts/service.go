package accounts

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct{}

func NewUseCases() *UseCases { return &UseCases{} }

func (*UseCases) CreateAnonymous(context.Context) (AuthResult, error) {
	return AuthResult{}, domain.ErrNotImplemented
}

func (*UseCases) CreatePhoneChallenge(context.Context, string) (string, error) {
	return "", domain.ErrNotImplemented
}

func (*UseCases) VerifyPhone(context.Context, string, string, string) (AuthResult, error) {
	return AuthResult{}, domain.ErrNotImplemented
}

func (*UseCases) Refresh(context.Context, string) (Tokens, error) {
	return Tokens{}, domain.ErrNotImplemented
}

func (*UseCases) Logout(context.Context, string) error { return domain.ErrNotImplemented }

func (*UseCases) Me(context.Context, string) (Account, error) {
	return Account{}, domain.ErrNotImplemented
}

// VerifyAccessToken fails closed until the concrete signing policy is wired.
func (*UseCases) VerifyAccessToken(context.Context, string) (AccessTokenClaims, error) {
	return AccessTokenClaims{}, domain.ErrNotImplemented
}
