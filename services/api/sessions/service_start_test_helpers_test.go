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
			SessionID: "vs_1", StartOperationID: "op_1",
			RuntimeState: RuntimeListening, UpdatedAt: now,
		},
		stopResult: RuntimeSnapshot{
			SessionID: "vs_1", RuntimeState: RuntimeStopped, UpdatedAt: now,
		},
		getResult: RuntimeSnapshot{
			SessionID: "vs_1", StartOperationID: "op_1",
			RuntimeState: RuntimeListening, UpdatedAt: now,
		},
	}
	clock := &fakeClock{now: now}
	service, err := NewService(Dependencies{
		Repository:          repository,
		LanguageConfigs:     languages,
		WebRTCConnections:   connections,
		Realtime:            realtime,
		IDs:                 &fakeIDGenerator{id: "op_1"},
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

func newSharedStartService(
	t *testing.T,
	repository Repository,
	languages LanguageConfigReader,
	connections WebRTCConnectionReader,
	realtime RealtimeLifecycle,
	clock Clock,
) *Service {
	t.Helper()
	service, err := NewService(Dependencies{
		Repository:          repository,
		LanguageConfigs:     languages,
		WebRTCConnections:   connections,
		Realtime:            realtime,
		IDs:                 &fakeIDGenerator{id: "op_1"},
		Clock:               clock,
		CompensationTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
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

func completedStartOperation(session VoiceSession, createdAt time.Time) *StartOperation {
	return &StartOperation{
		ID:             "op_1",
		SessionID:      session.ID,
		AccountID:      session.AccountID,
		IdempotencyKey: "start_1",
		RequestHash:    "hash_1",
		Status:         StartOperationCompleted,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}
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

	session                    VoiceSession
	getErr                     error
	getCalls                   int
	getHook                    func(context.Context)
	operation                  *StartOperation
	getOperationErr            error
	getOperationCalls          int
	beginErr                   error
	beginCalls                 int
	beginParams                []BeginStartOperationParams
	claimErr                   error
	claimResult                *ClaimStartCompensationResult
	claimCalls                 int
	claimParams                []ClaimStartCompensationParams
	claimHook                  func(context.Context)
	completeErr                error
	completeCalls              int
	completeParams             []CompleteStartCompensationParams
	completeHook               func(context.Context)
	completeContextErr         error
	requireLiveCompleteContext bool
	failErr                    error
	failCalls                  int
	failParams                 []FailStartCompensationParams
	failHook                   func(context.Context)
	failContextErr             error
	requireLiveFailContext     bool
	transitionErr              error
	transitionErrFor           func(StartTransitionParams) error
	transitionHook             func(context.Context)
	transitionAfter            func(StartTransitionParams)
	transitionResult           VoiceSession
	transitions                []StartTransitionParams
	lastReplayed               bool
}

func (*startRepository) Create(context.Context, CreateParams) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (r *startRepository) GetOwned(
	ctx context.Context,
	accountID string,
	sessionID string,
) (VoiceSession, error) {
	r.mu.Lock()
	r.getCalls++
	hook := r.getHook
	if r.getErr != nil {
		err := r.getErr
		r.mu.Unlock()
		return VoiceSession{}, err
	}
	if r.session.AccountID != accountID || r.session.ID != sessionID {
		r.mu.Unlock()
		return VoiceSession{}, ErrVoiceSessionNotFound
	}
	session := r.session
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	return session, nil
}

func (r *startRepository) BeginStartOperation(
	_ context.Context,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beginCalls++
	r.beginParams = append(r.beginParams, params)
	if r.beginErr != nil {
		return BeginStartOperationResult{}, r.beginErr
	}
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return BeginStartOperationResult{}, ErrVoiceSessionNotFound
	}
	if r.operation != nil && r.operation.IdempotencyKey == params.IdempotencyKey {
		if r.operation.RequestHash != params.RequestHash {
			return BeginStartOperationResult{}, ErrIdempotencyKeyConflict
		}
		switch r.operation.Status {
		case StartOperationPending,
			StartOperationCompensating,
			StartOperationCompleted:
			return BeginStartOperationResult{Operation: *r.operation, Replayed: true}, nil
		case StartOperationCompensated:
			return BeginStartOperationResult{}, ErrIdempotencyKeyConflict
		case StartOperationCompensationFailed:
			return BeginStartOperationResult{}, ErrSessionStartInProgress
		default:
			return BeginStartOperationResult{}, ErrConcurrentTransition
		}
	}
	if r.session.Status != StatusCreated {
		return BeginStartOperationResult{}, ErrConcurrentTransition
	}
	if r.operation != nil && r.operation.Status != StartOperationCompensated {
		return BeginStartOperationResult{}, ErrSessionStartInProgress
	}
	operation := StartOperation{
		ID:             params.OperationID,
		SessionID:      params.SessionID,
		AccountID:      params.AccountID,
		IdempotencyKey: params.IdempotencyKey,
		RequestHash:    params.RequestHash,
		Status:         StartOperationPending,
		CreatedAt:      params.CreatedAt,
		UpdatedAt:      params.CreatedAt,
	}
	r.operation = &operation
	return BeginStartOperationResult{Operation: operation}, nil
}

func (r *startRepository) GetStartOperation(
	_ context.Context,
	accountID string,
	sessionID string,
	idempotencyKey string,
) (StartOperation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getOperationCalls++
	if r.getOperationErr != nil {
		return StartOperation{}, r.getOperationErr
	}
	if r.session.ID != sessionID || r.session.AccountID != accountID {
		return StartOperation{}, ErrVoiceSessionNotFound
	}
	if r.operation == nil {
		return StartOperation{}, ErrStartOperationNotFound
	}
	if r.operation.IdempotencyKey != idempotencyKey {
		switch r.operation.Status {
		case StartOperationPending,
			StartOperationCompensating,
			StartOperationCompensationFailed:
			return StartOperation{}, ErrSessionStartInProgress
		default:
			return StartOperation{}, ErrStartOperationNotFound
		}
	}
	return *r.operation, nil
}

func (r *startRepository) ClaimStartCompensation(
	ctx context.Context,
	params ClaimStartCompensationParams,
) (ClaimStartCompensationResult, error) {
	r.mu.Lock()
	r.claimCalls++
	r.claimParams = append(r.claimParams, params)
	hook := r.claimHook
	err := r.claimErr
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return ClaimStartCompensationResult{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		return ClaimStartCompensationResult{}, err
	}
	if r.claimResult != nil {
		return *r.claimResult, nil
	}
	if r.session.ID != params.SessionID || r.session.AccountID != params.AccountID {
		return ClaimStartCompensationResult{}, ErrVoiceSessionNotFound
	}
	if r.session.Status != StatusCreated {
		return ClaimStartCompensationResult{
			Reason: StartCompensationSessionNotCreated,
		}, nil
	}
	if r.operation == nil || r.operation.ID != params.OperationID {
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationMismatch,
		}, nil
	}
	switch r.operation.Status {
	case StartOperationPending:
		claimID := params.ClaimID
		r.operation.Status = StartOperationCompensating
		r.operation.CompensationClaimID = &claimID
		r.operation.UpdatedAt = params.ClaimedAt
		return ClaimStartCompensationResult{Claimed: true}, nil
	case StartOperationCompensating:
		if r.operation.CompensationClaimID != nil &&
			*r.operation.CompensationClaimID == params.ClaimID {
			return ClaimStartCompensationResult{Claimed: true}, nil
		}
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationNotPending,
		}, nil
	default:
		return ClaimStartCompensationResult{
			Reason: StartCompensationOperationNotPending,
		}, nil
	}
}

func (r *startRepository) CompleteStartCompensation(
	ctx context.Context,
	params CompleteStartCompensationParams,
) error {
	r.mu.Lock()
	r.completeCalls++
	r.completeParams = append(r.completeParams, params)
	hook := r.completeHook
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.requireLiveCompleteContext {
		r.completeContextErr = ctx.Err()
		if r.completeContextErr != nil {
			return r.completeContextErr
		}
	}
	if r.completeErr != nil {
		return r.completeErr
	}
	if !r.ownsStartCompensation(
		params.AccountID,
		params.SessionID,
		params.OperationID,
		params.ClaimID,
	) {
		return ErrConcurrentTransition
	}
	r.operation.Status = StartOperationCompensated
	r.operation.UpdatedAt = params.CompletedAt
	return nil
}

func (r *startRepository) FailStartCompensation(
	ctx context.Context,
	params FailStartCompensationParams,
) error {
	r.mu.Lock()
	r.failCalls++
	r.failParams = append(r.failParams, params)
	hook := r.failHook
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.requireLiveFailContext {
		r.failContextErr = ctx.Err()
		if r.failContextErr != nil {
			return r.failContextErr
		}
	}
	if r.failErr != nil {
		return r.failErr
	}
	if !r.ownsStartCompensation(
		params.AccountID,
		params.SessionID,
		params.OperationID,
		params.ClaimID,
	) {
		return ErrConcurrentTransition
	}
	r.operation.Status = StartOperationCompensationFailed
	r.operation.UpdatedAt = params.FailedAt
	return nil
}

func (r *startRepository) ownsStartCompensation(
	accountID string,
	sessionID string,
	operationID string,
	claimID string,
) bool {
	return r.session.AccountID == accountID &&
		r.session.ID == sessionID &&
		r.session.Status == StatusCreated &&
		r.operation != nil &&
		r.operation.ID == operationID &&
		r.operation.Status == StartOperationCompensating &&
		r.operation.CompensationClaimID != nil &&
		*r.operation.CompensationClaimID == claimID
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
	errFor := r.transitionErrFor
	after := r.transitionAfter
	r.mu.Unlock()

	if hook != nil {
		hook(ctx)
	}
	if errFor != nil {
		err = errFor(params)
	}

	r.mu.Lock()
	if err != nil {
		r.mu.Unlock()
		return VoiceSession{}, false, err
	}
	if r.session.Status == StatusActive {
		if r.operation == nil || r.operation.Status != StartOperationCompleted {
			r.mu.Unlock()
			return VoiceSession{}, false, ErrConcurrentTransition
		}
		if r.operation.IdempotencyKey == params.IdempotencyKey &&
			r.operation.RequestHash != params.RequestHash {
			r.mu.Unlock()
			return VoiceSession{}, false, ErrIdempotencyKeyConflict
		}
		if r.operation.ID == params.OperationID &&
			r.operation.MatchesRequest(params.IdempotencyKey, params.RequestHash) {
			r.lastReplayed = true
			session := r.session
			r.mu.Unlock()
			if after != nil {
				after(params)
			}
			return session, true, nil
		}
		r.mu.Unlock()
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if r.session.Status != params.Expected ||
		r.operation == nil ||
		r.operation.ID != params.OperationID ||
		r.operation.Status != StartOperationPending {
		r.mu.Unlock()
		return VoiceSession{}, false, ErrConcurrentTransition
	}
	if !r.operation.MatchesRequest(params.IdempotencyKey, params.RequestHash) {
		r.mu.Unlock()
		return VoiceSession{}, false, ErrIdempotencyKeyConflict
	}
	if r.transitionResult.ID == "" {
		r.session.Status = StatusActive
		r.session.StartedAt = &params.StartedAt
	} else {
		r.session = r.transitionResult
	}
	r.operation.Status = StartOperationCompleted
	r.operation.UpdatedAt = params.StartedAt
	session := r.session
	r.mu.Unlock()
	if after != nil {
		after(params)
	}
	return session, false, nil
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

	getResult RuntimeSnapshot
	getErr    error
	getCalls  int
	getHook   func(context.Context)
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
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
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

func (r *startRealtime) GetRuntimeState(
	ctx context.Context,
	_ string,
) (RuntimeSnapshot, error) {
	r.mu.Lock()
	r.getCalls++
	result := r.getResult
	err := r.getErr
	hook := r.getHook
	r.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return RuntimeSnapshot{}, err
	}
	return result, err
}
