package sessions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errDependency = errors.New("dependency failed")

type fakeIDGenerator struct {
	id    string
	calls int
}

func (f *fakeIDGenerator) NewVoiceSessionID() string {
	f.calls++
	return f.id
}

func (f *fakeIDGenerator) NewStartOperationID() string {
	f.calls++
	return f.id
}

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	times []time.Time
	calls int
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.calls
	f.calls++
	if len(f.times) > 0 {
		if index >= len(f.times) {
			index = len(f.times) - 1
		}
		return f.times[index]
	}
	return f.now
}

type fakeRepository struct {
	createResult      VoiceSession
	createReplayed    bool
	createErr         error
	createHook        func(context.Context)
	createParams      []CreateParams
	getOwnedResult    VoiceSession
	getOwnedErr       error
	getOwnedCalls     int
	getOwnedAccountID string
	getOwnedSessionID string
	listResult        ListPage
	listErr           error
	listFilters       []ListFilter
}

var (
	_ Repository             = (*fakeRepository)(nil)
	_ LanguageConfigReader   = (*fakeLanguageConfigReader)(nil)
	_ WebRTCConnectionReader = (*fakeWebRTCConnectionReader)(nil)
	_ RealtimeLifecycle      = (*fakeRealtimeLifecycle)(nil)
	_ IDGenerator            = (*fakeIDGenerator)(nil)
	_ Clock                  = (*fakeClock)(nil)
)

type fakeLanguageConfigReader struct {
	result    LanguageConfigSnapshot
	err       error
	calls     int
	sessionID string
}

func (f *fakeLanguageConfigReader) GetCurrentConfig(
	_ context.Context,
	sessionID string,
) (LanguageConfigSnapshot, error) {
	f.calls++
	f.sessionID = sessionID
	return f.result, f.err
}

type fakeWebRTCConnectionReader struct {
	result    WebRTCConnectionSnapshot
	err       error
	calls     int
	sessionID string
}

func (f *fakeWebRTCConnectionReader) GetConnectionState(
	_ context.Context,
	sessionID string,
) (WebRTCConnectionSnapshot, error) {
	f.calls++
	f.sessionID = sessionID
	return f.result, f.err
}

type fakeRealtimeLifecycle struct {
	getResult    RuntimeSnapshot
	getErr       error
	getCalls     int
	getSessionID string
	getHook      func(context.Context)
}

func (*fakeRealtimeLifecycle) Start(
	context.Context,
	StartRealtimeCommand,
) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, ErrNotImplemented
}

func (*fakeRealtimeLifecycle) Stop(
	context.Context,
	StopRealtimeCommand,
) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, ErrNotImplemented
}

func (f *fakeRealtimeLifecycle) GetRuntimeState(
	ctx context.Context,
	sessionID string,
) (RuntimeSnapshot, error) {
	f.getCalls++
	f.getSessionID = sessionID
	if f.getHook != nil {
		f.getHook(ctx)
	}
	return f.getResult, f.getErr
}

func (f *fakeRepository) Create(
	ctx context.Context,
	params CreateParams,
) (VoiceSession, bool, error) {
	f.createParams = append(f.createParams, params)
	if f.createHook != nil {
		f.createHook(ctx)
	}
	return f.createResult, f.createReplayed, f.createErr
}

func (f *fakeRepository) GetOwned(_ context.Context, accountID string, sessionID string) (VoiceSession, error) {
	f.getOwnedCalls++
	f.getOwnedAccountID = accountID
	f.getOwnedSessionID = sessionID
	return f.getOwnedResult, f.getOwnedErr
}

func (f *fakeRepository) GetSession(
	ctx context.Context,
	sessionID string,
) (SessionSnapshot, error) {
	session, err := f.GetOwned(ctx, f.getOwnedResult.AccountID, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	return SessionSnapshot{
		SessionID: session.ID,
		AccountID: session.AccountID,
		Status:    session.Status,
		StartedAt: session.StartedAt,
		EndedAt:   session.EndedAt,
	}, nil
}

func (f *fakeRepository) List(_ context.Context, filter ListFilter) (ListPage, error) {
	f.listFilters = append(f.listFilters, filter)
	return f.listResult, f.listErr
}

func (*fakeRepository) GetStartOperation(
	context.Context,
	string,
	string,
	string,
) (StartOperation, error) {
	return StartOperation{}, ErrNotImplemented
}

func (*fakeRepository) BeginStartOperation(
	context.Context,
	BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	return BeginStartOperationResult{}, ErrNotImplemented
}

func (*fakeRepository) ClaimStartCompensation(
	context.Context,
	ClaimStartCompensationParams,
) (ClaimStartCompensationResult, error) {
	return ClaimStartCompensationResult{}, ErrNotImplemented
}

func (*fakeRepository) CompleteStartCompensation(
	context.Context,
	CompleteStartCompensationParams,
) error {
	return ErrNotImplemented
}

func (*fakeRepository) FailStartCompensation(
	context.Context,
	FailStartCompensationParams,
) error {
	return ErrNotImplemented
}

func (f *fakeRepository) SaveEndIntent(context.Context, EndIntent) (EndIntent, bool, error) {
	return EndIntent{}, false, ErrNotImplemented
}

func (f *fakeRepository) GetEndIntent(context.Context, string, string) (EndIntent, error) {
	return EndIntent{}, ErrNotImplemented
}

func (f *fakeRepository) CompleteEndIntent(context.Context, string, string, time.Time) error {
	return ErrNotImplemented
}

func (f *fakeRepository) TransitionToActive(
	context.Context,
	StartTransitionParams,
) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (f *fakeRepository) TransitionToEnded(
	context.Context,
	EndTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (f *fakeRepository) TransitionToFailed(
	context.Context,
	FailureTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func newCreateTestService(
	t *testing.T,
	repository Repository,
) (*Service, *fakeIDGenerator, *fakeClock) {
	t.Helper()
	ids := &fakeIDGenerator{id: "vs_generated"}
	clock := &fakeClock{now: time.Date(2026, 7, 27, 17, 0, 0, 0, time.FixedZone("CST", 8*60*60))}
	service, err := NewService(Dependencies{
		Repository:        repository,
		LanguageConfigs:   &fakeLanguageConfigReader{},
		WebRTCConnections: &fakeWebRTCConnectionReader{},
		Realtime:          &fakeRealtimeLifecycle{},
		IDs:               ids,
		Clock:             clock,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, ids, clock
}

func validCapabilities() Capabilities {
	return Capabilities{
		WebRTC:             true,
		DataChannel:        true,
		Microphone:         true,
		Speaker:            true,
		SpeakerDiarization: true,
	}
}
