package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestCommandSpeechFeedbackReusesSpeechOutputAndPublishesUsage(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Chunks: []tts.AudioChunk{{SequenceNo: 1, Encoding: "pcm_s16le", Data: []byte{1, 2}}},
		Result: tts.Result{Provider: "mock-tts", Model: "tts-v1", AudioDuration: 100 * time.Millisecond},
	})
	audio := &recordingAudioSink{}
	usage := &recordingUsageSink{}
	runtimeReporter := &recordingRuntimeReporter{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: audio, Runtime: runtimeReporter,
		}),
		Usage: usage, Runtime: runtimeReporter, AccountID: "account-1", TraceID: "trace-1",
		Logger: commandFeedbackTestLogger(), Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	feedback.Publish(command.FeedbackRequest{Event: validCommandFeedbackEvent()})
	feedback.wg.Wait()
	feedback.Close()

	requests := ttsProvider.Requests()
	facts := usage.Facts()
	if len(requests) != 1 || requests[0].Text != "已进入同声传译模式" || len(audio.Chunks()) != 1 ||
		len(facts) != 1 || facts[0].ServiceType != "tts" || facts[0].TurnID != "command_wake-1" {
		t.Fatalf("requests=%#v chunks=%#v usage=%#v", requests, audio.Chunks(), facts)
	}
	if !runtimeReporter.recordedState(session.RuntimeListening) {
		t.Fatalf("runtime states = %#v, want listening", runtimeReporter.states)
	}
}

func TestCommandSpeechFeedbackGeneratesNaturalMessageAndPublishesLLMUsage(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Result: tts.Result{Provider: "mock-tts", Model: "tts-v1", AudioDuration: 100 * time.Millisecond},
	})
	usage := &recordingUsageSink{}
	runtimeReporter := &recordingRuntimeReporter{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
		}),
		Usage: usage, Runtime: runtimeReporter,
		SuccessFeedback: command.SuccessFeedbackFunc(func(context.Context, command.SuccessFeedbackRequest) (command.SuccessFeedbackResult, error) {
			return command.SuccessFeedbackResult{
				Message: "好的，已切换为中文和日语同声传译。", Provider: "aliyun", Model: "qwen3.6-flash",
				InputTokens: 21, OutputTokens: 9,
			}, nil
		}),
		AccountID: "account-1", TraceID: "trace-1", Logger: commandFeedbackTestLogger(),
		Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	feedback.Publish(validGeneratedCommandFeedbackRequest())
	feedback.wg.Wait()
	feedback.Close()

	requests := ttsProvider.Requests()
	facts := usage.Facts()
	if len(requests) != 1 || requests[0].Text != "好的，已切换为中文和日语同声传译。" ||
		len(facts) != 2 || facts[0].ServiceType != "tts" || facts[1].ServiceType != "assistant_llm" ||
		facts[1].InputTokens != 21 {
		t.Fatalf("TTS requests = %#v, usage = %#v", requests, facts)
	}
}

func TestCommandSpeechFeedbackTimeoutPlaysDeterministicFallback(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}),
		Usage: &recordingUsageSink{}, Runtime: &recordingRuntimeReporter{},
		SuccessFeedback: command.SuccessFeedbackFunc(func(ctx context.Context, _ command.SuccessFeedbackRequest) (command.SuccessFeedbackResult, error) {
			<-ctx.Done()
			return command.SuccessFeedbackResult{}, ctx.Err()
		}),
		SuccessFeedbackTimeout: 10 * time.Millisecond,
		AccountID:              "account-1", TraceID: "trace-1", Logger: commandFeedbackTestLogger(), Now: time.Now,
	})
	feedback.Publish(validGeneratedCommandFeedbackRequest())
	feedback.wg.Wait()
	feedback.Close()

	requests := ttsProvider.Requests()
	if len(requests) != 1 || requests[0].Text != "已进入同声传译模式" {
		t.Fatalf("TTS requests = %#v, want deterministic fallback", requests)
	}
}

func TestCommandSpeechFeedbackInvalidGeneratedTextStillPublishesLLMUsage(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Result: tts.Result{Provider: "mock-tts", Model: "tts-v1", AudioDuration: 100 * time.Millisecond},
	})
	usage := &recordingUsageSink{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}),
		Usage: usage, Runtime: &recordingRuntimeReporter{},
		SuccessFeedback: command.SuccessFeedbackFunc(func(context.Context, command.SuccessFeedbackRequest) (command.SuccessFeedbackResult, error) {
			return command.SuccessFeedbackResult{
				Provider: "aliyun", Model: "qwen3.6-flash", InputTokens: 12, OutputTokens: 4,
			}, errors.New("invalid feedback")
		}),
		AccountID: "account-1", TraceID: "trace-1", Logger: commandFeedbackTestLogger(), Now: time.Now,
	})
	feedback.Publish(validGeneratedCommandFeedbackRequest())
	feedback.wg.Wait()
	feedback.Close()

	requests := ttsProvider.Requests()
	facts := usage.Facts()
	if len(requests) != 1 || requests[0].Text != "已进入同声传译模式" || len(facts) != 2 ||
		facts[0].ServiceType != "tts" || facts[1].ServiceType != "assistant_llm" || facts[1].InputTokens != 12 {
		t.Fatalf("TTS requests = %#v, usage = %#v", requests, facts)
	}
}

func TestCommandSpeechFeedbackInterruptCancelsGenerationWithoutFallbackSpeech(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{})
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}),
		Usage: &recordingUsageSink{}, Runtime: &recordingRuntimeReporter{},
		SuccessFeedback: command.SuccessFeedbackFunc(func(ctx context.Context, _ command.SuccessFeedbackRequest) (command.SuccessFeedbackResult, error) {
			close(started)
			<-ctx.Done()
			close(canceled)
			return command.SuccessFeedbackResult{}, ctx.Err()
		}),
		AccountID: "account-1", TraceID: "trace-1", Logger: commandFeedbackTestLogger(), Now: time.Now,
	})
	feedback.Publish(validGeneratedCommandFeedbackRequest())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("feedback generator did not start")
	}
	feedback.Interrupt()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("feedback generator was not canceled")
	}
	feedback.Close()

	if requests := ttsProvider.Requests(); len(requests) != 0 {
		t.Fatalf("canceled feedback produced TTS requests: %#v", requests)
	}
}

func TestCommandSpeechFeedbackFailureRestoresListeningWithoutRetry(t *testing.T) {
	ttsProvider := &failingCommandTTS{err: errors.New("TTS unavailable")}
	runtimeReporter := &recordingRuntimeReporter{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
		}),
		Usage: &recordingUsageSink{}, Runtime: runtimeReporter, AccountID: "account-1", TraceID: "trace-1",
		Logger: commandFeedbackTestLogger(), Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	feedback.Publish(command.FeedbackRequest{Event: validCommandFeedbackEvent()})
	feedback.wg.Wait()
	feedback.Close()

	if ttsProvider.calls != 1 {
		t.Fatalf("failed TTS attempts = %d, want one", ttsProvider.calls)
	}
	if !runtimeReporter.recordedState(session.RuntimeListening) {
		t.Fatalf("runtime states = %#v, want listening", runtimeReporter.states)
	}
}

func TestCommandSpeechFeedbackUsageFailureDoesNotRetrySpeech(t *testing.T) {
	ttsProvider := tts.NewFakeProvider(tts.FakeProviderConfig{
		Result: tts.Result{Provider: "mock-tts", Model: "tts-v1", AudioDuration: 100 * time.Millisecond},
	})
	usage := &failingCommandUsageSink{err: errors.New("usage unavailable")}
	runtimeReporter := &recordingRuntimeReporter{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
		}),
		Usage: usage, Runtime: runtimeReporter, AccountID: "account-1", TraceID: "trace-1",
		Logger: commandFeedbackTestLogger(), Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	feedback.Publish(command.FeedbackRequest{Event: validCommandFeedbackEvent()})
	feedback.wg.Wait()
	feedback.Close()

	if len(ttsProvider.Requests()) != 1 || usage.calls != 1 || !usage.hadDeadline {
		t.Fatalf("TTS requests = %d, usage calls = %d, deadline = %t; want one each with deadline",
			len(ttsProvider.Requests()), usage.calls, usage.hadDeadline)
	}
	if !runtimeReporter.recordedState(session.RuntimeListening) {
		t.Fatalf("runtime states = %#v, want listening", runtimeReporter.states)
	}
}

func TestCommandSpeechFeedbackDefaultsGenerationTimeout(t *testing.T) {
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{})
	if feedback.deps.SuccessFeedbackTimeout != commandFeedbackLLMTimeout {
		t.Fatalf("default feedback timeout = %s, want %s", feedback.deps.SuccessFeedbackTimeout, commandFeedbackLLMTimeout)
	}
}

func TestCommandSpeechFeedbackSkipsIncompleteLLMUsageAndReturnsPublishError(t *testing.T) {
	usageErr := errors.New("usage unavailable")
	tests := []struct {
		name         string
		result       command.SuccessFeedbackResult
		wantCalls    int
		wantUsageErr bool
	}{
		{name: "missing provider", result: command.SuccessFeedbackResult{Model: "model", InputTokens: 1}},
		{name: "missing model", result: command.SuccessFeedbackResult{Provider: "provider", InputTokens: 1}},
		{name: "no tokens", result: command.SuccessFeedbackResult{Provider: "provider", Model: "model"}},
		{
			name:      "publish error",
			result:    command.SuccessFeedbackResult{Provider: "provider", Model: "model", InputTokens: 1},
			wantCalls: 1, wantUsageErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := &failingCommandUsageSink{err: usageErr}
			feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
				Usage: usage, AccountID: "account-1", TraceID: "trace-1", Now: time.Now,
			})

			err := feedback.publishFeedbackUsage(t.Context(), validCommandFeedbackEvent(), test.result)
			if errors.Is(err, usageErr) != test.wantUsageErr {
				t.Fatalf("publishFeedbackUsage() error = %v, want usage error = %t", err, test.wantUsageErr)
			}
			if usage.calls != test.wantCalls {
				t.Fatalf("usage publish calls = %d, want %d", usage.calls, test.wantCalls)
			}
			if test.wantCalls > 0 && !usage.hadDeadline {
				t.Fatal("usage publish context did not have a deadline")
			}
		})
	}
}

func TestCommandSpeechFeedbackRejectsInvalidAndClosedRequests(t *testing.T) {
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{})
	feedback.Publish(command.FeedbackRequest{Event: realtimev1.CommandResultEvent{Message: "invalid"}})
	feedback.mu.Lock()
	if feedback.attempt != 0 {
		feedback.mu.Unlock()
		t.Fatalf("invalid request changed feedback state: attempt=%d", feedback.attempt)
	}
	feedback.mu.Unlock()

	feedback.Close()
	feedback.Publish(command.FeedbackRequest{Event: validCommandFeedbackEvent()})
	feedback.mu.Lock()
	defer feedback.mu.Unlock()
	if feedback.attempt != 0 {
		t.Fatalf("closed feedback accepted a request, attempt=%d", feedback.attempt)
	}
}

func validCommandFeedbackEvent() realtimev1.CommandResultEvent {
	return realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "wake-1", SessionID: "session-1", RuntimeInstanceID: "runtime-1", Generation: 2,
		Status: realtimev1.CommandResultApplied, Action: "activate_mode", TargetMode: realtimev1.ModeInterpretation,
		Message: "已进入同声传译模式", OccurredAt: time.Unix(10, 0).UTC(),
	}
}

func validGeneratedCommandFeedbackRequest() command.FeedbackRequest {
	event := validCommandFeedbackEvent()
	return command.FeedbackRequest{Event: event, Success: &command.SuccessFeedbackRequest{
		Command: command.Command{
			Text: "进入中日互译", Action: command.ActionActivateMode, TargetMode: realtimev1.ModeInterpretation,
		},
		Execution: command.ExecutionResult{
			Status: realtimev1.ModeSwitchApplied,
			State: realtimev1.ModeStateSnapshot{
				SessionID: event.SessionID, RuntimeInstanceID: event.RuntimeInstanceID,
				ActiveMode: event.TargetMode, Generation: event.Generation,
			},
		},
		ResponseLanguage: "zh-CN",
	}}
}

func commandFeedbackTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (r *recordingRuntimeReporter) recordedState(want session.RuntimeState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.states {
		if got == want {
			return true
		}
	}
	return false
}

type failingCommandTTS struct {
	calls int
	err   error
}

type failingCommandUsageSink struct {
	calls       int
	err         error
	hadDeadline bool
}

func (s *failingCommandUsageSink) Publish(ctx context.Context, _ pipeline.UsageFact) error {
	s.calls++
	_, s.hadDeadline = ctx.Deadline()
	return s.err
}

func (p *failingCommandTTS) StartStream(context.Context, tts.Request) (tts.Stream, error) {
	p.calls++
	return nil, p.err
}

var _ session.RuntimeStateReporter = (*recordingRuntimeReporter)(nil)
var _ tts.Provider = (*failingCommandTTS)(nil)
