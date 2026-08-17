package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
		Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	feedback.Publish(validCommandFeedbackEvent())
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

func TestCommandSpeechFeedbackFailureRestoresListeningWithoutRetry(t *testing.T) {
	ttsProvider := &failingCommandTTS{err: errors.New("TTS unavailable")}
	runtimeReporter := &recordingRuntimeReporter{}
	feedback := newCommandSpeechFeedback(commandSpeechFeedbackDependencies{
		Speech: pipeline.NewSpeechOutput(pipeline.SpeechOutputDependencies{
			TTS: ttsProvider, Audio: &recordingAudioSink{}, Runtime: runtimeReporter,
		}),
		Usage: &recordingUsageSink{}, Runtime: runtimeReporter, AccountID: "account-1", TraceID: "trace-1",
	})
	feedback.Publish(validCommandFeedbackEvent())
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
	})
	feedback.Publish(validCommandFeedbackEvent())
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

func validCommandFeedbackEvent() realtimev1.CommandResultEvent {
	return realtimev1.CommandResultEvent{
		Type: realtimev1.CommandResultTopic, EventVersion: realtimev1.CommandResultEventVersion,
		CommandID: "wake-1", SessionID: "session-1", RuntimeInstanceID: "runtime-1", Generation: 2,
		Status: realtimev1.CommandResultApplied, Action: "activate_mode", TargetMode: realtimev1.ModeInterpretation,
		Message: "已进入同声传译模式", OccurredAt: time.Unix(10, 0).UTC(),
	}
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
