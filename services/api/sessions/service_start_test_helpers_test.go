package sessions

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type startTraceKey struct{}

type startFixture struct {
	service     *Service
	repository  *startRepository
	languages   *fakeLanguageConfigReader
	connections *fakeWebRTCConnectionReader
	realtime    *startRealtime
	clock       *fakeClock
}

func newStartFixture(t *testing.T, status Status) *startFixture {
	t.Helper()
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	session := VoiceSession{
		ID:           "vs_1",
		AccountID:    "acct_1",
		Status:       status,
		AudioConfig:  marshalStartJSON(t, DefaultAudioConfig()),
		Capabilities: marshalStartJSON(t, validCapabilities()),
		CreatedAt:    now.Add(-time.Hour),
	}
	repository := &startRepository{session: session}
	languages := &fakeLanguageConfigReader{result: LanguageConfigSnapshot{
		SessionID: "vs_1", Version: 1, LanguagePairCount: 2, Status: LanguageConfigActive,
	}}
	connections := &fakeWebRTCConnectionReader{result: WebRTCConnectionSnapshot{
		SessionID: "vs_1", ConnectionID: "pc_1",
		ConnectionState: ConnectionConnected, UpdatedAt: now,
	}}
	realtime := &startRealtime{
		startResult: RuntimeSnapshot{
			SessionID: "vs_1", RuntimeState: RuntimeListening, UpdatedAt: now,
		},
		stopResult: RuntimeSnapshot{
			SessionID: "vs_1", RuntimeState: RuntimeStopped, UpdatedAt: now,
		},
	}
	clock := &fakeClock{now: now}
	service, err := NewService(Dependencies{
		Repository:          repository,
		LanguageConfigs:     languages,
		WebRTCConnections:   connections,
		Realtime:            realtime,
		IDs:                 &fakeIDGenerator{id: "vs_generated"},
		Clock:               clock,
		CompensationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &startFixture{
		service: service, repository: repository, languages: languages,
		connections: connections, realtime: realtime, clock: clock,
	}
}

func validStartInput() StartInput {
	return StartInput{
		AccountID:      "acct_1",
		SessionID:      "vs_1",
		IdempotencyKey: "start_1",
		RequestHash:    "hash_1",
		TraceID:        "req_1",
		StartedBy:      "acct_1",
	}
}

func activeStartSession(session VoiceSession, startedAt time.Time) VoiceSession {
	session.Status = StatusActive
	session.StartedAt = &startedAt
	return session
}

func marshalStartJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func assertNoStartPrerequisites(t *testing.T, fixture *startFixture) {
	t.Helper()
	if fixture.languages.calls != 0 ||
		fixture.connections.calls != 0 ||
		fixture.realtime.startCalls != 0 {
		t.Fatalf("prerequisite calls = language %d, WebRTC %d, realtime %d; want 0",
			fixture.languages.calls, fixture.connections.calls, fixture.realtime.startCalls)
	}
}

type startRepository struct {
	mu sync.Mutex

	session          VoiceSession
	getErr           error
	getCalls         int
	transitionErr    error
	transitionHook   func(context.Context)
	transitionResult VoiceSession
	transitions      []StartTransitionParams
	startKey         string
	startHash        string
	lastReplayed     bool
}

func (*startRepository) Create(context.Context, CreateParams) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (r *startRepository) GetOwned(
	_ context.Context,
	accountID string,
	sessionID string,
) (VoiceSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getCalls++
	if r.getErr != nil {
		return VoiceSession{}, r.getErr
	}
	if r.session.AccountID != accountID || r.session.ID != sessionID {
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	return r.session, nil
}

func (*startRepository) List(context.Context, ListFilter) (ListPage, error) {
	return ListPage{}, ErrNotImplemented
}

func (*startRepository) SaveEndIntent(context.Context, EndIntent) (EndIntent, bool, error) {
	return EndIntent{}, false, ErrNotImplemented
}

func (*startRepository) GetEndIntent(context.Context, string, string) (EndIntent, error) {
	return EndIntent{}, ErrNotImplemented
}

func (*startRepository) CompleteEndIntent(context.Context, string, string, time.Time) error {
	return ErrNotImplemented
}

func (r *startRepository) TransitionToActive(
	ctx context.Context,
	params StartTransitionParams,
) (VoiceSession, bool, error) {
	r.mu.Lock()
	r.transitions = append(r.transitions, params)
	hook := r.transitionHook
	err := r.transitionErr
	r.mu.Unlock()

	if hook != nil {
		hook(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		return VoiceSession{}, false, err
	}
	if r.startKey != "" {
		if r.startKey != params.IdempotencyKey || r.startHash != params.RequestHash {
			return VoiceSession{}, false, ErrIdempotencyKeyConflict
		}
		r.lastReplayed = true
		return r.session, true, nil
	}
	if r.session.Status != params.Expected {
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	r.startKey = params.IdempotencyKey
	r.startHash = params.RequestHash
	if r.transitionResult.ID == "" {
		r.session.Status = StatusActive
		r.session.StartedAt = &params.StartedAt
	} else {
		r.session = r.transitionResult
	}
	return r.session, false, nil
}

func (*startRepository) TransitionToEnded(context.Context, EndTransitionParams) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (*startRepository) TransitionToFailed(context.Context, FailureTransitionParams) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

type startRealtime struct {
	mu sync.Mutex

	startResult  RuntimeSnapshot
	startErr     error
	startCalls   int
	startCommand StartRealtimeCommand
	startHook    func(context.Context)

	stopResult  RuntimeSnapshot
	stopErr     error
	stopCalls   int
	stopCommand StopRealtimeCommand
	stopHook    func(context.Context)
}

func (r *startRealtime) Start(
	ctx context.Context,
	command StartRealtimeCommand,
) (RuntimeSnapshot, error) {
	r.mu.Lock()
	r.startCalls++
	r.startCommand = command
	result := r.startResult
	err := r.startErr
	hook := r.startHook
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	return result, err
}

func (r *startRealtime) Stop(
	ctx context.Context,
	command StopRealtimeCommand,
) (RuntimeSnapshot, error) {
	r.mu.Lock()
	r.stopCalls++
	r.stopCommand = command
	hook := r.stopHook
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	r.mu.Lock()
	result := r.stopResult
	err := r.stopErr
	r.mu.Unlock()
	return result, err
}

func (*startRealtime) GetRuntimeState(context.Context, string) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, ErrNotImplemented
}
