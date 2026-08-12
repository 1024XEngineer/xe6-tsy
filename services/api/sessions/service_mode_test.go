package sessions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestServiceGetModeChecksOwnershipBeforeRealtime(t *testing.T) {
	session := queryTestSession(StatusActive)
	repository := &fakeRepository{getOwnedResult: session}
	modes := &modeControlFake{snapshot: validModeTestSnapshot()}
	service := newModeTestService(t, repository, modes)

	got, err := service.GetMode(t.Context(), DetailInput{
		AccountID: session.AccountID,
		SessionID: session.ID,
	})
	if err != nil {
		t.Fatalf("GetMode() error = %v", err)
	}
	if !reflect.DeepEqual(got, modes.snapshot) {
		t.Fatalf("GetMode() = %#v, want %#v", got, modes.snapshot)
	}
	if repository.getOwnedAccountID != session.AccountID ||
		repository.getOwnedSessionID != session.ID || modes.getSessionID != session.ID {
		t.Fatalf("dependency inputs = repository %q/%q, mode %q",
			repository.getOwnedAccountID, repository.getOwnedSessionID, modes.getSessionID)
	}
}

func TestServiceModeRejectsUnownedAndInactiveSessionsBeforeRealtime(t *testing.T) {
	tests := []struct {
		name       string
		repository *fakeRepository
		want       error
	}{
		{
			name:       "missing or unowned",
			repository: &fakeRepository{getOwnedErr: ErrVoiceSessionNotFound},
			want:       ErrVoiceSessionNotFound,
		},
		{
			name:       "created",
			repository: &fakeRepository{getOwnedResult: queryTestSession(StatusCreated)},
			want:       ErrSessionStateConflict,
		},
		{
			name:       "ended",
			repository: &fakeRepository{getOwnedResult: queryTestSession(StatusEnded)},
			want:       ErrSessionStateConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modes := &modeControlFake{}
			service := newModeTestService(t, test.repository, modes)

			_, err := service.SwitchMode(t.Context(), validModeTestInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("SwitchMode() error = %v, want %v", err, test.want)
			}
			if modes.switchCalls != 0 || modes.getCalls != 0 {
				t.Fatalf("mode calls = get %d, switch %d; want 0", modes.getCalls, modes.switchCalls)
			}
		})
	}
}

func TestServiceSwitchModeForwardsCASAndReturnsReplay(t *testing.T) {
	operationID := "mode-operation-1"
	result := realtimev1.SwitchModeResult{
		OperationID: operationID,
		Status:      realtimev1.ModeSwitchApplied,
		State:       validModeTestSnapshot(),
	}
	result.State.ActiveMode = realtimev1.ModeAssistant
	result.State.Generation = 2
	result.State.LastOperationID = &operationID
	modes := &modeControlFake{result: result}
	service := newModeTestService(t, &fakeRepository{
		getOwnedResult: queryTestSession(StatusActive),
	}, modes)
	input := validModeTestInput()

	first, err := service.SwitchMode(t.Context(), input)
	if err != nil {
		t.Fatalf("first SwitchMode() error = %v", err)
	}
	second, err := service.SwitchMode(t.Context(), input)
	if err != nil {
		t.Fatalf("replayed SwitchMode() error = %v", err)
	}
	if !reflect.DeepEqual(first, result) || !reflect.DeepEqual(second, result) {
		t.Fatalf("results = %#v / %#v, want %#v", first, second, result)
	}
	wantCommand := SwitchModeCommand{
		SessionID:          input.SessionID,
		RuntimeInstanceID:  input.RuntimeInstanceID,
		OperationID:        input.OperationID,
		TraceID:            input.TraceID,
		ExpectedGeneration: input.ExpectedGeneration,
		TargetMode:         input.TargetMode,
	}
	if len(modes.commands) != 2 || !reflect.DeepEqual(modes.commands[0], wantCommand) ||
		!reflect.DeepEqual(modes.commands[1], wantCommand) {
		t.Fatalf("commands = %#v, want duplicate %#v", modes.commands, wantCommand)
	}
}

func TestServiceSwitchModeValidatesBeforeDependencies(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		edit func(*SwitchModeInput)
		want error
	}{
		{name: "cancelled", ctx: cancelled, want: context.Canceled},
		{name: "missing account", edit: func(input *SwitchModeInput) { input.AccountID = "" }, want: ErrUnauthorized},
		{name: "missing session", edit: func(input *SwitchModeInput) { input.SessionID = "" }, want: ErrInvalidRequest},
		{name: "missing runtime", edit: func(input *SwitchModeInput) { input.RuntimeInstanceID = "" }, want: ErrInvalidRequest},
		{name: "missing operation", edit: func(input *SwitchModeInput) { input.OperationID = "" }, want: ErrInvalidRequest},
		{name: "missing trace", edit: func(input *SwitchModeInput) { input.TraceID = "" }, want: ErrInvalidRequest},
		{name: "oversized trace", edit: func(input *SwitchModeInput) { input.TraceID = strings.Repeat("t", maxRequestIDLength+1) }, want: ErrInvalidRequest},
		{name: "zero generation", edit: func(input *SwitchModeInput) { input.ExpectedGeneration = 0 }, want: ErrInvalidRequest},
		{name: "future mode", edit: func(input *SwitchModeInput) { input.TargetMode = "english_practice" }, want: ErrInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validModeTestInput()
			if test.edit != nil {
				test.edit(&input)
			}
			ctx := test.ctx
			if ctx == nil {
				ctx = t.Context()
			}
			repository := &fakeRepository{getOwnedResult: queryTestSession(StatusActive)}
			modes := &modeControlFake{}
			service := newModeTestService(t, repository, modes)

			_, err := service.SwitchMode(ctx, input)
			if !errors.Is(err, test.want) {
				t.Fatalf("SwitchMode() error = %v, want %v", err, test.want)
			}
			if repository.getOwnedCalls != 0 || modes.switchCalls != 0 {
				t.Fatalf("dependency calls = repository %d, mode %d; want 0",
					repository.getOwnedCalls, modes.switchCalls)
			}
		})
	}
}

func TestServiceModeMapsConflictsAndDependencyFailures(t *testing.T) {
	dependencyErr := errors.New("realtime unavailable")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "generation conflict", err: ErrModeGenerationConflict, want: ErrModeGenerationConflict},
		{name: "runtime mismatch", err: ErrModeRuntimeMismatch, want: ErrModeRuntimeMismatch},
		{name: "operation conflict", err: ErrModeOperationConflict, want: ErrModeOperationConflict},
		{name: "mode unavailable", err: ErrModeNotAvailable, want: ErrModeNotAvailable},
		{name: "dependency unavailable", err: dependencyErr, want: ErrModeUnavailable},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modes := &modeControlFake{switchErr: test.err}
			service := newModeTestService(t, &fakeRepository{
				getOwnedResult: queryTestSession(StatusActive),
			}, modes)
			_, err := service.SwitchMode(t.Context(), validModeTestInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("SwitchMode() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServiceModeRejectsInvalidDependencySnapshots(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ModeSnapshot)
	}{
		{name: "session mismatch", edit: func(snapshot *ModeSnapshot) { snapshot.SessionID = "other" }},
		{name: "missing runtime", edit: func(snapshot *ModeSnapshot) { snapshot.RuntimeInstanceID = "" }},
		{name: "invalid mode", edit: func(snapshot *ModeSnapshot) { snapshot.ActiveMode = "future" }},
		{name: "zero generation", edit: func(snapshot *ModeSnapshot) { snapshot.Generation = 0 }},
		{name: "invalid phase", edit: func(snapshot *ModeSnapshot) { snapshot.Phase = "pending" }},
		{name: "zero updated at", edit: func(snapshot *ModeSnapshot) { snapshot.UpdatedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validModeTestSnapshot()
			test.edit(&snapshot)
			service := newModeTestService(t, &fakeRepository{
				getOwnedResult: queryTestSession(StatusActive),
			}, &modeControlFake{snapshot: snapshot})
			_, err := service.GetMode(t.Context(), DetailInput{AccountID: "acct_1", SessionID: "vs_1"})
			if !errors.Is(err, ErrModeUnavailable) {
				t.Fatalf("GetMode() error = %v, want ErrModeUnavailable", err)
			}
		})
	}
}

func TestServiceSwitchModeRejectsInvalidDependencyResults(t *testing.T) {
	input := validModeTestInput()
	base := ModeSwitchResult{
		OperationID: input.OperationID,
		Status:      realtimev1.ModeSwitchApplied,
		State:       validModeTestSnapshot(),
	}
	base.State.ActiveMode = input.TargetMode
	base.State.Generation = input.ExpectedGeneration + 1
	base.State.Phase = realtimev1.ModePhaseActive
	base.State.LastOperationID = &input.OperationID
	for _, test := range []struct {
		name string
		edit func(*ModeSwitchResult)
	}{
		{name: "operation mismatch", edit: func(result *ModeSwitchResult) { result.OperationID = "other" }},
		{name: "last operation missing", edit: func(result *ModeSwitchResult) { result.State.LastOperationID = nil }},
		{name: "last operation mismatch", edit: func(result *ModeSwitchResult) {
			other := "other"
			result.State.LastOperationID = &other
		}},
		{name: "runtime mismatch", edit: func(result *ModeSwitchResult) { result.State.RuntimeInstanceID = "other" }},
		{name: "target mismatch", edit: func(result *ModeSwitchResult) { result.State.ActiveMode = ModeInterpretation }},
		{name: "generation mismatch", edit: func(result *ModeSwitchResult) { result.State.Generation = input.ExpectedGeneration }},
		{name: "phase switching", edit: func(result *ModeSwitchResult) { result.State.Phase = realtimev1.ModePhaseSwitching }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.edit(&result)
			service := newModeTestService(t, &fakeRepository{
				getOwnedResult: queryTestSession(StatusActive),
			}, &modeControlFake{result: result})
			_, err := service.SwitchMode(t.Context(), input)
			if !errors.Is(err, ErrModeUnavailable) {
				t.Fatalf("SwitchMode() error = %v, want ErrModeUnavailable", err)
			}
		})
	}
}

type modeControlFake struct {
	snapshot     ModeSnapshot
	result       ModeSwitchResult
	getErr       error
	switchErr    error
	getCalls     int
	switchCalls  int
	getSessionID string
	commands     []SwitchModeCommand
}

func (f *modeControlFake) GetModeState(_ context.Context, sessionID string) (ModeSnapshot, error) {
	f.getCalls++
	f.getSessionID = sessionID
	return f.snapshot, f.getErr
}

func (f *modeControlFake) SwitchMode(_ context.Context, command SwitchModeCommand) (ModeSwitchResult, error) {
	f.switchCalls++
	f.commands = append(f.commands, command)
	return f.result, f.switchErr
}

func newModeTestService(t *testing.T, repository Repository, modes RealtimeModeControl) *Service {
	t.Helper()
	service := newQueryTestService(t, repository, &fakeRealtimeLifecycle{})
	service.deps.Modes = modes
	return service
}

func validModeTestSnapshot() ModeSnapshot {
	return ModeSnapshot{
		SessionID:         "vs_1",
		RuntimeInstanceID: "runtime-1",
		ActiveMode:        realtimev1.ModeInterpretation,
		Generation:        1,
		Phase:             realtimev1.ModePhaseActive,
		UpdatedAt:         time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
}

func validModeTestInput() SwitchModeInput {
	return SwitchModeInput{
		AccountID:          "acct_1",
		SessionID:          "vs_1",
		RuntimeInstanceID:  "runtime-1",
		OperationID:        "mode-operation-1",
		TraceID:            "trace-1",
		ExpectedGeneration: 1,
		TargetMode:         ModeAssistant,
	}
}

var _ RealtimeModeControl = (*modeControlFake)(nil)
