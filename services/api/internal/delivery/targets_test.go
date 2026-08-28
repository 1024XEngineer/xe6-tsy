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
	targets           []MessageTarget
	listAccountID     string
	listChannel       *Channel
	bindRecord        BindEmailTargetRecord
	bindWeChatRecord  BindWeChatTargetRecord
	bindWebhookRecord BindWebhookTargetRecord
	bindErr           error
	putPreference     Preference
	putPreferenceErr  error
	revokeAccount     string
	revokeChannel     Channel
	revokeRef         string
	revokeErr         error
}

type targetDestinationStub struct{}

func (targetDestinationStub) ResolveVerifiedDestination(context.Context, string, Channel, string) (VerifiedDestination, error) {
	return VerifiedDestination{}, nil
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
func (s *targetRepositoryStub) PutPreference(_ context.Context, preference Preference) (Preference, error) {
	if s.putPreferenceErr != nil {
		return Preference{}, s.putPreferenceErr
	}
	s.putPreference = preference
	preference.Verified = true
	return preference, nil
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

func (s *targetRepositoryStub) BindWeChatTarget(_ context.Context, record BindWeChatTargetRecord) (MessageTarget, error) {
	if s.bindErr != nil {
		return MessageTarget{}, s.bindErr
	}
	s.bindWeChatRecord = record
	return MessageTarget{
		DestinationRef: record.DestinationRef,
		Channel:        ChannelWeChat,
		Verified:       true,
		UpdatedAt:      record.VerifiedAt,
	}, nil
}

func (s *targetRepositoryStub) BindWebhookTarget(_ context.Context, record BindWebhookTargetRecord) (MessageTarget, error) {
	if s.bindErr != nil {
		return MessageTarget{}, s.bindErr
	}
	s.bindWebhookRecord = record
	target := MessageTarget{
		DestinationRef: record.DestinationRef,
		Channel:        ChannelWebhook,
		Verified:       true,
		UpdatedAt:      record.VerifiedAt,
	}
	s.targets = []MessageTarget{target}
	return target, nil
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

func TestBindWeChatTargetUsesDevShortcutInLocal(t *testing.T) {
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "local")

	target, err := service.BindWeChatTarget(t.Context(), "account-1", "dev:work-wechat:userid-1")
	if err != nil {
		t.Fatalf("BindWeChatTarget() error = %v", err)
	}
	if target.Channel != ChannelWeChat || target.DestinationRef != "work-wechat" {
		t.Fatalf("BindWeChatTarget() = %#v", target)
	}
	if repository.bindWeChatRecord.DestinationRef != "work-wechat" || repository.bindWeChatRecord.AccountID != "account-1" {
		t.Fatalf("bind record = %#v", repository.bindWeChatRecord)
	}
}

func TestBindWeChatTargetRequiresWeComClientOutsideLocal(t *testing.T) {
	service := NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil)
	service.ConfigureTargetBinding(testDestinationKey(t), "production")
	_, err := service.BindWeChatTarget(t.Context(), "account-1", "oauth-code")
	if !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("BindWeChatTarget() error = %v, want not implemented", err)
	}
}

func TestBindWebhookTargetEncryptsURLAndEnablesPreference(t *testing.T) {
	repository := &targetRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, targetDestinationStub{}, nil)
	key := testDestinationKey(t)
	service.ConfigureTargetBinding(key, "production")

	target, err := service.BindWebhookTarget(t.Context(), "account-1", " https://example.com/events ")
	if err != nil {
		t.Fatalf("BindWebhookTarget() error = %v", err)
	}
	if target.Channel != ChannelWebhook || target.DestinationRef != defaultWebhookDestinationRef || !target.Verified {
		t.Fatalf("BindWebhookTarget() = %#v", target)
	}
	record := repository.bindWebhookRecord
	plaintext, err := decryptTarget(key, record.Ciphertext)
	if err != nil {
		t.Fatalf("decrypt webhook target: %v", err)
	}
	if record.AccountID != "account-1" || record.DestinationRef != defaultWebhookDestinationRef || record.KeyVersion != destinationKeyVersion || plaintext != "https://example.com/events" {
		t.Fatalf("BindWebhookTarget() record = %#v plaintext = %q", record, plaintext)
	}
	preference := repository.putPreference
	if preference.AccountID != "account-1" || preference.Channel != ChannelWebhook || preference.DestinationRef != defaultWebhookDestinationRef || !preference.Enabled {
		t.Fatalf("BindWebhookTarget() preference = %#v", preference)
	}
}

func TestBindWebhookTargetRejectsInvalidConfigurationAndURL(t *testing.T) {
	tests := []struct {
		name      string
		service   *UseCases
		accountID string
		url       string
		want      error
	}{
		{name: "missing repository capability", service: NewPersistentUseCases(&retryRepositoryStub{}, nil, nil, nil), accountID: "account-1", url: "https://example.com/events", want: domain.ErrNotImplemented},
		{name: "missing account", service: NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil), url: "https://example.com/events", want: domain.ErrNotImplemented},
		{name: "insecure URL", service: NewPersistentUseCases(&targetRepositoryStub{}, nil, nil, nil), accountID: "account-1", url: "http://example.com/events", want: domain.ErrInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.service.ConfigureTargetBinding(testDestinationKey(t), "production")
			if _, err := test.service.BindWebhookTarget(t.Context(), test.accountID, test.url); !errors.Is(err, test.want) {
				t.Fatalf("BindWebhookTarget() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBindWebhookTargetReturnsPersistenceErrors(t *testing.T) {
	tests := []struct {
		name       string
		repository *targetRepositoryStub
		want       error
	}{
		{name: "target persistence", repository: &targetRepositoryStub{bindErr: domain.ErrConflict}, want: domain.ErrConflict},
		{name: "preference persistence", repository: &targetRepositoryStub{putPreferenceErr: domain.ErrConflict}, want: domain.ErrConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewPersistentUseCases(test.repository, nil, targetDestinationStub{}, nil)
			service.ConfigureTargetBinding(testDestinationKey(t), "production")
			if _, err := service.BindWebhookTarget(t.Context(), "account-1", "https://example.com/events"); !errors.Is(err, test.want) {
				t.Fatalf("BindWebhookTarget() error = %v, want %v", err, test.want)
			}
		})
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
		{name: "header injection", token: "dev:user@example.test\r\nBcc attacker@evil.test", want: domain.ErrInvalidArgument},
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
