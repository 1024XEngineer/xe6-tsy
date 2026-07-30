package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type emailBindChallengeStub struct {
	created  EmailBindChallenge
	consumed EmailBindChallenge
}

func (s *emailBindChallengeStub) CreateEmailBindChallenge(_ context.Context, challenge EmailBindChallenge) error {
	s.created = challenge
	return nil
}

func (s *emailBindChallengeStub) ConsumeEmailBindChallenge(_ context.Context, accountID, tokenHash string) (EmailBindChallenge, error) {
	if s.consumed.TokenHash != tokenHash || s.consumed.AccountID != accountID {
		return EmailBindChallenge{}, domain.ErrNotFound
	}
	return s.consumed, nil
}

type emailBindSenderStub struct {
	email          string
	destinationRef string
	token          string
}

func (s *emailBindSenderStub) SendBindToken(_ context.Context, email, destinationRef, token string) error {
	s.email = email
	s.destinationRef = destinationRef
	s.token = token
	return nil
}

func TestRequestEmailBindVerificationCreatesChallengeAndSendsToken(t *testing.T) {
	challenges := &emailBindChallengeStub{}
	sender := &emailBindSenderStub{}
	service := NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureEmailVerification(challenges, sender)

	if err := service.RequestEmailBindVerification(t.Context(), "account-1", "User@Example.test", ""); err != nil {
		t.Fatalf("RequestEmailBindVerification() error = %v", err)
	}
	if challenges.created.AccountID != "account-1" || challenges.created.Email != "user@example.test" {
		t.Fatalf("created challenge = %#v", challenges.created)
	}
	if sender.email != "user@example.test" || sender.destinationRef != "primary-email" || sender.token == "" {
		t.Fatalf("sender = (%q, %q, %q)", sender.email, sender.destinationRef, sender.token)
	}
}

func TestBindEmailTargetConsumesVerificationToken(t *testing.T) {
	token := "verification-token"
	challenges := &emailBindChallengeStub{
		consumed: EmailBindChallenge{
			AccountID:      "account-1",
			DestinationRef: "work-email",
			Email:          "user@example.test",
			TokenHash:      hashEmailBindToken(token),
		},
	}
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureEmailVerification(challenges, &emailBindSenderStub{})

	target, err := service.BindEmailTarget(t.Context(), "account-1", token)
	if err != nil {
		t.Fatalf("BindEmailTarget() error = %v", err)
	}
	if target.DestinationRef != "work-email" || !target.Verified {
		t.Fatalf("BindEmailTarget() = %#v", target)
	}
	if repository.bindRecord.DestinationRef != "work-email" || repository.bindRecord.AccountID != "account-1" {
		t.Fatalf("bind record = %#v", repository.bindRecord)
	}
}

func TestBindEmailTargetRejectsUnknownVerificationToken(t *testing.T) {
	service := NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureEmailVerification(&emailBindChallengeStub{}, &emailBindSenderStub{})

	_, err := service.BindEmailTarget(t.Context(), "account-1", "missing-token")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("BindEmailTarget() error = %v, want not found", err)
	}
}

func TestResolveEmailBindTokenStillAcceptsDevShortcutInLocal(t *testing.T) {
	ref, email, err := resolveEmailBindToken(t.Context(), "local", "dev:work-email:user@example.test", "account-1", nil)
	if err != nil {
		t.Fatalf("resolveEmailBindToken() error = %v", err)
	}
	if ref != "work-email" || email != "user@example.test" {
		t.Fatalf("resolveEmailBindToken() = (%q, %q)", ref, email)
	}
}

func TestNewEmailBindChallengeUsesConfiguredTTL(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	challenge := newEmailBindChallenge("account-1", "primary-email", "user@example.test", "hash", now)
	if challenge.ExpiresAt.Sub(now) != emailBindChallengeTTL {
		t.Fatalf("ExpiresAt = %s, want +%s", challenge.ExpiresAt, emailBindChallengeTTL)
	}
}
