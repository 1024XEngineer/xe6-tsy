package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
	preferences         []Preference
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
	if len(r.preferences) > 0 {
		return append([]Preference(nil), r.preferences...), nil
	}
	if r.preference.AccountID == "" {
		return nil, nil
	}
	return []Preference{r.preference}, nil
}

func (r *retryRepositoryStub) PutPreference(_ context.Context, preference Preference) (Preference, error) {
	r.putPreferenceCalls++
	r.putPreference = preference
	r.preference = preference
	for index, existing := range r.preferences {
		if existing.AccountID == preference.AccountID && existing.Channel == preference.Channel && existing.DestinationRef == preference.DestinationRef {
			r.preferences[index] = preference
			return preference, nil
		}
	}
	r.preferences = append(r.preferences, preference)
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

func TestPutPreferenceDisablesOneTargetWithoutClaimingVerification(t *testing.T) {
	repository := &retryRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)

	preference, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, " primary ", false)
	if err != nil {
		t.Fatalf("PutPreference() error = %v", err)
	}
	if preference.DestinationRef != "primary" || preference.Enabled {
		t.Fatalf("PutPreference() = %#v", preference)
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

func TestPutPreferenceResolvesAndPersistsOpaqueReference(t *testing.T) {
	repository := &retryRepositoryStub{}
	destinations := &preferenceDestinationStub{result: VerifiedDestination{
		AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary", ProviderTarget: "opaque",
	}}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.destinations = destinations

	preference, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, "primary", true)
	if err != nil {
		t.Fatalf("PutPreference() error = %v", err)
	}
	if destinations.calls != 1 || destinations.accountID != "account-1" || destinations.channel != ChannelEmail || destinations.reference != "primary" {
		t.Fatalf("ResolveVerifiedDestination() = %#v", destinations)
	}
	if repository.putPreferenceCalls != 1 || repository.putPreference.DestinationRef != "primary" || !repository.putPreference.Enabled {
		t.Fatalf("PutPreference() record = %#v", repository.putPreference)
	}
	if preference.DestinationRef != "primary" || preference.Verified {
		t.Fatalf("PutPreference() = %#v", preference)
	}
}

func TestPutPreferenceRejectsInvalidTargetAndLookupFailure(t *testing.T) {
	repository := &retryRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.destinations = &preferenceDestinationStub{err: domain.ErrNotFound}

	if _, err := service.PutPreference(context.Background(), "account-1", Channel("invalid"), "primary", true); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("PutPreference() invalid channel error = %v, want invalid argument", err)
	}
	if _, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, " ", true); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("PutPreference() blank target error = %v, want invalid argument", err)
	}
	if _, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, "primary", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("PutPreference() lookup error = %v, want not found", err)
	}
	if repository.putPreferenceCalls != 0 {
		t.Fatalf("PutPreference() calls = %d, want 0 after lookup failure", repository.putPreferenceCalls)
	}
}

func TestPutPreferenceKeepsTargetsIndependent(t *testing.T) {
	repository := &retryRepositoryStub{}
	service := NewPersistentUseCases(repository, nil, &preferenceDestinationStub{}, nil)

	for _, reference := range []string{"primary", "backup"} {
		if _, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, reference, true); err != nil {
			t.Fatalf("PutPreference(%q) error = %v", reference, err)
		}
	}
	if _, err := service.PutPreference(context.Background(), "account-1", ChannelEmail, "primary", false); err != nil {
		t.Fatalf("PutPreference(disable primary) error = %v", err)
	}

	preferences, err := service.Preferences(context.Background(), "account-1")
	if err != nil {
		t.Fatalf("Preferences() error = %v", err)
	}
	if len(preferences) != 2 {
		t.Fatalf("Preferences() = %#v, want two targets", preferences)
	}
	for _, preference := range preferences {
		if preference.DestinationRef == "primary" && preference.Enabled {
			t.Fatalf("primary preference = %#v, want disabled", preference)
		}
		if preference.DestinationRef == "backup" && !preference.Enabled {
			t.Fatalf("backup preference = %#v, want enabled", preference)
		}
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

func TestScheduleFinalTurnUsesAtomicRepositoryForEveryEnabledTarget(t *testing.T) {
	repository := &atomicScheduleRepository{retryRepositoryStub: retryRepositoryStub{preferences: []Preference{
		{AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary-email", Enabled: true, Verified: true},
		{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true},
		{AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "disabled-email", Enabled: false, Verified: true},
	}}}
	service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
	event := recordsv1.FinalTurnEvent{
		TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1", TargetLanguage: "en-US",
		TranslatedText: "translation", LanguageConfigVersion: 3, DeliveryEnabled: true,
	}
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
		t.Fatalf("ScheduleFinalTurn() error = %v", err)
	}
	if repository.record.Run.TargetCount != 2 || len(repository.record.Targets) != 2 {
		t.Fatalf("atomic schedule = %#v, want two enabled targets", repository.record)
	}
	if repository.record.Targets[0].Message.DestinationRef != "primary-email" || repository.record.Targets[1].Message.DestinationRef != "primary-wechat" {
		t.Fatalf("scheduled targets = %#v", repository.record.Targets)
	}
	if repository.record.Run.FallbackOperationID != "fallback_turn-1" {
		t.Fatalf("fallback operation = %q", repository.record.Run.FallbackOperationID)
	}
	if repository.record.Run.Trigger != AutomaticTurnTriggerConfiguredRoute {
		t.Fatalf("delivery trigger = %q, want configured route", repository.record.Run.Trigger)
	}
}

func TestScheduleFinalTurnRoutesLongSourceOnlyToConfiguredWeChat(t *testing.T) {
	startedAt := time.Unix(1_700_000_000, 0).UTC()
	tests := []struct {
		name       string
		sourceText string
		endedAt    time.Time
	}{
		{name: "text threshold", sourceText: strings.Repeat("字", recordsv1.LongSourceTextThreshold+1), endedAt: startedAt.Add(time.Second)},
		{name: "audio threshold", sourceText: "short", endedAt: startedAt.Add(recordsv1.LongSourceAudioThreshold)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &atomicScheduleRepository{retryRepositoryStub: retryRepositoryStub{preferences: []Preference{
				{AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary-email", Enabled: true, Verified: true},
				{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true},
			}}}
			service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
			service.ConfigureChannelRouter(NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), &channelProviderStub{channel: ChannelWeChat}))
			event := automaticScheduleEvent(tt.sourceText, startedAt, tt.endedAt)
			if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
				t.Fatalf("ScheduleFinalTurn() error = %v", err)
			}
			if repository.record.Run.Trigger != AutomaticTurnTriggerLongSentence || repository.record.Run.TargetCount != 1 {
				t.Fatalf("automatic run = %#v, want one long-sentence target", repository.record.Run)
			}
			if len(repository.record.Targets) != 1 || repository.record.Targets[0].Message.Channel != ChannelWeChat {
				t.Fatalf("targets = %#v, want WeChat only", repository.record.Targets)
			}
			if got := repository.record.Targets[0].IdempotencyKey; got != "auto:final_turn:turn-1:wechat:primary-wechat" {
				t.Fatalf("idempotency key = %q", got)
			}
		})
	}
}

func TestScheduleFinalTurnPersistsZeroTargetForUnavailableLongSourceWeChat(t *testing.T) {
	startedAt := time.Unix(1_700_000_000, 0).UTC()
	tests := []struct {
		name        string
		preferences []Preference
		router      *ChannelRouter
		destErr     error
	}{
		{name: "unbound", router: NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), &channelProviderStub{channel: ChannelWeChat})},
		{name: "provider unconfigured", preferences: []Preference{{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}}, router: NewChannelRouter(NewFakeEmailProvider(FakeEmailProviderConfig{}), UnconfiguredProvider{})},
		{name: "destination invalid", preferences: []Preference{{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}}, router: NewChannelRouter(nil, &channelProviderStub{channel: ChannelWeChat}), destErr: domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &atomicScheduleRepository{retryRepositoryStub: retryRepositoryStub{preferences: tt.preferences}}
			service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{err: tt.destErr}, nil)
			service.ConfigureChannelRouter(tt.router)
			event := automaticScheduleEvent(strings.Repeat("x", recordsv1.LongSourceTextThreshold+1), startedAt, startedAt.Add(time.Second))
			if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
				t.Fatalf("ScheduleFinalTurn() error = %v", err)
			}
			if repository.record.Run.Trigger != AutomaticTurnTriggerLongSentence || repository.record.Run.TargetCount != 0 || len(repository.record.Targets) != 0 {
				t.Fatalf("automatic schedule = %#v, want zero-target long run", repository.record)
			}
		})
	}
}

func TestScheduleFinalTurnLongSourcePropagatesTransientDestinationFailure(t *testing.T) {
	startedAt := time.Unix(1_700_000_000, 0).UTC()
	repository := &atomicScheduleRepository{retryRepositoryStub: retryRepositoryStub{preference: Preference{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}}}
	destinationErr := errors.New("destination store unavailable")
	service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{err: destinationErr}, nil)
	service.ConfigureChannelRouter(NewChannelRouter(nil, &channelProviderStub{channel: ChannelWeChat}))
	event := automaticScheduleEvent(strings.Repeat("x", recordsv1.LongSourceTextThreshold+1), startedAt, startedAt.Add(time.Second))
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); !errors.Is(err, destinationErr) {
		t.Fatalf("ScheduleFinalTurn() error = %v, want %v", err, destinationErr)
	}
	if repository.record.Run.TurnID != "" {
		t.Fatalf("automatic schedule = %#v, want no committed record", repository.record)
	}
}

func TestScheduleFinalTurnLongSourceFailsClosedWithoutAtomicRepository(t *testing.T) {
	service := NewPersistentUseCases(&retryRepositoryStub{}, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
	event := recordsv1.FinalTurnEvent{TurnID: "turn-1", SourceText: strings.Repeat("x", recordsv1.LongSourceTextThreshold+1)}
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("ScheduleFinalTurn() error = %v, want not implemented", err)
	}
}

func TestScheduleFinalTurnSkipsShortSourceWithoutConfiguredDelivery(t *testing.T) {
	repository := &atomicScheduleRepository{}
	service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
	startedAt := time.Unix(1_700_000_000, 0).UTC()
	event := automaticScheduleEvent("short", startedAt, startedAt.Add(recordsv1.LongSourceAudioThreshold-time.Millisecond))
	if err := service.ScheduleFinalTurn(t.Context(), "account-1", event); err != nil {
		t.Fatalf("ScheduleFinalTurn() error = %v", err)
	}
	if repository.record.Run.TurnID != "" {
		t.Fatalf("automatic schedule = %#v, want no-op", repository.record)
	}
}

func automaticScheduleEvent(sourceText string, startedAt, endedAt time.Time) recordsv1.FinalTurnEvent {
	return recordsv1.FinalTurnEvent{
		TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1", TargetLanguage: "en-US",
		SourceText: sourceText, TranslatedText: "translation", LanguageConfigVersion: 3,
		StartedAt: startedAt, EndedAt: endedAt,
	}
}

func TestScheduleFinalTurnAtomicRejectsInvalidAndConflictingEvents(t *testing.T) {
	validEvent := recordsv1.FinalTurnEvent{
		TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1", TargetLanguage: "en-US",
		TranslatedText: "translation", LanguageConfigVersion: 3, DeliveryEnabled: true,
	}
	tests := []struct {
		name       string
		event      recordsv1.FinalTurnEvent
		repository *atomicScheduleRepository
		turns      TurnReader
		dest       DestinationReader
		want       error
		wantText   string
	}{
		{name: "invalid event", event: recordsv1.FinalTurnEvent{TurnID: "turn-1", DeliveryEnabled: true}, repository: &atomicScheduleRepository{}, turns: automaticTurnReaderStub{}, dest: automaticDestinationReaderStub{}, want: domain.ErrInvalidArgument},
		{name: "missing dependencies", event: validEvent, repository: &atomicScheduleRepository{}, turns: nil, dest: nil, want: domain.ErrInvalidArgument},
		{name: "existing payload conflict", event: validEvent, repository: &atomicScheduleRepository{existing: AutomaticTurnRun{TurnID: "turn-1", SessionID: "other-session"}}, turns: automaticTurnReaderStub{}, dest: automaticDestinationReaderStub{}, want: domain.ErrConflict},
		{name: "lookup failure", event: validEvent, repository: &atomicScheduleRepository{getErr: errors.New("lookup unavailable")}, turns: automaticTurnReaderStub{}, dest: automaticDestinationReaderStub{}, wantText: "lookup unavailable"},
		{name: "final turn missing", event: validEvent, repository: &atomicScheduleRepository{}, turns: automaticTurnReaderStub{turns: []FinalTurnSnapshot{}}, dest: automaticDestinationReaderStub{}, want: domain.ErrNotFound},
		{name: "turn reader failure", event: validEvent, repository: &atomicScheduleRepository{}, turns: automaticTurnReaderStub{err: errors.New("turn store unavailable")}, dest: automaticDestinationReaderStub{}, wantText: "turn store unavailable"},
		{name: "destination failure", event: validEvent, repository: &atomicScheduleRepository{retryRepositoryStub: retryRepositoryStub{preference: Preference{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}}}, turns: automaticTurnReaderStub{}, dest: automaticDestinationReaderStub{err: errors.New("destination unavailable")}, wantText: "destination unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewPersistentUseCases(tt.repository, tt.turns, tt.dest, nil)
			err := service.ScheduleFinalTurn(t.Context(), "account-1", tt.event)
			if tt.wantText != "" {
				if err == nil || err.Error() != tt.wantText {
					t.Fatalf("ScheduleFinalTurn() error = %v, want %q", err, tt.wantText)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("ScheduleFinalTurn() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestScheduleFinalTurnAtomicWrapsScheduleFailure(t *testing.T) {
	repository := &atomicScheduleRepository{
		retryRepositoryStub: retryRepositoryStub{preference: Preference{AccountID: "account-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Enabled: true, Verified: true}},
		scheduleErr:         errors.New("database unavailable"),
	}
	service := NewPersistentUseCases(repository, automaticTurnReaderStub{}, automaticDestinationReaderStub{}, nil)
	err := service.ScheduleFinalTurn(t.Context(), "account-1", recordsv1.FinalTurnEvent{
		TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1", TargetLanguage: "en-US",
		TranslatedText: "translation", LanguageConfigVersion: 3, DeliveryEnabled: true,
	})
	if !errors.Is(err, repository.scheduleErr) {
		t.Fatalf("ScheduleFinalTurn() error = %v, want wrapped schedule failure", err)
	}
}

func TestAutomaticTurnRunStatus(t *testing.T) {
	tests := []struct {
		name                        string
		targetCount, settledCount   int
		succeededCount, failedCount int
		want                        AutomaticTurnRunStatus
	}{
		{name: "pending", targetCount: 2, settledCount: 1, succeededCount: 1, failedCount: 0, want: AutomaticTurnRunPending},
		{name: "succeeded", targetCount: 2, settledCount: 2, succeededCount: 2, failedCount: 0, want: AutomaticTurnRunSucceeded},
		{name: "failed", targetCount: 2, settledCount: 2, succeededCount: 0, failedCount: 2, want: AutomaticTurnRunFailed},
		{name: "mixed", targetCount: 2, settledCount: 2, succeededCount: 1, failedCount: 1, want: AutomaticTurnRunPartiallySucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := automaticTurnRunStatus(tt.targetCount, tt.settledCount, tt.succeededCount, tt.failedCount); got != tt.want {
				t.Fatalf("automaticTurnRunStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAutomaticTurnOperationsFailClosedWhenCapabilitiesAreMissing(t *testing.T) {
	service := NewPersistentUseCases(&retryRepositoryStub{}, nil, nil, nil)
	if NewUseCases() == nil {
		t.Fatal("NewUseCases() did not return *UseCases")
	}
	if err := service.RetryAutomaticTurns(t.Context(), 1); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("RetryAutomaticTurns() error = %v, want not implemented", err)
	}
	if err := service.RecoverAutomaticTurns(t.Context(), 1); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("RecoverAutomaticTurns() error = %v, want not implemented", err)
	}
	if err := service.RestoreAutomaticTurns(t.Context(), 1); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("RestoreAutomaticTurns() error = %v, want not implemented", err)
	}
	if err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("RecoverAutomaticTurn() error = %v, want not implemented", err)
	}
	if err := service.RestoreAutomaticTurn(t.Context(), "account-1", "turn-1"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("RestoreAutomaticTurn() error = %v, want not implemented", err)
	}
}

func TestRetryAutomaticTurnFailuresOnlyRetriesFailedTargetsAfterPartialSuccess(t *testing.T) {
	message := Message{ID: "message-1", AccountID: "account-1", Attempts: 1, Status: MessageStatusFailed}
	repository := &atomicScheduleRepository{
		retryRepositoryStub: retryRepositoryStub{current: map[string]Message{"account-1": message}},
		existing:            AutomaticTurnRun{AccountID: "account-1", TurnID: "turn-1", Status: AutomaticTurnRunPartiallySucceeded, SucceededCount: 1, FailedCount: 1},
		settlements:         []AutomaticTurnSettlement{{TurnID: "turn-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Status: AutomaticTurnSettlementFailed, MessageID: message.ID}},
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	if err := service.RetryAutomaticTurnFailures(t.Context(), "account-1", "turn-1"); err != nil {
		t.Fatalf("RetryAutomaticTurnFailures() error = %v", err)
	}
	if len(repository.retried) != 1 || repository.retried[0].idempotencyKey != "auto:final_turn_retry:turn-1:wechat:primary-wechat:2" {
		t.Fatalf("retried targets = %#v", repository.retried)
	}
}

func TestRetryAutomaticTurnFailuresSkipsAllInitialFailures(t *testing.T) {
	message := Message{ID: "message-1", AccountID: "account-1", Attempts: 1, Status: MessageStatusFailed}
	repository := &atomicScheduleRepository{
		retryRepositoryStub: retryRepositoryStub{current: map[string]Message{"account-1": message}},
		existing:            AutomaticTurnRun{AccountID: "account-1", TurnID: "turn-1", Status: AutomaticTurnRunFailed, FailedCount: 1},
		settlements:         []AutomaticTurnSettlement{{TurnID: "turn-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat", Status: AutomaticTurnSettlementFailed, MessageID: message.ID}},
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	if err := service.RetryAutomaticTurnFailures(t.Context(), "account-1", "turn-1"); err != nil {
		t.Fatalf("RetryAutomaticTurnFailures() error = %v", err)
	}
	if len(repository.retried) != 0 {
		t.Fatalf("retried targets = %#v, want no retry after total initial failure", repository.retried)
	}
}

func TestRecoverAutomaticTurnPlaysPersistedFallbackSnapshot(t *testing.T) {
	run := AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
		Status: AutomaticTurnRunFailed, TargetCount: 2, FailedCount: 2,
		FallbackOperationID: "fallback_turn-1",
	}
	repository := &atomicScheduleRepository{existing: run}
	player := &fallbackPlayerFake{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(player)
	if err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1"); err != nil {
		t.Fatalf("RecoverAutomaticTurn() error = %v", err)
	}
	if player.request.OperationID != run.FallbackOperationID || player.request.TranslatedText != run.TranslatedText || player.request.LanguageConfigVersion != 3 {
		t.Fatalf("fallback request = %#v", player.request)
	}
	if !repository.fallbackPlayed {
		t.Fatal("fallback run was not marked played")
	}
}

func TestRecoverAutomaticTurnLeavesPendingWhenPlaybackFails(t *testing.T) {
	repository := &atomicScheduleRepository{existing: AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
		Status: AutomaticTurnRunFailed, TargetCount: 1, FailedCount: 1, FallbackOperationID: "fallback_turn-1",
	}}
	player := &fallbackPlayerFake{err: errors.New("realtime unavailable")}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(player)
	if err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1"); err == nil {
		t.Fatal("RecoverAutomaticTurn() error = nil")
	}
	if repository.fallbackPlayed {
		t.Fatal("failed playback was marked played")
	}
}

func TestRecoverAutomaticTurnReportsFallbackStateUpdateFailure(t *testing.T) {
	repository := &atomicScheduleRepository{
		existing: AutomaticTurnRun{
			AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1",
			TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
			Status: AutomaticTurnRunFailed, FallbackOperationID: "fallback_turn-1",
		},
		fallbackPlayedErr: errors.New("fallback state unavailable"),
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(&fallbackPlayerFake{})

	err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1")
	if err == nil || err.Error() != "mark automatic fallback played: fallback state unavailable" {
		t.Fatalf("RecoverAutomaticTurn() error = %v", err)
	}
}

func TestRecoverAutomaticTurnSkipsFallbackWithoutClaimOwnership(t *testing.T) {
	repository := &atomicScheduleRepository{existing: AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
		Status: AutomaticTurnRunFallbackPending, TargetCount: 1, FailedCount: 1, FallbackOperationID: "fallback_turn-1",
	}}
	player := &fallbackPlayerFake{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(player)

	if err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1"); err != nil {
		t.Fatalf("RecoverAutomaticTurn() error = %v", err)
	}
	if player.calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", player.calls)
	}
	if repository.fallbackPlayed {
		t.Fatal("unowned fallback run was marked played")
	}
}

func TestRecoverAutomaticTurnPropagatesClaimFailure(t *testing.T) {
	claimErr := errors.New("fallback claim unavailable")
	repository := &atomicScheduleRepository{claimErr: claimErr}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(&fallbackPlayerFake{})

	if err := service.RecoverAutomaticTurn(t.Context(), "account-1", "turn-1"); !errors.Is(err, claimErr) {
		t.Fatalf("RecoverAutomaticTurn() error = %v, want %v", err, claimErr)
	}
}

func TestRestoreAutomaticTurnMarksRunAfterBidirectionalConfig(t *testing.T) {
	repository := &atomicScheduleRepository{existing: AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1",
		LanguageConfigVersion: 3, Status: AutomaticTurnRunFallbackPlayed,
		FallbackOperationID: "fallback_turn-1",
	}}
	restorer := &outputRestorerFake{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticOutputRestorer(restorer)
	if err := service.RestoreAutomaticTurn(t.Context(), "account-1", "turn-1"); err != nil {
		t.Fatalf("RestoreAutomaticTurn() error = %v", err)
	}
	if restorer.sessionID != "session-1" || restorer.expectedVersion != 3 || restorer.operationID != "restore_fallback_turn-1" {
		t.Fatalf("restore input = %#v", restorer)
	}
	if !repository.restored {
		t.Fatal("automatic turn was not marked restored")
	}
}

func TestRestoreAutomaticTurnReportsOutputRestoreFailure(t *testing.T) {
	repository := &atomicScheduleRepository{existing: AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1",
		LanguageConfigVersion: 3, Status: AutomaticTurnRunFallbackPlayed,
		FallbackOperationID: "fallback_turn-1",
	}}
	restorer := &outputRestorerFake{err: errors.New("realtime unavailable")}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticOutputRestorer(restorer)

	err := service.RestoreAutomaticTurn(t.Context(), "account-1", "turn-1")
	if err == nil || err.Error() != "restore bidirectional output: realtime unavailable" {
		t.Fatalf("RestoreAutomaticTurn() error = %v", err)
	}
}

func TestRestoreAutomaticTurnReportsStateUpdateFailure(t *testing.T) {
	repository := &atomicScheduleRepository{
		existing: AutomaticTurnRun{
			AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1",
			LanguageConfigVersion: 3, Status: AutomaticTurnRunFallbackPlayed,
			FallbackOperationID: "fallback_turn-1",
		},
		restoredErr: errors.New("restore state unavailable"),
	}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticOutputRestorer(&outputRestorerFake{})

	err := service.RestoreAutomaticTurn(t.Context(), "account-1", "turn-1")
	if err == nil || err.Error() != "restore state unavailable" {
		t.Fatalf("RestoreAutomaticTurn() error = %v", err)
	}
}

type atomicScheduleRepository struct {
	retryRepositoryStub
	record            AutomaticTurnScheduleRecord
	existing          AutomaticTurnRun
	getErr            error
	scheduleErr       error
	claimErr          error
	settlements       []AutomaticTurnSettlement
	retried           []automaticRetryRecord
	fallbackPlayed    bool
	fallbackPlayedErr error
	restored          bool
	restoredErr       error
}

func (r *atomicScheduleRepository) GetAutomaticTurnRun(context.Context, string, string) (AutomaticTurnRun, error) {
	if r.getErr != nil {
		return AutomaticTurnRun{}, r.getErr
	}
	if r.existing.TurnID == "" {
		return AutomaticTurnRun{}, domain.ErrNotFound
	}
	return r.existing, nil
}

func (r *atomicScheduleRepository) ScheduleAutomaticTurn(_ context.Context, record AutomaticTurnScheduleRecord) error {
	if r.scheduleErr != nil {
		return r.scheduleErr
	}
	r.record = record
	return nil
}

func (r *atomicScheduleRepository) ListAutomaticTurnSettlements(context.Context, string, string) ([]AutomaticTurnSettlement, error) {
	return r.settlements, nil
}

func (r *atomicScheduleRepository) ListAutomaticTurnRetryCandidates(context.Context, int) ([]AutomaticTurnRun, error) {
	return []AutomaticTurnRun{r.existing}, nil
}

type automaticRetryRecord struct {
	messageID, idempotencyKey string
}

func (r *atomicScheduleRepository) RetryAutomaticTurnTarget(_ context.Context, _, _, messageID, idempotencyKey string) (Message, error) {
	r.retried = append(r.retried, automaticRetryRecord{messageID: messageID, idempotencyKey: idempotencyKey})
	return Message{ID: messageID}, nil
}

func (r *atomicScheduleRepository) ListAutomaticTurnRecoveryCandidates(context.Context, int) ([]AutomaticTurnRun, error) {
	return []AutomaticTurnRun{r.existing}, nil
}

func (r *atomicScheduleRepository) ClaimAutomaticTurnFallback(context.Context, string, string) (AutomaticTurnRun, bool, error) {
	if r.claimErr != nil {
		return AutomaticTurnRun{}, false, r.claimErr
	}
	if r.existing.Status == AutomaticTurnRunFallbackPending {
		return r.existing, false, nil
	}
	r.existing.Status = AutomaticTurnRunFallbackPending
	return r.existing, true, nil
}

func (r *atomicScheduleRepository) MarkAutomaticTurnFallbackPlayed(context.Context, string, string) error {
	if r.fallbackPlayedErr != nil {
		return r.fallbackPlayedErr
	}
	r.fallbackPlayed = true
	r.existing.Status = AutomaticTurnRunFallbackPlayed
	return nil
}

func (r *atomicScheduleRepository) ListAutomaticTurnRestoreCandidates(context.Context, int) ([]AutomaticTurnRun, error) {
	return []AutomaticTurnRun{r.existing}, nil
}

func (r *atomicScheduleRepository) MarkAutomaticTurnRestored(context.Context, string, string) error {
	if r.restoredErr != nil {
		return r.restoredErr
	}
	r.restored = true
	r.existing.Status = AutomaticTurnRunRestored
	return nil
}

type fallbackPlayerFake struct {
	request realtimev1.FallbackPlaybackRequest
	err     error
	calls   int
}

type outputRestorerFake struct {
	sessionID       string
	expectedVersion int
	operationID     string
	err             error
}

func (f *outputRestorerFake) RestoreBidirectionalOutput(_ context.Context, _, sessionID string, expectedVersion int, operationID string) error {
	f.sessionID = sessionID
	f.expectedVersion = expectedVersion
	f.operationID = operationID
	return f.err
}

func (f *fallbackPlayerFake) PlayFallback(_ context.Context, _ string, request realtimev1.FallbackPlaybackRequest) (realtimev1.FallbackPlaybackReceipt, error) {
	f.calls++
	f.request = request
	if f.err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, f.err
	}
	return realtimev1.FallbackPlaybackReceipt{OperationID: request.OperationID, Status: realtimev1.FallbackPlaybackAccepted}, nil
}

type automaticTurnReaderStub struct {
	turns []FinalTurnSnapshot
	err   error
}

func (r automaticTurnReaderStub) ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.turns != nil {
		return r.turns, nil
	}
	return []FinalTurnSnapshot{{TurnID: "turn-1", SourceText: "原文", TranslatedText: "translation"}}, nil
}

type automaticDestinationReaderStub struct {
	err error
}

func (r automaticDestinationReaderStub) ResolveVerifiedDestination(_ context.Context, accountID string, channel Channel, reference string) (VerifiedDestination, error) {
	if r.err != nil {
		return VerifiedDestination{}, r.err
	}
	return VerifiedDestination{AccountID: accountID, Channel: channel, DestinationRef: reference, ProviderTarget: "opaque"}, nil
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

func TestRetryUsesDurableLookupInsteadOfProcessCache(t *testing.T) {
	repository := &retryRepositoryStub{current: map[string]Message{
		"account-1": {ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1},
	}}
	ctx := context.Background()
	service := NewPersistentUseCases(repository, nil, nil, nil)
	if _, err := service.Retry(ctx, "account-1", "message-1", "retry-key"); err != nil {
		t.Fatalf("first Retry() error = %v", err)
	}
	repository.lookup = Message{ID: "message-1", AccountID: "account-1", Status: MessageStatusRetrying, Attempts: 2}
	message, err := service.Retry(ctx, "account-1", "message-1", "retry-key")
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
