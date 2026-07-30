package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestParseDevEmailBindTokenAcceptsLocalFormats(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantRef   string
		wantEmail string
	}{
		{name: "email only", token: "dev:user@example.test", wantRef: "primary-email", wantEmail: "user@example.test"},
		{name: "ref and email", token: "dev:work-email:user@example.test", wantRef: "work-email", wantEmail: "user@example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref, email, err := parseDevEmailBindToken("local", test.token)
			if err != nil {
				t.Fatalf("parseDevEmailBindToken() error = %v", err)
			}
			if ref != test.wantRef || email != test.wantEmail {
				t.Fatalf("parseDevEmailBindToken() = (%q, %q)", ref, email)
			}
		})
	}
}

func TestParseDevEmailBindTokenFailsClosedOutsideLocal(t *testing.T) {
	tests := []struct {
		name   string
		appEnv string
	}{
		{name: "production", appEnv: "production"},
		{name: "staging", appEnv: "staging"},
		{name: "preview", appEnv: "preview"},
		{name: "empty", appEnv: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseDevEmailBindToken(test.appEnv, "dev:user@example.test")
			if !errors.Is(err, domain.ErrNotImplemented) {
				t.Fatalf("parseDevEmailBindToken() error = %v, want not implemented", err)
			}
		})
	}
}

func TestParseDevEmailBindTokenAllowsExplicitTestEnvironment(t *testing.T) {
	ref, email, err := parseDevEmailBindToken("test", "dev:user@example.test")
	if err != nil {
		t.Fatalf("parseDevEmailBindToken() error = %v", err)
	}
	if ref != "primary-email" || email != "user@example.test" {
		t.Fatalf("parseDevEmailBindToken() = (%q, %q)", ref, email)
	}
}

func TestParseDevEmailBindTokenRejectsNonDevTokens(t *testing.T) {
	_, _, err := parseDevEmailBindToken("local", "verify-token")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("parseDevEmailBindToken() error = %v, want not implemented", err)
	}
}

func TestBindEmailTargetRequiresConfiguredKey(t *testing.T) {
	repository := &PostgresRepository{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	_, err := service.BindEmailTarget(t.Context(), "account-1", "dev:user@example.test")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("BindEmailTarget() error = %v, want not implemented", err)
	}
}

type targetRepositoryStub struct {
	targets       []MessageTarget
	listAccountID string
	listChannel   *Channel
	bindRecord    BindEmailTargetRecord
	bindErr       error
	revokeAccount string
	revokeChannel Channel
	revokeRef     string
	revokeErr     error
}

func (targetRepositoryStub) CreateMessage(context.Context, CreateMessageRecord) error {
	return domain.ErrNotImplemented
}
func (targetRepositoryStub) GetMessage(context.Context, string, string) (Message, error) {
	return Message{}, domain.ErrNotFound
}
func (targetRepositoryStub) CreateRetry(context.Context, CreateRetryRecord) (Message, error) {
	return Message{}, domain.ErrNotImplemented
}
func (targetRepositoryStub) GetAttempt(context.Context, string) (DeliveryAttempt, error) {
	return DeliveryAttempt{}, domain.ErrNotFound
}
func (targetRepositoryStub) ClaimAttempt(context.Context, string) (DeliveryAttempt, error) {
	return DeliveryAttempt{}, domain.ErrNotFound
}
func (targetRepositoryStub) RequeueAttempt(context.Context, string, time.Time) error { return nil }
func (targetRepositoryStub) CompleteAttempt(context.Context, string, string, DeliveryAttemptStatus, MessageStatus, *string) error {
	return nil
}
func (targetRepositoryStub) SetMessageStatus(context.Context, string, MessageStatus, *string) error {
	return nil
}
func (targetRepositoryStub) SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error {
	return nil
}
func (targetRepositoryStub) ListPreferences(context.Context, string) ([]Preference, error) {
	return nil, nil
}
func (targetRepositoryStub) PutPreference(context.Context, Preference) (Preference, error) {
	return Preference{}, domain.ErrNotImplemented
}

func (s *targetRepositoryStub) ListMessageTargets(_ context.Context, accountID string, channel *Channel) ([]MessageTarget, error) {
	s.listAccountID = accountID
	s.listChannel = channel
	return s.targets, nil
}

func (s *targetRepositoryStub) BindEmailTarget(_ context.Context, record BindEmailTargetRecord) (MessageTarget, error) {
	if s.bindErr != nil {
		return MessageTarget{}, s.bindErr
	}
	s.bindRecord = record
	return MessageTarget{
		DestinationRef: record.DestinationRef,
		Channel:        ChannelEmail,
		Verified:       true,
		UpdatedAt:      record.VerifiedAt,
	}, nil
}

func (s *targetRepositoryStub) RevokeMessageTarget(_ context.Context, accountID string, channel Channel, destinationRef string, _ time.Time) error {
	s.revokeAccount = accountID
	s.revokeChannel = channel
	s.revokeRef = destinationRef
	return s.revokeErr
}

var _ Repository = (*targetRepositoryStub)(nil)
var _ TargetRepository = (*targetRepositoryStub)(nil)

func testDestinationKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	return key
}

func TestConfigureTargetBindingStoresKeyAndEnvironment(t *testing.T) {
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	key := testDestinationKey(t)
	service.ConfigureTargetBinding(key, "local")

	target, err := service.BindEmailTarget(t.Context(), "account-1", "dev:user@example.test")
	if err != nil {
		t.Fatalf("BindEmailTarget() error = %v", err)
	}
	if target.DestinationRef != "primary-email" || !target.Verified {
		t.Fatalf("BindEmailTarget() = %#v", target)
	}
	if repository.bindRecord.AccountID != "account-1" || len(repository.bindRecord.Ciphertext) == 0 {
		t.Fatalf("BindEmailTarget() record = %#v", repository.bindRecord)
	}
}

func TestListMessageTargetsDelegatesToRepository(t *testing.T) {
	repository := &targetRepositoryStub{targets: []MessageTarget{{DestinationRef: "primary-email", Channel: ChannelEmail, Verified: true}}}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	channel := ChannelEmail

	targets, err := service.ListMessageTargets(t.Context(), "account-1", &channel)
	if err != nil {
		t.Fatalf("ListMessageTargets() error = %v", err)
	}
	if len(targets) != 1 || repository.listAccountID != "account-1" || repository.listChannel == nil || *repository.listChannel != ChannelEmail {
		t.Fatalf("ListMessageTargets() = %#v, repository=(%q, %v)", targets, repository.listAccountID, repository.listChannel)
	}
}

func TestListMessageTargetsFailsClosedWithoutTargetRepository(t *testing.T) {
	service := NewPersistentUseCases(&retryRepositoryStub{}, nil, nil, nil)
	if _, err := service.ListMessageTargets(t.Context(), "account-1", nil); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("ListMessageTargets() error = %v, want not implemented", err)
	}
}

func TestBindWeChatTargetReturnsNotImplemented(t *testing.T) {
	service := NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil)
	if _, err := service.BindWeChatTarget(t.Context(), "account-1", "oauth-code"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("BindWeChatTarget() error = %v, want not implemented", err)
	}
}

func TestRevokeMessageTargetDelegatesToRepository(t *testing.T) {
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	if err := service.RevokeMessageTarget(t.Context(), "account-1", ChannelEmail, "primary-email"); err != nil {
		t.Fatalf("RevokeMessageTarget() error = %v", err)
	}
	if repository.revokeAccount != "account-1" || repository.revokeChannel != ChannelEmail || repository.revokeRef != "primary-email" {
		t.Fatalf("RevokeMessageTarget() repository = (%q, %q, %q)", repository.revokeAccount, repository.revokeChannel, repository.revokeRef)
	}
}

func TestParseDevEmailBindTokenRejectsInvalidPayload(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  error
	}{
		{name: "empty token", token: "", want: domain.ErrInvalidArgument},
		{name: "missing email", token: "dev:", want: domain.ErrInvalidArgument},
		{name: "invalid email", token: "dev:not-an-email", want: domain.ErrInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseDevEmailBindToken("local", test.token)
			if !errors.Is(err, test.want) {
				t.Fatalf("parseDevEmailBindToken() error = %v, want %v", err, test.want)
			}
		})
	}
}
