package sessions

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceGetDetailCombinesOwnedSessionAndRuntime(t *testing.T) {
	turnID := "turn_1"
	playbackID := "play_1"
	session := queryTestSession(StatusCreated)
	runtime := RuntimeSnapshot{
		SessionID:         session.ID,
		RuntimeState:      RuntimeFailed,
		CurrentTurnID:     &turnID,
		CurrentPlaybackID: &playbackID,
		UpdatedAt:         time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC),
	}
	repository := &fakeRepository{getOwnedResult: session}
	realtime := &fakeRealtimeLifecycle{getResult: runtime}
	service := newQueryTestService(t, repository, realtime)

	got, err := service.GetDetail(context.Background(), DetailInput{
		AccountID: session.AccountID,
		SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("GetDetail() error = %v", err)
	}
	if !reflect.DeepEqual(got.VoiceSession, session) ||
		got.RuntimeState != runtime.RuntimeState ||
		got.CurrentTurnID != runtime.CurrentTurnID ||
		got.CurrentPlaybackID != runtime.CurrentPlaybackID ||
		!got.Retryable ||
		!got.RuntimeUpdatedAt.Equal(runtime.UpdatedAt) {
		t.Fatalf("GetDetail() = %#v", got)
	}
	if repository.getOwnedAccountID != session.AccountID ||
		repository.getOwnedSessionID != session.ID ||
		realtime.getSessionID != session.ID {
		t.Fatalf("dependency inputs = account %q, repository session %q, runtime session %q",
			repository.getOwnedAccountID, repository.getOwnedSessionID, realtime.getSessionID)
	}
}

func TestServiceGetStateReturnsPollingProjection(t *testing.T) {
	lastError := "translation_timeout"
	session := queryTestSession(StatusActive)
	runtime := RuntimeSnapshot{
		SessionID:     session.ID,
		RuntimeState:  RuntimePlaying,
		LastErrorCode: &lastError,
		UpdatedAt:     time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC),
	}
	repository := &fakeRepository{getOwnedResult: session}
	realtime := &fakeRealtimeLifecycle{getResult: runtime}
	service := newQueryTestService(t, repository, realtime)

	got, err := service.GetState(context.Background(), DetailInput{
		AccountID: session.AccountID,
		SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	want := StateSnapshot{
		SessionID:        session.ID,
		Status:           StatusActive,
		RuntimeState:     RuntimePlaying,
		LastErrorCode:    &lastError,
		Retryable:        false,
		RuntimeUpdatedAt: runtime.UpdatedAt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetState() = %#v, want %#v", got, want)
	}
}

func TestServiceDetailReadsValidateBeforeDependencies(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		ctx   context.Context
		input DetailInput
		want  error
	}{
		{name: "cancelled context", ctx: cancelled, input: DetailInput{AccountID: "acct_1", SessionID: "vs_1"}, want: context.Canceled},
		{name: "missing account", ctx: context.Background(), input: DetailInput{SessionID: "vs_1"}, want: ErrUnauthorized},
		{name: "missing session", ctx: context.Background(), input: DetailInput{AccountID: "acct_1"}, want: ErrInvalidRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			realtime := &fakeRealtimeLifecycle{}
			service := newQueryTestService(t, repository, realtime)
			_, err := service.GetDetail(test.ctx, test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf("GetDetail() error = %v, want %v", err, test.want)
			}
			if repository.getOwnedCalls != 0 || realtime.getCalls != 0 {
				t.Fatalf("dependency calls = repository %d, realtime %d; want 0, 0",
					repository.getOwnedCalls, realtime.getCalls)
			}
		})
	}
}

func TestServiceDetailStopsAfterOwnedReadFailure(t *testing.T) {
	repository := &fakeRepository{getOwnedErr: ErrVoiceSessionNotFound}
	realtime := &fakeRealtimeLifecycle{}
	service := newQueryTestService(t, repository, realtime)

	_, err := service.GetDetail(context.Background(), DetailInput{
		AccountID: "acct_1", SessionID: "vs_1",
	})
	if !errors.Is(err, ErrVoiceSessionNotFound) {
		t.Fatalf("GetDetail() error = %v, want ErrVoiceSessionNotFound", err)
	}
	if repository.getOwnedCalls != 1 || realtime.getCalls != 0 {
		t.Fatalf("dependency calls = repository %d, realtime %d; want 1, 0",
			repository.getOwnedCalls, realtime.getCalls)
	}
}

func TestServiceDetailMapsRuntimeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "runtime unavailable", err: errDependency, want: ErrRuntimeUnavailable},
		{name: "runtime not implemented", err: ErrNotImplemented, want: ErrNotImplemented},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := queryTestSession(StatusActive)
			repository := &fakeRepository{getOwnedResult: session}
			realtime := &fakeRealtimeLifecycle{getErr: test.err}
			service := newQueryTestService(t, repository, realtime)

			_, err := service.GetDetail(context.Background(), DetailInput{
				AccountID: session.AccountID, SessionID: session.ID,
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("GetDetail() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServiceDetailReturnsRuntimeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := queryTestSession(StatusActive)
	repository := &fakeRepository{getOwnedResult: session}
	realtime := &fakeRealtimeLifecycle{
		getErr:  context.Canceled,
		getHook: cancel,
	}
	service := newQueryTestService(t, repository, realtime)

	_, err := service.GetDetail(ctx, DetailInput{
		AccountID: session.AccountID, SessionID: session.ID,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetDetail() error = %v, want context.Canceled", err)
	}
}

func TestServiceDetailRejectsInvalidRuntimeSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot RuntimeSnapshot
	}{
		{name: "session mismatch", snapshot: RuntimeSnapshot{SessionID: "other", RuntimeState: RuntimeListening, UpdatedAt: now}},
		{name: "unknown state", snapshot: RuntimeSnapshot{SessionID: "vs_1", RuntimeState: "unknown", UpdatedAt: now}},
		{name: "zero timestamp", snapshot: RuntimeSnapshot{SessionID: "vs_1", RuntimeState: RuntimeListening}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := queryTestSession(StatusActive)
			repository := &fakeRepository{getOwnedResult: session}
			realtime := &fakeRealtimeLifecycle{getResult: test.snapshot}
			service := newQueryTestService(t, repository, realtime)

			_, err := service.GetDetail(context.Background(), DetailInput{
				AccountID: session.AccountID, SessionID: session.ID,
			})
			if !errors.Is(err, ErrRuntimeUnavailable) {
				t.Fatalf("GetDetail() error = %v, want ErrRuntimeUnavailable", err)
			}
		})
	}
}

func TestServiceListUsesPersistentRepositoryOnly(t *testing.T) {
	status := StatusEnded
	nextCursor := "next_1"
	page := ListPage{
		Sessions: []VoiceSessionListItem{{
			ID: "vs_1", AccountID: "acct_1", Status: StatusEnded,
		}},
		NextCursor: &nextCursor,
	}
	repository := &fakeRepository{listResult: page}
	realtime := &fakeRealtimeLifecycle{}
	service := newQueryTestService(t, repository, realtime)

	got, err := service.List(context.Background(), ListInput{
		AccountID: "acct_1",
		Status:    &status,
		Cursor:    "cursor_1",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, page) {
		t.Fatalf("List() = %#v, want %#v", got, page)
	}
	if len(repository.listFilters) != 1 {
		t.Fatalf("repository List calls = %d, want 1", len(repository.listFilters))
	}
	wantFilter := ListFilter{
		AccountID: "acct_1",
		Status:    &status,
		Cursor:    "cursor_1",
		Limit:     defaultListLimit,
	}
	if !reflect.DeepEqual(repository.listFilters[0], wantFilter) {
		t.Fatalf("ListFilter = %#v, want %#v", repository.listFilters[0], wantFilter)
	}
	if realtime.getCalls != 0 {
		t.Fatalf("runtime calls = %d, want 0", realtime.getCalls)
	}
}

func TestServiceListValidatesBeforeDependencies(t *testing.T) {
	unknown := Status("unknown")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name  string
		ctx   context.Context
		input ListInput
		want  error
	}{
		{name: "cancelled context", ctx: cancelled, input: ListInput{AccountID: "acct_1"}, want: context.Canceled},
		{name: "missing account", ctx: context.Background(), input: ListInput{}, want: ErrUnauthorized},
		{name: "unknown status", ctx: context.Background(), input: ListInput{AccountID: "acct_1", Status: &unknown}, want: ErrInvalidRequest},
		{name: "negative limit", ctx: context.Background(), input: ListInput{AccountID: "acct_1", Limit: -1}, want: ErrInvalidRequest},
		{name: "limit too large", ctx: context.Background(), input: ListInput{AccountID: "acct_1", Limit: 101}, want: ErrInvalidRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			realtime := &fakeRealtimeLifecycle{}
			service := newQueryTestService(t, repository, realtime)
			_, err := service.List(test.ctx, test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf("List() error = %v, want %v", err, test.want)
			}
			if len(repository.listFilters) != 0 || realtime.getCalls != 0 {
				t.Fatalf("dependency calls = repository %d, realtime %d; want 0, 0",
					len(repository.listFilters), realtime.getCalls)
			}
		})
	}
}

func TestServiceListPreservesRepositoryError(t *testing.T) {
	repository := &fakeRepository{listErr: errDependency}
	realtime := &fakeRealtimeLifecycle{}
	service := newQueryTestService(t, repository, realtime)

	_, err := service.List(context.Background(), ListInput{AccountID: "acct_1"})
	if !errors.Is(err, errDependency) {
		t.Fatalf("List() error = %v, want repository error", err)
	}
	if realtime.getCalls != 0 {
		t.Fatalf("runtime calls = %d, want 0", realtime.getCalls)
	}
}

func newQueryTestService(
	t *testing.T,
	repository Repository,
	realtime RealtimeLifecycle,
) *Service {
	t.Helper()
	service, err := NewService(Dependencies{
		Repository: repository,
		Realtime:   realtime,
		IDs:        &fakeIDGenerator{id: "vs_generated"},
		Clock:      &fakeClock{now: time.Now()},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func queryTestSession(status Status) VoiceSession {
	return VoiceSession{
		ID:           "vs_1",
		AccountID:    "acct_1",
		Status:       status,
		AudioConfig:  []byte(`{"codec":"opus"}`),
		Capabilities: []byte(`{"webrtc":true}`),
		CreatedAt:    time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
	}
}
