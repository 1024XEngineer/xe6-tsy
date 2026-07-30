package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type emailBindChallengeStub struct {
	created        EmailBindChallenge
	consumed       EmailBindChallenge
	consumeCalls   int
	restoredID     string
	restoreCalls   int
	createErr      error
	restoreErr     error
	rateLimitAfter int
}

func (s *emailBindChallengeStub) CreateEmailBindChallenge(_ context.Context, challenge EmailBindChallenge) error {
	if s.createErr != nil {
		return s.createErr
	}
	if s.rateLimitAfter > 0 && s.consumeCalls >= s.rateLimitAfter {
		return domain.ErrRateLimited
	}
	s.created = challenge
	return nil
}

func (s *emailBindChallengeStub) ConsumeEmailBindChallenge(_ context.Context, accountID, tokenHash string) (EmailBindChallenge, error) {
	s.consumeCalls++
	if s.consumed.TokenHash != tokenHash || s.consumed.AccountID != accountID {
		return EmailBindChallenge{}, domain.ErrNotFound
	}
	return s.consumed, nil
}

func (s *emailBindChallengeStub) RestoreEmailBindChallenge(_ context.Context, id string) error {
	s.restoreCalls++
	s.restoredID = id
	return s.restoreErr
}

type emailBindSenderStub struct {
	email          string
	destinationRef string
	token          string
	err            error
}

func (s *emailBindSenderStub) SendBindToken(_ context.Context, email, destinationRef, token string) error {
	if s.err != nil {
		return s.err
	}
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

func TestRequestEmailBindVerificationSurfacesRateLimit(t *testing.T) {
	challenges := &emailBindChallengeStub{createErr: domain.ErrRateLimited}
	service := NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureEmailVerification(challenges, &emailBindSenderStub{})

	err := service.RequestEmailBindVerification(t.Context(), "account-1", "user@example.test", "")
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("RequestEmailBindVerification() error = %v, want rate limited", err)
	}
}

func TestBindEmailTargetConsumesVerificationToken(t *testing.T) {
	token := "verification-token"
	challenges := &emailBindChallengeStub{
		consumed: EmailBindChallenge{
			ID:             "email_bind_test",
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
	if challenges.restoreCalls != 0 {
		t.Fatalf("RestoreEmailBindChallenge() calls = %d, want 0", challenges.restoreCalls)
	}
}

func TestBindEmailTargetRestoresChallengeWhenPersistenceFails(t *testing.T) {
	token := "verification-token"
	challenges := &emailBindChallengeStub{
		consumed: EmailBindChallenge{
			ID:             "email_bind_restore",
			AccountID:      "account-1",
			DestinationRef: "work-email",
			Email:          "user@example.test",
			TokenHash:      hashEmailBindToken(token),
		},
	}
	repository := &targetRepositoryStub{bindErr: domain.ErrConflict}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	service.ConfigureEmailVerification(challenges, &emailBindSenderStub{})

	_, err := service.BindEmailTarget(t.Context(), "account-1", token)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("BindEmailTarget() error = %v, want conflict", err)
	}
	if challenges.restoreCalls != 1 || challenges.restoredID != "email_bind_restore" {
		t.Fatalf("restore = (%d, %q), want (1, email_bind_restore)", challenges.restoreCalls, challenges.restoredID)
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
	resolved, err := resolveEmailBindToken(t.Context(), "local", "dev:work-email:user@example.test", "account-1", nil)
	if err != nil {
		t.Fatalf("resolveEmailBindToken() error = %v", err)
	}
	if resolved.DestinationRef != "work-email" || resolved.Email != "user@example.test" || resolved.ChallengeID != "" {
		t.Fatalf("resolveEmailBindToken() = %#v", resolved)
	}
}

func TestNewEmailBindChallengeUsesConfiguredTTL(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	challenge := newEmailBindChallenge("account-1", "primary-email", "user@example.test", "hash", now)
	if challenge.ExpiresAt.Sub(now) != emailBindChallengeTTL {
		t.Fatalf("ExpiresAt = %s, want +%s", challenge.ExpiresAt, emailBindChallengeTTL)
	}
}

func TestEnforceEmailBindRateLimitRejectsCooldown(t *testing.T) {
	latest := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	now := latest.Add(30 * time.Second)
	err := enforceEmailBindRateLimit(&latest, 1, now)
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("enforceEmailBindRateLimit() error = %v, want rate limited", err)
	}
}

func TestEnforceEmailBindRateLimitRejectsWindowQuota(t *testing.T) {
	latest := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	now := latest.Add(2 * time.Minute)
	err := enforceEmailBindRateLimit(&latest, emailBindChallengeWindowMaxSends, now)
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("enforceEmailBindRateLimit() error = %v, want rate limited", err)
	}
}

func TestEnforceEmailBindRateLimitAllowsFreshRequest(t *testing.T) {
	latest := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	now := latest.Add(2 * time.Minute)
	if err := enforceEmailBindRateLimit(&latest, emailBindChallengeWindowMaxSends-1, now); err != nil {
		t.Fatalf("enforceEmailBindRateLimit() error = %v, want nil", err)
	}
}
