package accounts

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// UseCases reserves the account boundary while persistence and token policy are pending.
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
