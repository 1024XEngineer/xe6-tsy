package sessions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceStartRunsPrerequisitesBeforeTransition(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionResult = activeStartSession(fixture.repository.session, fixture.clock.now)

	got, err := fixture.service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive || got.StartedAt == nil {
		t.Fatalf("Start() = %#v", got)
	}
	if fixture.languages.calls != 1 || fixture.languages.sessionID != "vs_1" {
		t.Fatalf("language calls = %d for %q", fixture.languages.calls, fixture.languages.sessionID)
	}
	if fixture.connections.calls != 1 || fixture.connections.sessionID != "vs_1" {
		t.Fatalf("WebRTC calls = %d for %q", fixture.connections.calls, fixture.connections.sessionID)
	}
	if fixture.realtime.startCalls != 1 || fixture.realtime.stopCalls != 0 {
		t.Fatalf("realtime calls = start %d, stop %d; want 1, 0",
			fixture.realtime.startCalls, fixture.realtime.stopCalls)
	}
	if len(fixture.repository.transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(fixture.repository.transitions))
	}
	if fixture.repository.beginCalls != 1 {
		t.Fatalf("BeginStartOperation calls = %d, want 1", fixture.repository.beginCalls)
	}
	begin := fixture.repository.beginParams[0]
	if begin.OperationID != "op_1" ||
		begin.SessionID != "vs_1" ||
		begin.AccountID != "acct_1" ||
		begin.IdempotencyKey != "start_1" ||
		begin.RequestHash != "hash_1" {
		t.Fatalf("BeginStartOperationParams = %#v", begin)
	}
	command := fixture.realtime.startCommand
	if command.SessionID != "vs_1" ||
		command.OperationID != "op_1" ||
		command.TraceID != "req_1" ||
		command.StartedBy != "acct_1" {
		t.Fatalf("StartRealtimeCommand = %#v", command)
	}
	params := fixture.repository.transitions[0]
	if params.AccountID != "acct_1" ||
		params.OperationID != "op_1" ||
		params.Expected != StatusCreated ||
		params.IdempotencyKey != "start_1" ||
		params.RequestHash != "hash_1" ||
		!params.StartedAt.Equal(fixture.clock.now) ||
		params.StartedAt.Location() != time.UTC {
		t.Fatalf("StartTransitionParams = %#v", params)
	}
}

func TestServiceStartAllowsMergedActorAndPreservesOwner(t *testing.T) {
	fixture := newMergedStartFixture(t, StatusCreated)
	fixture.repository.transitionResult = activeStartSession(
		fixture.repository.session,
		fixture.clock.now,
	)
	input := mergedStartInput()

	got, err := fixture.service.Start(t.Context(), input)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive || got.AccountID != "acct_anonymous" {
		t.Fatalf("Start() = %#v", got)
	}
	if fixture.repository.getAccountID != "acct_registered" ||
		fixture.repository.getOperationAccountID != "acct_registered" {
		t.Fatalf(
			"repository actors = GetOwned %q, GetStartOperation %q",
			fixture.repository.getAccountID,
			fixture.repository.getOperationAccountID,
		)
	}
	if fixture.repository.beginCalls != 1 ||
		fixture.repository.beginParams[0].AccountID != "acct_registered" {
		t.Fatalf(
			"BeginStartOperation calls = %d, params = %#v",
			fixture.repository.beginCalls,
			fixture.repository.beginParams,
		)
	}
	if fixture.repository.operation == nil ||
		fixture.repository.operation.AccountID != "acct_anonymous" {
		t.Fatalf("persisted operation = %#v", fixture.repository.operation)
	}
	if fixture.realtime.startCalls != 1 ||
		fixture.realtime.startCommand.OperationID != "op_1" ||
		fixture.realtime.startCommand.StartedBy != "acct_registered" {
		t.Fatalf(
			"realtime calls = %d, command = %#v",
			fixture.realtime.startCalls,
			fixture.realtime.startCommand,
		)
	}
	if len(fixture.repository.transitions) != 1 ||
		fixture.repository.transitions[0].AccountID != "acct_registered" {
		t.Fatalf("transitions = %#v", fixture.repository.transitions)
	}
}

func TestServiceStartMergedActorContinuesOwnerPendingOperation(t *testing.T) {
	fixture := newMergedStartFixture(t, StatusCreated)
	fixture.repository.operation = &StartOperation{
		ID:             "op_1",
		SessionID:      "vs_1",
		AccountID:      "acct_anonymous",
		IdempotencyKey: "start_1",
		RequestHash:    "hash_1",
		Status:         StartOperationPending,
		CreatedAt:      fixture.clock.now,
		UpdatedAt:      fixture.clock.now,
	}
	fixture.repository.transitionResult = activeStartSession(
		fixture.repository.session,
		fixture.clock.now,
	)

	got, err := fixture.service.Start(t.Context(), mergedStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive || got.AccountID != "acct_anonymous" {
		t.Fatalf("Start() = %#v", got)
	}
	if fixture.repository.beginCalls != 0 ||
		fixture.realtime.startCalls != 1 ||
		len(fixture.repository.transitions) != 1 {
		t.Fatalf(
			"calls = begin %d, realtime %d, transition %d; want 0, 1, 1",
			fixture.repository.beginCalls,
			fixture.realtime.startCalls,
			len(fixture.repository.transitions),
		)
	}
}

func TestServiceStartMergedActorReplaysCompletedOwnerOperation(t *testing.T) {
	fixture := newMergedStartFixture(t, StatusActive)
	startedAt := fixture.clock.now.Add(-time.Minute)
	fixture.repository.session.StartedAt = &startedAt
	fixture.repository.operation = completedStartOperation(
		fixture.repository.session,
		startedAt,
	)

	got, err := fixture.service.Start(t.Context(), mergedStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive || got.AccountID != "acct_anonymous" {
		t.Fatalf("Start() = %#v", got)
	}
	if fixture.repository.beginCalls != 1 ||
		fixture.repository.beginParams[0].AccountID != "acct_registered" {
		t.Fatalf(
			"BeginStartOperation calls = %d, params = %#v",
			fixture.repository.beginCalls,
			fixture.repository.beginParams,
		)
	}
	assertNoStartPrerequisites(t, fixture)
	if len(fixture.repository.transitions) != 0 {
		t.Fatalf("transitions = %d, want 0", len(fixture.repository.transitions))
	}
}

func TestServiceStartMergedActorRejectsWrongOperationOwner(t *testing.T) {
	for _, operationOwner := range []string{"acct_registered", "acct_other"} {
		t.Run(operationOwner, func(t *testing.T) {
			fixture := newMergedStartFixture(t, StatusCreated)
			fixture.repository.operation = &StartOperation{
				ID:             "op_1",
				SessionID:      "vs_1",
				AccountID:      operationOwner,
				IdempotencyKey: "start_1",
				RequestHash:    "hash_1",
				Status:         StartOperationPending,
				CreatedAt:      fixture.clock.now,
				UpdatedAt:      fixture.clock.now,
			}

			_, err := fixture.service.Start(t.Context(), mergedStartInput())
			if !errors.Is(err, ErrConcurrentTransition) {
				t.Fatalf("Start() error = %v, want ErrConcurrentTransition", err)
			}
			assertNoStartPrerequisites(t, fixture)
			if fixture.repository.beginCalls != 0 {
				t.Fatalf("BeginStartOperation calls = %d, want 0", fixture.repository.beginCalls)
			}
		})
	}
}

func TestServiceStartMergedActorStillRequiresRepositoryAuthorization(t *testing.T) {
	fixture := newMergedStartFixture(t, StatusCreated)
	input := mergedStartInput()
	input.AccountID = "acct_unrelated"
	input.StartedBy = "acct_unrelated"

	_, err := fixture.service.Start(t.Context(), input)
	if !errors.Is(err, ErrVoiceSessionNotFound) {
		t.Fatalf("Start() error = %v, want ErrVoiceSessionNotFound", err)
	}
	assertNoStartPrerequisites(t, fixture)
}

func TestServiceStartRejectsSessionWithoutOwner(t *testing.T) {
	for _, status := range []Status{StatusCreated, StatusActive} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newMergedStartFixture(t, status)
			fixture.repository.session.AccountID = ""

			_, err := fixture.service.Start(t.Context(), mergedStartInput())
			if !errors.Is(err, ErrInvalidDependency) {
				t.Fatalf("Start() error = %v, want ErrInvalidDependency", err)
			}
			assertNoStartPrerequisites(t, fixture)
			if fixture.repository.beginCalls != 0 {
				t.Fatalf("BeginStartOperation calls = %d, want 0", fixture.repository.beginCalls)
			}
		})
	}
}

func TestServiceStartDefaultsStartedByToAccount(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionResult = activeStartSession(fixture.repository.session, fixture.clock.now)
	input := validStartInput()
	input.StartedBy = ""

	if _, err := fixture.service.Start(context.Background(), input); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if fixture.realtime.startCommand.StartedBy != input.AccountID {
		t.Fatalf("StartedBy = %q, want %q", fixture.realtime.startCommand.StartedBy, input.AccountID)
	}
}

func TestServiceStartValidatesBeforeDependencies(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		edit func(*StartInput)
		want error
	}{
		{name: "cancelled context", ctx: cancelled, want: context.Canceled},
		{name: "missing account", ctx: context.Background(), edit: func(input *StartInput) { input.AccountID = "" }, want: ErrUnauthorized},
		{name: "missing session", ctx: context.Background(), edit: func(input *StartInput) { input.SessionID = "" }, want: ErrInvalidRequest},
		{name: "missing idempotency key", ctx: context.Background(), edit: func(input *StartInput) { input.IdempotencyKey = "" }, want: ErrInvalidRequest},
		{name: "oversized idempotency key", ctx: context.Background(), edit: func(input *StartInput) { input.IdempotencyKey = strings.Repeat("k", maxIdempotencyKeyLength+1) }, want: ErrInvalidRequest},
		{name: "missing request hash", ctx: context.Background(), edit: func(input *StartInput) { input.RequestHash = "" }, want: ErrInvalidRequest},
		{name: "missing trace ID", ctx: context.Background(), edit: func(input *StartInput) { input.TraceID = "" }, want: ErrInvalidRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			input := validStartInput()
			if test.edit != nil {
				test.edit(&input)
			}

			_, err := fixture.service.Start(test.ctx, input)
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
			if fixture.repository.getCalls != 0 ||
				fixture.languages.calls != 0 ||
				fixture.connections.calls != 0 ||
				fixture.realtime.startCalls != 0 {
				t.Fatalf("dependency calls = repository %d, language %d, WebRTC %d, realtime %d; want 0",
					fixture.repository.getCalls, fixture.languages.calls,
					fixture.connections.calls, fixture.realtime.startCalls)
			}
		})
	}
}

func TestServiceStartRejectsNonStartableStates(t *testing.T) {
	for _, status := range []Status{StatusEnded, StatusFailed, Status("unknown")} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newStartFixture(t, status)

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrSessionStateConflict) {
				t.Fatalf("Start() error = %v, want ErrSessionStateConflict", err)
			}
			assertNoStartPrerequisites(t, fixture)
		})
	}
}

func TestServiceStartStopsAfterOwnedReadFailure(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.getErr = errDependency

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want repository error", err)
	}
	assertNoStartPrerequisites(t, fixture)
}

func TestServiceStartReplaysActiveSessionWithoutExternalDependencies(t *testing.T) {
	fixture := newStartFixture(t, StatusActive)
	startedAt := fixture.repository.session.CreatedAt.Add(time.Minute)
	fixture.repository.session.StartedAt = &startedAt
	fixture.repository.operation = completedStartOperation(
		fixture.repository.session,
		startedAt,
	)

	got, err := fixture.service.Start(context.Background(), validStartInput())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.Status != StatusActive || got.StartedAt == nil || !got.StartedAt.Equal(startedAt) {
		t.Fatalf("Start() = %#v", got)
	}
	assertNoStartPrerequisites(t, fixture)
	if fixture.repository.beginCalls != 1 || len(fixture.repository.transitions) != 0 {
		t.Fatalf("begin calls = %d, transitions = %d; want 1, 0",
			fixture.repository.beginCalls, len(fixture.repository.transitions))
	}
}

func TestServiceStartActiveReplayRejectsDifferentRequest(t *testing.T) {
	fixture := newStartFixture(t, StatusActive)
	fixture.repository.operation = completedStartOperation(
		fixture.repository.session,
		fixture.clock.now,
	)
	fixture.repository.operation.RequestHash = "other"

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("Start() error = %v, want ErrIdempotencyKeyConflict", err)
	}
	assertNoStartPrerequisites(t, fixture)
}

func TestServiceStartCreatedReplayRejectsDifferentRequestBeforeReadiness(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.operation = &StartOperation{
		ID:             "op_1",
		SessionID:      "vs_1",
		AccountID:      "acct_1",
		IdempotencyKey: "start_1",
		RequestHash:    "other_hash",
		Status:         StartOperationPending,
		CreatedAt:      fixture.clock.now,
		UpdatedAt:      fixture.clock.now,
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("Start() error = %v, want ErrIdempotencyKeyConflict", err)
	}
	assertNoStartPrerequisites(t, fixture)
	if fixture.repository.beginCalls != 0 {
		t.Fatalf("BeginStartOperation calls = %d, want 0", fixture.repository.beginCalls)
	}
}

func TestServiceStartRejectsInvalidPersistedReadiness(t *testing.T) {
	tests := []struct {
		name string
		edit func(*VoiceSession)
		want error
	}{
		{name: "malformed audio", edit: func(session *VoiceSession) { session.AudioConfig = []byte("{") }, want: ErrUnsupportedAudio},
		{name: "unsupported audio", edit: func(session *VoiceSession) {
			audio := DefaultAudioConfig()
			audio.Codec = "pcm"
			session.AudioConfig = marshalStartJSON(t, audio)
		}, want: ErrUnsupportedAudio},
		{name: "malformed capabilities", edit: func(session *VoiceSession) { session.Capabilities = []byte("{") }, want: ErrInvalidRequest},
		{name: "missing required capability", edit: func(session *VoiceSession) {
			capabilities := validCapabilities()
			capabilities.DataChannel = false
			session.Capabilities = marshalStartJSON(t, capabilities)
		}, want: ErrInvalidRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			test.edit(&fixture.repository.session)

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
			assertNoStartPrerequisites(t, fixture)
		})
	}
}

func TestServiceStartRejectsUnmetExternalPrerequisites(t *testing.T) {
	tests := []struct {
		name string
		edit func(*startFixture)
		want error
	}{
		{name: "language config not ready", edit: func(f *startFixture) { f.languages.result.LanguagePairCount = 1 }, want: ErrLanguageConfigNotReady},
		{name: "language session mismatch", edit: func(f *startFixture) { f.languages.result.SessionID = "other" }, want: ErrLanguageConfigNotReady},
		{name: "language dependency error", edit: func(f *startFixture) { f.languages.err = errDependency }, want: ErrLanguageConfigNotReady},
		{name: "language dependency not implemented", edit: func(f *startFixture) { f.languages.err = ErrNotImplemented }, want: ErrNotImplemented},
		{name: "WebRTC connecting", edit: func(f *startFixture) { f.connections.result.ConnectionState = ConnectionConnecting }, want: ErrWebRTCNotReady},
		{name: "WebRTC session mismatch", edit: func(f *startFixture) { f.connections.result.SessionID = "other" }, want: ErrWebRTCUnavailable},
		{name: "WebRTC invalid state", edit: func(f *startFixture) { f.connections.result.ConnectionState = "unknown" }, want: ErrWebRTCUnavailable},
		{name: "WebRTC dependency error", edit: func(f *startFixture) { f.connections.err = errDependency }, want: ErrWebRTCUnavailable},
		{name: "realtime start error", edit: func(f *startFixture) {
			f.realtime.startErr = errDependency
			f.realtime.getErr = ErrRuntimeSnapshotNotFound
		}, want: ErrRealtimeStartFailed},
		{name: "realtime cancellation", edit: func(f *startFixture) {
			f.realtime.startErr = context.Canceled
			f.realtime.getErr = ErrRuntimeSnapshotNotFound
		}, want: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			test.edit(fixture)

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
			if len(fixture.repository.transitions) != 0 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("transitions = %d, stop calls = %d; want 0, 0",
					len(fixture.repository.transitions), fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceStartCompensatesInvalidRuntimeSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot RuntimeSnapshot
	}{
		{name: "zero timestamp", snapshot: RuntimeSnapshot{SessionID: "vs_1", StartOperationID: "op_1", RuntimeState: RuntimeListening}},
		{name: "stopped", snapshot: RuntimeSnapshot{SessionID: "vs_1", StartOperationID: "op_1", RuntimeState: RuntimeStopped, UpdatedAt: now}},
		{name: "failed", snapshot: RuntimeSnapshot{SessionID: "vs_1", StartOperationID: "op_1", RuntimeState: RuntimeFailed, UpdatedAt: now}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startResult = test.snapshot

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeStartFailed) {
				t.Fatalf("Start() error = %v, want ErrRealtimeStartFailed", err)
			}
			if fixture.realtime.stopCalls != 1 || len(fixture.repository.transitions) != 0 {
				t.Fatalf("stop calls = %d, transitions = %d; want 1, 0",
					fixture.realtime.stopCalls, len(fixture.repository.transitions))
			}
		})
	}
}

func TestServiceStartDoesNotActivateInProgressRuntime(t *testing.T) {
	for _, state := range []RuntimeState{RuntimeStarting, RuntimeStopping} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startResult.RuntimeState = state

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, ErrRealtimeAlreadyRunning) {
				t.Fatalf("Start() error = %v, want ErrRealtimeAlreadyRunning", err)
			}
			if len(fixture.repository.transitions) != 0 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("transitions = %d, stop calls = %d; want 0, 0",
					len(fixture.repository.transitions), fixture.realtime.stopCalls)
			}
			if fixture.repository.session.Status != StatusCreated {
				t.Fatalf("status = %q, want created", fixture.repository.session.Status)
			}
		})
	}
}

func TestServiceStartRecoversExistingRunningRuntime(t *testing.T) {
	for _, state := range []RuntimeState{
		RuntimeListening,
		RuntimeASRProcessing,
		RuntimeTranslating,
		RuntimeTTSProcessing,
		RuntimePlaying,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.realtime.startResult.RuntimeState = state

			got, err := fixture.service.Start(context.Background(), validStartInput())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if got.Status != StatusActive || got.StartedAt == nil {
				t.Fatalf("Start() = %#v, want active session", got)
			}
			if len(fixture.repository.transitions) != 1 || fixture.realtime.stopCalls != 0 {
				t.Fatalf("transitions = %d, stop calls = %d; want 1, 0",
					len(fixture.repository.transitions), fixture.realtime.stopCalls)
			}
		})
	}
}

func TestServiceStartCompensatesTransitionFailureAfterCancellation(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	beginAt := fixture.clock.now
	startedAt := beginAt.Add(time.Second)
	claimedAt := startedAt.Add(time.Second)
	completedAt := claimedAt.Add(time.Second)
	fixture.clock.times = []time.Time{
		beginAt,
		startedAt,
		claimedAt,
		completedAt,
	}
	fixture.repository.transitionErr = errDependency
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), startTraceKey{}, "trace-value"))
	fixture.repository.transitionHook = func(context.Context) { cancel() }

	var compensationActive bool
	var retainedTrace any
	fixture.realtime.stopHook = func(ctx context.Context) {
		_, hasDeadline := ctx.Deadline()
		compensationActive = ctx.Err() == nil && hasDeadline
		retainedTrace = ctx.Value(startTraceKey{})
	}

	_, err := fixture.service.Start(ctx, validStartInput())
	if !errors.Is(err, errDependency) {
		t.Fatalf("Start() error = %v, want transition error", err)
	}
	if !compensationActive || retainedTrace != "trace-value" {
		t.Fatalf("compensation active = %t, trace = %#v", compensationActive, retainedTrace)
	}
	if fixture.realtime.stopCalls != 1 || fixture.repository.session.Status != StatusCreated {
		t.Fatalf("stop calls = %d, status = %q; want 1, created",
			fixture.realtime.stopCalls, fixture.repository.session.Status)
	}
	command := fixture.realtime.stopCommand
	if command.SessionID != "vs_1" ||
		command.TraceID != "req_1" ||
		command.Reason != EndReasonOperatorCancelled ||
		!command.EndedAt.Equal(claimedAt) {
		t.Fatalf("StopRealtimeCommand = %#v", command)
	}
	if !fixture.repository.transitions[0].StartedAt.Equal(startedAt) ||
		!command.EndedAt.After(fixture.repository.transitions[0].StartedAt) {
		t.Fatalf("transition StartedAt = %v, compensation EndedAt = %v",
			fixture.repository.transitions[0].StartedAt, command.EndedAt)
	}
}

func TestServiceStartJoinsTransitionAndCompensationFailures(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.realtime.stopErr = errors.New("stop dependency failed")

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) || !errors.Is(err, ErrRealtimeStopFailed) {
		t.Fatalf("Start() error = %v, want transition and compensation errors", err)
	}
	if fixture.repository.session.Status != StatusCreated {
		t.Fatalf("status = %q, want created", fixture.repository.session.Status)
	}
}

func TestServiceStartRejectsInvalidCompensationSnapshots(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RuntimeSnapshot)
	}{
		{name: "runtime still playing", edit: func(snapshot *RuntimeSnapshot) { snapshot.RuntimeState = RuntimePlaying }},
		{name: "session mismatch", edit: func(snapshot *RuntimeSnapshot) { snapshot.SessionID = "other" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStartFixture(t, StatusCreated)
			fixture.repository.transitionErr = errDependency
			test.edit(&fixture.realtime.stopResult)

			_, err := fixture.service.Start(context.Background(), validStartInput())
			if !errors.Is(err, errDependency) || !errors.Is(err, ErrRealtimeStopFailed) {
				t.Fatalf("Start() error = %v, want transition and stop validation errors", err)
			}
		})
	}
}

func TestServiceStartPersistsFailureWithFreshContextAfterStopTimeout(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.transitionErr = errDependency
	fixture.service.deps.CompensationTimeout = 10 * time.Millisecond
	fixture.repository.requireLiveFailContext = true
	fixture.realtime.stopHook = func(ctx context.Context) {
		<-ctx.Done()
		fixture.realtime.mu.Lock()
		fixture.realtime.stopErr = ctx.Err()
		fixture.realtime.mu.Unlock()
	}

	_, err := fixture.service.Start(context.Background(), validStartInput())
	if !errors.Is(err, errDependency) ||
		!errors.Is(err, ErrRealtimeStopFailed) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want transition, stop, and deadline errors", err)
	}
	if fixture.realtime.stopCalls != 1 ||
		fixture.repository.failCalls != 1 ||
		fixture.repository.completeCalls != 0 {
		t.Fatalf("calls = stop %d, fail %d, complete %d; want 1, 1, 0",
			fixture.realtime.stopCalls,
			fixture.repository.failCalls,
			fixture.repository.completeCalls,
		)
	}
	if fixture.repository.operation.Status != StartOperationCompensationFailed {
		t.Fatalf("operation status = %q, want compensation_failed",
			fixture.repository.operation.Status)
	}
	fixture.repository.mu.Lock()
	failContextErr := fixture.repository.failContextErr
	fixture.repository.mu.Unlock()
	if failContextErr != nil {
		t.Fatalf("FailStartCompensation context error = %v, want nil", failContextErr)
	}
}

func TestServiceStartSerializesConcurrentRequests(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	fixture.realtime.startHook = func(ctx context.Context) {
		close(startEntered)
		select {
		case <-releaseStart:
		case <-ctx.Done():
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		_, err := fixture.service.Start(ctx, validStartInput())
		results <- err
	}()
	waitForSignal(t, "first serialized Realtime.Start", startEntered)

	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		_, err := fixture.service.Start(ctx, validStartInput())
		results <- err
	}()
	waitForSignal(t, "second serialized Start goroutine", secondStarted)
	waitForLockReferences(t, &fixture.service.locks, "vs_1", 2)
	close(releaseStart)

	for range 2 {
		if err := waitForStartResult(t, results); err != nil {
			t.Fatalf("concurrent Start() error = %v", err)
		}
	}
	fixture.realtime.mu.Lock()
	startCalls := fixture.realtime.startCalls
	fixture.realtime.mu.Unlock()
	if startCalls != 1 {
		t.Fatalf("realtime start calls = %d, want 1", startCalls)
	}
	fixture.service.locks.mu.Lock()
	lockEntries := len(fixture.service.locks.locks)
	fixture.service.locks.mu.Unlock()
	if lockEntries != 0 {
		t.Fatalf("lock entries after requests = %d, want 0", lockEntries)
	}
}
