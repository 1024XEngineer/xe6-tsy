package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsumeRuntimeFailureTransitionsActiveSession(t *testing.T) {
	failedAt := time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)
	repository := &runtimeFailureRepository{
		fakeRepository: &fakeRepository{},
		snapshot: SessionSnapshot{
			SessionID: "vs_failure",
			AccountID: "acct_owner",
			Status:    StatusActive,
			StartedAt: timePointer(failedAt.Add(-time.Minute)),
		},
		transitionResult: VoiceSession{
			ID:        "vs_failure",
			AccountID: "acct_owner",
			Status:    StatusFailed,
			StartedAt: timePointer(failedAt.Add(-time.Minute)),
			EndedAt:   &failedAt,
		},
	}
	service := newRuntimeFailureTestService(t, repository)

	err := service.ConsumeRuntimeFailure(t.Context(), RuntimeFailure{
		SessionID:  "vs_failure",
		TraceID:    "trace_failure",
		ErrorCode:  "pipeline_unrecoverable",
		OccurredAt: failedAt,
	})
	if err != nil {
		t.Fatalf("ConsumeRuntimeFailure() error = %v", err)
	}
	if repository.getSessionCalls != 1 || repository.transitionCalls != 1 {
		t.Fatalf(
			"repository calls = get %d transition %d, want 1 each",
			repository.getSessionCalls,
			repository.transitionCalls,
		)
	}
	want := FailureTransitionParams{
		SessionID: "vs_failure",
		AccountID: "acct_owner",
		Expected:  StatusActive,
		FailedAt:  failedAt,
		ErrorCode: "pipeline_unrecoverable",
	}
	if repository.transitionParams != want {
		t.Fatalf("TransitionToFailed() params = %#v, want %#v", repository.transitionParams, want)
	}
}

func TestConsumeRuntimeFailureIsIdempotentAfterTerminalState(t *testing.T) {
	failedAt := time.Date(2026, time.July, 30, 9, 30, 0, 0, time.UTC)
	repository := &runtimeFailureRepository{
		fakeRepository: &fakeRepository{},
		snapshot: SessionSnapshot{
			SessionID: "vs_failure",
			AccountID: "acct_owner",
			Status:    StatusFailed,
			StartedAt: timePointer(failedAt.Add(-time.Minute)),
			EndedAt:   &failedAt,
		},
	}
	service := newRuntimeFailureTestService(t, repository)
	failure := RuntimeFailure{
		SessionID: "vs_failure", TraceID: "trace_failure",
		ErrorCode: "pipeline_unrecoverable", OccurredAt: failedAt,
	}

	if err := service.ConsumeRuntimeFailure(t.Context(), failure); err != nil {
		t.Fatalf("ConsumeRuntimeFailure() error = %v", err)
	}
	if repository.transitionCalls != 0 {
		t.Fatalf("TransitionToFailed() calls = %d, want 0", repository.transitionCalls)
	}
}

func TestConsumeRuntimeFailureRejectsNonActiveSession(t *testing.T) {
	failedAt := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	repository := &runtimeFailureRepository{
		fakeRepository: &fakeRepository{},
		snapshot: SessionSnapshot{
			SessionID: "vs_created",
			AccountID: "acct_owner",
			Status:    StatusCreated,
		},
	}
	service := newRuntimeFailureTestService(t, repository)

	err := service.ConsumeRuntimeFailure(t.Context(), RuntimeFailure{
		SessionID: "vs_created", TraceID: "trace_failure",
		ErrorCode: "pipeline_unrecoverable", OccurredAt: failedAt,
	})
	if !errors.Is(err, ErrSessionStateConflict) {
		t.Fatalf("ConsumeRuntimeFailure() error = %v, want ErrSessionStateConflict", err)
	}
	if repository.transitionCalls != 0 {
		t.Fatalf("TransitionToFailed() calls = %d, want 0", repository.transitionCalls)
	}
}

func TestConsumeRuntimeFailureValidatesInput(t *testing.T) {
	failedAt := time.Date(2026, time.July, 30, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		failure RuntimeFailure
	}{
		{name: "session", failure: RuntimeFailure{TraceID: "trace", ErrorCode: "code", OccurredAt: failedAt}},
		{name: "trace", failure: RuntimeFailure{SessionID: "vs_1", ErrorCode: "code", OccurredAt: failedAt}},
		{name: "error code", failure: RuntimeFailure{SessionID: "vs_1", TraceID: "trace", OccurredAt: failedAt}},
		{name: "occurred at", failure: RuntimeFailure{SessionID: "vs_1", TraceID: "trace", ErrorCode: "code"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &runtimeFailureRepository{fakeRepository: &fakeRepository{}}
			service := newRuntimeFailureTestService(t, repository)
			err := service.ConsumeRuntimeFailure(t.Context(), test.failure)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ConsumeRuntimeFailure() error = %v, want ErrInvalidRequest", err)
			}
			if repository.getSessionCalls != 0 {
				t.Fatalf("GetSession() calls = %d, want 0", repository.getSessionCalls)
			}
		})
	}
}

func TestConsumeRuntimeFailurePreservesRepositoryError(t *testing.T) {
	repository := &runtimeFailureRepository{
		fakeRepository: &fakeRepository{},
		getSessionErr:  errDependency,
	}
	service := newRuntimeFailureTestService(t, repository)

	err := service.ConsumeRuntimeFailure(t.Context(), RuntimeFailure{
		SessionID: "vs_failure", TraceID: "trace_failure",
		ErrorCode:  "pipeline_unrecoverable",
		OccurredAt: time.Date(2026, time.July, 30, 11, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, errDependency) {
		t.Fatalf("ConsumeRuntimeFailure() error = %v, want dependency cause", err)
	}
}

func TestConsumeRuntimeFailurePreservesTransitionError(t *testing.T) {
	repository := &runtimeFailureRepository{
		fakeRepository: &fakeRepository{},
		snapshot: SessionSnapshot{
			SessionID: "vs_failure",
			AccountID: "acct_owner",
			Status:    StatusActive,
		},
		transitionErr: errDependency,
	}
	service := newRuntimeFailureTestService(t, repository)

	err := service.ConsumeRuntimeFailure(t.Context(), RuntimeFailure{
		SessionID: "vs_failure", TraceID: "trace_failure",
		ErrorCode:  "pipeline_unrecoverable",
		OccurredAt: time.Date(2026, time.July, 30, 11, 30, 0, 0, time.UTC),
	})
	if !errors.Is(err, errDependency) {
		t.Fatalf("ConsumeRuntimeFailure() error = %v, want transition cause", err)
	}
}

type runtimeFailureRepository struct {
	*fakeRepository

	snapshot         SessionSnapshot
	getSessionErr    error
	getSessionCalls  int
	transitionResult VoiceSession
	transitionErr    error
	transitionCalls  int
	transitionParams FailureTransitionParams
}

func (r *runtimeFailureRepository) GetSession(
	_ context.Context,
	sessionID string,
) (SessionSnapshot, error) {
	r.getSessionCalls++
	if r.snapshot.SessionID != "" && r.snapshot.SessionID != sessionID {
		return SessionSnapshot{}, ErrVoiceSessionNotFound
	}
	return r.snapshot, r.getSessionErr
}

func (r *runtimeFailureRepository) TransitionToFailed(
	_ context.Context,
	params FailureTransitionParams,
) (VoiceSession, error) {
	r.transitionCalls++
	r.transitionParams = params
	return r.transitionResult, r.transitionErr
}

func newRuntimeFailureTestService(
	t *testing.T,
	repository Repository,
) *Service {
	t.Helper()
	service, err := NewService(Dependencies{
		Repository:        repository,
		LanguageConfigs:   &fakeLanguageConfigReader{},
		WebRTCConnections: &fakeWebRTCConnectionReader{},
		Realtime:          &fakeRealtimeLifecycle{},
		IDs:               &fakeIDGenerator{id: "unused"},
		Clock:             &fakeClock{},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func timePointer(value time.Time) *time.Time {
	return &value
}
