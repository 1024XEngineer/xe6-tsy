package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLifecycleActivatesPreparedPipelineAfterListeningSave(t *testing.T) {
	runtimes := NewMemoryRuntimeRepository()
	pipeline := &activatingPipeline{fakePipeline: &fakePipeline{}, runtimes: runtimes}
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: SessionSnapshot{SessionID: "session-1", AccountID: "account-1", Status: "created"}},
		Runtimes:    runtimes,
		Pipelines:   pipeline,
		Connections: &fakeConnection{},
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}
	if _, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1", OperationID: "operation-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !pipeline.activatedAfterListening {
		t.Fatal("pipeline activated before RuntimeListening was saved")
	}
	if pipeline.traceID != "trace-1" {
		t.Fatalf("pipeline trace id = %q, want trace-1", pipeline.traceID)
	}
	if pipeline.operationID != "operation-1" {
		t.Fatalf("pipeline operation id = %q, want operation-1", pipeline.operationID)
	}
	if err := pipeline.Activate(context.Background(), "session-1", "operation-2"); !errors.Is(err, ErrRuntimeOperationConflict) {
		t.Fatalf("foreign Activate() error = %v, want ErrRuntimeOperationConflict", err)
	}
}

func TestLifecycleCompensatesActivationFailure(t *testing.T) {
	runtimes := NewMemoryRuntimeRepository()
	activateErr := errors.New("activate failed")
	pipeline := &activatingPipeline{fakePipeline: &fakePipeline{}, runtimes: runtimes, activateErr: activateErr}
	service, err := NewLifecycleService(Dependencies{
		Sessions:    &fakeSessionReader{snapshot: SessionSnapshot{SessionID: "session-1", AccountID: "account-1", Status: "created"}},
		Runtimes:    runtimes,
		Pipelines:   pipeline,
		Connections: &fakeConnection{},
		Now:         func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewLifecycleService() error = %v", err)
	}
	got, err := service.Start(context.Background(), StartRealtimeCommand{SessionID: "session-1", OperationID: "operation-1", TraceID: "trace-1"})
	if !errors.Is(err, activateErr) {
		t.Fatalf("Start() error = %v, want activation error", err)
	}
	if got.RuntimeState != RuntimeFailed || pipeline.stopCalls != 1 {
		t.Fatalf("Start() = %#v, pipeline stop calls = %d", got, pipeline.stopCalls)
	}
	stored, err := runtimes.Get(context.Background(), "session-1")
	if err != nil || stored.RuntimeState != RuntimeFailed {
		t.Fatalf("stored runtime = %#v, %v", stored, err)
	}
}

type activatingPipeline struct {
	*fakePipeline
	runtimes                RuntimeRepository
	activatedAfterListening bool
	traceID                 string
	operationID             string
	activateErr             error
}

func (p *activatingPipeline) Start(ctx context.Context, snapshot SessionSnapshot) error {
	p.traceID = snapshot.TraceID
	p.operationID = snapshot.StartOperationID
	return p.fakePipeline.Start(ctx, snapshot)
}

func (p *activatingPipeline) Activate(ctx context.Context, sessionID string, operationID string) error {
	snapshot, err := p.runtimes.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if snapshot.StartOperationID != operationID {
		return ErrRuntimeOperationConflict
	}
	p.activatedAfterListening = snapshot.RuntimeState == RuntimeListening
	return p.activateErr
}
