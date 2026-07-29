package sessions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewServiceDependencies(t *testing.T) {
	valid := Dependencies{
		Repository:        &fakeRepository{},
		LanguageConfigs:   &fakeLanguageConfigReader{},
		WebRTCConnections: &fakeWebRTCConnectionReader{},
		Realtime:          &fakeRealtimeLifecycle{},
		IDs:               &fakeIDGenerator{id: "vs_1"},
		Clock:             &fakeClock{now: time.Now()},
	}
	tests := []struct {
		name        string
		edit        func(*Dependencies)
		wantErr     bool
		wantContext string
	}{
		{name: "valid dependencies"},
		{name: "missing repository", edit: func(deps *Dependencies) { deps.Repository = nil }, wantErr: true, wantContext: "repository"},
		{name: "missing language configs", edit: func(deps *Dependencies) { deps.LanguageConfigs = nil }, wantErr: true, wantContext: "language config"},
		{name: "missing WebRTC connections", edit: func(deps *Dependencies) { deps.WebRTCConnections = nil }, wantErr: true, wantContext: "WebRTC"},
		{name: "missing realtime", edit: func(deps *Dependencies) { deps.Realtime = nil }, wantErr: true, wantContext: "realtime"},
		{name: "missing ID generator", edit: func(deps *Dependencies) { deps.IDs = nil }, wantErr: true, wantContext: "ID generator"},
		{name: "missing clock", edit: func(deps *Dependencies) { deps.Clock = nil }, wantErr: true, wantContext: "clock"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := valid
			if test.edit != nil {
				test.edit(&deps)
			}
			_, err := NewService(deps)
			if (err != nil) != test.wantErr {
				t.Fatalf("NewService() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantErr && !errors.Is(err, ErrInvalidDependency) {
				t.Fatalf("NewService() error = %v, want ErrInvalidDependency", err)
			}
			if test.wantContext != "" && !strings.Contains(err.Error(), test.wantContext) {
				t.Fatalf("NewService() error = %v, want context %q", err, test.wantContext)
			}
		})
	}
}

func TestNewServiceDefaultsStartInfrastructure(t *testing.T) {
	service, err := NewService(Dependencies{
		Repository:        &fakeRepository{},
		LanguageConfigs:   &fakeLanguageConfigReader{},
		WebRTCConnections: &fakeWebRTCConnectionReader{},
		Realtime:          &fakeRealtimeLifecycle{},
		IDs:               &fakeIDGenerator{id: "vs_1"},
		Clock:             &fakeClock{now: time.Now()},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if service.deps.Logger == nil {
		t.Fatal("Logger is nil")
	}
	if service.deps.CompensationTimeout != defaultCompensationTimeout {
		t.Fatalf("CompensationTimeout = %v, want %v",
			service.deps.CompensationTimeout, defaultCompensationTimeout)
	}
	if service.deps.StartReconciliationTimeout != defaultStartReconciliationTimeout {
		t.Fatalf("StartReconciliationTimeout = %v, want %v",
			service.deps.StartReconciliationTimeout,
			defaultStartReconciliationTimeout)
	}
	if service.locks.locks == nil {
		t.Fatal("keyed locker is not initialized")
	}
}

func TestServiceCreatePassesCanonicalParameters(t *testing.T) {
	repositoryResult := VoiceSession{
		ID:        "vs_repository",
		AccountID: "acct_1",
		Status:    StatusCreated,
		CreatedAt: time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
	}
	repository := &fakeRepository{createResult: repositoryResult}
	service, ids, clock := newCreateTestService(t, repository)

	got, err := service.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.ID != repositoryResult.ID {
		t.Fatalf("Create() ID = %q, want repository result %q", got.ID, repositoryResult.ID)
	}
	if len(repository.createParams) != 1 {
		t.Fatalf("repository Create calls = %d, want 1", len(repository.createParams))
	}
	params := repository.createParams[0]
	if params.ID != ids.id ||
		params.AccountID != "acct_1" ||
		params.IdempotencyKey != "create_1" ||
		params.RequestHash != "hash_1" {
		t.Fatalf("Create params = %#v", params)
	}
	if params.AudioConfig != DefaultAudioConfig() {
		t.Fatalf("AudioConfig = %#v, want default", params.AudioConfig)
	}
	wantCapabilities := validCapabilities()
	if params.Capabilities != wantCapabilities {
		t.Fatalf("Capabilities = %#v, want %#v", params.Capabilities, wantCapabilities)
	}
	if params.CreatedAt.Location() != time.UTC || !params.CreatedAt.Equal(clock.now) {
		t.Fatalf("CreatedAt = %v, want %v converted to UTC", params.CreatedAt, clock.now)
	}
}

func TestServiceCreateUsesCustomAudioConfig(t *testing.T) {
	custom := DefaultAudioConfig()
	custom.EchoCancellation = false
	custom.NoiseSuppression = false
	repository := &fakeRepository{createResult: VoiceSession{ID: "vs_repository"}}
	service, _, _ := newCreateTestService(t, repository)
	input := validCreateInput()
	input.AudioConfig = &custom

	if _, err := service.Create(context.Background(), input); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := repository.createParams[0].AudioConfig; got != custom {
		t.Fatalf("AudioConfig = %#v, want %#v", got, custom)
	}
}

func TestServiceCreateReturnsRepositoryReplay(t *testing.T) {
	original := VoiceSession{
		ID:           "vs_original",
		AccountID:    "acct_1",
		Status:       StatusCreated,
		AudioConfig:  []byte(`{"codec":"opus"}`),
		Capabilities: []byte(`{"webrtc":true}`),
		CreatedAt:    time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC),
	}
	repository := &fakeRepository{createResult: original, createReplayed: true}
	service, _, _ := newCreateTestService(t, repository)

	got, err := service.Create(context.Background(), validCreateInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("Create() = %#v, want original replay %#v", got, original)
	}
}

func TestServiceCreateValidatesInput(t *testing.T) {
	tests := []struct {
		name           string
		edit           func(*CreateInput)
		editID         func(*fakeIDGenerator)
		want           error
		wantIDCalls    int
		wantClockCalls int
	}{
		{name: "missing account", edit: func(input *CreateInput) { input.AccountID = "" }, want: ErrUnauthorized},
		{name: "missing idempotency key", edit: func(input *CreateInput) { input.IdempotencyKey = "" }, want: ErrInvalidRequest},
		{name: "missing request hash", edit: func(input *CreateInput) { input.RequestHash = "" }, want: ErrInvalidRequest},
		{name: "unsupported codec", edit: editAudio(func(config *AudioConfig) { config.Codec = "pcm" }), want: ErrUnsupportedAudio},
		{name: "unsupported sample rate", edit: editAudio(func(config *AudioConfig) { config.SampleRateHz = 16000 }), want: ErrUnsupportedAudio},
		{name: "unsupported channels", edit: editAudio(func(config *AudioConfig) { config.Channels = 2 }), want: ErrUnsupportedAudio},
		{name: "missing WebRTC", edit: editCapabilities(func(value *Capabilities) { value.WebRTC = false }), want: ErrInvalidRequest},
		{name: "missing data channel", edit: editCapabilities(func(value *Capabilities) { value.DataChannel = false }), want: ErrInvalidRequest},
		{name: "missing microphone", edit: editCapabilities(func(value *Capabilities) { value.Microphone = false }), want: ErrInvalidRequest},
		{name: "missing speaker", edit: editCapabilities(func(value *Capabilities) { value.Speaker = false }), want: ErrInvalidRequest},
		{name: "missing diarization", edit: editCapabilities(func(value *Capabilities) { value.SpeakerDiarization = false }), want: ErrInvalidRequest},
		{
			name: "empty generated ID", editID: func(ids *fakeIDGenerator) { ids.id = "" },
			want: ErrInvalidDependency, wantIDCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service, ids, clock := newCreateTestService(t, repository)
			input := validCreateInput()
			if test.edit != nil {
				test.edit(&input)
			}
			if test.editID != nil {
				test.editID(ids)
			}

			_, err := service.Create(context.Background(), input)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
			if len(repository.createParams) != 0 {
				t.Fatalf("repository Create calls = %d, want 0", len(repository.createParams))
			}
			if ids.calls != test.wantIDCalls {
				t.Fatalf("ID generator calls = %d, want %d", ids.calls, test.wantIDCalls)
			}
			if clock.calls != test.wantClockCalls {
				t.Fatalf("Clock calls = %d, want %d", clock.calls, test.wantClockCalls)
			}
		})
	}
}

func TestServiceCreateReturnsContextAndRepositoryErrors(t *testing.T) {
	tests := []struct {
		name       string
		ctx        func() context.Context
		repository func() *fakeRepository
		want       error
		wantCalls  int
	}{
		{
			name: "context already cancelled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			repository: func() *fakeRepository { return &fakeRepository{} },
			want:       context.Canceled,
		},
		{
			name:       "repository error",
			ctx:        context.Background,
			repository: func() *fakeRepository { return &fakeRepository{createErr: errDependency} },
			want:       errDependency,
			wantCalls:  1,
		},
		{
			name:       "repository not implemented",
			ctx:        context.Background,
			repository: func() *fakeRepository { return &fakeRepository{createErr: ErrNotImplemented} },
			want:       ErrNotImplemented,
			wantCalls:  1,
		},
		{
			name:       "repository idempotency conflict",
			ctx:        context.Background,
			repository: func() *fakeRepository { return &fakeRepository{createErr: ErrIdempotencyKeyConflict} },
			want:       ErrIdempotencyKeyConflict,
			wantCalls:  1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := test.repository()
			service, ids, clock := newCreateTestService(t, repository)
			got, err := service.Create(test.ctx(), validCreateInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(got, VoiceSession{}) {
				t.Fatalf("Create() session = %#v, want zero session on error", got)
			}
			if len(repository.createParams) != test.wantCalls {
				t.Fatalf("repository Create calls = %d, want %d", len(repository.createParams), test.wantCalls)
			}
			if test.wantCalls == 0 && (ids.calls != 0 || clock.calls != 0) {
				t.Fatalf("side effects before cancellation: ID calls %d, Clock calls %d", ids.calls, clock.calls)
			}
		})
	}
}

func TestServiceCreateRejectsZeroClock(t *testing.T) {
	repository := &fakeRepository{}
	service, ids, clock := newCreateTestService(t, repository)
	clock.now = time.Time{}

	got, err := service.Create(context.Background(), validCreateInput())
	if !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("Create() error = %v, want ErrInvalidDependency", err)
	}
	if !reflect.DeepEqual(got, VoiceSession{}) {
		t.Fatalf("Create() session = %#v, want zero session", got)
	}
	if ids.calls != 1 || clock.calls != 1 || len(repository.createParams) != 0 {
		t.Fatalf("calls = IDs %d, Clock %d, Repository %d; want 1, 1, 0",
			ids.calls, clock.calls, len(repository.createParams))
	}
}

func TestServiceCreateReturnsCancellationObservedByRepository(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeRepository{
		createErr: context.Canceled,
		createHook: func(context.Context) {
			cancel()
		},
	}
	service, _, _ := newCreateTestService(t, repository)

	_, err := service.Create(ctx, validCreateInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	if len(repository.createParams) != 1 {
		t.Fatalf("repository Create calls = %d, want 1", len(repository.createParams))
	}
}

func validCreateInput() CreateInput {
	return CreateInput{
		AccountID:      "acct_1",
		Capabilities:   validCapabilities(),
		IdempotencyKey: "create_1",
		RequestHash:    "hash_1",
	}
}

func editAudio(edit func(*AudioConfig)) func(*CreateInput) {
	return func(input *CreateInput) {
		config := DefaultAudioConfig()
		edit(&config)
		input.AudioConfig = &config
	}
}

func editCapabilities(edit func(*Capabilities)) func(*CreateInput) {
	return func(input *CreateInput) {
		edit(&input.Capabilities)
	}
}
