package accounts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestPhoneAndVerificationCodeContracts(t *testing.T) {
	for _, phone := range []string{"+8613800000000", "+12025550123", "+12345678"} {
		if !canonicalPhonePattern.MatchString(phone) {
			t.Fatalf("canonicalPhonePattern rejected %q", phone)
		}
	}
	for _, phone := range []string{"13800000000", "+012345678", "+1 2025550123", "+1234567", "+1234567890123456", "+１２３４５６７８"} {
		if canonicalPhonePattern.MatchString(phone) {
			t.Fatalf("canonicalPhonePattern accepted %q", phone)
		}
	}
	for _, code := range []string{"12345", "1234567", "abcdef", "１２３４５６"} {
		if verificationCodePattern.MatchString(code) {
			t.Fatalf("verificationCodePattern accepted %q", code)
		}
	}
	if !verificationCodePattern.MatchString("012345") {
		t.Fatal("verificationCodePattern rejected six ASCII digits")
	}
}

func TestPhoneVerificationRequiresCredentialDigester(t *testing.T) {
	service := NewPersistentUseCases(newRefreshTestRepository(), &refreshTestIssuer{}, nil, verificationSenderStub{})
	if _, err := service.CreatePhoneChallenge(t.Context(), "+8613800000000"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("CreatePhoneChallenge() error = %v, want not implemented without digester", err)
	}
	if _, err := service.VerifyPhone(t.Context(), "challenge_test", "123456", ""); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("VerifyPhone() error = %v, want not implemented without digester", err)
	}
}

func TestPhoneChallengeSendFailureKeepsChallengeForRateLimiting(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	repository := &challengeTestRepository{refreshTestRepository: *newRefreshTestRepository()}
	senderErr := errors.New("sms provider unavailable")
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, verificationSenderErrorStub{err: senderErr}, digester)

	if _, err := service.CreatePhoneChallenge(t.Context(), "+8613800000000"); !errors.Is(err, senderErr) {
		t.Fatalf("CreatePhoneChallenge() error = %v, want sender error", err)
	}
	if repository.created.ID == "" {
		t.Fatal("CreatePhoneChallenge() did not persist a challenge before send")
	}
	if got, want := repository.created.LegacyRateLimitHash, hashValue("+8613800000000"); got != want {
		t.Fatalf("legacy rate-limit hash = %q, want %q", got, want)
	}
	if repository.created.LegacyPhoneHash == repository.created.LegacyRateLimitHash {
		t.Fatal("persisted legacy lookup value was not encrypted")
	}
}

func TestPhoneVerificationRestoresChallengeWhenSessionPersistenceFails(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	legacyHash, err := digester.EncryptLegacyPhoneHash(hashValue("+8613800000000"))
	if err != nil {
		t.Fatalf("EncryptLegacyPhoneHash() error = %v", err)
	}
	persistErr := errors.New("persist session")
	repository := &challengeTestRepository{
		refreshTestRepository: *newRefreshTestRepository(),
		consumed: PhoneChallenge{
			ID: "challenge_test", PhoneHash: digester.PhoneHash("+8613800000000"), LegacyPhoneHash: legacyHash,
		},
		phoneAccount:     Account{ID: "acct-phone", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
		createSessionErr: persistErr,
	}
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, verificationSenderStub{}, digester)

	if _, err := service.VerifyPhone(t.Context(), "challenge_test", "123456", ""); !errors.Is(err, persistErr) {
		t.Fatalf("VerifyPhone() error = %v, want %v", err, persistErr)
	}
	if repository.restoredID != "challenge_test" {
		t.Fatalf("restored challenge = %q, want %q", repository.restoredID, "challenge_test")
	}
}

func TestPhoneVerificationUsesAtomicBindForAnonymousAccount(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	legacyHash, err := digester.EncryptLegacyPhoneHash(hashValue("+8613800000000"))
	if err != nil {
		t.Fatalf("EncryptLegacyPhoneHash() error = %v", err)
	}
	repository := &challengeTestRepository{
		refreshTestRepository: *newRefreshTestRepository(),
		consumed: PhoneChallenge{
			ID: "challenge_atomic", PhoneHash: digester.PhoneHash("+8613800000000"), LegacyPhoneHash: legacyHash,
		},
		phoneAccount: Account{ID: "acct-registered", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
	}
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, verificationSenderStub{}, digester)
	ctx := authcontext.WithAccountID(t.Context(), "acct-anonymous")

	result, err := service.VerifyPhone(ctx, "challenge_atomic", "123456", "acct-anonymous")
	if err != nil {
		t.Fatalf("VerifyPhone() error = %v", err)
	}
	if !repository.atomicBindCalled {
		t.Fatal("VerifyPhone() did not use atomic anonymous binding")
	}
	if repository.createSessionCalls != 0 {
		t.Fatalf("CreateSession() calls = %d, want 0", repository.createSessionCalls)
	}
	if result.Account.ID != "acct-registered" || result.Account.Kind != AccountKindRegistered {
		t.Fatalf("VerifyPhone() account = %#v, want registered account", result.Account)
	}
}

func TestPhoneVerificationDoesNotBindWhenSessionIssuanceFails(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	legacyHash, err := digester.EncryptLegacyPhoneHash(hashValue("+8613800000000"))
	if err != nil {
		t.Fatalf("EncryptLegacyPhoneHash() error = %v", err)
	}
	repository := &challengeTestRepository{
		refreshTestRepository: *newRefreshTestRepository(),
		consumed: PhoneChallenge{
			ID: "challenge_atomic_fail", PhoneHash: digester.PhoneHash("+8613800000000"), LegacyPhoneHash: legacyHash,
		},
		phoneAccount: Account{ID: "acct-registered", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
	}
	issuer := &refreshTestIssuer{issueErr: errors.New("issuer unavailable")}
	service := NewPersistentUseCases(repository, issuer, nil, verificationSenderStub{}, digester)

	if _, err := service.VerifyPhone(authcontext.WithAccountID(t.Context(), "acct-anonymous"), "challenge_atomic_fail", "123456", "acct-anonymous"); !errors.Is(err, issuer.issueErr) {
		t.Fatalf("VerifyPhone() error = %v, want issuer error", err)
	}
	if repository.atomicBindCalled {
		t.Fatal("VerifyPhone() bound anonymous account despite token issuance failure")
	}
}

func TestPhoneVerificationRestoreSurvivesRequestCancellation(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	legacyHash, err := digester.EncryptLegacyPhoneHash(hashValue("+8613800000000"))
	if err != nil {
		t.Fatalf("EncryptLegacyPhoneHash() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	repository := &challengeTestRepository{
		refreshTestRepository: *newRefreshTestRepository(),
		consumed: PhoneChallenge{
			ID: "challenge_cancelled", PhoneHash: digester.PhoneHash("+8613800000000"), LegacyPhoneHash: legacyHash,
		},
		phoneAccount:      Account{ID: "acct-phone", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
		createSessionErr:  context.Canceled,
		createSessionHook: cancel,
	}
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, verificationSenderStub{}, digester)

	if _, err := service.VerifyPhone(ctx, "challenge_cancelled", "123456", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyPhone() error = %v, want canceled", err)
	}
	if repository.restoreContextErr != nil {
		t.Fatalf("RestoreChallenge() context error = %v, want nil", repository.restoreContextErr)
	}
	if !repository.restoreHasDeadline {
		t.Fatal("RestoreChallenge() context has no deadline")
	}
}

func TestRefreshLeavesCurrentSessionUsableWhenSuccessorPersistenceFails(t *testing.T) {
	persistErr := errors.New("persist successor")
	repository := newRefreshTestRepository()
	repository.rotateErr = persistErr
	issuer := &refreshTestIssuer{}
	service := NewPersistentUseCases(repository, issuer, nil, nil)

	if _, err := service.Refresh(context.Background(), "current-token"); !errors.Is(err, persistErr) {
		t.Fatalf("Refresh() error = %v, want %v", err, persistErr)
	}

	repository.mu.Lock()
	if !repository.currentActive {
		t.Fatal("failed rotation revoked the current session")
	}
	if len(repository.successors) != 0 {
		t.Fatalf("failed rotation stored %d successors, want 0", len(repository.successors))
	}
	if repository.createCalls != 0 || repository.revokeCalls != 0 {
		t.Fatalf("Refresh() used non-atomic operations: create=%d revoke=%d", repository.createCalls, repository.revokeCalls)
	}
	repository.rotateErr = nil
	repository.mu.Unlock()

	if _, err := service.Refresh(context.Background(), "current-token"); err != nil {
		t.Fatalf("Refresh() retry with current token error = %v", err)
	}
}

func TestLogoutRevokesSessionUsingRefreshTokenHash(t *testing.T) {
	repository := newRefreshTestRepository()
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, nil)

	if err := service.Logout(context.Background(), "current-token"); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.currentActive {
		t.Fatal("Logout() left the current session active")
	}
	if repository.revokeCalls != 1 {
		t.Fatalf("RevokeSession() calls = %d, want 1", repository.revokeCalls)
	}
}

func TestLogoutRejectsMissingInputsAndDependencies(t *testing.T) {
	revokeErr := errors.New("database unavailable")
	tests := []struct {
		name    string
		service *UseCases
		token   string
		want    error
	}{
		{name: "missing repository", service: NewPersistentUseCases(nil, &refreshTestIssuer{}, nil, nil), token: "current-token", want: domain.ErrNotImplemented},
		{name: "missing issuer", service: NewPersistentUseCases(newRefreshTestRepository(), nil, nil, nil), token: "current-token", want: domain.ErrNotImplemented},
		{name: "empty token", service: NewPersistentUseCases(newRefreshTestRepository(), &refreshTestIssuer{}, nil, nil), token: "", want: domain.ErrInvalidArgument},
		{name: "missing session", service: NewPersistentUseCases(func() *refreshTestRepository {
			repository := newRefreshTestRepository()
			repository.currentActive = false
			return repository
		}(), &refreshTestIssuer{}, nil, nil), token: "current-token", want: domain.ErrUnauthorized},
		{name: "revocation failure", service: NewPersistentUseCases(&logoutErrorRepository{refreshTestRepository: *newRefreshTestRepository(), revokeErr: revokeErr}, &refreshTestIssuer{}, nil, nil), token: "current-token", want: revokeErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.service.Logout(context.Background(), test.token)
			if test.want == nil {
				if err != nil {
					t.Fatalf("Logout() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Logout() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMeReturnsAccountAndRejectsMissingContextIdentity(t *testing.T) {
	repository := newRefreshTestRepository()
	service := NewPersistentUseCases(repository, nil, nil, nil)

	got, err := service.Me(context.Background(), repository.account.ID)
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if got.ID != repository.account.ID || got.Kind != repository.account.Kind {
		t.Fatalf("Me() account = %#v, want %#v", got, repository.account)
	}
	if _, err := service.Me(context.Background(), ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Me() empty account error = %v, want unauthorized", err)
	}
}

func TestMePropagatesLookupFailure(t *testing.T) {
	want := errors.New("database unavailable")
	service := NewPersistentUseCases(&accountErrorRepository{refreshTestRepository: *newRefreshTestRepository(), accountErr: want}, nil, nil, nil)

	if _, err := service.Me(context.Background(), "acct-current"); !errors.Is(err, want) {
		t.Fatalf("Me() error = %v, want %v", err, want)
	}
}

func TestVerifyAccessTokenDelegatesToConfiguredVerifier(t *testing.T) {
	want := AccessTokenClaims{AccountID: "acct-1", SessionID: "auths-1"}
	verifier := &accessTokenVerifierStub{claims: want}
	service := NewPersistentUseCases(newRefreshTestRepository(), nil, verifier, nil)

	got, err := service.VerifyAccessToken(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}
	if got != want {
		t.Fatalf("VerifyAccessToken() claims = %#v, want %#v", got, want)
	}
	if verifier.token != "access-token" {
		t.Fatalf("VerifyAccessToken() token = %q, want access-token", verifier.token)
	}
}

func TestVerifyAccessTokenRejectsMissingVerifierAndPropagatesFailure(t *testing.T) {
	if _, err := NewPersistentUseCases(newRefreshTestRepository(), nil, nil, nil).VerifyAccessToken(context.Background(), "access-token"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("VerifyAccessToken() error = %v, want not implemented", err)
	}
	want := errors.New("verification failed")
	service := NewPersistentUseCases(newRefreshTestRepository(), nil, &accessTokenVerifierStub{err: want}, nil)
	if _, err := service.VerifyAccessToken(context.Background(), "access-token"); !errors.Is(err, want) {
		t.Fatalf("VerifyAccessToken() error = %v, want %v", err, want)
	}
}

func TestConcurrentRefreshHasSingleSuccessor(t *testing.T) {
	repository := newRefreshTestRepository()
	repository.lookupArrived = make(chan struct{}, 2)
	repository.lookupRelease = make(chan struct{})
	issuer := &refreshTestIssuer{}
	service := NewPersistentUseCases(repository, issuer, nil, nil)

	type result struct {
		tokens Tokens
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			tokens, err := service.Refresh(context.Background(), "current-token")
			results <- result{tokens: tokens, err: err}
		}()
	}
	// Both callers resolve the same active credential before either is allowed
	// to rotate it, exercising the repository's conditional single-winner path.
	<-repository.lookupArrived
	<-repository.lookupArrived
	close(repository.lookupRelease)

	var winner Tokens
	successes := 0
	unauthorized := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			winner = result.tokens
		case errors.Is(result.err, domain.ErrUnauthorized):
			unauthorized++
		default:
			t.Fatalf("Refresh() concurrent error = %v, want unauthorized", result.err)
		}
	}
	if successes != 1 || unauthorized != 1 {
		t.Fatalf("concurrent results = %d success, %d unauthorized; want 1 and 1", successes, unauthorized)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.currentActive {
		t.Fatal("successful rotation left the current session active")
	}
	if len(repository.successors) != 1 {
		t.Fatalf("stored successors = %d, want 1", len(repository.successors))
	}
	if repository.successors[0].RefreshHash != issuer.HashRefreshToken(winner.RefreshToken) {
		t.Fatalf("stored successor hash = %q, want winner token hash", repository.successors[0].RefreshHash)
	}
}

type refreshTestRepository struct {
	mu            sync.Mutex
	account       Account
	current       Session
	currentActive bool
	rotateErr     error
	successors    []Session
	createCalls   int
	revokeCalls   int
	lookupArrived chan struct{}
	lookupRelease chan struct{}
}

type challengeTestRepository struct {
	refreshTestRepository
	created            PhoneChallenge
	consumed           PhoneChallenge
	restoredID         string
	restoreContextErr  error
	restoreHasDeadline bool
	phoneAccount       Account
	createSessionErr   error
	createSessionHook  func()
	atomicBindCalled   bool
	createSessionCalls int
}

func (r *challengeTestRepository) CreateChallenge(_ context.Context, challenge PhoneChallenge) error {
	r.created = challenge
	return nil
}

func (r *challengeTestRepository) ConsumeChallenge(_ context.Context, id, _ string) (PhoneChallenge, error) {
	if id != r.consumed.ID {
		return PhoneChallenge{}, domain.ErrNotFound
	}
	return r.consumed, nil
}

func (r *challengeTestRepository) RestoreChallenge(ctx context.Context, id string) error {
	r.restoredID = id
	r.restoreContextErr = ctx.Err()
	_, r.restoreHasDeadline = ctx.Deadline()
	return nil
}

func (r *challengeTestRepository) FindOrCreateByPhoneHashes(context.Context, string, string) (Account, error) {
	return r.phoneAccount, nil
}

func (r *challengeTestRepository) CreateSession(ctx context.Context, session Session) error {
	r.createSessionCalls++
	if r.createSessionErr != nil {
		if r.createSessionHook != nil {
			r.createSessionHook()
		}
		return r.createSessionErr
	}
	return r.refreshTestRepository.CreateSession(ctx, session)
}

func (r *challengeTestRepository) BindAnonymousAndCreateSession(_ context.Context, anonymousID, registeredID string, session Session) (Account, error) {
	r.atomicBindCalled = true
	if anonymousID != "acct-anonymous" || registeredID != r.phoneAccount.ID || session.AccountID != registeredID {
		return Account{}, domain.ErrConflict
	}
	if r.createSessionErr != nil {
		return Account{}, r.createSessionErr
	}
	r.refreshTestRepository.mu.Lock()
	r.refreshTestRepository.successors = append(r.refreshTestRepository.successors, session)
	r.refreshTestRepository.mu.Unlock()
	return r.phoneAccount, nil
}

func newRefreshTestRepository() *refreshTestRepository {
	return &refreshTestRepository{
		account:       Account{ID: "acct-current", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
		current:       Session{ID: "auths-current", AccountID: "acct-current", RefreshHash: "hash:current-token"},
		currentActive: true,
	}
}

func (r *refreshTestRepository) CreateAnonymous(context.Context) (Account, error) {
	return Account{}, domain.ErrNotImplemented
}

func (r *refreshTestRepository) GetAccount(_ context.Context, accountID string) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if accountID != r.account.ID {
		return Account{}, domain.ErrNotFound
	}
	return r.account, nil
}

func (r *refreshTestRepository) CreateChallenge(context.Context, PhoneChallenge) error {
	return domain.ErrNotImplemented
}

func (r *refreshTestRepository) ConsumeChallenge(context.Context, string, string) (PhoneChallenge, error) {
	return PhoneChallenge{}, domain.ErrNotImplemented
}

func (r *refreshTestRepository) RestoreChallenge(context.Context, string) error {
	return domain.ErrNotImplemented
}

func (r *refreshTestRepository) FindOrCreateByPhoneHashes(context.Context, string, string) (Account, error) {
	return Account{}, domain.ErrNotImplemented
}

func (r *refreshTestRepository) BindAnonymous(context.Context, string, string) (Account, error) {
	return Account{}, domain.ErrNotImplemented
}

func (r *refreshTestRepository) BindAnonymousAndCreateSession(_ context.Context, _, _ string, session Session) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	if r.rotateErr != nil {
		return Account{}, r.rotateErr
	}
	r.successors = append(r.successors, session)
	return r.account, nil
}

func (r *refreshTestRepository) CreateSession(_ context.Context, session Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	r.successors = append(r.successors, session)
	return nil
}

func (r *refreshTestRepository) GetSessionByRefreshHash(_ context.Context, hash string) (Session, error) {
	r.mu.Lock()
	if !r.currentActive || hash != r.current.RefreshHash {
		r.mu.Unlock()
		return Session{}, domain.ErrNotFound
	}
	current := r.current
	arrived := r.lookupArrived
	release := r.lookupRelease
	r.mu.Unlock()
	if arrived != nil {
		arrived <- struct{}{}
		<-release
	}
	return current, nil
}

func (r *refreshTestRepository) RotateSession(_ context.Context, currentSessionID string, successor Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rotateErr != nil {
		return r.rotateErr
	}
	if !r.currentActive || currentSessionID != r.current.ID || successor.AccountID != r.current.AccountID {
		return domain.ErrNotFound
	}
	r.currentActive = false
	r.successors = append(r.successors, successor)
	return nil
}

func (r *refreshTestRepository) RevokeSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.revokeCalls++
	if !r.currentActive || sessionID != r.current.ID {
		return domain.ErrNotFound
	}
	r.currentActive = false
	return nil
}

type refreshTestIssuer struct {
	issued   atomic.Int64
	issueErr error
}

type verificationSenderStub struct{}

func (verificationSenderStub) SendCode(context.Context, string, string) error { return nil }

type verificationSenderErrorStub struct{ err error }

func (s verificationSenderErrorStub) SendCode(context.Context, string, string) error { return s.err }

type logoutErrorRepository struct {
	refreshTestRepository
	revokeErr error
}

func (r *logoutErrorRepository) RevokeSession(context.Context, string) error {
	return r.revokeErr
}

type accountErrorRepository struct {
	refreshTestRepository
	accountErr error
}

func (r *accountErrorRepository) GetAccount(context.Context, string) (Account, error) {
	return Account{}, r.accountErr
}

type accessTokenVerifierStub struct {
	token  string
	claims AccessTokenClaims
	err    error
}

func (v *accessTokenVerifierStub) VerifyAccessToken(_ context.Context, token string) (AccessTokenClaims, error) {
	v.token = token
	if v.err != nil {
		return AccessTokenClaims{}, v.err
	}
	return v.claims, nil
}

func (i *refreshTestIssuer) Issue(context.Context, Account, Session) (Tokens, error) {
	if i.issueErr != nil {
		return Tokens{}, i.issueErr
	}
	number := i.issued.Add(1)
	return Tokens{
		AccessToken:  fmt.Sprintf("access-%d", number),
		RefreshToken: fmt.Sprintf("refresh-%d", number),
		ExpiresAt:    time.Unix(3600, 0).UTC(),
	}, nil
}

func (*refreshTestIssuer) HashRefreshToken(token string) string {
	return "hash:" + token
}

var _ Repository = (*refreshTestRepository)(nil)
var _ TokenIssuer = (*refreshTestIssuer)(nil)
var _ AccessTokenVerifier = (*accessTokenVerifierStub)(nil)
