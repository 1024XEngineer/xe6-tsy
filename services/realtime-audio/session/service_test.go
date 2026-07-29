package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	errProvider    = errors.New("provider unavailable")
	errPipeline    = errors.New("pipeline cleanup failed")
	errConnection  = errors.New("connection cleanup failed")
	errRuntimeSave = errors.New("runtime save failed")
	errNoDeadline  = errors.New("cleanup context has no deadline")
)

func TestLifecycleStartCreatedSession(t *testing.T) {
	pipeline := &fakePipeline{}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, pipeline, &fakeConnection{})

	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.RuntimeState != RuntimeListening {
		t.Fatalf("RuntimeState = %q, want listening", got.RuntimeState)
	}
	if pipeline.startCalls != 1 {
		t.Fatalf("pipeline start calls = %d, want 1", pipeline.startCalls)
	}
	service.locks.mu.Lock()
	entryCount := len(service.locks.locks)
	service.locks.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("idle lock entries = %d, want 0", entryCount)
	}
}

func TestLifecycleStartRejectsActiveSession(t *testing.T) {
	pipeline := &fakePipeline{}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "active"}, pipeline, &fakeConnection{})

	_, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, ErrSessionNotCreated) {
		t.Fatalf("Start() error = %v, want ErrSessionNotCreated", err)
	}
	if pipeline.startCalls != 0 {
		t.Fatalf("pipeline start calls = %d, want 0", pipeline.startCalls)
	}
}

func TestLifecycleStartIsIdempotentForRunningRuntime(t *testing.T) {
	pipeline := &fakePipeline{}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, pipeline, &fakeConnection{})
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.RuntimeState != RuntimeListening || pipeline.startCalls != 0 {
		t.Fatalf("Start() = %#v, pipeline calls = %d", got, pipeline.startCalls)
	}
}

func TestLifecycleStartReconcilesStaleActiveRuntime(t *testing.T) {
	pipeline := &healthPipeline{fakePipeline: &fakePipeline{}}
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: SessionSnapshot{SessionID: "session-1", Status: "created"}},
		Runtimes:    NewMemoryRuntimeRepository(),
		Pipelines:   pipeline,
		Connections: &fakeConnection{},
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{
		SessionID: "session-1", RuntimeState: RuntimeListening,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.RuntimeState != RuntimeListening || pipeline.startCalls != 1 {
		t.Fatalf("Start() = %#v, pipeline calls = %d; want reconciled listening and one start", got, pipeline.startCalls)
	}
}

func TestLifecycleStartFailureCanRetry(t *testing.T) {
	pipeline := &fakePipeline{startErrors: []error{errProvider, nil}}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, pipeline, &fakeConnection{})

	failed, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, errProvider) {
		t.Fatalf("first Start() error = %v, want provider error", err)
	}
	if failed.RuntimeState != RuntimeFailed || failed.LastErrorCode == nil || *failed.LastErrorCode != ErrorCodeStartFailed {
		t.Fatalf("failed snapshot = %#v", failed)
	}

	recovered, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if err != nil || recovered.RuntimeState != RuntimeListening {
		t.Fatalf("retry Start() = %#v, %v", recovered, err)
	}
	if pipeline.startCalls != 2 {
		t.Fatalf("pipeline start calls = %d, want 2", pipeline.startCalls)
	}
}

func TestLifecycleStartRejectsRetryAfterStopFailure(t *testing.T) {
	pipeline := &fakePipeline{}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, pipeline, &fakeConnection{})
	errorCode := ErrorCodeStopFailed
	failed := RuntimeSnapshot{
		SessionID:     "session-1",
		RuntimeState:  RuntimeFailed,
		LastErrorCode: &errorCode,
	}
	if err := service.deps.Runtimes.Save(context.Background(), failed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, ErrRuntimeCleanupRequired) {
		t.Fatalf("Start() error = %v, want ErrRuntimeCleanupRequired", err)
	}
	if got.RuntimeState != RuntimeFailed || pipeline.startCalls != 0 {
		t.Fatalf("Start() = %#v, pipeline calls = %d; want failed snapshot and no start", got, pipeline.startCalls)
	}
}

func TestLifecycleStartCompensatesWhenListeningSaveFails(t *testing.T) {
	runtimes := &scriptedRuntimeRepository{
		delegate:   NewMemoryRuntimeRepository(),
		saveErrors: []error{nil, errRuntimeSave, nil},
	}
	pipeline := &fakePipeline{}
	connection := &fakeConnection{}
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: SessionSnapshot{SessionID: "session-1", Status: "created"}},
		Runtimes:    runtimes,
		Pipelines:   pipeline,
		Connections: connection,
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}

	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, errRuntimeSave) {
		t.Fatalf("Start() error = %v, want runtime save error", err)
	}
	if got.RuntimeState != RuntimeFailed {
		t.Fatalf("RuntimeState = %q, want failed", got.RuntimeState)
	}
	if pipeline.startCalls != 1 || pipeline.stopCalls != 1 {
		t.Fatalf("pipeline calls = start %d, stop %d; want 1, 1", pipeline.startCalls, pipeline.stopCalls)
	}
	if connection.closeCalls != 0 {
		t.Fatalf("connection close calls = %d, want 0", connection.closeCalls)
	}
	stored, err := runtimes.Get(context.Background(), "session-1")
	if err != nil || stored.RuntimeState != RuntimeFailed {
		t.Fatalf("stored runtime = %#v, %v; want failed", stored, err)
	}
}

func TestLifecycleStartCompensationSurvivesRequestCancellation(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	runtimes := NewMemoryRuntimeRepository()
	pipeline := &fakePipeline{
		startHook:                cancelRequest,
		requireActiveStopContext: true,
		requireStopDeadline:      true,
	}
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: SessionSnapshot{SessionID: "session-1", Status: "created"}},
		Runtimes:    runtimes,
		Pipelines:   pipeline,
		Connections: &fakeConnection{},
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}

	got, err := service.Start(requestContext, StartRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context canceled", err)
	}
	if got.RuntimeState != RuntimeFailed || !pipeline.stopSucceeded {
		t.Fatalf("Start() = %#v, pipeline stop succeeded = %t", got, pipeline.stopSucceeded)
	}
	stored, err := runtimes.Get(context.Background(), "session-1")
	if err != nil || stored.RuntimeState != RuntimeFailed {
		t.Fatalf("stored runtime = %#v, %v; want failed", stored, err)
	}
}

func TestLifecycleGetRuntimeState(t *testing.T) {
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, &fakePipeline{}, &fakeConnection{})
	want := RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimePlaying}
	if err := service.deps.Runtimes.Save(context.Background(), want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := service.GetRuntimeState(context.Background(), want.SessionID)
	if err != nil || got.RuntimeState != want.RuntimeState {
		t.Fatalf("GetRuntimeState() = %#v, %v", got, err)
	}
}

func TestNewLifecycleServiceRejectsMissingDependencies(t *testing.T) {
	valid := Dependencies{
		Sessions:    &fakeSessionReader{},
		Runtimes:    NewMemoryRuntimeRepository(),
		Pipelines:   &fakePipeline{},
		Connections: &fakeConnection{},
		Now:         time.Now,
	}
	tests := []struct {
		name string
		edit func(*Dependencies)
	}{
		{name: "sessions", edit: func(deps *Dependencies) { deps.Sessions = nil }},
		{name: "runtimes", edit: func(deps *Dependencies) { deps.Runtimes = nil }},
		{name: "pipelines", edit: func(deps *Dependencies) { deps.Pipelines = nil }},
		{name: "connections", edit: func(deps *Dependencies) { deps.Connections = nil }},
		{name: "clock", edit: func(deps *Dependencies) { deps.Now = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := valid
			test.edit(&deps)
			if _, err := NewLifecycleService(deps); !errors.Is(err, ErrInvalidDependency) {
				t.Fatalf("NewLifecycleService() error = %v, want ErrInvalidDependency", err)
			}
		})
	}
}

func TestLifecycleStop(t *testing.T) {
	tests := []struct {
		name                 string
		existing             *RuntimeSnapshot
		pipelineError        error
		connectionError      error
		wantState            RuntimeState
		wantPipelineStops    int
		wantConnectionCloses int
		wantError            error
	}{
		{
			name: "listening runtime stops",
			existing: &RuntimeSnapshot{
				SessionID: "session-1", RuntimeState: RuntimeListening,
				CurrentTurnID: stringPointer("turn-1"), CurrentPlaybackID: stringPointer("playback-1"),
			},
			wantState: RuntimeStopped, wantPipelineStops: 1, wantConnectionCloses: 1,
		},
		{name: "missing runtime is idempotent", wantState: RuntimeStopped},
		{
			name:      "stopped runtime is idempotent",
			existing:  &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeStopped},
			wantState: RuntimeStopped,
		},
		{
			name:          "pipeline cleanup failure still closes connection",
			existing:      &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
			pipelineError: errPipeline, wantState: RuntimeFailed,
			wantPipelineStops: 1, wantConnectionCloses: 1, wantError: errPipeline,
		},
		{
			name:            "connection cleanup failure records failed state",
			existing:        &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
			connectionError: errConnection, wantState: RuntimeFailed,
			wantPipelineStops: 1, wantConnectionCloses: 1, wantError: errConnection,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make([]string, 0, 2)
			pipeline := &fakePipeline{stopError: test.pipelineError, events: &events}
			connection := &fakeConnection{closeError: test.connectionError, events: &events}
			service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "active"}, pipeline, connection)
			if test.existing != nil {
				if err := service.deps.Runtimes.Save(context.Background(), *test.existing); err != nil {
					t.Fatalf("Save() error = %v", err)
				}
			}

			got, err := service.Stop(context.Background(), StopRealtimeCommand{
				SessionID: "session-1", EndedAt: time.Unix(1700000001, 0).UTC(),
			})
			if got.RuntimeState != test.wantState {
				t.Fatalf("RuntimeState = %q, want %q", got.RuntimeState, test.wantState)
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("Stop() error = %v, want %v", err, test.wantError)
			}
			if test.wantError == nil && err != nil {
				t.Fatalf("Stop() error = %v, want nil", err)
			}
			if pipeline.stopCalls != test.wantPipelineStops || connection.closeCalls != test.wantConnectionCloses {
				t.Fatalf("cleanup calls = pipeline %d, connection %d", pipeline.stopCalls, connection.closeCalls)
			}
			if test.wantPipelineStops == 1 && !sameStrings(events, []string{"pipeline", "connection"}) {
				t.Fatalf("cleanup order = %#v, want pipeline then connection", events)
			}
			if test.wantState == RuntimeStopped && test.existing != nil {
				if got.CurrentTurnID != nil || got.CurrentPlaybackID != nil || got.LastErrorCode != nil {
					t.Fatalf("stopped snapshot retained active fields: %#v", got)
				}
			}
		})
	}
}

func TestLifecycleConcurrentStartCreatesOnePipeline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	pipeline := &fakePipeline{startEntered: entered, releaseStart: release}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "created"}, pipeline, &fakeConnection{})
	results := make(chan error, 2)

	go func() {
		_, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
		results <- err
	}()
	<-entered
	go func() {
		_, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1"})
		results <- err
	}()
	close(release)

	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	}
	if pipeline.startCalls != 1 {
		t.Fatalf("pipeline start calls = %d, want 1", pipeline.startCalls)
	}
}

func TestLifecycleStopFailureCanRetry(t *testing.T) {
	pipeline := &fakePipeline{stopErrors: []error{errPipeline, nil}}
	connection := &fakeConnection{}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "active"}, pipeline, connection)
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	failed, err := service.Stop(context.Background(), StopRealtimeCommand{SessionID: "session-1"})
	if !errors.Is(err, errPipeline) || failed.RuntimeState != RuntimeFailed {
		t.Fatalf("first Stop() = %#v, %v", failed, err)
	}
	recovered, err := service.Stop(context.Background(), StopRealtimeCommand{SessionID: "session-1"})
	if err != nil || recovered.RuntimeState != RuntimeStopped {
		t.Fatalf("retry Stop() = %#v, %v", recovered, err)
	}
}

func TestLifecycleStopCleanupSurvivesRequestCancellation(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	pipeline := &fakePipeline{
		stopHook:                 cancelRequest,
		requireActiveStopContext: true,
		requireStopDeadline:      true,
	}
	connection := &fakeConnection{requireActiveContext: true, requireDeadline: true}
	service := newTestLifecycleService(t, SessionSnapshot{SessionID: "session-1", Status: "active"}, pipeline, connection)
	if err := service.deps.Runtimes.Save(context.Background(), RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := service.Stop(requestContext, StopRealtimeCommand{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got.RuntimeState != RuntimeStopped || !pipeline.stopSucceeded || !connection.closeSucceeded {
		t.Fatalf("Stop() = %#v, pipeline stopped = %t, connection closed = %t", got, pipeline.stopSucceeded, connection.closeSucceeded)
	}
	stored, err := service.deps.Runtimes.Get(context.Background(), "session-1")
	if err != nil || stored.RuntimeState != RuntimeStopped {
		t.Fatalf("stored runtime = %#v, %v; want stopped", stored, err)
	}
}

func TestKeyedLockerReleasesIdleEntries(t *testing.T) {
	locker := newKeyedLocker()
	for index := 0; index < 100; index++ {
		unlock := locker.lock(fmt.Sprintf("session-%d", index))
		unlock()
	}

	locker.mu.Lock()
	entryCount := len(locker.locks)
	locker.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("idle lock entries = %d, want 0", entryCount)
	}
}

func newTestLifecycleService(t *testing.T, snapshot SessionSnapshot, pipeline *fakePipeline, connection *fakeConnection) *LifecycleService {
	t.Helper()
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: snapshot},
		Runtimes:    NewMemoryRuntimeRepository(),
		Pipelines:   pipeline,
		Connections: connection,
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}
	return service
}

func stringPointer(value string) *string {
	return &value
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

type fakeSessionReader struct {
	snapshot SessionSnapshot
	err      error
}

type scriptedRuntimeRepository struct {
	delegate   RuntimeRepository
	mu         sync.Mutex
	saveErrors []error
}

func (r *scriptedRuntimeRepository) Get(ctx context.Context, sessionID string) (RuntimeSnapshot, error) {
	return r.delegate.Get(ctx, sessionID)
}

func (r *scriptedRuntimeRepository) Save(ctx context.Context, snapshot RuntimeSnapshot) error {
	r.mu.Lock()
	var err error
	if len(r.saveErrors) > 0 {
		err = r.saveErrors[0]
		r.saveErrors = r.saveErrors[1:]
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.delegate.Save(ctx, snapshot)
}

func (f *fakeSessionReader) GetSession(_ context.Context, _ string) (SessionSnapshot, error) {
	if f.err != nil {
		return SessionSnapshot{}, f.err
	}
	return f.snapshot, nil
}

type fakePipeline struct {
	mu                       sync.Mutex
	startCalls               int
	stopCalls                int
	startErrors              []error
	stopError                error
	stopErrors               []error
	startEntered             chan struct{}
	releaseStart             <-chan struct{}
	startOnce                sync.Once
	startHook                func()
	stopHook                 func()
	requireActiveStopContext bool
	requireStopDeadline      bool
	stopSucceeded            bool
	events                   *[]string
}

type healthPipeline struct {
	*fakePipeline
	active bool
}

func (p *healthPipeline) PipelineActive(string) bool { return p.active }

func (f *fakePipeline) Start(_ context.Context, _ SessionSnapshot) error {
	f.mu.Lock()
	f.startCalls++
	var err error
	if len(f.startErrors) > 0 {
		err = f.startErrors[0]
		f.startErrors = f.startErrors[1:]
	}
	entered, release, hook, events := f.startEntered, f.releaseStart, f.startHook, f.events
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if events != nil {
		*events = append(*events, "pipeline")
	}
	if entered != nil {
		f.startOnce.Do(func() { close(entered) })
	}
	if release != nil {
		<-release
	}
	return err
}

func (f *fakePipeline) Stop(ctx context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	if f.stopHook != nil {
		f.stopHook()
	}
	if f.requireActiveStopContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.requireStopDeadline {
		if _, ok := ctx.Deadline(); !ok {
			return errNoDeadline
		}
	}
	f.stopSucceeded = true
	if f.events != nil {
		*f.events = append(*f.events, "pipeline")
	}
	if len(f.stopErrors) > 0 {
		err := f.stopErrors[0]
		f.stopErrors = f.stopErrors[1:]
		return err
	}
	return f.stopError
}

type fakeConnection struct {
	mu                   sync.Mutex
	closeCalls           int
	closeError           error
	requireActiveContext bool
	requireDeadline      bool
	closeSucceeded       bool
	events               *[]string
}

func (f *fakeConnection) Close(ctx context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	if f.requireActiveContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.requireDeadline {
		if _, ok := ctx.Deadline(); !ok {
			return errNoDeadline
		}
	}
	f.closeSucceeded = true
	if f.events != nil {
		*f.events = append(*f.events, "connection")
	}
	return f.closeError
}
