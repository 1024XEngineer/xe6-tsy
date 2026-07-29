package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/authcontext"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type UseCases struct {
	repository Repository
	issuer     TokenIssuer
	verifier   AccessTokenVerifier
	sender     VerificationSender
	digester   *CredentialDigester
}

var (
	canonicalPhonePattern   = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	verificationCodePattern = regexp.MustCompile(`^[0-9]{6}$`)
)

func NewUseCases() *UseCases { return &UseCases{} }

// NewPersistentUseCases wires account policy to durable adapters. The empty
// NewUseCases constructor intentionally remains fail-closed for tests and
// deployments that have not supplied database-backed dependencies.
func NewPersistentUseCases(repository Repository, issuer TokenIssuer, verifier AccessTokenVerifier, sender VerificationSender, digesters ...*CredentialDigester) *UseCases {
	var digester *CredentialDigester
	if len(digesters) > 0 {
		digester = digesters[0]
	}
	return &UseCases{repository: repository, issuer: issuer, verifier: verifier, sender: sender, digester: digester}
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
	if u.repository == nil || u.sender == nil || u.digester == nil {
		return "", domain.ErrNotImplemented
	}
	if !canonicalPhonePattern.MatchString(phone) {
		return "", domain.ErrInvalidArgument
	}
	code, err := randomDigits(6)
	if err != nil {
		return "", fmt.Errorf("generate verification code: %w", err)
	}
	challengeID, err := randomID()
	if err != nil {
		return "", fmt.Errorf("generate challenge ID: %w", err)
	}
	now := time.Now().UTC()
	id := "challenge_" + challengeID
	legacyPhoneHash, err := u.digester.EncryptLegacyPhoneHash(hashValue(phone))
	if err != nil {
		return "", fmt.Errorf("protect legacy phone lookup: %w", err)
	}
	challenge := PhoneChallenge{
		ID: id, PhoneHash: u.digester.PhoneHash(phone), LegacyPhoneHash: legacyPhoneHash,
		CodeHash: u.digester.CodeHash(id, code), DigestVersion: 2,
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now, MaxAttempts: defaultPhoneChallengeMaxAttempts,
	}
	if err := u.repository.CreateChallenge(ctx, challenge); err != nil {
		return "", err
	}
	if err := u.sender.SendCode(ctx, phone, code); err != nil {
		// Delivery failures can be ambiguous: the provider may have accepted the
		// message before the request timed out. Keep the challenge so every send
		// attempt remains covered by the cooldown and rolling quota.
		return "", err
	}
	return challenge.ID, nil
}

func (u *UseCases) VerifyPhone(ctx context.Context, challengeID, code, anonymousAccountID string) (result AuthResult, err error) {
	if u.repository == nil || u.issuer == nil || u.digester == nil {
		return AuthResult{}, domain.ErrNotImplemented
	}
	if challengeID == "" || !verificationCodePattern.MatchString(code) {
		return AuthResult{}, domain.ErrInvalidArgument
	}
	if err := verifyAnonymousBindingOwnership(ctx, anonymousAccountID); err != nil {
		return AuthResult{}, err
	}
	challenge, err := u.repository.ConsumeChallenge(ctx, challengeID, u.digester.CodeHash(challengeID, code))
	if err != nil {
		return AuthResult{}, err
	}
	completed := false
	defer func() {
		if !completed {
			// Consumption is committed separately because failed guesses must be
			// durable. Restore only a valid consumed code when account/session
			// work fails, otherwise a transient dependency failure burns the code.
			if restoreErr := u.repository.RestoreChallenge(ctx, challenge.ID); restoreErr != nil {
				err = fmt.Errorf("complete phone verification: %w", errors.Join(err, fmt.Errorf("restore consumed challenge: %w", restoreErr)))
			}
		}
	}()
	// The repository returns the phone-bound account while keeping the phone hash
	// itself out of the public model and logs.
	legacyPhoneHash, err := u.digester.DecryptLegacyPhoneHash(challenge.LegacyPhoneHash)
	if err != nil {
		return AuthResult{}, domain.ErrUnauthorized
	}
	challengeAccount, err := u.repository.FindOrCreateByPhoneHashes(ctx, challenge.PhoneHash, legacyPhoneHash)
	if err != nil {
		return AuthResult{}, err
	}
	if anonymousAccountID != "" {
		challengeAccount, err = u.repository.BindAnonymous(ctx, anonymousAccountID, challengeAccount.ID)
		if err != nil {
			return AuthResult{}, err
		}
	}
	result, err = u.issueSession(ctx, challengeAccount)
	if err != nil {
		return AuthResult{}, err
	}
	completed = true
	return result, nil
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
	account, err := u.repository.GetAccount(ctx, session.AccountID)
	if err != nil {
		return Tokens{}, err
	}
	successor, tokens, err := u.prepareSession(ctx, account)
	if err != nil {
		return Tokens{}, err
	}
	if err := u.repository.RotateSession(ctx, session.ID, successor); err != nil {
		return Tokens{}, mapCredentialLookupError(err)
	}
	return tokens, nil
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
	return mapCredentialLookupError(u.repository.RevokeSession(ctx, session.ID))
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
	session, tokens, err := u.prepareSession(ctx, account)
	if err != nil {
		return AuthResult{}, err
	}
	if err := u.repository.CreateSession(ctx, session); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{Account: account, Tokens: tokens}, nil
}

func (u *UseCases) prepareSession(ctx context.Context, account Account) (Session, Tokens, error) {
	now := time.Now().UTC()
	sessionID, err := randomID()
	if err != nil {
		return Session{}, Tokens{}, fmt.Errorf("generate session ID: %w", err)
	}
	session := Session{ID: "auths_" + sessionID, AccountID: account.ID, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	tokens, err := u.issuer.Issue(ctx, account, session)
	if err != nil {
		return Session{}, Tokens{}, err
	}
	session.RefreshHash = u.issuer.HashRefreshToken(tokens.RefreshToken)
	return session, tokens, nil
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func randomDigits(length int) (string, error) {
	digits := make([]byte, length)
	random := make([]byte, length)
	for written := 0; written < length; {
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		// 250 is the largest multiple of ten below 256. Discarding the tail
		// keeps every decimal digit equally likely.
		for _, value := range random {
			if value >= 250 {
				continue
			}
			digits[written] = '0' + value%10
			written++
			if written == length {
				break
			}
		}
	}
	return string(digits), nil
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
