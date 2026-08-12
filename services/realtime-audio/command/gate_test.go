package command

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

var testStart = time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

func TestGateExecutesAllowlistedCommandsAndQuarantinesAudio(t *testing.T) {
	tests := []struct {
		name string
		text string
		want realtimev1.Mode
	}{
		{name: "start interpretation", text: " 开始 同声传译。", want: realtimev1.ModeInterpretation},
		{name: "stop translation", text: "停止翻译", want: realtimev1.ModeAssistant},
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
				if index == 2 && (result.State != StateDormant || result.Executed == nil || result.Executed.TargetMode != test.want) {
					t.Fatalf("final Consume() = %#v, want executed %q", result, test.want)
				}
			}
			if len(executor.requests) != 1 || executor.requests[0].Command.TargetMode != test.want {
				t.Fatalf("executor requests = %#v, want one %q command", executor.requests, test.want)
			}
			if result := gate.Consume(t.Context(), testFrame(t, testStart.Add(time.Second), 100*time.Millisecond)); result.Consumed {
				t.Fatal("dormant gate consumed ordinary audio")
			}
		})
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
		{
			name: "audio too long", classifier: speechSequence{true, true, true}, want: FailureAudioTooLong,
			frames: []frameSpec{{100 * time.Millisecond, 200 * time.Millisecond}, {300 * time.Millisecond, 200 * time.Millisecond}, {500 * time.Millisecond, 200 * time.Millisecond}},
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
		{name: "parser", asrConfig: asr.FakeProviderConfig{Final: asr.FinalResult{Text: "今天天气不错"}}, want: FailureNotAllowed},
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
			if !result.Consumed || result.State != StateDormant || result.Failure != test.want {
				t.Fatalf("failure Consume() = %#v, want consumed dormant %q", result, test.want)
			}
			if gate.State() != StateDormant {
				t.Fatalf("State() = %q, want dormant", gate.State())
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
		Classifier: &classifier,
		ASR:        asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Executor:   &recordingExecutor{},
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

func TestOldCommandTimerCannotCloseReopenedWindow(t *testing.T) {
	classifier := speechSequence{false}
	gate, err := NewGate(Dependencies{
		Classifier: &classifier,
		ASR:        asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Executor:   &recordingExecutor{},
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
		Classifier: &classifier,
		ASR:        asr.NewFakeProvider(asr.FakeProviderConfig{}),
		Executor:   &recordingExecutor{},
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
		Classifier: &classifier, ASR: provider, Executor: &recordingExecutor{},
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
		Classifier: &classifier, ASR: provider, Executor: &recordingExecutor{},
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

func TestParseRejectsNearMatches(t *testing.T) {
	for _, text := range []string{"", "开始翻译", "停止同声传译", "请停止翻译", "开始同声传译模式"} {
		if _, err := Parse(text); !errors.Is(err, ErrCommandNotAllowed) {
			t.Fatalf("Parse(%q) error = %v, want ErrCommandNotAllowed", text, err)
		}
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
}

func (e *recordingExecutor) ExecuteCommand(_ context.Context, request ExecuteRequest) error {
	e.requests = append(e.requests, request)
	return e.err
}

func newTestGate(t *testing.T, classifier speechSequence, config asr.FakeProviderConfig, executor Executor) *Gate {
	t.Helper()
	gate, err := NewGate(Dependencies{Classifier: &classifier, ASR: asr.NewFakeProvider(config), Executor: executor}, Options{
		WindowTTL: 1500 * time.Millisecond, NoSpeechTimeout: 500 * time.Millisecond,
		MaxAudioDuration: 500 * time.Millisecond, EndSilence: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	return gate
}

func openTestGate(t *testing.T, gate *Gate) {
	t.Helper()
	if err := gate.Open(validOpenRequest()); err != nil {
		t.Fatalf("Open() error = %v", err)
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
