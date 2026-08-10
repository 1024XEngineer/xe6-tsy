package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type retryRepositoryStub struct {
	current             map[string]Message
	created             []CreateRetryRecord
	retryErr            error
	lookup              Message
	lookupErr           error
	lookupAccount       string
	lookupKey           string
	getMessageAccountID string
	getMessageID        string
	createLookup        Message
	createErr           error
	createCalls         int
	preference          Preference
	listAccountID       string
	putPreference       Preference
	putPreferenceCalls  int
	createdRecord       []CreateMessageRecord
}

func (r *retryRepositoryStub) CreateMessage(_ context.Context, record CreateMessageRecord) error {
	r.createCalls++
	r.createdRecord = append(r.createdRecord, record)
	r.createLookup = record.Message
	r.createErr = nil
	return nil
}

func (r *retryRepositoryStub) GetMessage(_ context.Context, accountID, messageID string) (Message, error) {
	r.getMessageAccountID = accountID
	r.getMessageID = messageID
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

func (r *retryRepositoryStub) ListPreferences(_ context.Context, accountID string) ([]Preference, error) {
	r.listAccountID = accountID
	if r.preference.AccountID == "" {
		return nil, nil
	}
	return []Preference{r.preference}, nil
}

func (r *retryRepositoryStub) PutPreference(_ context.Context, preference Preference) (Preference, error) {
	r.putPreferenceCalls++
	r.putPreference = preference
	r.preference = preference
	return preference, nil
}

type preferenceDestinationStub struct {
	calls     int
	accountID string
	channel   Channel
	reference string
	result    VerifiedDestination
	err       error
}

func (d *preferenceDestinationStub) ResolveVerifiedDestination(_ context.Context, accountID string, channel Channel, reference string) (VerifiedDestination, error) {
	d.calls++
	d.accountID = accountID
	d.channel = channel
	d.reference = reference
	if d.err != nil {
		return VerifiedDestination{}, d.err
	}
	if d.result.AccountID == "" {
		return VerifiedDestination{AccountID: accountID, Channel: channel, DestinationRef: reference, ProviderTarget: "opaque"}, nil
	}
	return d.result, nil
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

func TestGetReturnsAccountScopedMessage(t *testing.T) {
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusQueued},
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	got, err := service.Get(context.Background(), "account-1", "message-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != "message-1" || got.AccountID != "account-1" {
		t.Fatalf("Get() message = %#v", got)
	}
	if repository.getMessageAccountID != "account-1" || repository.getMessageID != "message-1" {
		t.Fatalf("GetMessage() args = (%q, %q)", repository.getMessageAccountID, repository.getMessageID)
	}
}

func TestGetAndPreferencesRejectMissingRepository(t *testing.T) {
	service := NewPersistentUseCases(nil, nil, nil, nil)

	if _, err := service.Get(context.Background(), "account-1", "message-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("Get() error = %v, want not implemented", err)
	}
	if _, err := service.Preferences(context.Background(), "account-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("Preferences() error = %v, want not implemented", err)
	}
}

func TestPreferencesReturnsCurrentAccountSettings(t *testing.T) {
	repository := &retryRepositoryStub{preference: Preference{
		AccountID: "account-1",
		Channel:   ChannelEmail,
		Enabled:   true,
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	got, err := service.Preferences(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("Preferences() error = %v", err)
	}
	if len(got) != 1 || got[0].AccountID != "account-1" || repository.listAccountID != "account-1" {
		t.Fatalf("Preferences() = %#v, listAccountID=%q", got, repository.listAccountID)
	}
}

func TestPutPreferenceForDestinationResolvesAndPersistsOpaqueReference(t *testing.T) {
	repository := &retryRepositoryStub{}
	destinations := &preferenceDestinationStub{result: VerifiedDestination{
		AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary", ProviderTarget: "opaque",
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.destinations = destinations

	preference, err := service.PutPreferenceForDestination(context.Background(), "account-1", ChannelEmail, true, "primary")
	if err != nil {
		t.Fatalf("PutPreferenceForDestination() error = %v", err)
	}
	if destinations.calls != 1 || destinations.accountID != "account-1" || destinations.channel != ChannelEmail || destinations.reference != "primary" {
		t.Fatalf("ResolveVerifiedDestination() = %#v", destinations)
	}
	if repository.putPreferenceCalls != 1 || repository.putPreference.DestinationRef != "primary" || !repository.putPreference.Enabled {
		t.Fatalf("PutPreference() record = %#v", repository.putPreference)
	}
	if preference.DestinationRef != "primary" || preference.Verified {
		t.Fatalf("PutPreferenceForDestination() = %#v", preference)
	}
}

func TestPutPreferenceForDestinationRejectsInvalidChannelAndLookupFailure(t *testing.T) {
	repository := &retryRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.destinations = &preferenceDestinationStub{err: domain.ErrNotFound}

	if _, err := service.PutPreferenceForDestination(context.Background(), "account-1", Channel("invalid"), true, "primary"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("PutPreferenceForDestination() invalid channel error = %v, want invalid argument", err)
	}
	if _, err := service.PutPreferenceForDestination(context.Background(), "account-1", ChannelEmail, true, "primary"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("PutPreferenceForDestination() lookup error = %v, want not found", err)
	}
	if repository.putPreferenceCalls != 0 {
		t.Fatalf("PutPreference() calls = %d, want 0 after lookup failure", repository.putPreferenceCalls)
	}
}

func TestScheduleFinalTurnCreatesOneIdempotentMessagePerPreference(t *testing.T) {
	repository := &retryRepositoryStub{preference: Preference{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}}
	service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
	event := recordsv1.FinalTurnEvent{TurnID: "turn-1", DeliveryEnabled: true}
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
		t.Fatalf("first ScheduleFinalTurn() error = %v", err)
	}
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
		t.Fatalf("replayed ScheduleFinalTurn() error = %v", err)
	}
	if len(repository.createdRecord) != 1 {
		t.Fatalf("created messages = %d, want 1", len(repository.createdRecord))
	}
	wantKey := "auto:final_turn:turn-1:wechat:primary-wechat"
	if len(repository.createdRecord[0].Message.Turns) != 1 {
		t.Fatalf("created record = %#v", repository.createdRecord[0])
	}
	turn := repository.createdRecord[0].Message.Turns[0]
	if repository.createdRecord[0].IdempotencyKey != wantKey || turn.SourceText != "原文" || turn.TranslatedText != "translation" {
		t.Fatalf("created record = %#v", repository.createdRecord[0])
	}
}

type automaticTurnReaderStub struct{}

func (automaticTurnReaderStub) ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error) {
	return []FinalTurnSnapshot{{TurnID: "turn-1", SourceText: "原文", TranslatedText: "translation"}}, nil
}

type automaticDestinationReaderStub struct{}

func (automaticDestinationReaderStub) ResolveVerifiedDestination(context.Context, string, Channel, string) (VerifiedDestination, error) {
	return VerifiedDestination{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", ProviderTarget: "opaque"}, nil
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
