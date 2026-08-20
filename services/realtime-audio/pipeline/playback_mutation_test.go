package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestSpeechOutputRejectsEveryMissingDependency(t *testing.T) {
	valid := func() SpeechOutputDependencies {
		return SpeechOutputDependencies{
			TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}
	}
	tests := []struct {
		name  string
		build func() *SpeechOutput
	}{
		{name: "nil output", build: func() *SpeechOutput { return nil }},
		{name: "tts", build: func() *SpeechOutput { deps := valid(); deps.TTS = nil; return NewSpeechOutput(deps) }},
		{name: "audio", build: func() *SpeechOutput { deps := valid(); deps.Audio = nil; return NewSpeechOutput(deps) }},
		{name: "runtime", build: func() *SpeechOutput { deps := valid(); deps.Runtime = nil; return NewSpeechOutput(deps) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.build().Play(t.Context(), SpeechOutputRequest{})
			var notStarted speechOutputNotStartedError
			if !errors.Is(err, ErrPipelineDependencyRequired) || !errors.As(err, &notStarted) {
				t.Fatalf("Play() error = %v, want not-started dependency error", err)
			}
		})
	}
}

func TestMarkFallbackPlaybackNotStartedPreservesNil(t *testing.T) {
	if got := MarkFallbackPlaybackNotStarted(nil); got != nil {
		t.Fatalf("MarkFallbackPlaybackNotStarted(nil) = %v, want nil", got)
	}
}

func TestSpeechOutputRejectsEveryInvalidRequestField(t *testing.T) {
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
	})
	valid := SpeechOutputRequest{Turn: testTurn(), Language: "en-US", Text: "hello", PlaybackID: "playback-1"}
	tests := []struct {
		name string
		set  func(*SpeechOutputRequest)
	}{
		{name: "session", set: func(request *SpeechOutputRequest) { request.Turn.SessionID = "" }},
		{name: "turn", set: func(request *SpeechOutputRequest) { request.Turn.ID = "" }},
		{name: "language", set: func(request *SpeechOutputRequest) { request.Language = "  " }},
		{name: "text", set: func(request *SpeechOutputRequest) { request.Text = "\t" }},
		{name: "playback", set: func(request *SpeechOutputRequest) { request.PlaybackID = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.set(&request)
			_, err := speech.Play(t.Context(), request)
			if !errors.Is(err, ErrSpeechOutputRequestInvalid) {
				t.Fatalf("Play() error = %v, want ErrSpeechOutputRequestInvalid", err)
			}
		})
	}
}

func TestPlayFallbackRequiresEveryImmutableInputAndAcceptsVersionOne(t *testing.T) {
	newService := func() *PipelineService {
		return newTestPipelineService(PipelineDependencies{
			Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
			FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		})
	}
	valid := FallbackPlayback{
		SessionID: "session-1", TurnID: "turn-1", AccountID: "account-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "translated", LanguageConfigVersion: 1, PlaybackID: "playback-1",
	}
	tests := []struct {
		name  string
		input FallbackPlayback
	}{
		{name: "session", input: FallbackPlayback{TurnID: valid.TurnID, AccountID: valid.AccountID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "turn", input: FallbackPlayback{SessionID: valid.SessionID, AccountID: valid.AccountID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "account", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "trace", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, AccountID: valid.AccountID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "language", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, AccountID: valid.AccountID, TraceID: valid.TraceID, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "text", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, AccountID: valid.AccountID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, LanguageConfigVersion: valid.LanguageConfigVersion, PlaybackID: valid.PlaybackID}},
		{name: "version", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, AccountID: valid.AccountID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, PlaybackID: valid.PlaybackID}},
		{name: "playback", input: FallbackPlayback{SessionID: valid.SessionID, TurnID: valid.TurnID, AccountID: valid.AccountID, TraceID: valid.TraceID, TargetLanguage: valid.TargetLanguage, TranslatedText: valid.TranslatedText, LanguageConfigVersion: valid.LanguageConfigVersion}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newService().PlayFallback(t.Context(), test.input)
			if !errors.Is(err, ErrPipelineDependencyRequired) || !hasFallbackPlaybackNotStarted(err) {
				t.Fatalf("PlayFallback() error = %v, want marked invalid input", err)
			}
		})
	}

	if err := newService().PlayFallback(t.Context(), valid); err != nil {
		t.Fatalf("PlayFallback() version one error = %v", err)
	}
}

func TestSpeechOutputStopsWhenRuntimeOwnershipIsSuperseded(t *testing.T) {
	for _, test := range []struct {
		name  string
		state session.RuntimeState
	}{
		{name: "before synthesis", state: session.RuntimeTTSProcessing},
		{name: "before first chunk", state: session.RuntimePlaying},
	} {
		t.Run(test.name, func(t *testing.T) {
			speech := NewSpeechOutput(SpeechOutputDependencies{
				TTS:   tts.NewFakeProvider(tts.FakeProviderConfig{Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1}}}}),
				Audio: &recordingAudioSink{}, Runtime: stateFailingRuntimeReporter{failState: test.state, err: session.ErrRuntimeIdentityConflict},
			})
			_, err := speech.Play(t.Context(), SpeechOutputRequest{Turn: testTurn(), Language: "en-US", Text: "hello", PlaybackID: "playback-1"})
			if !errors.Is(err, ErrSpeechOutputSuperseded) {
				t.Fatalf("Play() error = %v, want ErrSpeechOutputSuperseded", err)
			}
		})
	}
}

func TestSpeechOutputReturnsPlaybackCompletionFailure(t *testing.T) {
	wantErr := errors.New("completion unavailable")
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS:   tts.NewFakeProvider(tts.FakeProviderConfig{Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{1}}}}),
		Audio: &failingPlaybackLifecycleSink{completeErr: wantErr}, Runtime: &recordingRuntimeReporter{},
	})

	_, err := speech.Play(t.Context(), SpeechOutputRequest{Turn: testTurn(), Language: "en-US", Text: "hello", PlaybackID: "playback-1"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Play() error = %v, want completion error", err)
	}
}

func TestSpeechOutputSkipsLifecycleCallbacksWhenNoAudioWasPlayed(t *testing.T) {
	sink := &recordingPlaybackSink{}
	speech := NewSpeechOutput(SpeechOutputDependencies{
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{}), Audio: sink, Runtime: &recordingRuntimeReporter{},
	})
	if err := speech.completePlayback(t.Context(), "session-1", "playback-1", false); err != nil {
		t.Fatalf("completePlayback() error = %v", err)
	}
	if err := speech.cancelPlayback(t.Context(), "session-1", "playback-1", "failed", false); err != nil {
		t.Fatalf("cancelPlayback() error = %v", err)
	}
	if len(sink.completed) != 0 || len(sink.cancelled) != 0 {
		t.Fatalf("lifecycle callbacks = completed %#v cancelled %#v, want none", sink.completed, sink.cancelled)
	}
}

func TestPlayFallbackPreservesUsageAndRestoreErrors(t *testing.T) {
	usageErr := errors.New("usage unavailable")
	restoreErr := errors.New("restore unavailable")
	input := FallbackPlayback{
		SessionID: "session-1", TurnID: "turn-1", AccountID: "account-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "translated", LanguageConfigVersion: 1, PlaybackID: "playback-1",
	}
	tests := []struct {
		name string
		svc  *PipelineService
		want error
	}{
		{name: "usage", svc: newTestPipelineService(PipelineDependencies{
			Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
			FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{failService: "tts", err: usageErr}, Audio: &recordingAudioSink{}, Runtime: &recordingRuntimeReporter{},
		}), want: usageErr},
		{name: "restore", svc: newTestPipelineService(PipelineDependencies{
			Translator: &translate.FakeProvider{}, TTS: tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{Provider: "mock-tts", Model: "v1"}}),
			FinalTurns: &recordingFinalSink{}, Usage: &recordingUsageSink{}, Audio: &recordingAudioSink{}, Runtime: stateFailingRuntimeReporter{failState: session.RuntimeListening, err: restoreErr},
		}), want: restoreErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.svc.PlayFallback(t.Context(), input)
			if !errors.Is(err, test.want) || hasFallbackPlaybackNotStarted(err) {
				t.Fatalf("PlayFallback() error = %v, want post-start %v without marker", err, test.want)
			}
		})
	}
}

type failingPlaybackLifecycleSink struct {
	recordingAudioSink
	completeErr error
}

func (s *failingPlaybackLifecycleSink) Complete(_ context.Context, _, _ string) error {
	return s.completeErr
}

func (s *failingPlaybackLifecycleSink) Cancel(context.Context, string, string, string) error {
	return nil
}
