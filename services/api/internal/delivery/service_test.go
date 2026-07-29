package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type retryRepositoryStub struct {
	current       map[string]Message
	created       []CreateRetryRecord
	retryErr      error
	lookup        Message
	lookupErr     error
	lookupAccount string
	lookupKey     string
	createLookup  Message
	createErr     error
	createCalls   int
	preference    Preference
}

func (r *retryRepositoryStub) CreateMessage(context.Context, CreateMessageRecord) error {
	r.createCalls++
	return nil
}

func (r *retryRepositoryStub) GetMessage(_ context.Context, accountID, _ string) (Message, error) {
	message, ok := r.current[accountID]
	if !ok {
		return Message{}, domain.ErrNotFound
	}
	return message, nil
}

func (r *retryRepositoryStub) CreateRetry(_ context.Context, record CreateRetryRecord) (Message, error) {
	r.created = append(r.created, record)
	if r.retryErr != nil {
		return Message{}, r.retryErr
	}
	message := r.current[record.AccountID]
	message.Status = MessageStatusRetrying
	message.Attempts = record.Attempt.AttemptNumber
	return message, nil
}

func (r *retryRepositoryStub) GetAttempt(context.Context, string) (DeliveryAttempt, error) {
	return DeliveryAttempt{}, domain.ErrNotFound
}

func (r *retryRepositoryStub) ClaimAttempt(context.Context, string) (DeliveryAttempt, error) {
	return DeliveryAttempt{}, domain.ErrNotFound
}

func (r *retryRepositoryStub) RequeueAttempt(context.Context, string, time.Time) error {
	return nil
}

func (r *retryRepositoryStub) CompleteAttempt(context.Context, string, string, DeliveryAttemptStatus, MessageStatus, *string) error {
	return nil
}

func (r *retryRepositoryStub) SetMessageStatus(context.Context, string, MessageStatus, *string) error {
	return nil
}

func (r *retryRepositoryStub) SetAttemptStatus(context.Context, string, DeliveryAttemptStatus, *string) error {
	return nil
}

func (r *retryRepositoryStub) ListPreferences(context.Context, string) ([]Preference, error) {
	return nil, nil
}

func (r *retryRepositoryStub) PutPreference(_ context.Context, preference Preference) (Preference, error) {
	r.preference = preference
	return preference, nil
}

func TestPutPreferenceDoesNotClaimVerification(t *testing.T) {
	repository := &retryRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	preference, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, true)
	if err != nil {
		t.Fatalf("PutPreference() error = %v", err)
	}
	if preference.Verified || repository.preference.Verified {
		t.Fatal("PutPreference() must leave destination verification to the repository")
	}
}

func (r *retryRepositoryStub) GetMessageByIdempotency(context.Context, string, string) (Message, error) {
	if r.createLookup.ID == "" && r.createErr == nil {
		return Message{}, domain.ErrNotFound
	}
	return r.createLookup, r.createErr
}

func (r *retryRepositoryStub) GetMessageByDeliveryIdempotency(_ context.Context, accountID, key string) (Message, error) {
	r.lookupAccount = accountID
	r.lookupKey = key
	if r.lookup.ID == "" && r.lookupErr == nil {
		return Message{}, domain.ErrNotFound
	}
	return r.lookup, r.lookupErr
}

func TestCreateReplayRequiresSameRequest(t *testing.T) {
	repository := &retryRepositoryStub{createLookup: Message{
		ID: "message-1", AccountID: "account-1", Channel: ChannelEmail,
		DestinationRef: "primary", Turns: []FinalTurnSnapshot{{TurnID: "turn-1"}, {TurnID: "turn-2"}},
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	input := CreateInput{
		AccountID: "account-1", IdempotencyKey: "create-key", Channel: ChannelEmail,
		DestinationRef: "primary", TurnIDs: []string{"turn-2", "turn-1"},
	}

	message, err := service.Create(t.Context(), input)
	if err != nil || message.ID != "message-1" {
		t.Fatalf("Create() = (%#v, %v), want existing message", message, err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("CreateMessage calls = %d, want 0", repository.createCalls)
	}

	input.DestinationRef = "other"
	if _, err := service.Create(t.Context(), input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create() mismatch error = %v, want conflict", err)
	}
	input.DestinationRef = "primary"
	input.TurnIDs = []string{"turn-1", "turn-3"}
	if _, err := service.Create(t.Context(), input); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Create() turn mismatch error = %v, want conflict", err)
	}
}

func TestRetryConflictResolvesThroughDeliveryOutboxKey(t *testing.T) {
	repository := &retryRepositoryStub{
		current:  map[string]Message{"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1}},
		retryErr: domain.ErrConflict,
		lookup:   Message{ID: "message-1", AccountID: "account-1", Status: MessageStatusRetrying, Attempts: 2},
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	message, err := service.Retry(context.Background(), "account-1", "message-1", "retry-key")
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if message.ID != "message-1" || repository.lookupAccount != "account-1" || repository.lookupKey != "retry-key" {
		t.Fatalf("resolved message = %#v, lookup=(%q,%q)", message, repository.lookupAccount, repository.lookupKey)
	}
}

func TestRetryConflictRejectsKeyBoundToAnotherMessage(t *testing.T) {
	repository := &retryRepositoryStub{
		current:  map[string]Message{"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1}},
		retryErr: domain.ErrConflict,
		lookup:   Message{ID: "message-2", AccountID: "account-1", Status: MessageStatusRetrying, Attempts: 2},
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	if _, err := service.Retry(context.Background(), "account-1", "message-1", "retry-key"); err != domain.ErrConflict {
		t.Fatalf("Retry() error = %v, want conflict", err)
	}
}

func TestRetryRejectsUnknownNonIdempotentDelivery(t *testing.T) {
	unknown := deliveryUnknownErrorCode
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {
			ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed,
			Attempts: 1, LastErrorCode: &unknown,
		},
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	if _, err := service.Retry(context.Background(), "account-1", "message-1", "retry-key"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Retry() error = %v, want conflict", err)
	}
	if len(repository.created) != 0 {
		t.Fatalf("CreateRetry calls = %d, want 0", len(repository.created))
	}
}

func TestRetryKeysAreScopedByAccount(t *testing.T) {
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1},
		"account-2": {ID: "message-2", AccountID: "account-2", Status: MessageStatusFailed, Attempts: 1},
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	ctx := context.Background()
	for accountID, messageID := range map[string]string{"account-1": "message-1", "account-2": "message-2"} {
		if _, err := service.Retry(ctx, accountID, messageID, "same-key"); err != nil {
			t.Fatalf("Retry(%q) error = %v", accountID, err)
		}
	}
	if len(repository.created) != 2 {
		t.Fatalf("CreateRetry calls = %d, want 2", len(repository.created))
	}
}

func TestRetryRejectsOversizedIdempotencyKey(t *testing.T) {
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1},
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	if _, err := service.Retry(t.Context(), "account-1", "message-1", string(make([]byte, MaxIdempotencyKeyLength+1))); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Retry() error = %v, want invalid argument", err)
	}
	if len(repository.created) != 0 {
		t.Fatalf("CreateRetry calls = %d, want 0", len(repository.created))
	}
}

func TestRetryUsesDurableLookupAfterProcessStateIsLost(t *testing.T) {
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1},
	}}
	ctx := context.Background()
	first := NewPersistentUseCases(repository, nil, nil, nil)
	if _, err := first.Retry(ctx, "account-1", "message-1", "retry-key"); err != nil {
		t.Fatalf("first Retry() error = %v", err)
	}
	repository.lookup = Message{ID: "message-1", AccountID: "account-1", Status: MessageStatusRetrying, Attempts: 2}
	second := NewPersistentUseCases(repository, nil, nil, nil)
	message, err := second.Retry(ctx, "account-1", "message-1", "retry-key")
	if err != nil {
		t.Fatalf("replayed Retry() error = %v", err)
	}
	if message.Status != MessageStatusRetrying || len(repository.created) != 1 {
		t.Fatalf("replayed message = %#v, CreateRetry calls = %d", message, len(repository.created))
	}
}

var _ Repository = (*retryRepositoryStub)(nil)
var _ IdempotencyReader = (*retryRepositoryStub)(nil)
var _ RetryIdempotencyReader = (*retryRepositoryStub)(nil)
