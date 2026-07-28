package sessions

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestServiceStartAllowsDifferentSessionsInParallel(t *testing.T) {
	now := time.Date(2026, 7, 28, 16, 0, 0, 0, time.UTC)
	repository := newMultiSessionStartRepository(t, now, "vs_1", "vs_2")
	realtime := newParallelStartRealtime(now, "vs_1", "vs_2")
	service, err := NewService(Dependencies{
		Repository:        repository,
		LanguageConfigs:   parallelLanguageConfigs{},
		WebRTCConnections: parallelWebRTCConnections{now: now},
		Realtime:          realtime,
		IDs:               &sequenceStartIDGenerator{ids: []string{"op_1", "op_2"}},
		Clock:             &fakeClock{now: now},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	results := make(chan error, 2)
	go func() {
		_, startErr := service.Start(context.Background(), parallelStartInput("vs_1"))
		results <- startErr
	}()
	<-realtime.entered["vs_1"]

	go func() {
		_, startErr := service.Start(context.Background(), parallelStartInput("vs_2"))
		results <- startErr
	}()
	// Reaching this channel while vs_1 remains blocked proves that Start uses
	// the SessionID as its lock key instead of serializing the whole Service.
	<-realtime.entered["vs_2"]

	close(realtime.release["vs_1"])
	close(realtime.release["vs_2"])
	for range 2 {
		if startErr := <-results; startErr != nil {
			t.Fatalf("Start() error = %v", startErr)
		}
	}

	expectedOperationIDs := map[string]string{"vs_1": "op_1", "vs_2": "op_2"}
	for _, sessionID := range []string{"vs_1", "vs_2"} {
		state := repository.states[sessionID]
		state.mu.Lock()
		sessionStatus := state.session.Status
		operationStatus := state.operation.Status
		operationID := state.operation.ID
		state.mu.Unlock()
		if sessionStatus != StatusActive ||
			operationStatus != StartOperationCompleted ||
			operationID != expectedOperationIDs[sessionID] {
			t.Fatalf(
				"%s status = %q, operation = %q (%q); want active, completed (%q)",
				sessionID,
				sessionStatus,
				operationStatus,
				operationID,
				expectedOperationIDs[sessionID],
			)
		}
	}
	service.locks.mu.Lock()
	lockEntries := len(service.locks.locks)
	service.locks.mu.Unlock()
	if lockEntries != 0 {
		t.Fatalf("lock entries after requests = %d, want 0", lockEntries)
	}
}

func parallelStartInput(sessionID string) StartInput {
	return StartInput{
		AccountID:      "acct_1",
		SessionID:      sessionID,
		IdempotencyKey: "start_" + sessionID,
		RequestHash:    "hash_" + sessionID,
		TraceID:        "trace_" + sessionID,
		StartedBy:      "acct_1",
	}
}

type multiSessionStartRepository struct {
	states map[string]*startOperationRepository
}

func newMultiSessionStartRepository(
	t *testing.T,
	now time.Time,
	sessionIDs ...string,
) *multiSessionStartRepository {
	t.Helper()
	states := make(map[string]*startOperationRepository, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		states[sessionID] = &startOperationRepository{session: VoiceSession{
			ID:           sessionID,
			AccountID:    "acct_1",
			Status:       StatusCreated,
			AudioConfig:  marshalStartJSON(t, DefaultAudioConfig()),
			Capabilities: marshalStartJSON(t, validCapabilities()),
			CreatedAt:    now.Add(-time.Hour),
		}}
	}
	return &multiSessionStartRepository{states: states}
}

func (r *multiSessionStartRepository) state(sessionID string) (*startOperationRepository, error) {
	state := r.states[sessionID]
	if state == nil {
		return nil, ErrVoiceSessionNotFound
	}
	return state, nil
}

func (*multiSessionStartRepository) Create(
	context.Context,
	CreateParams,
) (VoiceSession, bool, error) {
	return VoiceSession{}, false, ErrNotImplemented
}

func (r *multiSessionStartRepository) GetOwned(
	ctx context.Context,
	accountID string,
	sessionID string,
) (VoiceSession, error) {
	state, err := r.state(sessionID)
	if err != nil {
		return VoiceSession{}, err
	}
	return state.GetOwned(ctx, accountID, sessionID)
}

func (*multiSessionStartRepository) List(context.Context, ListFilter) (ListPage, error) {
	return ListPage{}, ErrNotImplemented
}

func (r *multiSessionStartRepository) BeginStartOperation(
	ctx context.Context,
	params BeginStartOperationParams,
) (BeginStartOperationResult, error) {
	state, err := r.state(params.SessionID)
	if err != nil {
		return BeginStartOperationResult{}, err
	}
	return state.BeginStartOperation(ctx, params)
}

func (r *multiSessionStartRepository) ClaimStartCompensation(
	ctx context.Context,
	params ClaimStartCompensationParams,
) (ClaimStartCompensationResult, error) {
	state, err := r.state(params.SessionID)
	if err != nil {
		return ClaimStartCompensationResult{}, err
	}
	return state.ClaimStartCompensation(ctx, params)
}

func (r *multiSessionStartRepository) CompleteStartCompensation(
	ctx context.Context,
	params CompleteStartCompensationParams,
) error {
	state, err := r.state(params.SessionID)
	if err != nil {
		return err
	}
	return state.CompleteStartCompensation(ctx, params)
}

func (r *multiSessionStartRepository) FailStartCompensation(
	ctx context.Context,
	params FailStartCompensationParams,
) error {
	state, err := r.state(params.SessionID)
	if err != nil {
		return err
	}
	return state.FailStartCompensation(ctx, params)
}

func (*multiSessionStartRepository) SaveEndIntent(
	context.Context,
	EndIntent,
) (EndIntent, bool, error) {
	return EndIntent{}, false, ErrNotImplemented
}

func (*multiSessionStartRepository) GetEndIntent(
	context.Context,
	string,
	string,
) (EndIntent, error) {
	return EndIntent{}, ErrNotImplemented
}

func (*multiSessionStartRepository) CompleteEndIntent(
	context.Context,
	string,
	string,
	time.Time,
) error {
	return ErrNotImplemented
}

func (r *multiSessionStartRepository) TransitionToActive(
	ctx context.Context,
	params StartTransitionParams,
) (VoiceSession, bool, error) {
	state, err := r.state(params.SessionID)
	if err != nil {
		return VoiceSession{}, false, err
	}
	return state.TransitionToActive(ctx, params)
}

func (*multiSessionStartRepository) TransitionToEnded(
	context.Context,
	EndTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

func (*multiSessionStartRepository) TransitionToFailed(
	context.Context,
	FailureTransitionParams,
) (VoiceSession, error) {
	return VoiceSession{}, ErrNotImplemented
}

type parallelLanguageConfigs struct{}

func (parallelLanguageConfigs) GetCurrentConfig(
	_ context.Context,
	sessionID string,
) (LanguageConfigSnapshot, error) {
	return LanguageConfigSnapshot{
		SessionID:         sessionID,
		Version:           1,
		LanguagePairCount: 2,
		Status:            LanguageConfigActive,
	}, nil
}

type parallelWebRTCConnections struct {
	now time.Time
}

func (c parallelWebRTCConnections) GetConnectionState(
	_ context.Context,
	sessionID string,
) (WebRTCConnectionSnapshot, error) {
	return WebRTCConnectionSnapshot{
		SessionID:       sessionID,
		ConnectionID:    "pc_" + sessionID,
		ConnectionState: ConnectionConnected,
		UpdatedAt:       c.now,
	}, nil
}

type parallelStartRealtime struct {
	now     time.Time
	entered map[string]chan struct{}
	release map[string]chan struct{}
}

func newParallelStartRealtime(now time.Time, sessionIDs ...string) *parallelStartRealtime {
	entered := make(map[string]chan struct{}, len(sessionIDs))
	release := make(map[string]chan struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		entered[sessionID] = make(chan struct{})
		release[sessionID] = make(chan struct{})
	}
	return &parallelStartRealtime{now: now, entered: entered, release: release}
}

func (r *parallelStartRealtime) Start(
	_ context.Context,
	command StartRealtimeCommand,
) (RuntimeSnapshot, error) {
	close(r.entered[command.SessionID])
	<-r.release[command.SessionID]
	return RuntimeSnapshot{
		SessionID:    command.SessionID,
		RuntimeState: RuntimeListening,
		UpdatedAt:    r.now,
	}, nil
}

func (*parallelStartRealtime) Stop(
	context.Context,
	StopRealtimeCommand,
) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, ErrNotImplemented
}

func (*parallelStartRealtime) GetRuntimeState(
	context.Context,
	string,
) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, ErrNotImplemented
}

type sequenceStartIDGenerator struct {
	mu   sync.Mutex
	ids  []string
	next int
}

func (*sequenceStartIDGenerator) NewVoiceSessionID() string {
	return ""
}

func (g *sequenceStartIDGenerator) NewStartOperationID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.ids[g.next]
	g.next++
	return id
}
