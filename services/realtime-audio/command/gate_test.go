package command

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

var testStart = time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

func TestNewGateRejectsMissingDependencies(t *testing.T) {
	classifier := speechSequence{}
	base := Dependencies{
		Classifier: &classifier, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: &recordingExecutor{},
	}
	tests := []struct {
		name string
		edit func(*Dependencies)
	}{
		{name: "classifier", edit: func(deps *Dependencies) { deps.Classifier = nil }},
		{name: "asr", edit: func(deps *Dependencies) { deps.ASR = nil }},
		{name: "interpreter", edit: func(deps *Dependencies) { deps.Interpreter = nil }},
		{name: "validator", edit: func(deps *Dependencies) { deps.Validator = nil }},
		{name: "executor", edit: func(deps *Dependencies) { deps.Executor = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := base
			test.edit(&deps)
			if gate, err := NewGate(deps, validGateOptions()); gate != nil || !errors.Is(err, ErrDependenciesRequired) {
				t.Fatalf("NewGate() = (%v, %v), want nil and %v", gate, err, ErrDependenciesRequired)
			}
		})
	}
}

func TestNewGateRejectsOptionBoundaries(t *testing.T) {
	base := validGateOptions()
	tests := []struct {
		name string
		edit func(*Options)
	}{
		{name: "zero window", edit: func(options *Options) { options.WindowTTL = 0 }},
		{name: "zero no speech", edit: func(options *Options) { options.NoSpeechTimeout = 0 }},
		{name: "zero maximum", edit: func(options *Options) { options.MaxAudioDuration = 0 }},
		{name: "zero end silence", edit: func(options *Options) { options.EndSilence = 0 }},
		{name: "negative prefix", edit: func(options *Options) { options.PrefixPadding = -time.Nanosecond }},
		{name: "no speech exceeds window", edit: func(options *Options) { options.NoSpeechTimeout = options.WindowTTL + time.Nanosecond }},
		{name: "maximum exceeds window", edit: func(options *Options) { options.MaxAudioDuration = options.WindowTTL + time.Nanosecond }},
		{name: "end silence equals maximum", edit: func(options *Options) { options.EndSilence = options.MaxAudioDuration }},
		{name: "prefix equals maximum", edit: func(options *Options) { options.PrefixPadding = options.MaxAudioDuration }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.edit(&options)
			if gate, err := NewGate(validGateDependencies(t), options); gate != nil || !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("NewGate() = (%v, %v), want nil and %v", gate, err, ErrInvalidOptions)
			}
		})
	}
}

func TestNewGateUsesDefaultsAndNilReceiverGuards(t *testing.T) {
	gate, err := NewGate(validGateDependencies(t), validGateOptions())
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if gate.logger == nil || gate.now == nil || gate.State() != StateDormant {
		t.Fatalf("NewGate() did not initialize logger/clock, state = %q", gate.State())
	}
	var nilGate *Gate
	if nilGate.State() != StateDormant {
		t.Fatalf("nil State() = %q, want dormant", nilGate.State())
	}
	nilGate.Cancel()
	if err := nilGate.Open(validOpenRequest()); !errors.Is(err, ErrDependenciesRequired) {
		t.Fatalf("nil Open() error = %v, want %v", err, ErrDependenciesRequired)
	}
	if got := nilGate.Consume(t.Context(), audio.Frame{}); got != (Result{State: StateDormant}) {
		t.Fatalf("nil Consume() = %#v, want dormant result", got)
	}
}

func TestOpenRejectsInvalidRequestsAndClosedGate(t *testing.T) {
	gate, err := NewGate(validGateDependencies(t), validGateOptions())
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	tests := []struct {
		name string
		edit func(*OpenRequest)
	}{
		{name: "missing session", edit: func(request *OpenRequest) { request.SessionID = "" }},
		{name: "missing command", edit: func(request *OpenRequest) { request.CommandID = "" }},
		{name: "missing opened at", edit: func(request *OpenRequest) { request.OpenedAt = time.Time{} }},
		{name: "capture after opened", edit: func(request *OpenRequest) { request.CaptureFrom = request.OpenedAt.Add(time.Nanosecond) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validOpenRequest()
			test.edit(&request)
			if err := gate.Open(request); !errors.Is(err, ErrInvalidOpenRequest) {
				t.Fatalf("Open() error = %v, want %v", err, ErrInvalidOpenRequest)
			}
		})
	}
	gate.Cancel()
	if err := gate.Open(validOpenRequest()); !errors.Is(err, ErrGateClosed) {
		t.Fatalf("Open() after Cancel error = %v, want %v", err, ErrGateClosed)
	}
}

func validGateDependencies(t *testing.T) Dependencies {
	t.Helper()
	classifier := speechSequence{}
	return Dependencies{
		Classifier: &classifier, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: &recordingExecutor{},
	}
}

func validGateOptions() Options {
	return Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	}
}

func TestGateExecutesSemanticCandidateAndQuarantinesAudio(t *testing.T) {
	tests := []struct {
		name string
		text string
		want realtimev1.Mode
	}{
		{name: "activate interpretation", text: "开启同声传译", want: realtimev1.ModeInterpretation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{}
			gate := newTestGate(t, speechSequence{false, true, false}, asr.FakeProviderConfig{
				Final: asr.FinalResult{Text: test.text},
			}, executor)
			openTestGate(t, gate)

			for index, offset := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond} {
				result := gate.Consume(t.Context(), testFrame(t, testStart.Add(offset), 100*time.Millisecond))
				if !result.Consumed {
					t.Fatalf("Consume(%d).Consumed = false, command audio escaped", index)
				}
				if index < 2 && result.State == StateDormant {
					t.Fatalf("Consume(%d).State = dormant before recognition", index)
				}
				if index == 2 && result.State != StateRecognizing {
					t.Fatalf("final Consume() = %#v, want recognizing", result)
				}
			}
			waitGateRecognition(t, gate)
			if len(executor.requests) != 1 || executor.requests[0].Command.TargetMode != test.want {
				t.Fatalf("executor requests = %#v, want one %q command", executor.requests, test.want)
			}
			if result := gate.Consume(t.Context(), testFrame(t, testStart.Add(time.Second), 100*time.Millisecond)); result.Consumed {
				t.Fatal("dormant gate consumed ordinary audio")
			}
		})
	}
}

func TestGatePublishesTypedResultWithoutReexecutingOnDeliveryFailure(t *testing.T) {
	executor := &recordingExecutor{}
	results := &recordingResultSink{err: errors.New("data channel closed")}
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "开始同声传译"},
		}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: executor,
		Results: results, Now: func() time.Time { return testStart.Add(time.Second) },
	}, Options{
		WindowTTL: 1500 * time.Millisecond, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 500 * time.Millisecond, EndSilence: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(400*time.Millisecond), 100*time.Millisecond))
	if result.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", result)
	}
	waitGateRecognition(t, gate)
	if len(executor.requests) != 1 || len(results.events) != 1 {
		t.Fatalf("result=%#v executor=%#v events=%#v", result, executor.requests, results.events)
	}
	event := results.events[0]
	if event.Status != realtimev1.CommandResultApplied || event.CommandID != "command-1" ||
		event.TargetMode != realtimev1.ModeInterpretation || event.Generation != 2 {
		t.Fatalf("command result = %#v", event)
	}
	if err := gate.Open(validOpenRequest()); !errors.Is(err, ErrDuplicateOpen) {
		t.Fatalf("duplicate Open() error = %v, want ErrDuplicateOpen", err)
	}
	if len(executor.requests) != 1 {
		t.Fatalf("delivery failure or duplicate wake reexecuted command: %#v", executor.requests)
	}
}

func TestGateReportsEmptyPostWakeASRWithoutSendingItToInterpreter(t *testing.T) {
	var logs bytes.Buffer
	results := &recordingResultSink{}
	interpreterCalls := 0
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR:        asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "小灵小灵"}}),
		Interpreter: InterpreterFunc(func(context.Context, InterpretRequest) (Candidate, error) {
			interpreterCalls++
			return Candidate{}, nil
		}),
		Validator: testGateRegistry(t), Executor: &recordingExecutor{}, Results: results,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)), Now: func() time.Time { return testStart.Add(time.Second) },
	}, Options{
		WindowTTL: 1500 * time.Millisecond, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 500 * time.Millisecond, EndSilence: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	gate.Consume(t.Context(), testFrame(t, testStart.Add(400*time.Millisecond), 100*time.Millisecond))
	waitGateRecognition(t, gate)
	if interpreterCalls != 0 || len(results.events) != 1 {
		t.Fatalf("interpreter calls = %d, events = %#v", interpreterCalls, results.events)
	}
	if got := results.events[0]; got.Status != realtimev1.CommandResultFailed ||
		got.Message != "没有识别到唤醒词后的问题，请稍作停顿后重试" {
		t.Fatalf("command result = %#v", got)
	}
	if output := logs.String(); !strings.Contains(output, "stage=asr_empty") || strings.Contains(output, "小灵小灵") {
		t.Fatalf("diagnostic log = %q", output)
	}
}

func TestGatePublishesClarificationAndKeepsRuntimeUsable(t *testing.T) {
	executor := &recordingExecutor{err: ErrClarificationRequired}
	results := &recordingResultSink{}
	feedback := &recordingFeedbackSink{}
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier, ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "开始同声传译"},
		}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: executor,
		Results: results, Feedback: feedback, Now: func() time.Time { return testStart.Add(time.Second) },
	}, Options{
		WindowTTL: 1500 * time.Millisecond, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 500 * time.Millisecond, EndSilence: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(400*time.Millisecond), 100*time.Millisecond))
	if result.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", result)
	}
	waitGateRecognition(t, gate)
	if gate.State() != StateDormant || len(results.events) != 1 ||
		results.events[0].Status != realtimev1.CommandResultClarificationRequired {
		t.Fatalf("result=%#v state=%q events=%#v", result, gate.State(), results.events)
	}
	if len(feedback.requests) != 1 || feedback.requests[0].Event != results.events[0] || feedback.requests[0].Success != nil {
		t.Fatalf("feedback = %#v, want clarification result %#v", feedback.requests, results.events[0])
	}
	if gate.Consume(t.Context(), testFrame(t, testStart.Add(time.Second), 100*time.Millisecond)).Consumed {
		t.Fatal("single-command failure left ordinary audio quarantined")
	}
}

func TestGateSpeechFeedbackExcludesCanceledAttemptsAndAssistantQueries(t *testing.T) {
	feedback := &recordingFeedbackSink{}
	gate := &Gate{deps: Dependencies{Feedback: feedback}}

	gate.publishTerminalOutcome(realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "command-canceled", SessionID: "session-1", Status: realtimev1.CommandResultFailed,
		Message: "上一条命令已被新的唤醒取消", OccurredAt: testStart,
	}, FailureCanceled, nil)
	gate.publishTerminalOutcome(realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "command-query", SessionID: "session-1", Status: realtimev1.CommandResultApplied,
		Action: string(ActionAssistantQuery), TargetMode: realtimev1.ModeAssistant,
		Message: "助手已处理本轮提问", OccurredAt: testStart,
	}, FailureNone, nil)

	if len(feedback.requests) != 0 {
		t.Fatalf("feedback = %#v, want canceled and assistant outcomes to remain silent", feedback.requests)
	}
}

func TestExecutionFailureFeedbackDistinguishesAssistantQueries(t *testing.T) {
	t.Parallel()
	status, message := executionFailureFeedback(Command{Action: ActionAssistantQuery}, errors.New("assistant unavailable"))
	if status != realtimev1.CommandResultFailed || message != "助手暂时无法回答，请重试" {
		t.Fatalf("assistant failure feedback = %q/%q", status, message)
	}
	status, message = executionFailureFeedback(Command{Action: ActionActivateMode}, errors.New("mode unavailable"))
	if status != realtimev1.CommandResultFailed || message != "命令未执行，原模式保持不变" {
		t.Fatalf("mode failure feedback = %q/%q", status, message)
	}
}

func TestGateQueuesSuccessfulExecutionFactsAfterDeterministicResult(t *testing.T) {
	t.Parallel()
	execution := ExecutionResult{
		Status: realtimev1.ModeSwitchUnchanged,
		State: realtimev1.ModeStateSnapshot{
			SessionID: "session-1", RuntimeInstanceID: "runtime-1",
			ActiveMode: realtimev1.ModeInterpretation, Generation: 2,
		},
		LanguageConfig: &AppliedLanguageConfig{
			SourceLanguage: "zh-CN", TargetLanguage: "ja-JP", Version: 3,
		},
	}
	feedback := &recordingFeedbackSink{}
	gate := &Gate{deps: Dependencies{Feedback: feedback}}
	parsed := Command{
		Text: "切换为中日传译", Action: ActionActivateMode, TargetMode: realtimev1.ModeInterpretation,
		Arguments: Arguments{SourceLanguage: "zh-CN", TargetLanguage: "ja-JP"},
	}
	event := commandResultEvent(validOpenRequest(), parsed, execution, commandSuccessFallback(parsed, execution), testStart)
	request := FeedbackRequest{Event: event, Success: &SuccessFeedbackRequest{
		Command: parsed, Execution: execution, ResponseLanguage: "zh-CN",
	}}
	gate.publishTerminalOutcome(event, FailureNone, &request)

	if event.Message != "已设置为中文和日语同声传译" || len(feedback.requests) != 1 ||
		feedback.requests[0].Success == nil || feedback.requests[0].Success.Execution.LanguageConfig.TargetLanguage != "ja-JP" {
		t.Fatalf("event = %#v, feedback = %#v", event, feedback.requests)
	}
}

func TestGateBoundsRestoreDormant(t *testing.T) {
	tests := []struct {
		name       string
		classifier speechSequence
		frames     []frameSpec
		want       Failure
	}{
		{
			name: "window ttl", classifier: speechSequence{true, true}, want: FailureWindowExpired,
			frames: []frameSpec{{100 * time.Millisecond, 100 * time.Millisecond}, {2 * time.Second, 100 * time.Millisecond}},
		},
		{
			name: "no speech", classifier: speechSequence{false}, want: FailureNoSpeech,
			frames: []frameSpec{{600 * time.Millisecond, 100 * time.Millisecond}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{}
			gate := newTestGate(t, test.classifier, asr.FakeProviderConfig{Final: asr.FinalResult{Text: "开始同声传译"}}, executor)
			openTestGate(t, gate)
			var result Result
			for _, frame := range test.frames {
				result = gate.Consume(t.Context(), testFrame(t, testStart.Add(frame.offset), frame.length))
			}
			if !result.Consumed || result.State != StateDormant || result.Failure != test.want {
				t.Fatalf("Consume() = %#v, want consumed dormant failure %q", result, test.want)
			}
			if gate.State() != StateDormant || len(executor.requests) != 0 {
				t.Fatalf("failure left state %q or executed %#v", gate.State(), executor.requests)
			}
		})
	}
}

func TestGateFinalizesAtSharedVADMaximumDuration(t *testing.T) {
	executor := &recordingExecutor{}
	gate := newTestGate(t, speechSequence{true, true}, asr.FakeProviderConfig{
		Final: asr.FinalResult{Text: "开始同声传译"},
	}, executor)
	openTestGate(t, gate)

	if result := gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond)); result.State != StateCapturing {
		t.Fatalf("first Consume() = %#v, want capturing", result)
	}
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(600*time.Millisecond), 100*time.Millisecond))
	if !result.Consumed || result.State != StateRecognizing || result.Failure != FailureNone {
		t.Fatalf("boundary Consume() = %#v, want normal VAD finalization", result)
	}
	waitGateRecognition(t, gate)
	if len(executor.requests) != 1 {
		t.Fatalf("executor requests = %#v, want one complete command", executor.requests)
	}
}

func TestGateOperationalFailuresRestoreDormant(t *testing.T) {
	dependencyErr := errors.New("dependency failed")
	tests := []struct {
		name        string
		asrConfig   asr.FakeProviderConfig
		executorErr error
		cancel      bool
		want        Failure
	}{
		{name: "asr start", asrConfig: asr.FakeProviderConfig{StartErr: dependencyErr}, want: FailureASR},
		{name: "asr finish", asrConfig: asr.FakeProviderConfig{FinishErr: dependencyErr}, want: FailureASR},
		{name: "executor", asrConfig: asr.FakeProviderConfig{Final: asr.FinalResult{Text: "停止翻译"}}, executorErr: dependencyErr, want: FailureExecution},
		{name: "canceled", asrConfig: asr.FakeProviderConfig{Final: asr.FinalResult{Text: "停止翻译"}}, cancel: true, want: FailureCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{err: test.executorErr}
			classifier := speechSequence{true, false}
			gate := newTestGate(t, classifier, test.asrConfig, executor)
			openTestGate(t, gate)
			ctx := t.Context()
			if test.cancel {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			result := gate.Consume(ctx, testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
			if test.asrConfig.StartErr == nil && !test.cancel {
				result = gate.Consume(ctx, testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond))
			}
			if result.State == StateRecognizing {
				waitGateRecognition(t, gate)
			} else if !result.Consumed || result.State != StateDormant || result.Failure != test.want {
				t.Fatalf("failure Consume() = %#v, want consumed dormant %q", result, test.want)
			}
			if gate.State() != StateDormant {
				t.Fatalf("State() = %q, want dormant", gate.State())
			}
		})
	}
}

func TestGatePassesFinalASRIdentityToInterpreter(t *testing.T) {
	t.Parallel()
	var received InterpretRequest
	interpreter := InterpreterFunc(func(_ context.Context, request InterpretRequest) (Candidate, error) {
		received = request
		return Candidate{Text: request.Text, Action: ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant}, nil
	})
	executor := &recordingExecutor{}
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{
			Text: "回到助手", SourceLanguage: "zh-CN",
		}}),
		Interpreter: interpreter, Validator: testGateRegistry(t), Executor: executor,
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond))
	if result.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", result)
	}
	waitGateRecognition(t, gate)
	if len(executor.requests) != 1 {
		t.Fatalf("Consume() = %#v, executor = %#v", result, executor.requests)
	}
	if received.SessionID != "session-1" || received.CommandID != "command-1" || received.Text != "回到助手" || received.Language != "zh-CN" {
		t.Fatalf("Interpret request = %#v", received)
	}
}

func TestGateStripsFixedWakeWordFromContinuousCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "continuous", text: "小灵小灵开始同声传译，中译英", want: "开始同声传译，中译英"},
		{name: "pre-roll speech", text: "刚才还在聊天小灵小灵，结束同声传译", want: "结束同声传译"},
		{name: "alias", text: "小林小林 停止翻译", want: "停止翻译"},
		{name: "already stripped", text: "开始同声传译，中译英", want: "开始同声传译，中译英"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripWakeWordPrefix(tt.text); got != tt.want {
				t.Fatalf("stripWakeWordPrefix(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestGateAcceptsBufferedFrameFromCaptureBoundary(t *testing.T) {
	t.Parallel()
	gate := newTestGate(t, speechSequence{true}, asr.FakeProviderConfig{}, &recordingExecutor{})
	request := validOpenRequest()
	request.OpenedAt = testStart.Add(time.Second)
	request.CaptureFrom = testStart.Add(-time.Second)
	if err := gate.Open(request); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	result := gate.Consume(t.Context(), testFrame(t, testStart, 100*time.Millisecond))
	if !result.Consumed || result.State != StateCapturing {
		t.Fatalf("buffered Consume() = %#v, want capturing", result)
	}
}

func TestGateReplayAndLiveAudioShareUtteranceBoundary(t *testing.T) {
	classifier := speechSequence{false, true, false}
	executor := &recordingExecutor{}
	provider := &recordingAudioProvider{stream: &recordingAudioStream{
		final: asr.FinalResult{Text: "停止翻译"},
	}}
	g, err := NewGate(Dependencies{
		Classifier:  &classifier,
		ASR:         provider,
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: executor,
	}, Options{
		WindowTTL: 3 * time.Second, NoSpeechTimeout: time.Second,
		MaxAudioDuration: 900 * time.Millisecond, EndSilence: 100 * time.Millisecond,
		PrefixPadding: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	request := validOpenRequest()
	request.CaptureFrom = testStart.Add(-time.Second)
	if err := g.Open(request); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for index, offset := range []time.Duration{-300 * time.Millisecond, -100 * time.Millisecond} {
		frame := testFrame(t, testStart.Add(offset), 100*time.Millisecond)
		frame.PCM[0] = byte(index + 1)
		result := g.Replay(t.Context(), []audio.Frame{frame})
		if !result.Consumed || index == 0 && result.State != StateArmed || index == 1 && result.State != StateCapturing {
			t.Fatalf("Replay(%d) = %#v", index, result)
		}
	}
	result := g.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	if !result.Consumed || result.State != StateRecognizing || result.Failure != FailureNone {
		t.Fatalf("live end-silence Consume() = %#v, want shared VAD finalization", result)
	}
	waitGateRecognition(t, g)
	if len(executor.requests) != 1 {
		t.Fatalf("executor requests = %#v, want one command", executor.requests)
	}
	if len(provider.stream.audio) != 2 || provider.stream.audio[0][0] != 1 || provider.stream.audio[1][0] != 2 {
		t.Fatalf("ASR audio = %#v, want prefix before first speech frame", provider.stream.audio)
	}
}

func TestGateClassifiesInterpreterAndValidatorFailures(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("semantic provider failed")
	tests := []struct {
		name        string
		interpreter Interpreter
		validator   Validator
		want        Failure
	}{
		{
			name: "provider failure",
			interpreter: InterpreterFunc(func(context.Context, InterpretRequest) (Candidate, error) {
				return Candidate{}, dependencyErr
			}),
			validator: testGateRegistry(t), want: FailureInterpretation,
		},
		{
			name: "candidate rejected",
			interpreter: InterpreterFunc(func(context.Context, InterpretRequest) (Candidate, error) {
				return Candidate{Action: ActionActivateMode, TargetMode: "english_practice"}, nil
			}),
			validator: testGateRegistry(t), want: FailureNotAllowed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classifier := speechSequence{true, false}
			gate, err := NewGate(Dependencies{
				Classifier:  &classifier,
				ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{Final: asr.FinalResult{Text: "自然语言命令"}}),
				Interpreter: test.interpreter, Validator: test.validator, Executor: &recordingExecutor{},
			}, Options{
				WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
				MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewGate() error = %v", err)
			}
			openTestGate(t, gate)
			gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
			result := gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond))
			if result.State != StateRecognizing {
				t.Fatalf("Consume() = %#v, want recognizing before %q", result, test.want)
			}
			waitGateRecognition(t, gate)
			if gate.State() != StateDormant {
				t.Fatalf("State() = %q, want dormant after %q", gate.State(), test.want)
			}
		})
	}
}

func TestOpenReplacesIncompleteCommandWindow(t *testing.T) {
	gate := newTestGate(t, speechSequence{true}, asr.FakeProviderConfig{}, &recordingExecutor{})
	openTestGate(t, gate)
	if result := gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond)); result.State != StateCapturing {
		t.Fatalf("first Consume().State = %q, want capturing", result.State)
	}
	second := validOpenRequest()
	second.CommandID = "command-2"
	second.OpenedAt = testStart.Add(200 * time.Millisecond)
	if err := gate.Open(second); err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	if gate.State() != StateArmed {
		t.Fatalf("State() = %q, want armed", gate.State())
	}
}

func TestOpenCancelsRecognizingAttemptWithoutPublishingLateResult(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	interpreter := InterpreterFunc(func(ctx context.Context, _ InterpretRequest) (Candidate, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return Candidate{}, ctx.Err()
	})
	results := &synchronizedResultSink{published: make(chan realtimev1.CommandResultEvent, 4)}
	classifier := speechSequence{true, false, true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "开始同声传译"},
		}),
		Interpreter: interpreter, Validator: testGateRegistry(t), Executor: &recordingExecutor{}, Results: results,
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	if got := gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond)); got.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interpreter did not start")
	}

	second := validOpenRequest()
	second.CommandID = "command-2"
	second.OpenedAt = testStart.Add(600 * time.Millisecond)
	if err := gate.Open(second); err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("new wake did not cancel the recognizing attempt")
	}
	waitForCommandResult(t, results.published, "command-1", realtimev1.CommandResultFailed)
	select {
	case event := <-results.published:
		t.Fatalf("old recognition published a late result: %#v", event)
	default:
	}
	if gate.State() != StateArmed {
		t.Fatalf("State() = %q, want second attempt armed", gate.State())
	}
}

func TestGateCaptureDeadlineDoesNotCancelAssistantProcessing(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	interpreter := InterpreterFunc(func(ctx context.Context, request InterpretRequest) (Candidate, error) {
		close(started)
		select {
		case <-release:
			return Candidate{
				Text: request.Text, Action: ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant,
			}, nil
		case <-ctx.Done():
			return Candidate{}, ctx.Err()
		}
	})
	executor := &recordingExecutor{}
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "今天的天气怎么样"},
		}),
		Interpreter: interpreter, Validator: testGateRegistry(t), Executor: executor,
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	var timers []*manualTimer
	gate.afterFunc = func(_ time.Duration, callback func()) commandTimer {
		timer := &manualTimer{callback: callback}
		timers = append(timers, timer)
		return timer
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond))
	if result.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", result)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interpreter did not start")
	}
	timers[0].Fire()
	if gate.State() != StateRecognizing {
		t.Fatalf("capture deadline changed processing state to %q", gate.State())
	}
	close(release)
	waitGateRecognition(t, gate)
	if len(executor.requests) != 1 || executor.requests[0].Command.Action != ActionAssistantQuery {
		t.Fatalf("executor requests = %#v, want assistant query", executor.requests)
	}
}

func TestGateStartsOnlyOneRecognitionWorkerWhileLaterFramesArrive(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	interpreter := InterpreterFunc(func(ctx context.Context, request InterpretRequest) (Candidate, error) {
		started <- struct{}{}
		select {
		case <-release:
			return Candidate{
				Text: request.Text, Action: ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant,
			}, nil
		case <-ctx.Done():
			return Candidate{}, ctx.Err()
		}
	})
	executor := &recordingExecutor{}
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "今天的天气怎么样"},
		}),
		Interpreter: interpreter, Validator: testGateRegistry(t), Executor: executor,
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	if got := gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond)); got.State != StateRecognizing {
		t.Fatalf("Consume() = %#v, want recognizing", got)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interpreter did not start")
	}
	for index := 0; index < 8; index++ {
		capturedAt := testStart.Add(600*time.Millisecond + time.Duration(index)*20*time.Millisecond)
		if got := gate.Consume(t.Context(), testFrame(t, capturedAt, 20*time.Millisecond)); !got.Consumed || got.State != StateRecognizing {
			t.Fatalf("later Consume(%d) = %#v, want quarantined recognizing", index, got)
		}
	}
	select {
	case <-started:
		t.Fatal("later audio started duplicate semantic interpretation")
	default:
	}
	close(release)
	waitGateRecognition(t, gate)
	if len(executor.requests) != 1 || executor.requests[0].Command.Action != ActionAssistantQuery {
		t.Fatalf("executor requests = %#v, want one assistant query", executor.requests)
	}
}

func TestCancelWaitsForRecognitionWorkerAndClosesGate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	interpreter := InterpreterFunc(func(ctx context.Context, _ InterpretRequest) (Candidate, error) {
		close(started)
		<-ctx.Done()
		<-release
		return Candidate{}, ctx.Err()
	})
	classifier := speechSequence{true, false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{Text: "开始同声传译"},
		}),
		Interpreter: interpreter, Validator: testGateRegistry(t), Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	openTestGate(t, gate)
	gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	gate.Consume(t.Context(), testFrame(t, testStart.Add(500*time.Millisecond), 100*time.Millisecond))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("interpreter did not start")
	}

	cancelDone := make(chan struct{})
	go func() {
		gate.Cancel()
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
		t.Fatal("Cancel returned before the recognition worker released resources")
	default:
	}
	close(release)
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not finish after the worker returned")
	}
	if err := gate.Open(validOpenRequest()); !errors.Is(err, ErrGateClosed) {
		t.Fatalf("Open() after Cancel error = %v, want ErrGateClosed", err)
	}
}

func TestGateAllowsAutoDetectedCommandLanguage(t *testing.T) {
	gate := newTestGate(t, speechSequence{false}, asr.FakeProviderConfig{}, &recordingExecutor{})
	request := validOpenRequest()
	request.SourceLanguage = ""
	if err := gate.Open(request); err != nil {
		t.Fatalf("Open() with auto-detect language error = %v", err)
	}
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond))
	if !result.Consumed || result.State != StateArmed {
		t.Fatalf("Consume() = %#v, want quarantined armed frame", result)
	}
}

func TestGateQuarantinesPreWakeFrameWithoutStartingASR(t *testing.T) {
	gate := newTestGate(t, speechSequence{true}, asr.FakeProviderConfig{}, &recordingExecutor{})
	request := validOpenRequest()
	request.OpenedAt = testStart.Add(time.Second)
	if err := gate.Open(request); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	result := gate.Consume(t.Context(), testFrame(t, testStart, 100*time.Millisecond))
	if !result.Consumed || result.State != StateArmed || gate.State() != StateArmed {
		t.Fatalf("pre-wake Consume() = %#v, gate state %q", result, gate.State())
	}
}

func TestGateExpiresWithoutAnotherAudioFrame(t *testing.T) {
	classifier := speechSequence{false}
	gate, err := NewGate(Dependencies{
		Classifier:  &classifier,
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t),
		Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: 100 * time.Millisecond, NoSpeechTimeout: 20 * time.Millisecond,
		MaxAudioDuration: 50 * time.Millisecond, EndSilence: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	var timers []*manualTimer
	gate.afterFunc = func(_ time.Duration, callback func()) commandTimer {
		timer := &manualTimer{callback: callback}
		timers = append(timers, timer)
		return timer
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	timers[1].Fire()
	if gate.State() != StateDormant {
		t.Fatal("no-speech timer did not restore dormant state")
	}
}

func TestGateAcceptsSpeechAfterPostWakePause(t *testing.T) {
	classifier := speechSequence{true}
	gate, err := NewGate(Dependencies{
		Classifier:  &classifier,
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t),
		Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: 15 * time.Second, NoSpeechTimeout: 5 * time.Second,
		MaxAudioDuration: 12 * time.Second, EndSilence: 800 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	gate.afterFunc = func(_ time.Duration, callback func()) commandTimer {
		return &manualTimer{callback: callback}
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(4*time.Second), 100*time.Millisecond))
	if !result.Consumed || result.State != StateCapturing {
		t.Fatalf("Consume() = %#v, want consumed capturing after post-wake pause", result)
	}
}

func TestOldCommandTimerCannotCloseReopenedWindow(t *testing.T) {
	classifier := speechSequence{false}
	gate, err := NewGate(Dependencies{
		Classifier:  &classifier,
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t),
		Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: 200 * time.Millisecond, NoSpeechTimeout: 80 * time.Millisecond,
		MaxAudioDuration: 100 * time.Millisecond, EndSilence: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	var timers []*manualTimer
	gate.afterFunc = func(_ time.Duration, callback func()) commandTimer {
		timer := &manualTimer{callback: callback}
		timers = append(timers, timer)
		return timer
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	second := validOpenRequest()
	second.CommandID = "command-2"
	second.OpenedAt = time.Now()
	if err := gate.Open(second); err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	timers[1].Fire()
	if gate.State() != StateArmed {
		t.Fatalf("old timer changed reopened gate state to %q", gate.State())
	}
}

func TestLateNoSpeechTimerCannotCloseActiveCapture(t *testing.T) {
	classifier := speechSequence{true}
	gate, err := NewGate(Dependencies{
		Classifier:  &classifier,
		ASR:         asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t),
		Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: 200 * time.Millisecond, NoSpeechTimeout: 80 * time.Millisecond,
		MaxAudioDuration: 100 * time.Millisecond, EndSilence: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	var timers []*manualTimer
	gate.afterFunc = func(_ time.Duration, callback func()) commandTimer {
		timer := &manualTimer{callback: callback}
		timers = append(timers, timer)
		return timer
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	result := gate.Consume(t.Context(), testFrame(t, testStart.Add(10*time.Millisecond), 10*time.Millisecond))
	if result.State != StateCapturing {
		t.Fatalf("Consume().State = %q, want capturing", result.State)
	}
	// time.Timer.Stop cannot prevent a callback that already started and is
	// waiting for Gate.mu. Simulate that callback reaching expire late.
	timers[1].Fire()
	if gate.State() != StateCapturing {
		t.Fatalf("late no-speech timer changed gate state to %q", gate.State())
	}
}

func TestGatePropagatesCallerCancellationToCommandASR(t *testing.T) {
	provider := &blockingASRProvider{started: make(chan struct{}), canceled: make(chan struct{})}
	classifier := speechSequence{true}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier, ASR: provider, Interpreter: testSemanticInterpreter(),
		Validator: testGateRegistry(t), Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: time.Second, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 800 * time.Millisecond, EndSilence: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan Result, 1)
	frame := testFrame(t, testStart.Add(100*time.Millisecond), 100*time.Millisecond)
	go func() {
		result <- gate.Consume(ctx, frame)
	}()
	<-provider.started
	cancel()
	<-provider.canceled
	got := <-result
	if got.Failure != FailureCanceled || got.State != StateDormant {
		t.Fatalf("Consume() after runtime cancel = %#v", got)
	}
}

func TestGateDeadlineCancelsBlockedCommandASR(t *testing.T) {
	provider := &blockingASRProvider{started: make(chan struct{}), canceled: make(chan struct{})}
	classifier := speechSequence{true}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier, ASR: provider, Interpreter: testSemanticInterpreter(),
		Validator: testGateRegistry(t), Executor: &recordingExecutor{},
	}, Options{
		WindowTTL: 50 * time.Millisecond, NoSpeechTimeout: 25 * time.Millisecond,
		MaxAudioDuration: 40 * time.Millisecond, EndSilence: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	frame := testFrame(t, testStart.Add(10*time.Millisecond), 10*time.Millisecond)
	result := make(chan Result, 1)
	go func() { result <- gate.Consume(context.Background(), frame) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("command ASR did not start")
	}
	select {
	case <-provider.canceled:
	case <-time.After(time.Second):
		t.Fatal("command ASR was not canceled at window deadline")
	}
	if got := <-result; got.Failure != FailureWindowExpired || got.State != StateDormant {
		t.Fatalf("Consume() after deadline = %#v", got)
	}
}

type blockingASRProvider struct {
	started  chan struct{}
	canceled chan struct{}
}

type recordingAudioProvider struct {
	stream *recordingAudioStream
}

func (p *recordingAudioProvider) StartStream(context.Context, asr.StreamRequest) (asr.Stream, error) {
	return p.stream, nil
}

type recordingAudioStream struct {
	audio [][]byte
	final asr.FinalResult
}

func (s *recordingAudioStream) PushAudio(_ context.Context, pcm []byte) error {
	s.audio = append(s.audio, append([]byte(nil), pcm...))
	return nil
}

func (s *recordingAudioStream) Events() <-chan asr.Event {
	events := make(chan asr.Event)
	close(events)
	return events
}

func (s *recordingAudioStream) Finish(context.Context) (asr.FinalResult, error) {
	return s.final, nil
}

func (s *recordingAudioStream) Close() error { return nil }

func (p *blockingASRProvider) StartStream(ctx context.Context, _ asr.StreamRequest) (asr.Stream, error) {
	close(p.started)
	<-ctx.Done()
	close(p.canceled)
	return nil, ctx.Err()
}

type manualTimer struct {
	callback func()
	stopped  bool
}

func (t *manualTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *manualTimer) Fire() {
	if t.callback != nil {
		t.callback()
	}
}

type frameSpec struct {
	offset time.Duration
	length time.Duration
}

type speechSequence []bool

func (s *speechSequence) Speech(audio.Frame) bool {
	if len(*s) == 0 {
		return false
	}
	result := (*s)[0]
	*s = (*s)[1:]
	return result
}

type recordingExecutor struct {
	requests []ExecuteRequest
	err      error
	result   ExecutionResult
}

type recordingResultSink struct {
	events []realtimev1.CommandResultEvent
	err    error
}

type recordingFeedbackSink struct {
	requests   []FeedbackRequest
	interrupts int
	closed     bool
}

type synchronizedResultSink struct {
	published chan realtimev1.CommandResultEvent
}

func (s *synchronizedResultSink) Publish(_ context.Context, event realtimev1.CommandResultEvent) error {
	s.published <- event
	return nil
}

func waitForCommandResult(
	t *testing.T,
	events <-chan realtimev1.CommandResultEvent,
	commandID string,
	status realtimev1.CommandResultStatus,
) realtimev1.CommandResultEvent {
	t.Helper()
	select {
	case event := <-events:
		if event.CommandID != commandID || event.Status != status {
			t.Fatalf("command result = %#v, want command %q status %q", event, commandID, status)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("command result %q was not published", commandID)
		return realtimev1.CommandResultEvent{}
	}
}

func (s *recordingResultSink) Publish(_ context.Context, event realtimev1.CommandResultEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func (s *recordingFeedbackSink) Publish(request FeedbackRequest) {
	s.requests = append(s.requests, request)
}

func (s *recordingFeedbackSink) Interrupt() { s.interrupts++ }

func (s *recordingFeedbackSink) Close() { s.closed = true }

func (e *recordingExecutor) ExecuteCommand(_ context.Context, request ExecuteRequest) (ExecutionResult, error) {
	e.requests = append(e.requests, request)
	if e.result.Status == "" {
		e.result = ExecutionResult{
			Status: realtimev1.ModeSwitchApplied,
			State: realtimev1.ModeStateSnapshot{
				SessionID: request.SessionID, RuntimeInstanceID: "runtime-1",
				ActiveMode: request.Command.TargetMode, Generation: 2,
			},
		}
	}
	return e.result, e.err
}

func newTestGate(t *testing.T, classifier speechSequence, config asr.FakeProviderConfig, executor Executor) *Gate {
	t.Helper()
	gate, err := NewGate(Dependencies{
		Classifier: &classifier, ASR: asr.NewFakeProvider(config),
		Interpreter: testSemanticInterpreter(), Validator: testGateRegistry(t), Executor: executor,
	}, Options{
		WindowTTL: 1500 * time.Millisecond, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 500 * time.Millisecond, EndSilence: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	return gate
}

func testSemanticInterpreter() Interpreter {
	return InterpreterFunc(func(_ context.Context, request InterpretRequest) (Candidate, error) {
		return Candidate{
			Text: request.Text, Action: ActionActivateMode, TargetMode: realtimev1.ModeInterpretation,
		}, nil
	})
}

func testGateRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(
		CapabilityDescriptor{
			Mode: realtimev1.ModeInterpretation, Description: "interpretation", SchemaVersion: 1,
			Actions: []Action{ActionActivateMode},
		},
		CapabilityDescriptor{
			Mode: realtimev1.ModeAssistant, Description: "assistant", SchemaVersion: 1,
			Actions: []Action{ActionReturnToAssistant, ActionAssistantQuery},
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func openTestGate(t *testing.T, gate *Gate) {
	t.Helper()
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
}

func waitGateRecognition(t *testing.T, gate *Gate) {
	t.Helper()
	gate.mu.Lock()
	done := gate.recognitionTasks[gate.attempt]
	state := gate.state
	gate.mu.Unlock()
	if done == nil {
		if state != StateDormant {
			t.Fatalf("recognition task missing in state %q", state)
		}
		return
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("command recognition did not complete")
	}
}

func validOpenRequest() OpenRequest {
	return OpenRequest{SessionID: "session-1", CommandID: "command-1", SourceLanguage: "zh-CN", OpenedAt: testStart}
}

func testFrame(t *testing.T, capturedAt time.Time, length time.Duration) audio.Frame {
	t.Helper()
	samples := int(length * audio.SupportedSampleRate / time.Second)
	frame, err := audio.NewFrame(make([]byte, samples*2), audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatalf("audio.NewFrame() error = %v", err)
	}
	return frame
}
