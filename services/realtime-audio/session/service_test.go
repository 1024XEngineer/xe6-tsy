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
	errProvider   = errors.New("provider unavailable")
	errPipeline   = errors.New("pipeline cleanup failed")
	errConnection = errors.New("connection cleanup failed")
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

type fakeSessionReader struct {
	snapshot SessionSnapshot
	err      error
}

func (f *fakeSessionReader) GetSession(_ context.Context, _ string) (SessionSnapshot, error) {
	if f.err != nil {
		return SessionSnapshot{}, f.err
	}
	return f.snapshot, nil
}

type fakePipeline struct {
	mu          sync.Mutex
	startCalls  int
	stopCalls   int
	startErrors []error
	stopError   error
}

func (f *fakePipeline) Start(_ context.Context, _ SessionSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if len(f.startErrors) == 0 {
		return nil
	}
	err := f.startErrors[0]
	f.startErrors = f.startErrors[1:]
	return err
}

func (f *fakePipeline) Stop(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	return f.stopError
}

type fakeConnection struct {
	mu         sync.Mutex
	closeCalls int
	closeError error
}

func (f *fakeConnection) Close(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeError
}

func (f *fakePipeline) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fmt.Sprintf("starts=%d stops=%d", f.startCalls, f.stopCalls)
}
