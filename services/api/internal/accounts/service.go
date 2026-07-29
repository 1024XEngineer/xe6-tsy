package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/authcontext"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct {
	repository Repository
	issuer     TokenIssuer
	verifier   AccessTokenVerifier
	sender     VerificationSender
}

func NewUseCases() *UseCases { return &UseCases{} }

// NewPersistentUseCases wires account policy to durable adapters. The empty
// NewUseCases constructor intentionally remains fail-closed for tests and
// deployments that have not supplied database-backed dependencies.
func NewPersistentUseCases(repository Repository, issuer TokenIssuer, verifier AccessTokenVerifier, sender VerificationSender) *UseCases {
	return &UseCases{repository: repository, issuer: issuer, verifier: verifier, sender: sender}
}

func (u *UseCases) CreateAnonymous(ctx context.Context) (AuthResult, error) {
	if u.repository == nil || u.issuer == nil {
		return AuthResult{}, domain.ErrNotImplemented
	}
	account, err := u.repository.CreateAnonymous(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	return u.issueSession(ctx, account)
}

func (u *UseCases) CreatePhoneChallenge(ctx context.Context, phone string) (string, error) {
	if u.repository == nil || u.sender == nil {
		return "", domain.ErrNotImplemented
	}
	phone = strings.TrimSpace(phone)
	if !strings.HasPrefix(phone, "+") || len(phone) < 8 || len(phone) > 20 {
		return "", domain.ErrInvalidArgument
	}
	code, err := randomDigits(6)
	if err != nil {
		return "", fmt.Errorf("generate verification code: %w", err)
	}
	now := time.Now().UTC()
	challenge := PhoneChallenge{
		ID: "challenge_" + randomID(), PhoneHash: hashValue(phone), CodeHash: hashValue(code),
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	}
	if err := u.repository.CreateChallenge(ctx, challenge); err != nil {
		return "", err
	}
	if err := u.sender.SendCode(ctx, phone, code); err != nil {
		return "", err
	}
	return challenge.ID, nil
}

func (u *UseCases) VerifyPhone(ctx context.Context, challengeID, code, anonymousAccountID string) (AuthResult, error) {
	if u.repository == nil || u.issuer == nil {
		return AuthResult{}, domain.ErrNotImplemented
	}
	if challengeID == "" || len(code) != 6 {
		return AuthResult{}, domain.ErrInvalidArgument
	}
	if err := verifyAnonymousBindingOwnership(ctx, anonymousAccountID); err != nil {
		return AuthResult{}, err
	}
	challenge, err := u.repository.GetChallenge(ctx, challengeID)
	if err != nil {
		return AuthResult{}, err
	}
	if err := u.repository.ConsumeChallenge(ctx, challengeID, code); err != nil {
		return AuthResult{}, err
	}
	// The repository returns the phone-bound account while keeping the phone hash
	// itself out of the public model and logs.
	challengeAccount, err := u.repository.FindOrCreateByPhoneHash(ctx, challenge.PhoneHash)
	if err != nil {
		return AuthResult{}, err
	}
	if anonymousAccountID != "" {
		challengeAccount, err = u.repository.BindAnonymous(ctx, anonymousAccountID, challengeAccount.ID)
		if err != nil {
			return AuthResult{}, err
		}
	}
	return u.issueSession(ctx, challengeAccount)
}

// verifyAnonymousBindingOwnership makes the optional account merge an
// authenticated operation. A phone verification request without an anonymous
// account remains public (it creates or resolves the registered account), but
// supplying an anonymous account ID requires a trusted context for that exact
// account. This check lives in the use case as well as the HTTP adapter so
// internal callers cannot bypass the ownership boundary.
func verifyAnonymousBindingOwnership(ctx context.Context, anonymousAccountID string) error {
	if anonymousAccountID == "" {
		return nil
	}
	accountID, ok := authcontext.AccountID(ctx)
	if !ok {
		return domain.ErrUnauthorized
	}
	if accountID != anonymousAccountID {
		return domain.ErrForbidden
	}
	return nil
}

func (u *UseCases) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	if u.repository == nil || u.issuer == nil {
		return Tokens{}, domain.ErrNotImplemented
	}
	if refreshToken == "" {
		return Tokens{}, domain.ErrInvalidArgument
	}
	session, err := u.repository.GetSessionByRefreshHash(ctx, u.issuer.HashRefreshToken(refreshToken))
	if err != nil {
		return Tokens{}, mapCredentialLookupError(err)
	}
	if err := u.repository.RevokeSession(ctx, session.ID); err != nil {
		return Tokens{}, err
	}
	account, err := u.repository.GetAccount(ctx, session.AccountID)
	if err != nil {
		return Tokens{}, err
	}
	result, err := u.issueSession(ctx, account)
	if err != nil {
		return Tokens{}, err
	}
	return result.Tokens, nil
}

func (u *UseCases) Logout(ctx context.Context, refreshToken string) error {
	if u.repository == nil || u.issuer == nil {
		return domain.ErrNotImplemented
	}
	if refreshToken == "" {
		return domain.ErrInvalidArgument
	}
	session, err := u.repository.GetSessionByRefreshHash(ctx, u.issuer.HashRefreshToken(refreshToken))
	if err != nil {
		return mapCredentialLookupError(err)
	}
	return u.repository.RevokeSession(ctx, session.ID)
}

func (u *UseCases) Me(ctx context.Context, accountID string) (Account, error) {
	if u.repository == nil {
		return Account{}, domain.ErrNotImplemented
	}
	if accountID == "" {
		return Account{}, domain.ErrUnauthorized
	}
	return u.repository.GetAccount(ctx, accountID)
}

// VerifyAccessToken delegates all token parsing and signature checks to the
// configured verifier; no client-supplied account identity is accepted here.
func (u *UseCases) VerifyAccessToken(ctx context.Context, token string) (AccessTokenClaims, error) {
	if u.verifier == nil {
		return AccessTokenClaims{}, domain.ErrNotImplemented
	}
	return u.verifier.VerifyAccessToken(ctx, token)
}

func (u *UseCases) issueSession(ctx context.Context, account Account) (AuthResult, error) {
	session := Session{ID: "auths_" + randomID(), AccountID: account.ID, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}
	tokens, err := u.issuer.Issue(ctx, account, session)
	if err != nil {
		return AuthResult{}, err
	}
	session.RefreshHash = u.issuer.HashRefreshToken(tokens.RefreshToken)
	if err := u.repository.CreateSession(ctx, session); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Account: account, Tokens: tokens}, nil
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return hex.EncodeToString(value)
}

func randomDigits(length int) (string, error) {
	value := make([]byte, length)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	for i := range value {
		value[i] = '0' + value[i]%10
	}
	return string(value), nil
}

func hashValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// Refresh credentials are authentication material, not public resources. A
// missing, expired, or already-rotated session therefore has the same external
// meaning as any other invalid credential and must not surface as a 404.
func mapCredentialLookupError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrUnauthorized
	}
	return err
}
