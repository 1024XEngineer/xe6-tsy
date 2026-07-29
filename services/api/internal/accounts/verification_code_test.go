package accounts

import (
	"testing"
	"time"
)

func TestNormalizeVerificationCode(t *testing.T) {
	if got := NormalizeVerificationCode("8888"); got != "888888" {
		t.Fatalf("NormalizeVerificationCode(8888) = %q, want 888888", got)
	}
	if got := NormalizeVerificationCode("123456"); got != "123456" {
		t.Fatalf("NormalizeVerificationCode(123456) = %q, want 123456", got)
	}
}

func TestUniversalVerificationCodeCreateAndVerify(t *testing.T) {
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	repository := &challengeTestRepository{
		refreshTestRepository: *newRefreshTestRepository(),
		phoneAccount:          Account{ID: "acct-registered", Kind: AccountKindRegistered, CreatedAt: time.Unix(1, 0).UTC()},
	}
	service := NewPersistentUseCases(repository, &refreshTestIssuer{}, nil, verificationSenderStub{}, digester).
		WithVerificationPolicy(VerificationPolicy{UniversalCode: "8888"})

	challengeID, err := service.CreatePhoneChallenge(t.Context(), "+8613800000000")
	if err != nil {
		t.Fatalf("CreatePhoneChallenge() error = %v", err)
	}
	if repository.created.CodeHash != digester.CodeHash(challengeID, "888888") {
		t.Fatalf("stored code hash = %q, want hash for universal code", repository.created.CodeHash)
	}

	repository.consumed = repository.created
	result, err := service.VerifyPhone(t.Context(), challengeID, "8888", "")
	if err != nil {
		t.Fatalf("VerifyPhone(8888) error = %v", err)
	}
	if result.Account.Kind != AccountKindRegistered {
		t.Fatalf("account kind = %q, want registered", result.Account.Kind)
	}
}
